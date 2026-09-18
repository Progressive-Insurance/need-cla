/*
Copyright (c) 2021-2022 Progressive Casualty Insurance Company. All rights reserved.

Use of this source code is governed by an MIT license that can be found in
the LICENSE file at https://github.com/Progressive-Insurance/need-cla/blob/main/LICENSE.md
*/

package needcla

import (
	"context"
	"encoding/base64"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"

	"github.com/google/go-github/v43/github"
)

// Immutable SHAs used by the truncated-tree recovery fixtures. They are
// deliberately distinct from the branch name so a fallback that restarts from
// the mutable default-branch name is easy to detect.
const (
	ttRootTreeSHA  = "tt-root-tree-sha"
	ttGitHubSHA    = "tt-github-sha"
	ttWorkflowsSHA = "tt-workflows-sha"
	ttReadmeSHA    = "tt-readme-sha"
	ttContribSHA   = "tt-contrib-sha"
	ttWorkflowSHA  = "tt-workflow-sha"
	ttOtherSHA     = "tt-other-sha"
	ttFailAlphaSHA = "tt-fail-alpha-sha"
	ttFailZetaSHA  = "tt-fail-zeta-sha"
)

// truncatedTreeRecoveryTree models a non-recursive tree listing at a specific
// SHA.
type truncatedTreeRecoveryTree struct {
	entries   []string
	truncated bool
	status    int
}

// truncatedTreeRecoveryConfig controls the fixture server for the
// truncated-tree recovery tests.
type truncatedTreeRecoveryConfig struct {
	// rootTreeSHA is the SHA reported by the recursive repository tree.
	// Empty models an unavailable root tree SHA.
	rootTreeSHA string
	// recursiveEntries are the raw JSON entries of the recursive tree.
	recursiveEntries []string
	// recursiveTruncated marks the recursive tree as truncated.
	recursiveTruncated bool
	// trees maps a tree SHA to its non-recursive listing.
	trees map[string]truncatedTreeRecoveryTree
	// blobs maps blob SHAs to content.
	blobs map[string]string
	// blobStatus maps blob SHAs to an HTTP status; 0 means success.
	blobStatus map[string]int
}

// startTruncatedTreeRecoveryServer starts an httptest.Server modeling the
// GitHub API surface used by the truncated-tree recovery paths. The recursive
// repository tree and the non-recursive trees at arbitrary SHAs are modeled
// according to cfg. Every response is a local fixture: no credentials and no
// external network are required. Routes that are not modeled return 500 so
// fixture mistakes surface as errors instead of passing silently.
func startTruncatedTreeRecoveryServer(t *testing.T, cfg truncatedTreeRecoveryConfig) (*github.Client, func() []string) {
	t.Helper()
	record, recorded := newRecordingPaths()

	repoPrefix := "/repos/" + fixtureOwner + "/" + fixtureRepo

	writeJSON := func(w http.ResponseWriter, body string) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("X-RateLimit-Limit", "5000")
		w.Header().Set("X-RateLimit-Remaining", "4990")
		w.Header().Set("X-RateLimit-Reset", "1900000000")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(body))
	}

	recursiveTruncated := "false"
	if cfg.recursiveTruncated {
		recursiveTruncated = "true"
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/rate_limit", func(w http.ResponseWriter, r *http.Request) {
		record(r)
		failPreflight(w, r)
	})
	mux.HandleFunc(repoPrefix, func(w http.ResponseWriter, r *http.Request) {
		record(r)
		writeJSON(w, `{"name":"`+fixtureRepo+`","full_name":"`+fixtureOwner+"/"+fixtureRepo+`","default_branch":"`+fixtureBranch+`"}`)
	})
	mux.HandleFunc(repoPrefix+"/git/trees/"+fixtureBranch, func(w http.ResponseWriter, r *http.Request) {
		record(r)
		if r.URL.Query().Get("recursive") != "1" {
			http.Error(w, "repository tree must be requested recursively", http.StatusInternalServerError)
			return
		}
		writeJSON(w, `{"sha":"`+cfg.rootTreeSHA+`","tree":[`+strings.Join(cfg.recursiveEntries, ",")+`],"truncated":`+recursiveTruncated+`}`)
	})
	mux.HandleFunc(repoPrefix+"/git/trees/", func(w http.ResponseWriter, r *http.Request) {
		record(r)
		sha := strings.TrimPrefix(r.URL.Path, repoPrefix+"/git/trees/")
		tree, ok := cfg.trees[sha]
		if !ok {
			http.Error(w, "unexpected tree requested: "+sha, http.StatusInternalServerError)
			return
		}
		if tree.status != 0 {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(tree.status)
			_, _ = w.Write([]byte(`{"message":"Internal Server Error"}`))
			return
		}
		truncated := "false"
		if tree.truncated {
			truncated = "true"
		}
		writeJSON(w, `{"sha":"`+sha+`","tree":[`+strings.Join(tree.entries, ",")+`],"truncated":`+truncated+`}`)
	})
	mux.HandleFunc(repoPrefix+"/git/blobs/", func(w http.ResponseWriter, r *http.Request) {
		record(r)
		sha := strings.TrimPrefix(r.URL.Path, repoPrefix+"/git/blobs/")
		if status, ok := cfg.blobStatus[sha]; ok && status != 0 {
			http.Error(w, "blob unavailable: "+sha, status)
			return
		}
		content, ok := cfg.blobs[sha]
		if !ok {
			http.Error(w, "unexpected blob requested: "+sha, http.StatusInternalServerError)
			return
		}
		encoded := base64.StdEncoding.EncodeToString([]byte(content))
		writeJSON(w, `{"encoding":"base64","content":"`+encoded+`","size":`+strconv.Itoa(len(content))+`}`)
	})
	mux.HandleFunc(repoPrefix+"/pulls", func(w http.ResponseWriter, r *http.Request) {
		record(r)
		writeJSON(w, `[]`)
	})
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		record(r)
		http.Error(w, "unexpected request: "+r.URL.Path, http.StatusInternalServerError)
	})

	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)
	client := github.NewClient(nil)
	baseURL, err := url.Parse(server.URL + "/")
	if err != nil {
		t.Fatalf("parsing fixture base URL: %v", err)
	}
	client.BaseURL = baseURL
	return client, recorded
}

// TestTruncatedTreeFoundFastPath verifies that an entry present in the
// original recursive tree is returned without any fallback recovery request.
func TestTruncatedTreeFoundFastPath(t *testing.T) {
	client, recorded := startTruncatedTreeRecoveryServer(t, truncatedTreeRecoveryConfig{
		rootTreeSHA: ttRootTreeSHA,
		recursiveEntries: []string{
			`{"path":"CONTRIBUTING.md","sha":"` + ttContribSHA + `","mode":"100644","type":"blob"}`,
			`{"path":"README.md","sha":"` + ttReadmeSHA + `","mode":"100644","type":"blob"}`,
			`{"path":".github/workflows","sha":"` + ttWorkflowsSHA + `","mode":"040000","type":"tree"}`,
		},
		recursiveTruncated: false,
		trees: map[string]truncatedTreeRecoveryTree{
			ttWorkflowsSHA: {entries: []string{}},
		},
		blobs: map[string]string{
			ttContribSHA: contributingContent,
			ttReadmeSHA:  readmeContent,
		},
	})

	details, err := Detail(client, fixtureOwner, fixtureRepo)
	if err != nil {
		t.Fatalf("Detail returned unexpected error: %v", err)
	}
	if details != (Details{}) {
		t.Errorf("Details = %+v; want zero value", details)
	}

	repoPrefix := "/repos/" + fixtureOwner + "/" + fixtureRepo
	want := []string{
		repoPrefix,
		repoPrefix + "/git/trees/" + fixtureBranch + "?recursive=1",
		repoPrefix + "/git/trees/" + ttWorkflowsSHA,
		repoPrefix + "/git/blobs/" + ttContribSHA,
		repoPrefix + "/git/blobs/" + ttReadmeSHA,
		repoPrefix + "/pulls?per_page=100&state=all",
	}
	if got := recorded(); !samePathSet(got, want) {
		t.Errorf("recorded requests %v; want %v", got, want)
	}
	// No fallback recovery: no tree fetch at the root tree SHA or the
	// .github directory SHA.
	for _, p := range recorded() {
		if strings.Contains(p, ttRootTreeSHA) || strings.Contains(p, ttGitHubSHA) {
			t.Errorf("fallback tree request made: %s", p)
		}
	}
}

// TestTruncatedTreeOmittedReadmeRecovery verifies that a README.md omitted
// from a truncated recursive tree is recovered by a non-recursive fetch at
// the immutable root tree SHA.
func TestTruncatedTreeOmittedReadmeRecovery(t *testing.T) {
	client, recorded := startTruncatedTreeRecoveryServer(t, truncatedTreeRecoveryConfig{
		rootTreeSHA: ttRootTreeSHA,
		recursiveEntries: []string{
			`{"path":"CONTRIBUTING.md","sha":"` + ttContribSHA + `","mode":"100644","type":"blob"}`,
			`{"path":".github/workflows","sha":"` + ttWorkflowsSHA + `","mode":"040000","type":"tree"}`,
		},
		recursiveTruncated: true,
		trees: map[string]truncatedTreeRecoveryTree{
			ttRootTreeSHA: {
				entries: []string{
					`{"path":"CONTRIBUTING.md","sha":"` + ttContribSHA + `","mode":"100644","type":"blob"}`,
					`{"path":"README.md","sha":"` + ttReadmeSHA + `","mode":"100644","type":"blob"}`,
					`{"path":".github","sha":"` + ttGitHubSHA + `","mode":"040000","type":"tree"}`,
				},
			},
			ttWorkflowsSHA: {entries: []string{}},
		},
		blobs: map[string]string{
			ttContribSHA: contributingContent,
			ttReadmeSHA:  readmeContent,
		},
	})

	details, err := Detail(client, fixtureOwner, fixtureRepo)
	if err != nil {
		t.Fatalf("Detail returned unexpected error: %v", err)
	}
	if details != (Details{}) {
		t.Errorf("Details = %+v; want zero value", details)
	}

	repoPrefix := "/repos/" + fixtureOwner + "/" + fixtureRepo
	want := []string{
		repoPrefix,
		repoPrefix + "/git/trees/" + fixtureBranch + "?recursive=1",
		repoPrefix + "/git/trees/" + ttRootTreeSHA,
		repoPrefix + "/git/trees/" + ttWorkflowsSHA,
		repoPrefix + "/git/blobs/" + ttContribSHA,
		repoPrefix + "/git/blobs/" + ttReadmeSHA,
		repoPrefix + "/pulls?per_page=100&state=all",
	}
	if got := recorded(); !samePathSet(got, want) {
		t.Errorf("recorded requests %v; want %v", got, want)
	}
}

// TestTruncatedTreeNestedWorkflowRecovery verifies that .github/workflows
// omitted from a truncated recursive tree is recovered by walking the
// non-recursive root tree and then the .github directory tree, using only the
// immutable SHAs and never the branch name.
func TestTruncatedTreeNestedWorkflowRecovery(t *testing.T) {
	client, recorded := startTruncatedTreeRecoveryServer(t, truncatedTreeRecoveryConfig{
		rootTreeSHA: ttRootTreeSHA,
		recursiveEntries: []string{
			`{"path":"CONTRIBUTING.md","sha":"` + ttContribSHA + `","mode":"100644","type":"blob"}`,
			`{"path":"README.md","sha":"` + ttReadmeSHA + `","mode":"100644","type":"blob"}`,
		},
		recursiveTruncated: true,
		trees: map[string]truncatedTreeRecoveryTree{
			ttRootTreeSHA: {
				entries: []string{
					`{"path":"CONTRIBUTING.md","sha":"` + ttContribSHA + `","mode":"100644","type":"blob"}`,
					`{"path":"README.md","sha":"` + ttReadmeSHA + `","mode":"100644","type":"blob"}`,
					`{"path":".github","sha":"` + ttGitHubSHA + `","mode":"040000","type":"tree"}`,
				},
			},
			ttGitHubSHA: {
				entries: []string{
					`{"path":"workflows","sha":"` + ttWorkflowsSHA + `","mode":"040000","type":"tree"}`,
				},
			},
			ttWorkflowsSHA: {
				entries: []string{
					`{"path":"cla.yml","sha":"` + ttWorkflowSHA + `","mode":"100644","type":"blob"}`,
				},
			},
		},
		blobs: map[string]string{
			ttContribSHA:  contributingContent,
			ttReadmeSHA:   readmeContent,
			ttWorkflowSHA: workflowContent,
		},
	})

	details, err := Detail(client, fixtureOwner, fixtureRepo)
	if err != nil {
		t.Fatalf("Detail returned unexpected error: %v", err)
	}
	if !details.Action {
		t.Errorf("Action = false; want true (the workflow was recovered)")
	}

	repoPrefix := "/repos/" + fixtureOwner + "/" + fixtureRepo
	want := []string{
		repoPrefix,
		repoPrefix + "/git/trees/" + fixtureBranch + "?recursive=1",
		repoPrefix + "/git/trees/" + ttRootTreeSHA,
		repoPrefix + "/git/trees/" + ttGitHubSHA,
		repoPrefix + "/git/trees/" + ttWorkflowsSHA,
		repoPrefix + "/git/blobs/" + ttContribSHA,
		repoPrefix + "/git/blobs/" + ttReadmeSHA,
		repoPrefix + "/git/blobs/" + ttWorkflowSHA,
		repoPrefix + "/pulls?per_page=100&state=all",
	}
	if got := recorded(); !samePathSet(got, want) {
		t.Errorf("recorded requests %v; want %v", got, want)
	}
	// The fallback never restarts from the mutable default-branch name.
	for _, p := range recorded() {
		if strings.Contains(p, "/git/trees/"+fixtureBranch) && !strings.Contains(p, "recursive=1") {
			t.Errorf("fallback restarted from the branch name: %s", p)
		}
	}
}

// TestTruncatedTreeCompleteListingAbsence verifies that a path omitted from a
// truncated recursive tree but absent from a complete non-recursive listing is
// ordinary absence, not an error.
func TestTruncatedTreeCompleteListingAbsence(t *testing.T) {
	client, _ := startTruncatedTreeRecoveryServer(t, truncatedTreeRecoveryConfig{
		rootTreeSHA:        ttRootTreeSHA,
		recursiveEntries:   []string{},
		recursiveTruncated: true,
		trees: map[string]truncatedTreeRecoveryTree{
			ttRootTreeSHA: {
				entries: []string{
					`{"path":"CONTRIBUTING.md","sha":"` + ttContribSHA + `","mode":"100644","type":"blob"}`,
				},
			},
		},
	})
	c, err := newChecker(context.Background(), client, fixtureOwner, fixtureRepo, fixtureBranch)
	if err != nil {
		t.Fatalf("newChecker returned error: %v", err)
	}
	entry, err := c.find(context.Background(), "README.md")
	if err != nil {
		t.Fatalf("find returned error: %v", err)
	}
	if entry != nil {
		t.Errorf("find returned entry %v; want nil (ordinary absence)", entry)
	}
}

// TestTruncatedTreeIntermediateNonTree verifies that an intermediate
// component that is a blob (not a directory) makes the path absent without
// requesting a tree for the blob's SHA, reusing task 09's entry-type rules.
func TestTruncatedTreeIntermediateNonTree(t *testing.T) {
	client, recorded := startTruncatedTreeRecoveryServer(t, truncatedTreeRecoveryConfig{
		rootTreeSHA:        ttRootTreeSHA,
		recursiveEntries:   []string{},
		recursiveTruncated: true,
		trees: map[string]truncatedTreeRecoveryTree{
			ttRootTreeSHA: {
				entries: []string{
					`{"path":".github","sha":"` + ttOtherSHA + `","mode":"100644","type":"blob"}`,
				},
			},
		},
	})
	c, err := newChecker(context.Background(), client, fixtureOwner, fixtureRepo, fixtureBranch)
	if err != nil {
		t.Fatalf("newChecker returned error: %v", err)
	}
	entry, err := c.find(context.Background(), ".github/workflows")
	if err != nil {
		t.Fatalf("find returned error: %v", err)
	}
	if entry != nil {
		t.Errorf("find returned entry %v; want nil (a blob cannot contain workflows)", entry)
	}
	// The recovery fetched the root tree but did not request a tree for the
	// blob's SHA.
	for _, p := range recorded() {
		if strings.Contains(p, "/git/trees/"+ttOtherSHA) {
			t.Errorf("unexpected tree request for a blob SHA: %s", p)
		}
	}
}

// TestTruncatedTreeStillTruncatedIntermediate verifies that a path omitted
// from a truncated recursive tree and from a still-truncated non-recursive
// listing reports ErrTruncatedTree.
func TestTruncatedTreeStillTruncatedIntermediate(t *testing.T) {
	client, _ := startTruncatedTreeRecoveryServer(t, truncatedTreeRecoveryConfig{
		rootTreeSHA:        ttRootTreeSHA,
		recursiveEntries:   []string{},
		recursiveTruncated: true,
		trees: map[string]truncatedTreeRecoveryTree{
			ttRootTreeSHA: {
				entries:   []string{},
				truncated: true,
			},
		},
	})
	c, err := newChecker(context.Background(), client, fixtureOwner, fixtureRepo, fixtureBranch)
	if err != nil {
		t.Fatalf("newChecker returned error: %v", err)
	}
	_, err = c.find(context.Background(), "README.md")
	if err == nil {
		t.Fatal("find returned nil error; want the truncated tree error")
	}
	if !errors.Is(err, ErrTruncatedTree) {
		t.Errorf("errors.Is(err, ErrTruncatedTree) = false; want true. err: %v", err)
	}
}

// TestTruncatedTreeUnavailableRootSHA verifies that a truncated recursive
// tree with no root tree SHA reports ErrTruncatedTree without making any
// fallback request.
func TestTruncatedTreeUnavailableRootSHA(t *testing.T) {
	client, recorded := startTruncatedTreeRecoveryServer(t, truncatedTreeRecoveryConfig{
		rootTreeSHA:        "",
		recursiveEntries:   []string{},
		recursiveTruncated: true,
	})
	c, err := newChecker(context.Background(), client, fixtureOwner, fixtureRepo, fixtureBranch)
	if err != nil {
		t.Fatalf("newChecker returned error: %v", err)
	}
	_, err = c.find(context.Background(), "README.md")
	if err == nil {
		t.Fatal("find returned nil error; want the truncated tree error")
	}
	if !errors.Is(err, ErrTruncatedTree) {
		t.Errorf("errors.Is(err, ErrTruncatedTree) = false; want true. err: %v", err)
	}
	// No fallback request was made because the root tree SHA is unavailable.
	for _, p := range recorded() {
		if strings.Contains(p, "/git/trees/") && !strings.Contains(p, fixtureBranch+"?recursive=1") {
			t.Errorf("unexpected tree request: %s", p)
		}
	}
}

// TestTruncatedTreeCancellation verifies that a canceled context during the
// fallback recovery keeps its identity.
func TestTruncatedTreeCancellation(t *testing.T) {
	client, _ := startTruncatedTreeRecoveryServer(t, truncatedTreeRecoveryConfig{
		rootTreeSHA:        ttRootTreeSHA,
		recursiveEntries:   []string{},
		recursiveTruncated: true,
		trees: map[string]truncatedTreeRecoveryTree{
			ttRootTreeSHA: {
				entries: []string{
					`{"path":"README.md","sha":"` + ttReadmeSHA + `","mode":"100644","type":"blob"}`,
				},
			},
		},
	})
	c, err := newChecker(context.Background(), client, fixtureOwner, fixtureRepo, fixtureBranch)
	if err != nil {
		t.Fatalf("newChecker returned error: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = c.find(ctx, "README.md")
	if err == nil {
		t.Fatal("find returned nil error; want the canceled context error")
	}
	if !errors.Is(err, context.Canceled) {
		t.Errorf("errors.Is(err, context.Canceled) = false; want true. err: %v", err)
	}
}

// TestTruncatedTreeTransportFailure verifies that a failed fallback recovery
// request preserves its original cause.
func TestTruncatedTreeTransportFailure(t *testing.T) {
	client, _ := startTruncatedTreeRecoveryServer(t, truncatedTreeRecoveryConfig{
		rootTreeSHA:        ttRootTreeSHA,
		recursiveEntries:   []string{},
		recursiveTruncated: true,
		trees: map[string]truncatedTreeRecoveryTree{
			ttRootTreeSHA: {
				status: http.StatusInternalServerError,
			},
		},
	})
	c, err := newChecker(context.Background(), client, fixtureOwner, fixtureRepo, fixtureBranch)
	if err != nil {
		t.Fatalf("newChecker returned error: %v", err)
	}
	_, err = c.find(context.Background(), "README.md")
	if err == nil {
		t.Fatal("find returned nil error; want the transport failure")
	}
	var ge *github.ErrorResponse
	if !errors.As(err, &ge) {
		t.Errorf("errors.As(err, *github.ErrorResponse) = false; want the transport failure. err: %v", err)
	}
}

// TestTruncatedTreeWorkflowNoEntries verifies that a truncated workflow
// listing with no entries and no positive match reports ErrTruncatedTree
// instead of claiming complete negative evidence.
func TestTruncatedTreeWorkflowNoEntries(t *testing.T) {
	client, _ := startTruncatedTreeRecoveryServer(t, truncatedTreeRecoveryConfig{
		rootTreeSHA: ttRootTreeSHA,
		recursiveEntries: []string{
			`{"path":".github/workflows","sha":"` + ttWorkflowsSHA + `","mode":"040000","type":"tree"}`,
		},
		recursiveTruncated: false,
		trees: map[string]truncatedTreeRecoveryTree{
			ttWorkflowsSHA: {
				entries:   []string{},
				truncated: true,
			},
		},
	})

	details, err := Detail(client, fixtureOwner, fixtureRepo)
	if err == nil {
		t.Fatal("Detail returned nil error; want the truncated workflow listing error")
	}
	if details.Action {
		t.Errorf("Action = true; want false")
	}
	if !errors.Is(err, ErrTruncatedTree) {
		t.Errorf("errors.Is(err, ErrTruncatedTree) = false; want true. err: %v", err)
	}
}

// TestTruncatedTreeWorkflowNonmatchingOnly verifies that a truncated workflow
// listing containing only non-matching workflows reports ErrTruncatedTree.
func TestTruncatedTreeWorkflowNonmatchingOnly(t *testing.T) {
	client, _ := startTruncatedTreeRecoveryServer(t, truncatedTreeRecoveryConfig{
		rootTreeSHA: ttRootTreeSHA,
		recursiveEntries: []string{
			`{"path":".github/workflows","sha":"` + ttWorkflowsSHA + `","mode":"040000","type":"tree"}`,
		},
		recursiveTruncated: false,
		trees: map[string]truncatedTreeRecoveryTree{
			ttWorkflowsSHA: {
				entries: []string{
					`{"path":"ci.yml","sha":"` + ttOtherSHA + `","mode":"100644","type":"blob"}`,
				},
				truncated: true,
			},
		},
		blobs: map[string]string{
			ttOtherSHA: "name: ci\njobs:\n  ci:\n    steps:\n      - uses: actions/checkout@v3",
		},
	})

	details, err := Detail(client, fixtureOwner, fixtureRepo)
	if err == nil {
		t.Fatal("Detail returned nil error; want the truncated workflow listing error")
	}
	if details.Action {
		t.Errorf("Action = true; want false")
	}
	if !errors.Is(err, ErrTruncatedTree) {
		t.Errorf("errors.Is(err, ErrTruncatedTree) = false; want true. err: %v", err)
	}
}

// TestTruncatedTreeWorkflowPositive verifies that a positively identified
// eligible CLA workflow resolves the check affirmatively even when the
// listing is truncated.
func TestTruncatedTreeWorkflowPositive(t *testing.T) {
	client, _ := startTruncatedTreeRecoveryServer(t, truncatedTreeRecoveryConfig{
		rootTreeSHA: ttRootTreeSHA,
		recursiveEntries: []string{
			`{"path":".github/workflows","sha":"` + ttWorkflowsSHA + `","mode":"040000","type":"tree"}`,
		},
		recursiveTruncated: false,
		trees: map[string]truncatedTreeRecoveryTree{
			ttWorkflowsSHA: {
				entries: []string{
					`{"path":"cla.yml","sha":"` + ttWorkflowSHA + `","mode":"100644","type":"blob"}`,
				},
				truncated: true,
			},
		},
		blobs: map[string]string{
			ttWorkflowSHA: workflowContent,
		},
	})

	details, err := Detail(client, fixtureOwner, fixtureRepo)
	if err != nil {
		t.Fatalf("Detail returned unexpected error: %v", err)
	}
	if !details.Action {
		t.Errorf("Action = false; want true (positive evidence resolves affirmatively)")
	}
}

// TestTruncatedTreeWorkflowConcurrentBlobFailures verifies that a truncated
// workflow listing with multiple failing eligible blobs reports
// ErrTruncatedTree alongside the retained blob failures.
func TestTruncatedTreeWorkflowConcurrentBlobFailures(t *testing.T) {
	client, _ := startTruncatedTreeRecoveryServer(t, truncatedTreeRecoveryConfig{
		rootTreeSHA: ttRootTreeSHA,
		recursiveEntries: []string{
			`{"path":".github/workflows","sha":"` + ttWorkflowsSHA + `","mode":"040000","type":"tree"}`,
		},
		recursiveTruncated: false,
		trees: map[string]truncatedTreeRecoveryTree{
			ttWorkflowsSHA: {
				entries: []string{
					`{"path":"alpha.yml","sha":"` + ttFailAlphaSHA + `","mode":"100644","type":"blob"}`,
					`{"path":"zeta.yml","sha":"` + ttFailZetaSHA + `","mode":"100644","type":"blob"}`,
				},
				truncated: true,
			},
		},
		blobStatus: map[string]int{
			ttFailAlphaSHA: http.StatusInternalServerError,
			ttFailZetaSHA:  http.StatusInternalServerError,
		},
	})

	details, err := Detail(client, fixtureOwner, fixtureRepo)
	if err == nil {
		t.Fatal("Detail returned nil error; want the blob failures and truncation")
	}
	if details.Action {
		t.Errorf("Action = true; want false")
	}
	if !errors.Is(err, ErrTruncatedTree) {
		t.Errorf("errors.Is(err, ErrTruncatedTree) = false; want true. err: %v", err)
	}
	var agg *Errors
	if !errors.As(err, &agg) {
		t.Fatalf("errors.As did not find the *Errors aggregate: %v", err)
	}
	if agg.ActionErr == nil {
		t.Fatal("ActionErr = nil; want the workflow error aggregate")
	}
	msg := agg.ActionErr.Error()
	if !strings.Contains(msg, "alpha.yml") || !strings.Contains(msg, "zeta.yml") {
		t.Errorf("workflow diagnostics do not mention both failing blobs: %s", msg)
	}
}

// TestTruncatedTreeConcurrentFallback exercises concurrent README,
// CONTRIBUTING, and workflow fallback through DetailWithContext, verifying
// that the fallbacks walk the immutable SHAs and never fetch unrelated
// directories.
func TestTruncatedTreeConcurrentFallback(t *testing.T) {
	client, recorded := startTruncatedTreeRecoveryServer(t, truncatedTreeRecoveryConfig{
		rootTreeSHA:        ttRootTreeSHA,
		recursiveEntries:   []string{},
		recursiveTruncated: true,
		trees: map[string]truncatedTreeRecoveryTree{
			ttRootTreeSHA: {
				entries: []string{
					`{"path":"CONTRIBUTING.md","sha":"` + ttContribSHA + `","mode":"100644","type":"blob"}`,
					`{"path":"README.md","sha":"` + ttReadmeSHA + `","mode":"100644","type":"blob"}`,
					`{"path":".github","sha":"` + ttGitHubSHA + `","mode":"040000","type":"tree"}`,
				},
			},
			ttGitHubSHA: {
				entries: []string{
					`{"path":"workflows","sha":"` + ttWorkflowsSHA + `","mode":"040000","type":"tree"}`,
				},
			},
			ttWorkflowsSHA: {
				entries: []string{
					`{"path":"cla.yml","sha":"` + ttWorkflowSHA + `","mode":"100644","type":"blob"}`,
				},
			},
		},
		blobs: map[string]string{
			ttContribSHA:  contributingContent,
			ttReadmeSHA:   readmeContent,
			ttWorkflowSHA: workflowContent,
		},
	})

	details, err := DetailWithContext(context.Background(), client, fixtureOwner, fixtureRepo)
	if err != nil {
		t.Fatalf("DetailWithContext returned unexpected error: %v", err)
	}
	if !details.Action {
		t.Errorf("Action = false; want true")
	}
	if details.InContributing || details.InREADME || details.BotFile || details.Known || details.Tag {
		t.Errorf("Details = %+v; want only Action set", details)
	}

	// The concurrent fallbacks walked the immutable SHAs: the root tree SHA
	// (for README, CONTRIBUTING, .clabot, and .github) and the .github
	// directory SHA (for workflows). No unrelated directory was fetched.
	foundRoot := false
	for _, p := range recorded() {
		if strings.Contains(p, ttRootTreeSHA) {
			foundRoot = true
		}
		if strings.Contains(p, "/git/trees/") &&
			!strings.Contains(p, fixtureBranch+"?recursive=1") &&
			!strings.Contains(p, ttRootTreeSHA) &&
			!strings.Contains(p, ttGitHubSHA) &&
			!strings.Contains(p, ttWorkflowsSHA) {
			t.Errorf("unexpected tree request: %s", p)
		}
	}
	if !foundRoot {
		t.Errorf("no fallback request at the root tree SHA %s", ttRootTreeSHA)
	}
}
