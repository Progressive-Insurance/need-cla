/*
Copyright (c) 2021-2022 Progressive Casualty Insurance Company. All rights reserved.

Use of this source code is governed by an MIT license that can be found in
the LICENSE file at https://opensource.progressive.com/resources/license
*/

package needcla

import (
	"context"
	"encoding/base64"
	"encoding/json"
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

// fixtureOwner is intentionally absent from knownOwners so results depend on
// repository contents rather than the owner allowlist.
const fixtureOwner = "fixture-owner"

const (
	fixtureRepo         = "fixture-repo"
	fixtureBranch       = "main"
	workflowsTreeSHA    = "workflows-tree-sha"
	contributingBlobSHA = "contributing-blob-sha"
	readmeBlobSHA       = "readme-blob-sha"
	clabotBlobSHA       = "clabot-blob-sha"

	contributingContent = "Pull requests require at least one approving review before they can be merged."
	readmeContent       = "A small utility that inspects a repository and reports whether contributors appear to need extra paperwork before merging."
)

// startFixtureServer starts an httptest.Server modeling the GitHub API
// surface used by Check and Detail, and returns a go-github v43 client
// pointed at it. Every response is a local fixture: no credentials and no
// external network are required. Routes that are not modeled return 500 so
// fixture mistakes surface as errors instead of passing silently.
func startFixtureServer(positive bool) (*httptest.Server, *github.Client, func() []string) {
	return startFixtureServerWithContent(fixtureOwner, positive, contributingContent, readmeContent)
}

// startFixtureServerWithContent is startFixtureServer with caller-supplied
// CONTRIBUTING.md and README.md blob contents, so the document checks can be
// exercised against specific text.
func startFixtureServerWithContent(owner string, positive bool, contributing, readme string) (*httptest.Server, *github.Client, func() []string) {
	return startFixtureServerWithPulls(owner, positive, contributing, readme, `[]`)
}

// startFixtureServerWithPulls is startFixtureServerWithContent with a
// caller-supplied pull request list body, so the PR label check can be
// exercised against specific labels. The owner is caller-supplied so cases
// that depend on the owner allowlist can use a different account name.
func startFixtureServerWithPulls(owner string, positive bool, contributing, readme, pullsBody string) (*httptest.Server, *github.Client, func() []string) {
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

	repoPrefix := "/repos/" + owner + "/" + fixtureRepo
	blobs := map[string]string{
		contributingBlobSHA: contributing,
		readmeBlobSHA:       readme,
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/rate_limit", func(w http.ResponseWriter, r *http.Request) {
		record(r)
		failPreflight(w, r)
	})
	mux.HandleFunc(repoPrefix, func(w http.ResponseWriter, r *http.Request) {
		record(r)
		writeJSON(w, `{"name":"`+fixtureRepo+`","full_name":"`+owner+"/"+fixtureRepo+`","default_branch":"`+fixtureBranch+`"}`)
	})
	mux.HandleFunc(repoPrefix+"/git/trees/"+fixtureBranch, func(w http.ResponseWriter, r *http.Request) {
		record(r)
		if r.URL.Query().Get("recursive") != "1" {
			http.Error(w, "repository tree must be requested recursively", http.StatusInternalServerError)
			return
		}
		entries := []string{
			`{"path":".github/workflows","sha":"` + workflowsTreeSHA + `","mode":"040000","type":"tree"}`,
			`{"path":"CONTRIBUTING.md","sha":"` + contributingBlobSHA + `","mode":"100644","type":"blob"}`,
			`{"path":"README.md","sha":"` + readmeBlobSHA + `","mode":"100644","type":"blob"}`,
		}
		if positive {
			entries = append(entries, `{"path":".clabot","sha":"`+clabotBlobSHA+`","mode":"100644","type":"blob"}`)
		}
		writeJSON(w, `{"sha":"`+fixtureBranch+`-tree-sha","tree":[`+strings.Join(entries, ",")+`],"truncated":false}`)
	})
	mux.HandleFunc(repoPrefix+"/git/trees/"+workflowsTreeSHA, func(w http.ResponseWriter, r *http.Request) {
		record(r)
		writeJSON(w, `{"sha":"`+workflowsTreeSHA+`","tree":[],"truncated":false}`)
	})
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
		writeJSON(w, pullsBody)
	})
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		record(r)
		http.Error(w, "unexpected request: "+r.URL.Path, http.StatusInternalServerError)
	})

	server := httptest.NewServer(mux)
	client := github.NewClient(nil)
	baseURL, err := url.Parse(server.URL + "/")
	if err != nil {
		server.Close()
		panic(err)
	}
	client.BaseURL = baseURL
	return server, client, recorded
}

// newFixtureServer is startFixtureServer with the server closed through a
// test cleanup hook.
func newFixtureServer(t *testing.T, positive bool) (*httptest.Server, *github.Client, func() []string) {
	t.Helper()
	server, client, recorded := startFixtureServer(positive)
	t.Cleanup(server.Close)
	return server, client, recorded
}

// newFixtureServerWithContent is newFixtureServer with caller-supplied
// document contents.
func newFixtureServerWithContent(t *testing.T, positive bool, contributing, readme string) (*httptest.Server, *github.Client, func() []string) {
	t.Helper()
	server, client, recorded := startFixtureServerWithContent(fixtureOwner, positive, contributing, readme)
	t.Cleanup(server.Close)
	return server, client, recorded
}

// failPreflight fails any /rate_limit request immediately so a resurrected
// preflight cannot pass silently. The speculative quota gate was removed:
// scans must start with the real repository lookup and surface actual
// request outcomes instead of a cached quota guess.
func failPreflight(w http.ResponseWriter, r *http.Request) {
	http.Error(w, "preflight /rate_limit request is not allowed", http.StatusInternalServerError)
}

func samePathSet(a, b []string) bool {
	setA := make(map[string]bool, len(a))
	setB := make(map[string]bool, len(b))
	for _, p := range a {
		setA[p] = true
	}
	for _, p := range b {
		setB[p] = true
	}
	if len(setA) != len(setB) {
		return false
	}
	for p := range setA {
		if !setB[p] {
			return false
		}
	}
	return true
}

func expectedFixturePaths() []string {
	repoPrefix := "/repos/" + fixtureOwner + "/" + fixtureRepo
	return []string{
		repoPrefix,
		repoPrefix + "/git/trees/" + fixtureBranch + "?recursive=1",
		repoPrefix + "/git/trees/" + workflowsTreeSHA,
		repoPrefix + "/git/blobs/" + contributingBlobSHA,
		repoPrefix + "/git/blobs/" + readmeBlobSHA,
		repoPrefix + "/pulls?per_page=100&state=all",
	}
}

func TestDetailWithFixture(t *testing.T) {
	for _, tc := range []struct {
		name     string
		positive bool
		want     Details
	}{
		{name: "positive", positive: true, want: Details{BotFile: true}},
		{name: "negative", positive: false, want: Details{}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, client, recorded := newFixtureServer(t, tc.positive)

			details, err := Detail(client, fixtureOwner, fixtureRepo)
			if err != nil {
				t.Fatalf("Detail returned unexpected error: %v", err)
			}
			if details != tc.want {
				t.Errorf("Detail = %+v; want %+v", details, tc.want)
			}
			if got := details.Required(); got != tc.positive {
				t.Errorf("Required() = %v; want %v", got, tc.positive)
			}

			required, err := Check(client, fixtureOwner, fixtureRepo)
			if err != nil {
				t.Fatalf("Check returned unexpected error: %v", err)
			}
			if required != tc.positive {
				t.Errorf("Check = %v; want %v", required, tc.positive)
			}

			if got := recorded(); !samePathSet(got, expectedFixturePaths()) {
				t.Errorf("recorded requests %v; want %v", got, expectedFixturePaths())
			}
		})
	}
}

func ExampleCheck() {
	server, client, _ := startFixtureServer(true)
	defer server.Close()

	required, err := Check(client, fixtureOwner, fixtureRepo)
	if err != nil {
		fmt.Println("error:", err)
		return
	}
	if required {
		fmt.Println("A CLA is required.")
		return
	}
	fmt.Println("A CLA is not required.")
	// Output: A CLA is required.
}

// repositoryStageFailure is a transport-level sentinel returned by the
// round tripper while the repository request is in flight.
var repositoryStageFailure = errors.New("repository request failed at the transport")

// recordingTransport records every request it makes and lets tests inject a
// failure that surfaces only while the repository request is outstanding.
type recordingTransport struct {
	mu       sync.Mutex
	paths    []string
	failRepo error
}

func (rt *recordingTransport) Record() []string {
	rt.mu.Lock()
	defer rt.mu.Unlock()
	return append([]string(nil), rt.paths...)
}

func (rt *recordingTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	rt.mu.Lock()
	rt.paths = append(rt.paths, r.URL.RequestURI())
	failRepo := rt.failRepo
	rt.mu.Unlock()
	if r.URL.Path == "/repos/"+fixtureOwner+"/"+fixtureRepo {
		if failRepo != nil {
			return nil, failRepo
		}
		select {
		case <-r.Context().Done():
			return nil, r.Context().Err()
		default:
		}
	}
	return http.DefaultTransport.RoundTrip(r)
}

// startRepoFailureServer starts an httptest.Server whose only modeled route
// is the repository lookup. Any /rate_limit request fails immediately (the
// preflight was removed) and all other routes return 500 so any request made
// beyond the repository lookup is recorded as a fixture failure. The client's
// round tripper is wrapped in a recordingTransport.
func startRepoFailureServer(t *testing.T, transport *recordingTransport) (*github.Client, func() []string) {
	t.Helper()

	mux := http.NewServeMux()
	mux.HandleFunc("/rate_limit", func(w http.ResponseWriter, r *http.Request) {
		failPreflight(w, r)
	})
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "unexpected request: "+r.URL.Path, http.StatusInternalServerError)
	})

	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)

	client := github.NewClient(&http.Client{Transport: transport})
	baseURL, err := url.Parse(server.URL + "/")
	if err != nil {
		panic(err)
	}
	client.BaseURL = baseURL
	return client, transport.Record
}

// expectedRepoFailureRequests is the exact ordered request sequence that a
// repository lookup failure must produce: the repository lookup and nothing
// else. The /rate_limit preflight was removed, so a resurrected preflight or
// any downstream request (tree, blob, PR, ...) fails the assertion.
func expectedRepoFailureRequests() []string {
	return []string{
		"/repos/" + fixtureOwner + "/" + fixtureRepo,
	}
}

// sameRequestSequence reports whether a and b contain the same request
// paths in the same order.
func sameRequestSequence(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// assertOnlyRepoLookup asserts that the recorded requests are exactly the
// repository lookup and nothing else. Any other request (a resurrected
// /rate_limit preflight, or a downstream tree, blob, PR, ...) fails the
// assertion.
func assertOnlyRepoLookup(t *testing.T, rec []string) {
	t.Helper()
	want := expectedRepoFailureRequests()
	if !sameRequestSequence(rec, want) {
		t.Errorf("recorded requests %v; want exactly %v", rec, want)
	}
}

// TestRequestSequencePredicateRejectsDownstreamRequests pins the assertion
// used by the repository-failure cases: a recorded tree request or PR
// request after the repository lookup must be rejected, not filtered away.
func TestRequestSequencePredicateRejectsDownstreamRequests(t *testing.T) {
	repoPrefix := "/repos/" + fixtureOwner + "/" + fixtureRepo
	want := expectedRepoFailureRequests()

	cases := []struct {
		name string
		rec  []string
		want bool
	}{
		{
			name: "exact repository lookup",
			rec:  []string{repoPrefix},
			want: true,
		},
		{
			name: "tree request after lookup is rejected",
			rec:  []string{repoPrefix, repoPrefix + "/git/trees/" + fixtureBranch + "?recursive=1"},
			want: false,
		},
		{
			name: "PR request after lookup is rejected",
			rec:  []string{repoPrefix, repoPrefix + "/pulls?per_page=100&state=all"},
			want: false,
		},
		{
			name: "blob request after lookup is rejected",
			rec:  []string{repoPrefix, repoPrefix + "/git/blobs/" + contributingBlobSHA},
			want: false,
		},
		{
			name: "resurrected preflight before lookup is rejected",
			rec:  []string{"/rate_limit", repoPrefix},
			want: false,
		},
		{
			name: "out-of-order requests are rejected",
			rec:  []string{repoPrefix + "/git/trees/" + fixtureBranch + "?recursive=1", repoPrefix},
			want: false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := sameRequestSequence(tc.rec, want); got != tc.want {
				t.Errorf("sameRequestSequence(%v, %v) = %v; want %v", tc.rec, want, got, tc.want)
			}
		})
	}
}

func TestDetailRepositoryTransportFailure(t *testing.T) {
	rt := &recordingTransport{failRepo: repositoryStageFailure}
	client, rec := startRepoFailureServer(t, rt)

	details, err := Detail(client, fixtureOwner, fixtureRepo)
	if err == nil {
		t.Fatalf("Detail returned nil error; want the transport failure")
	}
	if !errors.Is(err, repositoryStageFailure) {
		t.Errorf("errors.Is(err, repositoryStageFailure) = false; want true. err: %v", err)
	}
	if details.Required() {
		t.Errorf("Details.Required() = true on lookup failure; want false")
	}
	assertOnlyRepoLookup(t, rec())
}

func TestDetailRepositoryCancelInFlight(t *testing.T) {
	rt := &recordingTransport{}
	client, rec := startRepoFailureServer(t, rt)

	repoPrefix := "/repos/" + fixtureOwner + "/" + fixtureRepo
	repoInFlight := make(chan struct{})
	releaseRepo := make(chan struct{})

	mux := http.NewServeMux()
	mux.HandleFunc("/rate_limit", func(w http.ResponseWriter, r *http.Request) {
		failPreflight(w, r)
	})
	mux.HandleFunc(repoPrefix, func(w http.ResponseWriter, r *http.Request) {
		close(repoInFlight)
		// Park the repository request in flight until the test releases it.
		// The client context is canceled while we are parked here, which is
		// what exercises the repository stage of DetailWithContext.
		select {
		case <-releaseRepo:
		case <-r.Context().Done():
		}
	})
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "unexpected request: "+r.URL.Path, http.StatusInternalServerError)
	})
	server := httptest.NewServer(mux)
	t.Cleanup(func() {
		close(releaseRepo)
		server.Close()
	})
	client.BaseURL, _ = url.Parse(server.URL + "/")

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
	case <-repoInFlight:
	case <-time.After(5 * time.Second):
		cancel()
		wg.Wait()
		t.Fatal("the repository request never reached the server")
	}
	cancel()

	wg.Wait()
	err := <-errCh
	if err == nil {
		t.Fatalf("DetailWithContext returned nil error; want context.Canceled")
	}
	if !errors.Is(err, context.Canceled) {
		t.Errorf("errors.Is(err, context.Canceled) = false; want true. err: %v", err)
	}
	assertOnlyRepoLookup(t, rec())
}

func TestDetailRepositoryServerFailure(t *testing.T) {
	t.Run("server error 500", func(t *testing.T) {
		rt := &recordingTransport{}
		client, rec := startRepoFailureServer(t, rt)
		client.BaseURL, _ = url.Parse(serverWithRepoBody(t, http.StatusInternalServerError, `{"message":"Internal Server Error"}`))

		_, err := Detail(client, fixtureOwner, fixtureRepo)
		if err == nil {
			t.Fatalf("Detail returned nil error; want the server error")
		}
		if !strings.Contains(err.Error(), "500") {
			t.Errorf("error does not carry the server failure: %v", err)
		}
		var ge *github.ErrorResponse
		if !errors.As(err, &ge) {
			t.Errorf("errors.As did not find *github.ErrorResponse: %v", err)
		}
		if ge == nil || ge.Response == nil || ge.Response.StatusCode != http.StatusInternalServerError {
			t.Errorf("underlying *github.ErrorResponse is wrong: %+v", ge)
		}
		assertOnlyRepoLookup(t, rec())
	})

	t.Run("malformed 200 body", func(t *testing.T) {
		rt := &recordingTransport{}
		client, rec := startRepoFailureServer(t, rt)
		client.BaseURL, _ = url.Parse(serverWithRepoBody(t, http.StatusOK, `{not-json`))

		details, err := Detail(client, fixtureOwner, fixtureRepo)
		if err == nil {
			t.Fatalf("Detail returned nil error; want the decode error")
		}
		if !strings.Contains(err.Error(), "invalid character") {
			t.Errorf("error does not carry the decode failure: %v", err)
		}
		var syntaxErr *json.SyntaxError
		if !errors.As(err, &syntaxErr) {
			t.Errorf("errors.As did not find *json.SyntaxError; the decoder error identity was not preserved. err: %v", err)
		}
		if details != (Details{}) {
			t.Errorf("Details = %+v; want zero value", details)
		}
		assertOnlyRepoLookup(t, rec())
	})
}

// serverWithRepoBody starts a fixture server whose repository route returns
// the given status and body, and returns its base URL. Any /rate_limit
// request fails immediately (the preflight was removed) and all other routes
// return 500.
func serverWithRepoBody(t *testing.T, status int, body string) string {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/rate_limit", func(w http.ResponseWriter, r *http.Request) {
		failPreflight(w, r)
	})
	mux.HandleFunc("/repos/"+fixtureOwner+"/"+fixtureRepo, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	})
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "unexpected request: "+r.URL.Path, http.StatusInternalServerError)
	})
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)
	return server.URL + "/"
}

func TestDetailRepositoryNotFoundAndUnauthorized(t *testing.T) {
	cases := []struct {
		name     string
		status   int
		body     string
		wantSent error
	}{
		{name: "404 is ErrNotFound", status: http.StatusNotFound, body: `{"message":"Not Found"}`, wantSent: ErrNotFound},
		{name: "401 is ErrInvalidToken", status: http.StatusUnauthorized, body: `{"message":"Bad credentials"}`, wantSent: ErrInvalidToken},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rt := &recordingTransport{}
			client, rec := startRepoFailureServer(t, rt)
			client.BaseURL, _ = url.Parse(serverWithRepoBody(t, tc.status, tc.body))

			details, err := Detail(client, fixtureOwner, fixtureRepo)
			if err == nil {
				t.Fatalf("Detail returned nil error; want %v", tc.wantSent)
			}
			if !errors.Is(err, tc.wantSent) {
				t.Errorf("errors.Is(err, %v) = false; want true. err: %v", tc.wantSent, err)
			}
			var ge *github.ErrorResponse
			if !errors.As(err, &ge) || ge == nil {
				t.Fatalf("errors.As did not find *github.ErrorResponse: %v", err)
			}
			if ge.Response.StatusCode != tc.status {
				t.Errorf("underlying status = %d; want %d", ge.Response.StatusCode, tc.status)
			}
			if details != (Details{}) {
				t.Errorf("Details = %+v; want zero value", details)
			}
			assertOnlyRepoLookup(t, rec())
		})
	}
}

func TestDetailRepositoryForbiddenVariants(t *testing.T) {
	cases := []struct {
		name     string
		headers  map[string]string
		body     string
		wantType string
	}{
		{
			name:     "permission 403",
			headers:  map[string]string{"X-RateLimit-Remaining": "4990"},
			body:     `{"message":"You are not permitted to view this repository.","documentation_url":"https://docs.github.com/rest/reference/repos#get-a-repository"}`,
			wantType: "github.ErrorResponse",
		},
		{
			name:     "primary rate limit 403",
			headers:  map[string]string{"X-RateLimit-Remaining": "0", "X-RateLimit-Reset": "1900000000"},
			body:     `{"message":"API rate limit exceeded for 1.2.3.4.","documentation_url":"https://docs.github.com/rest/overview/resources-in-the-rest-api#rate-limiting"}`,
			wantType: "github.RateLimitError",
		},
		{
			name:     "secondary rate limit 403",
			headers:  map[string]string{"Retry-After": "30"},
			body:     `{"message":"You have exceeded a secondary rate limit.","documentation_url":"https://docs.github.com/rest/overview/resources-in-the-rest-api#secondary-rate-limits"}`,
			wantType: "github.AbuseRateLimitError",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rt := &recordingTransport{}
			client, rec := startRepoFailureServer(t, rt)

			mux := http.NewServeMux()
			mux.HandleFunc("/rate_limit", func(w http.ResponseWriter, r *http.Request) {
				failPreflight(w, r)
			})
			mux.HandleFunc("/repos/"+fixtureOwner+"/"+fixtureRepo, func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				for k, v := range tc.headers {
					w.Header().Set(k, v)
				}
				w.WriteHeader(http.StatusForbidden)
				_, _ = w.Write([]byte(tc.body))
			})
			mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
				http.Error(w, "unexpected request: "+r.URL.Path, http.StatusInternalServerError)
			})
			server := httptest.NewServer(mux)
			t.Cleanup(server.Close)
			client.BaseURL, _ = url.Parse(server.URL + "/")

			details, err := Detail(client, fixtureOwner, fixtureRepo)
			if err == nil {
				t.Fatalf("Detail returned nil error; want the 403 to surface")
			}
			if errors.Is(err, ErrInvalidToken) {
				t.Fatalf("errors.Is(err, ErrInvalidToken) = true; a %s must not map to ErrInvalidToken. err: %v", tc.name, err)
			}
			var as error
			switch tc.wantType {
			case "github.ErrorResponse":
				var ge *github.ErrorResponse
				if !errors.As(err, &ge) {
					t.Fatalf("errors.As did not find *github.ErrorResponse: %v", err)
				}
				as = ge
			case "github.RateLimitError":
				var rle *github.RateLimitError
				if !errors.As(err, &rle) {
					t.Fatalf("errors.As did not find *github.RateLimitError: %v", err)
				}
				as = rle
			case "github.AbuseRateLimitError":
				var ale *github.AbuseRateLimitError
				if !errors.As(err, &ale) {
					t.Fatalf("errors.As did not find *github.AbuseRateLimitError: %v", err)
				}
				as = ale
			}
			if !strings.Contains(err.Error(), "403") {
				t.Errorf("error does not carry the 403 status: %v", err)
			}
			if details != (Details{}) {
				t.Errorf("Details = %+v; want zero value", details)
			}
			assertOnlyRepoLookup(t, rec())
			_ = as
		})
	}
}

func TestDetailRepositoryDefaultBranch(t *testing.T) {
	t.Run("uses develop default branch", func(t *testing.T) {
		rt := &recordingTransport{}
		client, rec := startRepoFailureServer(t, rt)

		repoPrefix := "/repos/" + fixtureOwner + "/" + fixtureRepo
		mux := http.NewServeMux()
		writeJSON := func(w http.ResponseWriter, body string) {
			w.Header().Set("Content-Type", "application/json")
			w.Header().Set("X-RateLimit-Limit", "5000")
			w.Header().Set("X-RateLimit-Remaining", "4990")
			w.Header().Set("X-RateLimit-Reset", "1900000000")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(body))
		}
		mux.HandleFunc("/rate_limit", func(w http.ResponseWriter, r *http.Request) {
			failPreflight(w, r)
		})
		mux.HandleFunc(repoPrefix, func(w http.ResponseWriter, r *http.Request) {
			writeJSON(w, `{"name":"`+fixtureRepo+`","full_name":"`+fixtureOwner+"/"+fixtureRepo+`","default_branch":"develop"}`)
		})
		mux.HandleFunc(repoPrefix+"/git/trees/develop", func(w http.ResponseWriter, r *http.Request) {
			writeJSON(w, `{"sha":"develop-tree-sha","tree":[`+
				`{"path":".github/workflows","sha":"`+workflowsTreeSHA+`","mode":"040000","type":"tree"},`+
				`{"path":"CONTRIBUTING.md","sha":"`+contributingBlobSHA+`","mode":"100644","type":"blob"},`+
				`{"path":"README.md","sha":"`+readmeBlobSHA+`","mode":"100644","type":"blob"}]`+
				`,"truncated":false}`)
		})
		mux.HandleFunc(repoPrefix+"/git/trees/"+workflowsTreeSHA, func(w http.ResponseWriter, r *http.Request) {
			writeJSON(w, `{"sha":"`+workflowsTreeSHA+`","tree":[],"truncated":false}`)
		})
		mux.HandleFunc(repoPrefix+"/git/blobs/", func(w http.ResponseWriter, r *http.Request) {
			sha := strings.TrimPrefix(r.URL.Path, repoPrefix+"/git/blobs/")
			content, ok := map[string]string{
				contributingBlobSHA: contributingContent,
				readmeBlobSHA:       readmeContent,
			}[sha]
			if !ok {
				http.Error(w, "unexpected blob requested: "+sha, http.StatusInternalServerError)
				return
			}
			writeJSON(w, `{"encoding":"base64","content":"`+base64.StdEncoding.EncodeToString([]byte(content))+`","size":`+strconv.Itoa(len(content))+`}`)
		})
		mux.HandleFunc(repoPrefix+"/pulls", func(w http.ResponseWriter, r *http.Request) {
			writeJSON(w, `[]`)
		})
		mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
			http.Error(w, "unexpected request: "+r.URL.Path, http.StatusInternalServerError)
		})
		server := httptest.NewServer(mux)
		t.Cleanup(server.Close)
		client.BaseURL, _ = url.Parse(server.URL + "/")

		details, err := Detail(client, fixtureOwner, fixtureRepo)
		if err != nil {
			t.Fatalf("Detail returned unexpected error: %v", err)
		}
		if details != (Details{}) {
			t.Errorf("Details = %+v; want zero value", details)
		}
		found := false
		for _, p := range rec() {
			if p == repoPrefix+"/git/trees/develop?recursive=1" {
				found = true
			}
		}
		if !found {
			t.Errorf("tree was not fetched from the develop branch; requests: %v", rec())
		}
	})

	t.Run("missing default branch fails clearly", func(t *testing.T) {
		rt := &recordingTransport{}
		client, rec := startRepoFailureServer(t, rt)

		mux := http.NewServeMux()
		mux.HandleFunc("/rate_limit", func(w http.ResponseWriter, r *http.Request) {
			failPreflight(w, r)
		})
		mux.HandleFunc("/repos/"+fixtureOwner+"/"+fixtureRepo, func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"name":"` + fixtureRepo + `","full_name":"` + fixtureOwner + "/" + fixtureRepo + `","default_branch":""}`))
		})
		mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
			http.Error(w, "unexpected request: "+r.URL.Path, http.StatusInternalServerError)
		})
		server := httptest.NewServer(mux)
		t.Cleanup(server.Close)
		client.BaseURL, _ = url.Parse(server.URL + "/")

		details, err := Detail(client, fixtureOwner, fixtureRepo)
		if err == nil {
			t.Fatalf("Detail returned nil error; want a clear failure for the missing default branch")
		}
		if !strings.Contains(err.Error(), "default branch") {
			t.Errorf("error does not mention the default branch problem: %v", err)
		}
		if details != (Details{}) {
			t.Errorf("Details = %+v; want zero value", details)
		}
		assertOnlyRepoLookup(t, rec())
	})
}

// TestConcurrentDetailAgainstFixture verifies that concurrent checks sharing
// one fixture server do not race the request recording.
func TestConcurrentDetailAgainstFixture(t *testing.T) {
	_, client, _ := newFixtureServer(t, true)

	var wg sync.WaitGroup
	errs := make(chan error, 8)
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			required, err := Check(client, fixtureOwner, fixtureRepo)
			if err != nil {
				errs <- err
				return
			}
			if !required {
				errs <- fmt.Errorf("Check = false; want true")
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Error(err)
	}
}

// TestReferencesCLAInContent pins the standalone-acronym matcher: a bare CLA
// at word boundaries must match, while larger words that merely contain the
// letters must not.
func TestReferencesCLAInContent(t *testing.T) {
	cases := []struct {
		name    string
		content string
		want    bool
	}{
		{name: "bare acronym", content: "CLA", want: true},
		{name: "sentence", content: "Please sign the CLA.", want: true},
		{name: "parenthesized", content: "(CLA)", want: true},
		{name: "newline delimited", content: "first line\nCLA\nlast line", want: true},
		{name: "full phrase", content: "Contributor License Agreement", want: true},
		{name: "empty", content: "", want: false},
		{name: "larger word after", content: "CLAW", want: false},
		{name: "larger word middle", content: "DECLARE", want: false},
		{name: "prefixed", content: "preCLA", want: false},
		{name: "digits after", content: "CLA123", want: false},
		{name: "underscored", content: "_CLA_", want: false},
		{name: "lowercase", content: "cla", want: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := (checker{}).referencesCLAInContent([]byte(tc.content))
			if err != nil {
				t.Fatalf("referencesCLAInContent(%q) returned error: %v", tc.content, err)
			}
			if got != tc.want {
				t.Errorf("referencesCLAInContent(%q) = %v; want %v", tc.content, got, tc.want)
			}
		})
	}
}

// TestDetailDocumentCLAReferences exercises the README and CONTRIBUTING
// checks end to end against offline blob fixtures. Every case uses the
// negative fixture shape (no .clabot, no PR labels, no workflows), so the
// asserted Details depend only on the document contents.
func TestDetailDocumentCLAReferences(t *testing.T) {
	neutral := "Pull requests require at least one approving review before they can be merged."
	cases := []struct {
		name         string
		readme       string
		contributing string
		want         Details
	}{
		{
			name:         "acronym only in readme",
			readme:       "CLA",
			contributing: neutral,
			want:         Details{InREADME: true},
		},
		{
			name:         "acronym only in contributing",
			readme:       neutral,
			contributing: "CLA",
			want:         Details{InContributing: true},
		},
		{
			name:         "full phrase in readme",
			readme:       "Contributor License Agreement",
			contributing: neutral,
			want:         Details{InREADME: true},
		},
		{
			name:         "larger words in both documents",
			readme:       "CLAW DECLARE preCLA CLA123 _CLA_ cla",
			contributing: "Nothing here mentions an agreement.",
			want:         Details{},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, client, _ := newFixtureServerWithContent(t, false, tc.contributing, tc.readme)

			details, err := Detail(client, fixtureOwner, fixtureRepo)
			if err != nil {
				t.Fatalf("Detail returned unexpected error: %v", err)
			}
			if details != tc.want {
				t.Errorf("Detail = %+v; want %+v", details, tc.want)
			}
		})
	}
}
