/*
Copyright (c) 2021-2022 Progressive Casualty Insurance Company. All rights reserved.

Use of this source code is governed by an MIT license that can be found in
the LICENSE file at https://github.com/Progressive-Insurance/need-cla/blob/main/LICENSE.md
*/

package needcla

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/go-github/v43/github"
)

// lowQuotaHeaders are realistic rate headers showing a low but positive
// remaining quota: enough to finish a small scan, yet below the old
// speculative threshold of ten that used to reject the scan up front.
const (
	lowQuotaLimit     = "5000"
	lowQuotaRemaining = "5"
	lowQuotaReset     = "1900000000"
)

func writeLowQuotaJSON(w http.ResponseWriter, body string) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("X-RateLimit-Limit", lowQuotaLimit)
	w.Header().Set("X-RateLimit-Remaining", lowQuotaRemaining)
	w.Header().Set("X-RateLimit-Reset", lowQuotaReset)
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(body))
}

// newRecordingPaths returns a record function and a snapshot function for
// tracking the request paths a fixture server receives.
func newRecordingPaths() (func(r *http.Request), func() []string) {
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
	return record, recorded
}

// newFixtureClient points a fresh go-github client at the given server URL.
func newFixtureClient(t *testing.T, serverURL string) *github.Client {
	t.Helper()
	client := github.NewClient(nil)
	baseURL, err := url.Parse(serverURL + "/")
	if err != nil {
		t.Fatalf("parsing fixture base URL: %v", err)
	}
	client.BaseURL = baseURL
	return client
}

// assertNoRateLimitRequests fails the test if any recorded request hit
// /rate_limit.
func assertNoRateLimitRequests(t *testing.T, rec []string) {
	t.Helper()
	for _, p := range rec {
		if p == "/rate_limit" {
			t.Fatalf("recorded a /rate_limit request; the preflight must be gone. requests: %v", rec)
		}
	}
}

// assertOnlyRepoLookupRequest fails the test unless the recorded requests are
// exactly the single repository lookup (no preflight, no retry, no
// downstream call).
func assertOnlyRepoLookupRequest(t *testing.T, rec []string) {
	t.Helper()
	want := []string{"/repos/" + fixtureOwner + "/" + fixtureRepo}
	if !sameRequestSequence(rec, want) {
		t.Fatalf("recorded requests %v; want exactly %v", rec, want)
	}
}

// startLowQuotaServer starts a fixture server modeling a successful scan of a
// repository whose tree contains only an empty .github/workflows directory:
// no document blobs and no workflow files. Every response carries low but
// positive rate headers, and any /rate_limit request fails the fixture
// immediately.
func startLowQuotaServer(t *testing.T) (*github.Client, func() []string) {
	t.Helper()
	record, recorded := newRecordingPaths()

	repoPrefix := "/repos/" + fixtureOwner + "/" + fixtureRepo
	mux := http.NewServeMux()
	mux.HandleFunc("/rate_limit", func(w http.ResponseWriter, r *http.Request) {
		record(r)
		failPreflight(w, r)
	})
	mux.HandleFunc(repoPrefix, func(w http.ResponseWriter, r *http.Request) {
		record(r)
		writeLowQuotaJSON(w, `{"name":"`+fixtureRepo+`","full_name":"`+fixtureOwner+"/"+fixtureRepo+`","default_branch":"`+fixtureBranch+`"}`)
	})
	mux.HandleFunc(repoPrefix+"/git/trees/"+fixtureBranch, func(w http.ResponseWriter, r *http.Request) {
		record(r)
		if r.URL.Query().Get("recursive") != "1" {
			http.Error(w, "repository tree must be requested recursively", http.StatusInternalServerError)
			return
		}
		// No document blobs and no workflow files: only the (empty)
		// workflows directory exists.
		writeLowQuotaJSON(w, `{"sha":"`+fixtureBranch+`-tree-sha","tree":[{"path":".github/workflows","sha":"`+workflowsTreeSHA+`","mode":"040000","type":"tree"}],"truncated":false}`)
	})
	mux.HandleFunc(repoPrefix+"/git/trees/"+workflowsTreeSHA, func(w http.ResponseWriter, r *http.Request) {
		record(r)
		writeLowQuotaJSON(w, `{"sha":"`+workflowsTreeSHA+`","tree":[],"truncated":false}`)
	})
	mux.HandleFunc(repoPrefix+"/pulls", func(w http.ResponseWriter, r *http.Request) {
		record(r)
		writeLowQuotaJSON(w, `[]`)
	})
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		record(r)
		http.Error(w, "unexpected request: "+r.URL.Path, http.StatusInternalServerError)
	})

	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)
	return newFixtureClient(t, server.URL), recorded
}

// TestDetailLowQuotaWithoutPreflight verifies that a small scan with a low
// but positive remaining quota completes successfully without any /rate_limit
// request, through both CheckWithContext and DetailWithContext.
func TestDetailLowQuotaWithoutPreflight(t *testing.T) {
	client, rec := startLowQuotaServer(t)

	details, err := DetailWithContext(context.Background(), client, fixtureOwner, fixtureRepo)
	if err != nil {
		t.Fatalf("DetailWithContext returned unexpected error: %v", err)
	}
	if details != (Details{}) {
		t.Errorf("Details = %+v; want zero value", details)
	}
	if details.Required() {
		t.Errorf("Required() = true; want false")
	}

	required, err := CheckWithContext(context.Background(), client, fixtureOwner, fixtureRepo)
	if err != nil {
		t.Fatalf("CheckWithContext returned unexpected error: %v", err)
	}
	if required {
		t.Errorf("CheckWithContext = true; want false")
	}

	assertNoRateLimitRequests(t, rec())
}

// startThrottledRepoServer starts a fixture server whose repository route
// returns the given status, headers, and body, and whose /rate_limit route
// fails immediately.
func startThrottledRepoServer(t *testing.T, status int, headers map[string]string, body string) (*github.Client, func() []string) {
	t.Helper()
	record, recorded := newRecordingPaths()

	repoPrefix := "/repos/" + fixtureOwner + "/" + fixtureRepo
	mux := http.NewServeMux()
	mux.HandleFunc("/rate_limit", func(w http.ResponseWriter, r *http.Request) {
		record(r)
		failPreflight(w, r)
	})
	mux.HandleFunc(repoPrefix, func(w http.ResponseWriter, r *http.Request) {
		record(r)
		w.Header().Set("Content-Type", "application/json")
		for k, v := range headers {
			w.Header().Set(k, v)
		}
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	})
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		record(r)
		http.Error(w, "unexpected request: "+r.URL.Path, http.StatusInternalServerError)
	})

	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)
	return newFixtureClient(t, server.URL), recorded
}

// TestDetailPrimaryRateLimitFromRepositoryRequest verifies that a primary
// rate limit response from the first repository request surfaces as a
// *github.RateLimitError carrying reset metadata, and is not mapped to
// ErrInvalidToken.
func TestDetailPrimaryRateLimitFromRepositoryRequest(t *testing.T) {
	client, rec := startThrottledRepoServer(t, http.StatusForbidden, map[string]string{
		"X-RateLimit-Limit":     "5000",
		"X-RateLimit-Remaining": "0",
		"X-RateLimit-Reset":     "1900000000",
	}, `{"message":"API rate limit exceeded for 1.2.3.4.","documentation_url":"https://docs.github.com/rest/overview/resources-in-the-rest-api#rate-limiting"}`)

	details, err := DetailWithContext(context.Background(), client, fixtureOwner, fixtureRepo)
	if err == nil {
		t.Fatal("DetailWithContext returned nil error; want the rate limit error")
	}
	if errors.Is(err, ErrInvalidToken) {
		t.Errorf("errors.Is(err, ErrInvalidToken) = true; a rate limit error must not map to ErrInvalidToken. err: %v", err)
	}
	var rle *github.RateLimitError
	if !errors.As(err, &rle) {
		t.Fatalf("errors.As did not find *github.RateLimitError: %v", err)
	}
	if rle.Rate.Remaining != 0 {
		t.Errorf("RateLimitError.Rate.Remaining = %d; want 0", rle.Rate.Remaining)
	}
	if want := time.Unix(1900000000, 0); !rle.Rate.Reset.Time.Equal(want) {
		t.Errorf("RateLimitError.Rate.Reset = %v; want %v", rle.Rate.Reset.Time, want)
	}
	if details != (Details{}) {
		t.Errorf("Details = %+v; want zero value", details)
	}
	assertOnlyRepoLookupRequest(t, rec())
}

// TestDetailSecondaryRateLimitFromRepositoryRequest verifies that secondary
// rate limit responses keep the typed error and retry information provided by
// go-github v43, without any automatic retry. The fixtures use v43's actual
// response classification: a 403 with a secondary-rate-limits documentation
// URL becomes *AbuseRateLimitError, while a 429 becomes *ErrorResponse.
func TestDetailSecondaryRateLimitFromRepositoryRequest(t *testing.T) {
	t.Run("403 secondary rate limit", func(t *testing.T) {
		client, rec := startThrottledRepoServer(t, http.StatusForbidden, map[string]string{
			"Retry-After": "30",
		}, `{"message":"You have exceeded a secondary rate limit.","documentation_url":"https://docs.github.com/rest/overview/resources-in-the-rest-api#secondary-rate-limits"}`)

		details, err := DetailWithContext(context.Background(), client, fixtureOwner, fixtureRepo)
		if err == nil {
			t.Fatal("DetailWithContext returned nil error; want the secondary rate limit error")
		}
		if errors.Is(err, ErrInvalidToken) {
			t.Errorf("errors.Is(err, ErrInvalidToken) = true; a secondary rate limit error must not map to ErrInvalidToken. err: %v", err)
		}
		var ale *github.AbuseRateLimitError
		if !errors.As(err, &ale) {
			t.Fatalf("errors.As did not find *github.AbuseRateLimitError: %v", err)
		}
		if ale.RetryAfter == nil || *ale.RetryAfter != 30*time.Second {
			t.Errorf("AbuseRateLimitError.RetryAfter = %v; want 30s", ale.RetryAfter)
		}
		if details != (Details{}) {
			t.Errorf("Details = %+v; want zero value", details)
		}
		assertOnlyRepoLookupRequest(t, rec())
	})

	t.Run("429 secondary rate limit", func(t *testing.T) {
		client, rec := startThrottledRepoServer(t, http.StatusTooManyRequests, map[string]string{
			"Retry-After": "30",
		}, `{"message":"You have exceeded a secondary rate limit.","documentation_url":"https://docs.github.com/rest/overview/resources-in-the-rest-api#secondary-rate-limits"}`)

		details, err := DetailWithContext(context.Background(), client, fixtureOwner, fixtureRepo)
		if err == nil {
			t.Fatal("DetailWithContext returned nil error; want the 429 error")
		}
		if errors.Is(err, ErrInvalidToken) {
			t.Errorf("errors.Is(err, ErrInvalidToken) = true; a 429 must not map to ErrInvalidToken. err: %v", err)
		}
		// go-github v43 classifies a 429 as *github.ErrorResponse, not as a
		// typed rate limit error; assert the actual classification.
		var ge *github.ErrorResponse
		if !errors.As(err, &ge) {
			t.Fatalf("errors.As did not find *github.ErrorResponse (v43's actual 429 classification): %v", err)
		}
		if ge.Response.StatusCode != http.StatusTooManyRequests {
			t.Errorf("underlying status = %d; want 429", ge.Response.StatusCode)
		}
		if !strings.Contains(err.Error(), "429") {
			t.Errorf("error does not carry the 429 status: %v", err)
		}
		if details != (Details{}) {
			t.Errorf("Details = %+v; want zero value", details)
		}
		assertOnlyRepoLookupRequest(t, rec())
	})
}

// TestDetailRespectsCachedRateLimit verifies that the caller-provided
// client's rate limit tracking is preserved and not bypassed: after the
// client observes an exhausted core rate limit from a real response, a
// subsequent scan is rejected with a *github.RateLimitError before making any
// further network request.
func TestDetailRespectsCachedRateLimit(t *testing.T) {
	client, rec := startThrottledRepoServer(t, http.StatusForbidden, map[string]string{
		"X-RateLimit-Limit":     "5000",
		"X-RateLimit-Remaining": "0",
		"X-RateLimit-Reset":     "1900000000",
	}, `{"message":"API rate limit exceeded for 1.2.3.4.","documentation_url":"https://docs.github.com/rest/overview/resources-in-the-rest-api#rate-limiting"}`)

	// First scan: the repository request returns 403 with an exhausted rate,
	// which the client caches from the response headers.
	if _, err := DetailWithContext(context.Background(), client, fixtureOwner, fixtureRepo); err == nil {
		t.Fatal("expected the first scan to return the rate limit error")
	}

	// Second scan with the same client: the cached exhausted rate must block
	// the request before it reaches the network, returning a
	// *github.RateLimitError without any new request.
	_, secondErr := DetailWithContext(context.Background(), client, fixtureOwner, fixtureRepo)
	if secondErr == nil {
		t.Fatal("expected the second scan to return the cached rate limit error")
	}
	var rle *github.RateLimitError
	if !errors.As(secondErr, &rle) {
		t.Fatalf("errors.As did not find *github.RateLimitError on the second scan: %v", secondErr)
	}

	// Only the first scan's repository request reached the network; the
	// second scan was blocked by the client's cached rate limit.
	if got := rec(); len(got) != 1 {
		t.Errorf("recorded %d requests %v; want exactly 1 (the second scan must be blocked by the cached rate limit)", len(got), got)
	}
}
