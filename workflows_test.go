/*
Copyright (c) 2021-2022 Progressive Casualty Insurance Company. All rights reserved.

Use of this source code is governed by an MIT license that can be found in
the LICENSE file at https://github.com/Progressive-Insurance/need-cla/blob/main/LICENSE.md
*/

package needcla

import (
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/google/go-github/v43/github"
)

// workflowsEntryKind describes how the fixture models the .github/workflows
// path in the repository tree.
const (
	workflowsEntryAbsent  = "absent"
	workflowsEntryBlob    = "blob"
	workflowsEntryGitlink = "gitlink"
	workflowsEntryTree    = "tree"
)

const (
	workflowsEntrySHA = "workflows-entry-sha"
	workflowBlobSHA   = "workflow-blob-sha"
	workflowContent   = "name: cla\njobs:\n  cla:\n    steps:\n      - uses: cla-assistant/github-action@v2"

	ineligibleReadmeSHA   = "ineligible-readme-sha"
	ineligibleNotesSHA    = "ineligible-notes-sha"
	ineligibleDisabledSHA = "ineligible-disabled-sha"
	ineligibleNoExtSHA    = "ineligible-noext-sha"
	workflowDirSHA        = "workflow-dir-sha"
	gitlinkSHA            = "gitlink-sha"
	failingBlobSHA        = "failing-blob-sha"
)

// workflowsFixtureConfig controls how the fixture server models the
// .github/workflows path and the workflows tree.
type workflowsFixtureConfig struct {
	// entry is the kind of the .github/workflows entry in the repository
	// tree: workflowsEntryAbsent, workflowsEntryBlob, workflowsEntryGitlink,
	// or workflowsEntryTree.
	entry string
	// truncated marks the repository tree as truncated.
	truncated bool
	// treeStatus is the HTTP status returned for the workflows tree request.
	// Zero means a successful response carrying treeEntries.
	treeStatus int
	// treeEntries are the raw JSON entries of the workflows tree.
	treeEntries []string
	// treeBlobs maps workflow file blob SHAs to their content.
	treeBlobs map[string]string
}

// startWorkflowsFixtureServer starts an httptest.Server modeling the GitHub
// API surface used by Detail, with the .github/workflows path modeled
// according to cfg. Every response is a local fixture: no credentials and no
// external network are required. Routes that are not modeled return 500 so
// fixture mistakes surface as errors instead of passing silently.
func startWorkflowsFixtureServer(t *testing.T, cfg workflowsFixtureConfig) (*github.Client, func() []string) {
	t.Helper()

	var mu sync.Mutex
	var paths []string
	record := func(r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		if r.URL.RawQuery != "" {
			paths = append(paths, r.URL.Path+"?"+r.URL.RawQuery)
			return
		}
		paths = append(paths, r.URL.Path)
	}
	recorded := func() []string {
		mu.Lock()
		defer mu.Unlock()
		return append([]string(nil), paths...)
	}

	writeJSON := func(w http.ResponseWriter, body string) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("X-RateLimit-Limit", "5000")
		w.Header().Set("X-RateLimit-Remaining", "4990")
		w.Header().Set("X-RateLimit-Reset", "1900000000")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(body))
	}

	repoPrefix := "/repos/" + fixtureOwner + "/" + fixtureRepo

	blobs := map[string]string{
		contributingBlobSHA: contributingContent,
		readmeBlobSHA:       readmeContent,
	}
	for sha, content := range cfg.treeBlobs {
		blobs[sha] = content
	}

	entries := []string{
		`{"path":"CONTRIBUTING.md","sha":"` + contributingBlobSHA + `","mode":"100644","type":"blob"}`,
		`{"path":"README.md","sha":"` + readmeBlobSHA + `","mode":"100644","type":"blob"}`,
	}
	switch cfg.entry {
	case workflowsEntryBlob:
		entries = append(entries, `{"path":".github/workflows","sha":"`+workflowsEntrySHA+`","mode":"100644","type":"blob"}`)
	case workflowsEntryGitlink:
		entries = append(entries, `{"path":".github/workflows","sha":"`+workflowsEntrySHA+`","mode":"160000","type":"commit"}`)
	case workflowsEntryTree:
		entries = append(entries, `{"path":".github/workflows","sha":"`+workflowsEntrySHA+`","mode":"040000","type":"tree"}`)
	}

	truncated := "false"
	if cfg.truncated {
		truncated = "true"
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
		writeJSON(w, `{"sha":"`+fixtureBranch+`-tree-sha","tree":[`+strings.Join(entries, ",")+`],"truncated":`+truncated+`}`)
	})
	if cfg.truncated {
		// A truncated recursive tree triggers bounded recovery, which
		// requests the non-recursive root tree at the immutable root tree
		// SHA. That listing is itself truncated and omits every path, so the
		// recovery reports ErrTruncatedTree instead of inventing an answer.
		mux.HandleFunc(repoPrefix+"/git/trees/"+fixtureBranch+"-tree-sha", func(w http.ResponseWriter, r *http.Request) {
			record(r)
			writeJSON(w, `{"sha":"`+fixtureBranch+`-tree-sha","tree":[],"truncated":true}`)
		})
	}
	if cfg.entry == workflowsEntryTree {
		mux.HandleFunc(repoPrefix+"/git/trees/"+workflowsEntrySHA, func(w http.ResponseWriter, r *http.Request) {
			record(r)
			if cfg.treeStatus != 0 {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(cfg.treeStatus)
				_, _ = w.Write([]byte(`{"message":"Internal Server Error"}`))
				return
			}
			writeJSON(w, `{"sha":"`+workflowsEntrySHA+`","tree":[`+strings.Join(cfg.treeEntries, ",")+`],"truncated":false}`)
		})
	}
	mux.HandleFunc(repoPrefix+"/git/blobs/", func(w http.ResponseWriter, r *http.Request) {
		record(r)
		sha := strings.TrimPrefix(r.URL.Path, repoPrefix+"/git/blobs/")
		content, ok := blobs[sha]
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
		t.Fatalf("parsing server URL: %v", err)
	}
	client.BaseURL = baseURL
	return client, recorded
}

// expectedWorkflowsNegativeRequests is the exact request set a repository
// without a usable .github/workflows tree must produce: the repository
// lookup, the recursive repository tree, the two documented blobs, and the
// PR list. No workflows-tree request and no workflow file blob request.
func expectedWorkflowsNegativeRequests() []string {
	repoPrefix := "/repos/" + fixtureOwner + "/" + fixtureRepo
	return []string{
		repoPrefix,
		repoPrefix + "/git/trees/" + fixtureBranch + "?recursive=1",
		repoPrefix + "/git/blobs/" + contributingBlobSHA,
		repoPrefix + "/git/blobs/" + readmeBlobSHA,
		repoPrefix + "/pulls?per_page=100&state=all",
	}
}

// TestUsesCLAAssistantActionAbsentWorkflows covers acceptance cases 1 and 6:
// a complete repository tree without .github/workflows yields Action=false
// with no error through the public details API, and no workflow-tree or
// workflow blob request is made.
func TestUsesCLAAssistantActionAbsentWorkflows(t *testing.T) {
	client, recorded := startWorkflowsFixtureServer(t, workflowsFixtureConfig{
		entry: workflowsEntryAbsent,
	})

	details, err := Detail(client, fixtureOwner, fixtureRepo)
	if err != nil {
		t.Fatalf("Detail returned unexpected error: %v", err)
	}
	if details.Action {
		t.Errorf("Action = true; want false")
	}
	if details != (Details{}) {
		t.Errorf("Details = %+v; want zero value", details)
	}

	if got := recorded(); !samePathSet(got, expectedWorkflowsNegativeRequests()) {
		t.Errorf("recorded requests %v; want %v", got, expectedWorkflowsNegativeRequests())
	}
}

// TestUsesCLAAssistantActionNonTreeWorkflows covers acceptance case 2: a
// blob or gitlink at .github/workflows cannot contain workflows, so the
// check returns false without requesting its SHA as a tree.
func TestUsesCLAAssistantActionNonTreeWorkflows(t *testing.T) {
	for _, tc := range []struct {
		name  string
		entry string
	}{
		{name: "blob", entry: workflowsEntryBlob},
		{name: "gitlink", entry: workflowsEntryGitlink},
	} {
		t.Run(tc.name, func(t *testing.T) {
			client, recorded := startWorkflowsFixtureServer(t, workflowsFixtureConfig{
				entry: tc.entry,
			})

			details, err := Detail(client, fixtureOwner, fixtureRepo)
			if err != nil {
				t.Fatalf("Detail returned unexpected error: %v", err)
			}
			if details.Action {
				t.Errorf("Action = true; want false")
			}
			if details != (Details{}) {
				t.Errorf("Details = %+v; want zero value", details)
			}

			if got := recorded(); !samePathSet(got, expectedWorkflowsNegativeRequests()) {
				t.Errorf("recorded requests %v; want %v", got, expectedWorkflowsNegativeRequests())
			}
		})
	}
}

// TestUsesCLAAssistantActionPositive covers acceptance case 3: a genuine
// tree entry is fetched by its nonempty SHA and positive action detection
// still works.
func TestUsesCLAAssistantActionPositive(t *testing.T) {
	client, recorded := startWorkflowsFixtureServer(t, workflowsFixtureConfig{
		entry: workflowsEntryTree,
		treeEntries: []string{
			`{"path":"cla.yml","sha":"` + workflowBlobSHA + `","mode":"100644","type":"blob"}`,
		},
		treeBlobs: map[string]string{workflowBlobSHA: workflowContent},
	})

	details, err := Detail(client, fixtureOwner, fixtureRepo)
	if err != nil {
		t.Fatalf("Detail returned unexpected error: %v", err)
	}
	if !details.Action {
		t.Errorf("Action = false; want true")
	}

	repoPrefix := "/repos/" + fixtureOwner + "/" + fixtureRepo
	want := append(expectedWorkflowsNegativeRequests(),
		repoPrefix+"/git/trees/"+workflowsEntrySHA,
		repoPrefix+"/git/blobs/"+workflowBlobSHA,
	)
	if got := recorded(); !samePathSet(got, want) {
		t.Errorf("recorded requests %v; want %v", got, want)
	}
}

// TestUsesCLAAssistantActionTruncatedTree covers acceptance case 4: a
// truncated repository tree that omits .github/workflows must remain an
// error, not be converted into absence.
func TestUsesCLAAssistantActionTruncatedTree(t *testing.T) {
	client, recorded := startWorkflowsFixtureServer(t, workflowsFixtureConfig{
		entry:     workflowsEntryAbsent,
		truncated: true,
	})

	details, err := Detail(client, fixtureOwner, fixtureRepo)
	if err == nil {
		t.Fatalf("Detail returned nil error; want the truncated tree error")
	}
	if details.Action {
		t.Errorf("Action = true; want false")
	}
	if !strings.Contains(err.Error(), "truncated") {
		t.Errorf("error does not mention the truncated tree: %v", err)
	}

	repoPrefix := "/repos/" + fixtureOwner + "/" + fixtureRepo
	// The truncated recursive tree triggers bounded recovery: the omitted
	// .clabot and .github/workflows paths are looked up in the non-recursive
	// root tree at the immutable root tree SHA, which is itself truncated, so
	// the lookup reports ErrTruncatedTree.
	want := append(expectedWorkflowsNegativeRequests(),
		repoPrefix+"/git/trees/"+fixtureBranch+"-tree-sha",
	)
	if got := recorded(); !samePathSet(got, want) {
		t.Errorf("recorded requests %v; want %v", got, want)
	}
}

// TestUsesCLAAssistantActionTreeFetchFailure covers acceptance case 5: a
// failed workflows-tree request remains an error with useful context, and a
// checker running on the repository's actual branch never reports master.
func TestUsesCLAAssistantActionTreeFetchFailure(t *testing.T) {
	client, _ := startWorkflowsFixtureServer(t, workflowsFixtureConfig{
		entry:      workflowsEntryTree,
		treeStatus: http.StatusInternalServerError,
	})

	details, err := Detail(client, fixtureOwner, fixtureRepo)
	if err == nil {
		t.Fatalf("Detail returned nil error; want the workflows tree fetch failure")
	}
	if details.Action {
		t.Errorf("Action = true; want false")
	}
	msg := err.Error()
	if strings.Contains(msg, "master") {
		t.Errorf("error reports master; want the actual checker branch %q: %v", fixtureBranch, err)
	}
	if !strings.Contains(msg, fixtureBranch) {
		t.Errorf("error does not mention the checker branch %q: %v", fixtureBranch, err)
	}
	if !strings.Contains(msg, ".github/workflows") {
		t.Errorf("error does not mention the workflow path: %v", err)
	}
	if !strings.Contains(msg, fixtureOwner+"/"+fixtureRepo) {
		t.Errorf("error does not mention the repository: %v", err)
	}
}

// TestUsesCLAAssistantActionEligibleExtensions covers acceptance case 1:
// blob entries named cla.yml and cla.yaml are fetched and can supply
// positive evidence.
func TestUsesCLAAssistantActionEligibleExtensions(t *testing.T) {
	for _, name := range []string{"cla.yml", "cla.yaml"} {
		t.Run(name, func(t *testing.T) {
			client, recorded := startWorkflowsFixtureServer(t, workflowsFixtureConfig{
				entry: workflowsEntryTree,
				treeEntries: []string{
					`{"path":"` + name + `","sha":"` + workflowBlobSHA + `","mode":"100644","type":"blob"}`,
				},
				treeBlobs: map[string]string{workflowBlobSHA: workflowContent},
			})

			details, err := Detail(client, fixtureOwner, fixtureRepo)
			if err != nil {
				t.Fatalf("Detail returned unexpected error: %v", err)
			}
			if !details.Action {
				t.Errorf("Action = false; want true")
			}

			repoPrefix := "/repos/" + fixtureOwner + "/" + fixtureRepo
			want := append(expectedWorkflowsNegativeRequests(),
				repoPrefix+"/git/trees/"+workflowsEntrySHA,
				repoPrefix+"/git/blobs/"+workflowBlobSHA,
			)
			if got := recorded(); !samePathSet(got, want) {
				t.Errorf("recorded requests %v; want %v", got, want)
			}
		})
	}
}

// TestUsesCLAAssistantActionSkipsIneligibleFiles covers acceptance case 2:
// README.md, notes.txt, cla.yml.disabled, and extensionless blobs are
// skipped without blob requests, even when their content would match the
// action expression.
func TestUsesCLAAssistantActionSkipsIneligibleFiles(t *testing.T) {
	client, recorded := startWorkflowsFixtureServer(t, workflowsFixtureConfig{
		entry: workflowsEntryTree,
		treeEntries: []string{
			`{"path":"README.md","sha":"` + ineligibleReadmeSHA + `","mode":"100644","type":"blob"}`,
			`{"path":"notes.txt","sha":"` + ineligibleNotesSHA + `","mode":"100644","type":"blob"}`,
			`{"path":"cla.yml.disabled","sha":"` + ineligibleDisabledSHA + `","mode":"100644","type":"blob"}`,
			`{"path":"Makefile","sha":"` + ineligibleNoExtSHA + `","mode":"100644","type":"blob"}`,
		},
		treeBlobs: map[string]string{
			ineligibleReadmeSHA:   workflowContent,
			ineligibleNotesSHA:    workflowContent,
			ineligibleDisabledSHA: workflowContent,
			ineligibleNoExtSHA:    workflowContent,
		},
	})

	details, err := Detail(client, fixtureOwner, fixtureRepo)
	if err != nil {
		t.Fatalf("Detail returned unexpected error: %v", err)
	}
	if details.Action {
		t.Errorf("Action = true; want false")
	}

	repoPrefix := "/repos/" + fixtureOwner + "/" + fixtureRepo
	want := append(expectedWorkflowsNegativeRequests(),
		repoPrefix+"/git/trees/"+workflowsEntrySHA,
	)
	if got := recorded(); !samePathSet(got, want) {
		t.Errorf("recorded requests %v; want %v", got, want)
	}
}

// TestUsesCLAAssistantActionSkipsNonBlobs covers acceptance case 3: tree
// entries and gitlinks are skipped without blob requests, including ones
// with YAML-looking names.
func TestUsesCLAAssistantActionSkipsNonBlobs(t *testing.T) {
	client, recorded := startWorkflowsFixtureServer(t, workflowsFixtureConfig{
		entry: workflowsEntryTree,
		treeEntries: []string{
			`{"path":"cla.yml","sha":"` + workflowDirSHA + `","mode":"040000","type":"tree"}`,
			`{"path":"ci.yaml","sha":"` + gitlinkSHA + `","mode":"160000","type":"commit"}`,
		},
	})

	details, err := Detail(client, fixtureOwner, fixtureRepo)
	if err != nil {
		t.Fatalf("Detail returned unexpected error: %v", err)
	}
	if details.Action {
		t.Errorf("Action = true; want false")
	}

	repoPrefix := "/repos/" + fixtureOwner + "/" + fixtureRepo
	want := append(expectedWorkflowsNegativeRequests(),
		repoPrefix+"/git/trees/"+workflowsEntrySHA,
	)
	if got := recorded(); !samePathSet(got, want) {
		t.Errorf("recorded requests %v; want %v", got, want)
	}
}

// TestUsesCLAAssistantActionEmptyAndIneligibleOnly covers acceptance case 4:
// an empty workflows directory and one containing only ineligible entries
// both return false without error.
func TestUsesCLAAssistantActionEmptyAndIneligibleOnly(t *testing.T) {
	t.Run("empty", func(t *testing.T) {
		client, recorded := startWorkflowsFixtureServer(t, workflowsFixtureConfig{
			entry: workflowsEntryTree,
		})

		details, err := Detail(client, fixtureOwner, fixtureRepo)
		if err != nil {
			t.Fatalf("Detail returned unexpected error: %v", err)
		}
		if details.Action {
			t.Errorf("Action = true; want false")
		}

		repoPrefix := "/repos/" + fixtureOwner + "/" + fixtureRepo
		want := append(expectedWorkflowsNegativeRequests(),
			repoPrefix+"/git/trees/"+workflowsEntrySHA,
		)
		if got := recorded(); !samePathSet(got, want) {
			t.Errorf("recorded requests %v; want %v", got, want)
		}
	})

	t.Run("ineligible-only", func(t *testing.T) {
		client, recorded := startWorkflowsFixtureServer(t, workflowsFixtureConfig{
			entry: workflowsEntryTree,
			treeEntries: []string{
				`{"path":"README.md","sha":"` + ineligibleReadmeSHA + `","mode":"100644","type":"blob"}`,
			},
			treeBlobs: map[string]string{ineligibleReadmeSHA: workflowContent},
		})

		details, err := Detail(client, fixtureOwner, fixtureRepo)
		if err != nil {
			t.Fatalf("Detail returned unexpected error: %v", err)
		}
		if details.Action {
			t.Errorf("Action = true; want false")
		}

		repoPrefix := "/repos/" + fixtureOwner + "/" + fixtureRepo
		want := append(expectedWorkflowsNegativeRequests(),
			repoPrefix+"/git/trees/"+workflowsEntrySHA,
		)
		if got := recorded(); !samePathSet(got, want) {
			t.Errorf("recorded requests %v; want %v", got, want)
		}
	})
}

// TestUsesCLAAssistantActionMixedEntries covers acceptance case 5: a
// mixture of irrelevant entries and a valid positive workflow returns true,
// and only the eligible workflow blob is fetched.
func TestUsesCLAAssistantActionMixedEntries(t *testing.T) {
	client, recorded := startWorkflowsFixtureServer(t, workflowsFixtureConfig{
		entry: workflowsEntryTree,
		treeEntries: []string{
			`{"path":"README.md","sha":"` + ineligibleReadmeSHA + `","mode":"100644","type":"blob"}`,
			`{"path":"notes.txt","sha":"` + ineligibleNotesSHA + `","mode":"100644","type":"blob"}`,
			`{"path":"cla.yml","sha":"` + workflowBlobSHA + `","mode":"100644","type":"blob"}`,
		},
		treeBlobs: map[string]string{
			ineligibleReadmeSHA: workflowContent,
			ineligibleNotesSHA:  workflowContent,
			workflowBlobSHA:     workflowContent,
		},
	})

	details, err := Detail(client, fixtureOwner, fixtureRepo)
	if err != nil {
		t.Fatalf("Detail returned unexpected error: %v", err)
	}
	if !details.Action {
		t.Errorf("Action = false; want true")
	}

	repoPrefix := "/repos/" + fixtureOwner + "/" + fixtureRepo
	want := append(expectedWorkflowsNegativeRequests(),
		repoPrefix+"/git/trees/"+workflowsEntrySHA,
		repoPrefix+"/git/blobs/"+workflowBlobSHA,
	)
	if got := recorded(); !samePathSet(got, want) {
		t.Errorf("recorded requests %v; want %v", got, want)
	}
}

// TestUsesCLAAssistantActionFailedEligibleBlob covers acceptance case 6: a
// failed eligible blob download still produces a contextual workflow-check
// error when no eligible workflow matches.
func TestUsesCLAAssistantActionFailedEligibleBlob(t *testing.T) {
	client, recorded := startWorkflowsFixtureServer(t, workflowsFixtureConfig{
		entry: workflowsEntryTree,
		treeEntries: []string{
			`{"path":"cla.yml","sha":"` + failingBlobSHA + `","mode":"100644","type":"blob"}`,
		},
	})

	details, err := Detail(client, fixtureOwner, fixtureRepo)
	if err == nil {
		t.Fatalf("Detail returned nil error; want the failed blob download error")
	}
	if details.Action {
		t.Errorf("Action = true; want false")
	}
	msg := err.Error()
	if !strings.Contains(msg, "cla-assistant action") {
		t.Errorf("error does not mention the cla-assistant action check: %v", err)
	}
	if !strings.Contains(msg, "cla.yml") {
		t.Errorf("error does not mention the failing workflow path: %v", err)
	}

	repoPrefix := "/repos/" + fixtureOwner + "/" + fixtureRepo
	want := append(expectedWorkflowsNegativeRequests(),
		repoPrefix+"/git/trees/"+workflowsEntrySHA,
		repoPrefix+"/git/blobs/"+failingBlobSHA,
	)
	if got := recorded(); !samePathSet(got, want) {
		t.Errorf("recorded requests %v; want %v", got, want)
	}
}

// TestUsesCLAAssistantActionWorkflowREADME is the regression for the
// original false positive: a README.md inside .github/workflows whose
// content matches the action expression must not set Details.Action.
func TestUsesCLAAssistantActionWorkflowREADME(t *testing.T) {
	client, recorded := startWorkflowsFixtureServer(t, workflowsFixtureConfig{
		entry: workflowsEntryTree,
		treeEntries: []string{
			`{"path":"README.md","sha":"` + ineligibleReadmeSHA + `","mode":"100644","type":"blob"}`,
		},
		treeBlobs: map[string]string{ineligibleReadmeSHA: workflowContent},
	})

	details, err := Detail(client, fixtureOwner, fixtureRepo)
	if err != nil {
		t.Fatalf("Detail returned unexpected error: %v", err)
	}
	if details.Action {
		t.Errorf("Action = true; want false (a workflow-directory README is not a workflow)")
	}

	repoPrefix := "/repos/" + fixtureOwner + "/" + fixtureRepo
	want := append(expectedWorkflowsNegativeRequests(),
		repoPrefix+"/git/trees/"+workflowsEntrySHA,
	)
	if got := recorded(); !samePathSet(got, want) {
		t.Errorf("recorded requests %v; want %v", got, want)
	}
}
