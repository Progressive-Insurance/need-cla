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
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/go-github/v43/github"
)

// newFixtureChecker builds a checker over an in-memory fixture tree that
// contains the two document blobs and is not truncated, so the blob
// retrieval paths can be exercised without a repository or tree request.
func newFixtureChecker(client *github.Client) checker {
	return checker{
		client: client,
		branch: fixtureBranch,
		repo:   fixtureRepo,
		owner:  fixtureOwner,
		tree: &github.Tree{
			SHA:       github.String(fixtureBranch + "-tree-sha"),
			Truncated: github.Bool(false),
			Entries: []*github.TreeEntry{
				{Path: github.String("CONTRIBUTING.md"), SHA: github.String(contributingBlobSHA), Type: github.String("blob")},
				{Path: github.String("README.md"), SHA: github.String(readmeBlobSHA), Type: github.String("blob")},
			},
		},
	}
}

// newTruncatedFixtureChecker builds a checker over an in-memory truncated
// tree that omits every path, so find reports ErrTruncatedTree before any
// client call is made.
func newTruncatedFixtureChecker() checker {
	return checker{
		branch: fixtureBranch,
		repo:   fixtureRepo,
		owner:  fixtureOwner,
		tree: &github.Tree{
			SHA:       github.String(fixtureBranch + "-tree-sha"),
			Truncated: github.Bool(true),
			Entries:   []*github.TreeEntry{},
		},
	}
}

// startParkedReadmeBlobServer starts a fixture server whose README blob
// route parks the request until the request context is done. The other
// routes respond normally, so DetailWithContext reaches the concurrent
// checks and the README blob retrieval is the outstanding request when the
// test cancels or expires the context.
func startParkedReadmeBlobServer(t *testing.T) (*github.Client, <-chan struct{}, func() []string) {
	t.Helper()
	record, recorded := newRecordingPaths()

	repoPrefix := "/repos/" + fixtureOwner + "/" + fixtureRepo
	readmeInFlight := make(chan struct{}, 1)

	writeJSON := func(w http.ResponseWriter, body string) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("X-RateLimit-Limit", "5000")
		w.Header().Set("X-RateLimit-Remaining", "4990")
		w.Header().Set("X-RateLimit-Reset", "1900000000")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(body))
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
		writeJSON(w, `{"sha":"`+fixtureBranch+`-tree-sha","tree":[`+
			`{"path":"CONTRIBUTING.md","sha":"`+contributingBlobSHA+`","mode":"100644","type":"blob"},`+
			`{"path":"README.md","sha":"`+readmeBlobSHA+`","mode":"100644","type":"blob"}]`+
			`,"truncated":false}`)
	})
	mux.HandleFunc(repoPrefix+"/git/blobs/"+readmeBlobSHA, func(w http.ResponseWriter, r *http.Request) {
		record(r)
		select {
		case readmeInFlight <- struct{}{}:
		default:
		}
		// Park until the request context is done; the client then reports
		// the context error for the outstanding request.
		<-r.Context().Done()
	})
	mux.HandleFunc(repoPrefix+"/git/blobs/"+contributingBlobSHA, func(w http.ResponseWriter, r *http.Request) {
		record(r)
		writeJSON(w, `{"encoding":"base64","content":"`+base64.StdEncoding.EncodeToString([]byte(contributingContent))+`","size":`+strconv.Itoa(len(contributingContent))+`}`)
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
	return client, readmeInFlight, recorded
}

// startRateLimitedServer starts a fixture server whose repository and
// repository-tree routes succeed, while the PR list and document blobs
// return a 403 with an exhausted core rate limit, which go-github
// classifies as *github.RateLimitError. When limitWorkflowBlobs is false
// the workflows-tree request is rate limited; when true the workflows tree
// succeeds and the workflow file blobs are rate limited instead.
func startRateLimitedServer(t *testing.T, limitWorkflowBlobs bool) (*github.Client, func() []string) {
	t.Helper()
	record, recorded := newRecordingPaths()

	repoPrefix := "/repos/" + fixtureOwner + "/" + fixtureRepo

	writeOK := func(w http.ResponseWriter, body string) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("X-RateLimit-Limit", "5000")
		w.Header().Set("X-RateLimit-Remaining", "4990")
		w.Header().Set("X-RateLimit-Reset", "1900000000")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(body))
	}
	// writeRateLimited returns a 403 with an exhausted core rate limit. The
	// reset is in the past so the client's cached-rate short circuit does
	// not block the remaining fixture requests.
	writeRateLimited := func(w http.ResponseWriter) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("X-RateLimit-Limit", "5000")
		w.Header().Set("X-RateLimit-Remaining", "0")
		w.Header().Set("X-RateLimit-Reset", "1000000000")
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"message":"API rate limit exceeded for 1.2.3.4.","documentation_url":"https://docs.github.com/rest/overview/resources-in-the-rest-api#rate-limiting"}`))
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/rate_limit", func(w http.ResponseWriter, r *http.Request) {
		record(r)
		failPreflight(w, r)
	})
	mux.HandleFunc(repoPrefix, func(w http.ResponseWriter, r *http.Request) {
		record(r)
		writeOK(w, `{"name":"`+fixtureRepo+`","full_name":"`+fixtureOwner+"/"+fixtureRepo+`","default_branch":"`+fixtureBranch+`"}`)
	})
	mux.HandleFunc(repoPrefix+"/git/trees/"+fixtureBranch, func(w http.ResponseWriter, r *http.Request) {
		record(r)
		writeOK(w, `{"sha":"`+fixtureBranch+`-tree-sha","tree":[`+
			`{"path":"CONTRIBUTING.md","sha":"`+contributingBlobSHA+`","mode":"100644","type":"blob"},`+
			`{"path":"README.md","sha":"`+readmeBlobSHA+`","mode":"100644","type":"blob"},`+
			`{"path":".github/workflows","sha":"`+workflowsEntrySHA+`","mode":"040000","type":"tree"}]`+
			`,"truncated":false}`)
	})
	mux.HandleFunc(repoPrefix+"/git/trees/"+workflowsEntrySHA, func(w http.ResponseWriter, r *http.Request) {
		record(r)
		if limitWorkflowBlobs {
			writeOK(w, `{"sha":"`+workflowsEntrySHA+`","tree":[`+
				`{"path":"zeta.yml","sha":"rate-limited-zeta-sha","mode":"100644","type":"blob"},`+
				`{"path":"alpha.yml","sha":"rate-limited-alpha-sha","mode":"100644","type":"blob"}]`+
				`,"truncated":false}`)
			return
		}
		writeRateLimited(w)
	})
	mux.HandleFunc(repoPrefix+"/git/blobs/", func(w http.ResponseWriter, r *http.Request) {
		record(r)
		writeRateLimited(w)
	})
	mux.HandleFunc(repoPrefix+"/pulls", func(w http.ResponseWriter, r *http.Request) {
		record(r)
		writeRateLimited(w)
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

// startPositiveActionFailingReadmeServer starts a fixture server whose
// workflows tree contains a positive cla-assistant workflow while the README
// blob request fails with a 500, so the positive Action evidence and the
// failing README check must both be retained.
func startPositiveActionFailingReadmeServer(t *testing.T) (*github.Client, func() []string) {
	t.Helper()
	record, recorded := newRecordingPaths()

	repoPrefix := "/repos/" + fixtureOwner + "/" + fixtureRepo

	writeOK := func(w http.ResponseWriter, body string) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("X-RateLimit-Limit", "5000")
		w.Header().Set("X-RateLimit-Remaining", "4990")
		w.Header().Set("X-RateLimit-Reset", "1900000000")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(body))
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/rate_limit", func(w http.ResponseWriter, r *http.Request) {
		record(r)
		failPreflight(w, r)
	})
	mux.HandleFunc(repoPrefix, func(w http.ResponseWriter, r *http.Request) {
		record(r)
		writeOK(w, `{"name":"`+fixtureRepo+`","full_name":"`+fixtureOwner+"/"+fixtureRepo+`","default_branch":"`+fixtureBranch+`"}`)
	})
	mux.HandleFunc(repoPrefix+"/git/trees/"+fixtureBranch, func(w http.ResponseWriter, r *http.Request) {
		record(r)
		writeOK(w, `{"sha":"`+fixtureBranch+`-tree-sha","tree":[`+
			`{"path":"CONTRIBUTING.md","sha":"`+contributingBlobSHA+`","mode":"100644","type":"blob"},`+
			`{"path":"README.md","sha":"`+readmeBlobSHA+`","mode":"100644","type":"blob"},`+
			`{"path":".github/workflows","sha":"`+workflowsEntrySHA+`","mode":"040000","type":"tree"}]`+
			`,"truncated":false}`)
	})
	mux.HandleFunc(repoPrefix+"/git/trees/"+workflowsEntrySHA, func(w http.ResponseWriter, r *http.Request) {
		record(r)
		writeOK(w, `{"sha":"`+workflowsEntrySHA+`","tree":[`+
			`{"path":"cla.yml","sha":"`+workflowBlobSHA+`","mode":"100644","type":"blob"}]`+
			`,"truncated":false}`)
	})
	mux.HandleFunc(repoPrefix+"/git/blobs/", func(w http.ResponseWriter, r *http.Request) {
		record(r)
		sha := strings.TrimPrefix(r.URL.Path, repoPrefix+"/git/blobs/")
		switch sha {
		case readmeBlobSHA:
			http.Error(w, "readme blob unavailable", http.StatusInternalServerError)
		case contributingBlobSHA:
			writeOK(w, `{"encoding":"base64","content":"`+base64.StdEncoding.EncodeToString([]byte(contributingContent))+`","size":`+strconv.Itoa(len(contributingContent))+`}`)
		case workflowBlobSHA:
			writeOK(w, `{"encoding":"base64","content":"`+base64.StdEncoding.EncodeToString([]byte(workflowContent))+`","size":`+strconv.Itoa(len(workflowContent))+`}`)
		default:
			http.Error(w, "unexpected blob requested: "+sha, http.StatusInternalServerError)
		}
	})
	mux.HandleFunc(repoPrefix+"/pulls", func(w http.ResponseWriter, r *http.Request) {
		record(r)
		writeOK(w, `[]`)
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

// TestErrorIdentityTreeAcquisitionCancellation verifies that a canceled
// context during the repository tree fetch keeps its identity through
// newChecker's wrapping.
func TestErrorIdentityTreeAcquisitionCancellation(t *testing.T) {
	_, client, _ := newFixtureServer(t, false)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := newChecker(ctx, client, fixtureOwner, fixtureRepo, fixtureBranch)
	if err == nil {
		t.Fatal("newChecker returned nil error; want the canceled context error")
	}
	if !errors.Is(err, context.Canceled) {
		t.Errorf("errors.Is(err, context.Canceled) = false; want true. err: %v", err)
	}
}

// TestErrorIdentityTreeAcquisitionDeadline verifies that an expired deadline
// during the repository tree fetch keeps its identity through newChecker's
// wrapping.
func TestErrorIdentityTreeAcquisitionDeadline(t *testing.T) {
	_, client, _ := newFixtureServer(t, false)
	ctx, cancel := context.WithTimeout(context.Background(), 0)
	defer cancel()

	_, err := newChecker(ctx, client, fixtureOwner, fixtureRepo, fixtureBranch)
	if err == nil {
		t.Fatal("newChecker returned nil error; want the deadline error")
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("errors.Is(err, context.DeadlineExceeded) = false; want true. err: %v", err)
	}
}

// TestErrorIdentityBlobRetrievalCancellation verifies that a canceled
// context during blob retrieval keeps its identity through both content
// helpers.
func TestErrorIdentityBlobRetrievalCancellation(t *testing.T) {
	_, client, _ := newFixtureServer(t, false)
	c := newFixtureChecker(client)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if _, err := c.contentAtPath(ctx, "README.md"); err == nil {
		t.Fatal("contentAtPath returned nil error; want the canceled context error")
	} else if !errors.Is(err, context.Canceled) {
		t.Errorf("errors.Is(err, context.Canceled) = false; want true. err: %v", err)
	}

	if _, err := c.contentAtSHA(ctx, readmeBlobSHA); err == nil {
		t.Fatal("contentAtSHA returned nil error; want the canceled context error")
	} else if !errors.Is(err, context.Canceled) {
		t.Errorf("errors.Is(err, context.Canceled) = false; want true. err: %v", err)
	}
}

// TestErrorIdentityBlobRetrievalDeadline verifies that an expired deadline
// during blob retrieval keeps its identity through both content helpers.
func TestErrorIdentityBlobRetrievalDeadline(t *testing.T) {
	_, client, _ := newFixtureServer(t, false)
	c := newFixtureChecker(client)
	ctx, cancel := context.WithTimeout(context.Background(), 0)
	defer cancel()

	if _, err := c.contentAtPath(ctx, "README.md"); err == nil {
		t.Fatal("contentAtPath returned nil error; want the deadline error")
	} else if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("errors.Is(err, context.DeadlineExceeded) = false; want true. err: %v", err)
	}

	if _, err := c.contentAtSHA(ctx, readmeBlobSHA); err == nil {
		t.Fatal("contentAtSHA returned nil error; want the deadline error")
	} else if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("errors.Is(err, context.DeadlineExceeded) = false; want true. err: %v", err)
	}
}

// TestErrorIdentityCancellationThroughPublicAggregate verifies that a
// cancellation while the README blob retrieval is in flight surfaces through
// the final public *Errors aggregate with its identity intact.
func TestErrorIdentityCancellationThroughPublicAggregate(t *testing.T) {
	client, readmeInFlight, _ := startParkedReadmeBlobServer(t)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	errCh := make(chan error, 1)
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		_, err := DetailWithContext(ctx, client, fixtureOwner, fixtureRepo)
		errCh <- err
	}()

	select {
	case <-readmeInFlight:
	case <-time.After(5 * time.Second):
		cancel()
		wg.Wait()
		t.Fatal("the README blob request never reached the server")
	}
	cancel()

	wg.Wait()
	err := <-errCh
	if err == nil {
		t.Fatal("DetailWithContext returned nil error; want the canceled context error")
	}
	if !errors.Is(err, context.Canceled) {
		t.Errorf("errors.Is(err, context.Canceled) = false; want true. err: %v", err)
	}
	var agg *Errors
	if !errors.As(err, &agg) {
		t.Fatalf("errors.As did not find the *Errors aggregate: %v", err)
	}
	if agg.InREADMEErr == nil {
		t.Errorf("InREADMEErr = nil; want the canceled blob retrieval error")
	}
}

// TestErrorIdentityDeadlineThroughPublicAggregate verifies that a deadline
// expiring while the README blob retrieval is in flight surfaces through the
// final public *Errors aggregate with its identity intact.
func TestErrorIdentityDeadlineThroughPublicAggregate(t *testing.T) {
	client, readmeInFlight, _ := startParkedReadmeBlobServer(t)

	// The deadline must outlast the repository and tree lookups but expire
	// while the README blob request is parked.
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	errCh := make(chan error, 1)
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		_, err := DetailWithContext(ctx, client, fixtureOwner, fixtureRepo)
		errCh <- err
	}()

	select {
	case <-readmeInFlight:
	case <-time.After(5 * time.Second):
		wg.Wait()
		t.Fatal("the README blob request never reached the server")
	}
	// Do not cancel; let the deadline expire while the blob request is parked.

	wg.Wait()
	err := <-errCh
	if err == nil {
		t.Fatal("DetailWithContext returned nil error; want the deadline error")
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("errors.Is(err, context.DeadlineExceeded) = false; want true. err: %v", err)
	}
	var agg *Errors
	if !errors.As(err, &agg) {
		t.Fatalf("errors.As did not find the *Errors aggregate: %v", err)
	}
	if agg.InREADMEErr == nil {
		t.Errorf("InREADMEErr = nil; want the deadline blob retrieval error")
	}
}

// TestErrorIdentityTruncatedTreeREADME verifies that the README path keeps
// the ErrTruncatedTree sentinel identity when the tree is truncated.
func TestErrorIdentityTruncatedTreeREADME(t *testing.T) {
	c := newTruncatedFixtureChecker()
	_, err := c.contentAtPath(context.Background(), "README.md")
	if err == nil {
		t.Fatal("contentAtPath returned nil error; want the truncated tree error")
	}
	if !errors.Is(err, ErrTruncatedTree) {
		t.Errorf("errors.Is(err, ErrTruncatedTree) = false; want true. err: %v", err)
	}
}

// TestErrorIdentityTruncatedTreeWorkflow verifies that the workflow path
// keeps the ErrTruncatedTree sentinel identity when the tree is truncated.
func TestErrorIdentityTruncatedTreeWorkflow(t *testing.T) {
	c := newTruncatedFixtureChecker()
	_, err := c.usesCLAAssistantAction(context.Background())
	if err == nil {
		t.Fatal("usesCLAAssistantAction returned nil error; want the truncated tree error")
	}
	if !errors.Is(err, ErrTruncatedTree) {
		t.Errorf("errors.Is(err, ErrTruncatedTree) = false; want true. err: %v", err)
	}
}

// startTruncatedTreeServer starts a fixture server whose repository tree is
// truncated and omits every entry, so every path lookup reports
// ErrTruncatedTree.
func startTruncatedTreeServer(t *testing.T) (*github.Client, func() []string) {
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
		writeJSON(w, `{"sha":"`+fixtureBranch+`-tree-sha","tree":[],"truncated":true}`)
	})
	// The truncated recursive tree triggers bounded recovery, which requests
	// the non-recursive root tree at the immutable root tree SHA. That
	// listing is itself truncated and omits every path, so the recovery
	// reports ErrTruncatedTree instead of inventing an answer.
	mux.HandleFunc(repoPrefix+"/git/trees/"+fixtureBranch+"-tree-sha", func(w http.ResponseWriter, r *http.Request) {
		record(r)
		writeJSON(w, `{"sha":"`+fixtureBranch+`-tree-sha","tree":[],"truncated":true}`)
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

// TestErrorIdentityTruncatedTreeThroughPublicAggregate verifies that a
// truncated repository tree surfaces the ErrTruncatedTree sentinel through
// the final public *Errors aggregate in every affected field.
func TestErrorIdentityTruncatedTreeThroughPublicAggregate(t *testing.T) {
	client, _ := startTruncatedTreeServer(t)

	details, err := Detail(client, fixtureOwner, fixtureRepo)
	if err == nil {
		t.Fatal("Detail returned nil error; want the truncated tree error")
	}
	if !errors.Is(err, ErrTruncatedTree) {
		t.Errorf("errors.Is(err, ErrTruncatedTree) = false; want true. err: %v", err)
	}
	var agg *Errors
	if !errors.As(err, &agg) {
		t.Fatalf("errors.As did not find the *Errors aggregate: %v", err)
	}
	// The truncated tree omits every path, so the bot-file, document, and
	// workflow checks all carry the sentinel.
	for name, fieldErr := range map[string]error{
		"BotFileErr":        agg.BotFileErr,
		"InContributingErr": agg.InContributingErr,
		"InREADMEErr":       agg.InREADMEErr,
		"ActionErr":         agg.ActionErr,
	} {
		if fieldErr == nil {
			t.Errorf("%s = nil; want the truncated tree error", name)
			continue
		}
		if !errors.Is(fieldErr, ErrTruncatedTree) {
			t.Errorf("errors.Is(%s, ErrTruncatedTree) = false; want true. err: %v", name, fieldErr)
		}
	}
	if details != (Details{}) {
		t.Errorf("Details = %+v; want zero value", details)
	}
}

// TestErrorIdentityRateLimitInEachField verifies that a typed
// *github.RateLimitError placed in each relevant aggregate field is
// recoverable through errors.As with its metadata intact.
func TestErrorIdentityRateLimitInEachField(t *testing.T) {
	client, _ := startRateLimitedServer(t, false)

	details, err := Detail(client, fixtureOwner, fixtureRepo)
	if err == nil {
		t.Fatal("Detail returned nil error; want the rate limit errors")
	}
	var agg *Errors
	if !errors.As(err, &agg) {
		t.Fatalf("errors.As did not find the *Errors aggregate: %v", err)
	}

	fields := map[string]error{
		"TagErr":            agg.TagErr,
		"InContributingErr": agg.InContributingErr,
		"InREADMEErr":       agg.InREADMEErr,
		"ActionErr":         agg.ActionErr,
	}
	for name, fieldErr := range fields {
		if fieldErr == nil {
			t.Errorf("%s = nil; want the rate limit error", name)
			continue
		}
		var rle *github.RateLimitError
		if !errors.As(fieldErr, &rle) {
			t.Errorf("errors.As(%s, *github.RateLimitError) = false: %v", name, fieldErr)
			continue
		}
		if rle.Rate.Remaining != 0 {
			t.Errorf("%s: RateLimitError.Rate.Remaining = %d; want 0", name, rle.Rate.Remaining)
		}
	}

	// The public aggregate itself must expose the typed error.
	var rle *github.RateLimitError
	if !errors.As(err, &rle) {
		t.Fatalf("errors.As(aggregate, *github.RateLimitError) = false: %v", err)
	}
	if rle.Rate.Remaining != 0 {
		t.Errorf("aggregate RateLimitError.Rate.Remaining = %d; want 0", rle.Rate.Remaining)
	}
	if details != (Details{}) {
		t.Errorf("Details = %+v; want zero value", details)
	}
}

// TestErrorIdentityRateLimitPointerRecovery verifies, through direct
// helpers, that errors.As recovers the original *github.RateLimitError
// pointer and its metadata from each populated field and from the
// aggregate.
func TestErrorIdentityRateLimitPointerRecovery(t *testing.T) {
	reset := time.Unix(1900000000, 0)
	newRateLimit := func(msg string) *github.RateLimitError {
		req, err := http.NewRequest(http.MethodGet, "https://api.github.com/repos/"+fixtureOwner+"/"+fixtureRepo, nil)
		if err != nil {
			t.Fatalf("building fixture request: %v", err)
		}
		return &github.RateLimitError{
			Rate:     github.Rate{Limit: 5000, Remaining: 0, Reset: github.Timestamp{Time: reset}},
			Response: &http.Response{StatusCode: http.StatusForbidden, Request: req},
			Message:  msg,
		}
	}

	tagErr := newRateLimit("tag rate limit")
	contribErr := newRateLimit("contributing rate limit")
	readmeErr := newRateLimit("readme rate limit")
	actionErr := newRateLimit("action rate limit")

	e := Errors{
		TagErr:            fmt.Errorf("error getting %s/%s PRs: %w", fixtureOwner, fixtureRepo, tagErr),
		InContributingErr: fmt.Errorf("failed to check CONTRIBUTING.md: %w", contribErr),
		InREADMEErr:       fmt.Errorf("failed to check README.md: %w", readmeErr),
		ActionErr:         fmt.Errorf("failed to get workflows tree: %w", actionErr),
	}
	agg := e.ErrOrNil()
	if agg == nil {
		t.Fatal("ErrOrNil returned nil; want the populated aggregate")
	}

	cases := []struct {
		name  string
		field error
		want  *github.RateLimitError
	}{
		{"TagErr", e.TagErr, tagErr},
		{"InContributingErr", e.InContributingErr, contribErr},
		{"InREADMEErr", e.InREADMEErr, readmeErr},
		{"ActionErr", e.ActionErr, actionErr},
	}
	for _, tc := range cases {
		var got *github.RateLimitError
		if !errors.As(tc.field, &got) {
			t.Errorf("errors.As(%s, *github.RateLimitError) = false; want the original pointer", tc.name)
			continue
		}
		if got != tc.want {
			t.Errorf("errors.As(%s) recovered %p; want the original pointer %p", tc.name, got, tc.want)
		}
		if got.Rate.Remaining != 0 || !got.Rate.Reset.Time.Equal(reset) || got.Message != tc.want.Message {
			t.Errorf("errors.As(%s) lost metadata: %+v", tc.name, got)
		}
	}

	// The aggregate recovers the first populated field's cause in field
	// order.
	var first *github.RateLimitError
	if !errors.As(agg, &first) {
		t.Fatalf("errors.As(aggregate, *github.RateLimitError) = false; want the rate limit error")
	}
	if first != tagErr {
		t.Errorf("errors.As(aggregate) recovered %p; want the TagErr cause %p (field order)", first, tagErr)
	}
}

// TestErrorIdentityRateLimitInWorkflowAggregate verifies that a typed
// *github.RateLimitError inside the workflow error aggregate is recoverable
// through errors.As, and that the diagnostics keep both paths in sorted
// order.
func TestErrorIdentityRateLimitInWorkflowAggregate(t *testing.T) {
	client, _ := startRateLimitedServer(t, true)

	details, err := Detail(client, fixtureOwner, fixtureRepo)
	if err == nil {
		t.Fatal("Detail returned nil error; want the rate limit errors")
	}
	var agg *Errors
	if !errors.As(err, &agg) {
		t.Fatalf("errors.As did not find the *Errors aggregate: %v", err)
	}
	if agg.ActionErr == nil {
		t.Fatal("ActionErr = nil; want the workflow error aggregate")
	}

	// The workflow aggregate must expose the typed rate limit error.
	var rle *github.RateLimitError
	if !errors.As(agg.ActionErr, &rle) {
		t.Fatalf("errors.As(ActionErr, *github.RateLimitError) = false: %v", agg.ActionErr)
	}
	if rle.Rate.Remaining != 0 {
		t.Errorf("workflow aggregate RateLimitError.Rate.Remaining = %d; want 0", rle.Rate.Remaining)
	}

	// And the public aggregate must expose it too.
	var rle2 *github.RateLimitError
	if !errors.As(err, &rle2) {
		t.Fatalf("errors.As(aggregate, *github.RateLimitError) = false: %v", err)
	}

	// The diagnostics must mention both workflow paths in sorted order.
	msg := agg.ActionErr.Error()
	idxAlpha := strings.Index(msg, "alpha.yml")
	idxZeta := strings.Index(msg, "zeta.yml")
	if idxAlpha < 0 || idxZeta < 0 {
		t.Errorf("workflow diagnostics do not mention both paths: %s", msg)
	} else if idxAlpha > idxZeta {
		t.Errorf("workflow diagnostics are not in sorted path order: %s", msg)
	}
	if details != (Details{}) {
		t.Errorf("Details = %+v; want zero value", details)
	}
}

// TestErrorIdentityTwoDistinctCauses verifies that an aggregate carrying two
// simultaneous distinct causes exposes both through errors.Is, without
// cross-contaminating the individual fields.
func TestErrorIdentityTwoDistinctCauses(t *testing.T) {
	e := Errors{
		TagErr:      fmt.Errorf("error getting %s/%s PRs: %w", fixtureOwner, fixtureRepo, context.Canceled),
		InREADMEErr: fmt.Errorf("tree was truncated and README.md was possibly missed: %w", ErrTruncatedTree),
	}
	agg := e.ErrOrNil()
	if agg == nil {
		t.Fatal("ErrOrNil returned nil; want the populated aggregate")
	}

	if !errors.Is(agg, context.Canceled) {
		t.Errorf("errors.Is(agg, context.Canceled) = false; want true")
	}
	if !errors.Is(agg, ErrTruncatedTree) {
		t.Errorf("errors.Is(agg, ErrTruncatedTree) = false; want true")
	}
	// Each field carries its own cause.
	if !errors.Is(e.TagErr, context.Canceled) {
		t.Errorf("errors.Is(TagErr, context.Canceled) = false; want true")
	}
	if !errors.Is(e.InREADMEErr, ErrTruncatedTree) {
		t.Errorf("errors.Is(InREADMEErr, ErrTruncatedTree) = false; want true")
	}
	// Cross-field: TagErr does not carry the truncation sentinel, and
	// InREADMEErr does not carry cancellation.
	if errors.Is(e.TagErr, ErrTruncatedTree) {
		t.Errorf("errors.Is(TagErr, ErrTruncatedTree) = true; want false")
	}
	if errors.Is(e.InREADMEErr, context.Canceled) {
		t.Errorf("errors.Is(InREADMEErr, context.Canceled) = true; want false")
	}
}

// TestErrorIdentityUnrelatedTargetNegatives verifies that an aggregate does
// not match sentinels or types it does not carry.
func TestErrorIdentityUnrelatedTargetNegatives(t *testing.T) {
	e := Errors{
		TagErr: fmt.Errorf("error getting PRs: %w", context.Canceled),
	}
	agg := e.ErrOrNil()
	if agg == nil {
		t.Fatal("ErrOrNil returned nil; want the populated aggregate")
	}

	if errors.Is(agg, ErrNotFound) {
		t.Errorf("errors.Is(agg, ErrNotFound) = true; want false")
	}
	if errors.Is(agg, ErrInvalidToken) {
		t.Errorf("errors.Is(agg, ErrInvalidToken) = true; want false")
	}
	if errors.Is(agg, ErrTruncatedTree) {
		t.Errorf("errors.Is(agg, ErrTruncatedTree) = true; want false")
	}
	if errors.Is(agg, context.DeadlineExceeded) {
		t.Errorf("errors.Is(agg, context.DeadlineExceeded) = true; want false")
	}
	var rle *github.RateLimitError
	if errors.As(agg, &rle) {
		t.Errorf("errors.As(agg, *github.RateLimitError) = true; want false")
	}
	var ge *github.ErrorResponse
	if errors.As(agg, &ge) {
		t.Errorf("errors.As(agg, *github.ErrorResponse) = true; want false")
	}
}

// TestErrorIdentityNilFields verifies that an aggregate with only one
// populated field traverses safely: nil fields neither panic nor match, and
// both the value and pointer forms of Errors support traversal.
func TestErrorIdentityNilFields(t *testing.T) {
	e := Errors{
		InREADMEErr: fmt.Errorf("failed to check README.md: %w", context.Canceled),
	}
	agg := e.ErrOrNil()
	if agg == nil {
		t.Fatal("ErrOrNil returned nil; want the populated aggregate")
	}

	// The populated field is discoverable.
	if !errors.Is(agg, context.Canceled) {
		t.Errorf("errors.Is(agg, context.Canceled) = false; want true")
	}
	// Nil fields do not panic and do not match.
	if errors.Is(agg, ErrTruncatedTree) {
		t.Errorf("errors.Is(agg, ErrTruncatedTree) = true; want false")
	}
	var rle *github.RateLimitError
	if errors.As(agg, &rle) {
		t.Errorf("errors.As(agg, *github.RateLimitError) = true; want false")
	}

	// A value Errors (not just *Errors) also traverses.
	if !errors.Is(e, context.Canceled) {
		t.Errorf("errors.Is(value Errors, context.Canceled) = false; want true")
	}
	if errors.Is(e, ErrNotFound) {
		t.Errorf("errors.Is(value Errors, ErrNotFound) = true; want false")
	}
}

// TestErrorIdentityEmptyErrOrNil verifies that an empty aggregate still
// yields a nil error from ErrOrNil.
func TestErrorIdentityEmptyErrOrNil(t *testing.T) {
	e := Errors{}
	if err := e.ErrOrNil(); err != nil {
		t.Errorf("ErrOrNil on an empty aggregate = %v; want nil", err)
	}
}

// TestErrorIdentityWorkflowDiagnosticsStableOrder verifies that repeated
// workflow diagnostics render identically and list the failing paths in
// sorted order, regardless of map iteration order.
func TestErrorIdentityWorkflowDiagnosticsStableOrder(t *testing.T) {
	client, _ := startWorkflowsFixtureServer(t, workflowsFixtureConfig{
		entry: workflowsEntryTree,
		treeEntries: []string{
			`{"path":"zeta.yml","sha":"missing-zeta-sha","mode":"100644","type":"blob"}`,
			`{"path":"alpha.yml","sha":"missing-alpha-sha","mode":"100644","type":"blob"}`,
			`{"path":"mid.yaml","sha":"missing-mid-sha","mode":"100644","type":"blob"}`,
		},
	})

	var first string
	for i := 0; i < 5; i++ {
		_, err := Detail(client, fixtureOwner, fixtureRepo)
		if err == nil {
			t.Fatal("Detail returned nil error; want the workflow diagnostics")
		}
		if i == 0 {
			first = err.Error()
		} else if err.Error() != first {
			t.Fatalf("run %d rendered differently:\n---\n%s\n---\nvs\n---\n%s\n---", i, err.Error(), first)
		}
	}

	idxAlpha := strings.Index(first, "alpha.yml")
	idxMid := strings.Index(first, "mid.yaml")
	idxZeta := strings.Index(first, "zeta.yml")
	if !(idxAlpha >= 0 && idxAlpha < idxMid && idxMid < idxZeta) {
		t.Errorf("workflow diagnostics are not in sorted path order:\n%s", first)
	}
}

// TestErrorIdentityPositiveHeuristicWithFailingCheck verifies that a
// positive workflow match is retained as Details evidence while an
// independent failing check still surfaces a recognizable error.
func TestErrorIdentityPositiveHeuristicWithFailingCheck(t *testing.T) {
	client, _ := startPositiveActionFailingReadmeServer(t)

	details, err := Detail(client, fixtureOwner, fixtureRepo)
	if err == nil {
		t.Fatal("Detail returned nil error; want the failing README check to surface")
	}
	if !details.Action {
		t.Errorf("Action = false; the positive workflow match must be retained")
	}
	if details.InREADME {
		t.Errorf("InREADME = true; want false (the README blob failed)")
	}

	var agg *Errors
	if !errors.As(err, &agg) {
		t.Fatalf("errors.As did not find the *Errors aggregate: %v", err)
	}
	if agg.InREADMEErr == nil {
		t.Errorf("InREADMEErr = nil; want the failing README blob error")
	}
	if agg.ActionErr != nil {
		t.Errorf("ActionErr = %v; the positive workflow match must not be an error", agg.ActionErr)
	}
	// The failing check's error must remain recognizable.
	var ge *github.ErrorResponse
	if !errors.As(err, &ge) {
		t.Errorf("errors.As(aggregate, *github.ErrorResponse) = false; want the 500 to be recognizable: %v", err)
	}
}
