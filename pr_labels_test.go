/*
Copyright (c) 2021-2022 Progressive Casualty Insurance Company. All rights reserved.

Use of this source code is governed by an MIT license that can be found in
the LICENSE file at https://github.com/Progressive-Insurance/need-cla/blob/main/LICENSE.md
*/

package needcla

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"testing"

	"github.com/google/go-github/v43/github"
)

// TestPRLabelMatcher pins the exact Google-style CLA label contract: only
// the complete labels "cla: yes" and "cla: no" match, case-sensitively,
// with optional whitespace after the colon and no extra text.
func TestPRLabelMatcher(t *testing.T) {
	cases := []struct {
		name  string
		label string
		want  bool
	}{
		{name: "cla yes", label: "cla: yes", want: true},
		{name: "cla no", label: "cla: no", want: true},
		{name: "no whitespace after colon", label: "cla:yes", want: true},
		{name: "tab after colon", label: "cla:\tyes", want: true},
		{name: "empty label", label: "", want: false},
		{name: "single letter y", label: "cla: y", want: false},
		{name: "single letter n", label: "cla: n", want: false},
		{name: "pipe character", label: "cla: |", want: false},
		{name: "optional value", label: "cla: optional", want: false},
		{name: "nonsense value", label: "cla: nonsense", want: false},
		{name: "prefixed label", label: "not-cla: yes", want: false},
		{name: "yesterday value", label: "cla: yesterday", want: false},
		{name: "nobody value", label: "cla: nobody", want: false},
		{name: "trailing text", label: "cla: yes please", want: false},
		{name: "leading whitespace", label: " cla: yes", want: false},
		{name: "trailing whitespace", label: "cla: yes ", want: false},
		{name: "uppercase prefix", label: "CLA: yes", want: false},
		{name: "uppercase value yes", label: "cla: YES", want: false},
		{name: "uppercase value no", label: "cla: No", want: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := regexp.MatchString(prLabelMatcher, tc.label)
			if err != nil {
				t.Fatalf("regexp.MatchString(%q, %q) returned error: %v", prLabelMatcher, tc.label, err)
			}
			if got != tc.want {
				t.Errorf("prLabelMatcher matched %q = %v; want %v", tc.label, got, tc.want)
			}
		})
	}
}

// fixturePR builds a pull request carrying the given label names.
func fixturePR(labels ...string) *github.PullRequest {
	pr := &github.PullRequest{}
	for _, name := range labels {
		pr.Labels = append(pr.Labels, &github.Label{Name: github.String(name)})
	}
	return pr
}

// startPullsOnlyServer starts an offline GitHub API fixture whose only
// modeled route is the pull request list. When status is 200 the route
// returns the given pull requests; otherwise it returns that status. The
// second return value reports the raw query of the recorded pulls request.
func startPullsOnlyServer(t *testing.T, status int, prs []*github.PullRequest) (*github.Client, func() string) {
	t.Helper()
	if status == 0 {
		status = http.StatusOK
	}
	repoPrefix := "/repos/" + fixtureOwner + "/" + fixtureRepo
	var mu sync.Mutex
	var rawQuery string
	mux := http.NewServeMux()
	mux.HandleFunc(repoPrefix+"/pulls", func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		rawQuery = r.URL.RawQuery
		mu.Unlock()
		if status != http.StatusOK {
			http.Error(w, "pull request list failed", status)
			return
		}
		body, err := json.Marshal(prs)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(body)
	})
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
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
	return client, func() string {
		mu.Lock()
		defer mu.Unlock()
		return rawQuery
	}
}

// TestHasCLATag exercises the PR label check against offline pull request
// fixtures: only the complete "cla: yes" and "cla: no" labels count as
// evidence, on any inspected PR, and API failures stay contextual.
func TestHasCLATag(t *testing.T) {
	cases := []struct {
		name    string
		status  int
		prs     []*github.PullRequest
		want    bool
		wantErr bool
	}{
		{
			name: "unrelated labels",
			prs:  []*github.PullRequest{fixturePR("bug"), fixturePR("cla: optional", "not-cla: yes")},
			want: false,
		},
		{
			name: "cla yes label",
			prs:  []*github.PullRequest{fixturePR("cla: yes")},
			want: true,
		},
		{
			name: "cla no label",
			prs:  []*github.PullRequest{fixturePR("cla: no")},
			want: true,
		},
		{
			name: "label on later PR",
			prs:  []*github.PullRequest{fixturePR("bug"), fixturePR("cla:yes")},
			want: true,
		},
		{
			name: "empty PR list",
			prs:  []*github.PullRequest{},
			want: false,
		},
		{
			name: "PR without labels",
			prs:  []*github.PullRequest{fixturePR()},
			want: false,
		},
		{
			name:    "API failure",
			status:  http.StatusInternalServerError,
			wantErr: true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			client, rawQuery := startPullsOnlyServer(t, tc.status, tc.prs)
			c := checker{owner: fixtureOwner, repo: fixtureRepo, client: client}

			got, err := c.hasCLATag(context.Background())
			if tc.wantErr {
				if err == nil {
					t.Fatalf("hasCLATag returned nil error; want the API failure")
				}
				if !strings.Contains(err.Error(), "error getting "+fixtureOwner+"/"+fixtureRepo+" PRs") {
					t.Errorf("error is not contextual: %v", err)
				}
				if got {
					t.Errorf("hasCLATag = true on API failure; want false")
				}
				return
			}
			if err != nil {
				t.Fatalf("hasCLATag returned unexpected error: %v", err)
			}
			if got != tc.want {
				t.Errorf("hasCLATag = %v; want %v", got, tc.want)
			}
			if q := rawQuery(); q != "per_page=100&state=all" {
				t.Errorf("pulls request query = %q; want %q", q, "per_page=100&state=all")
			}
		})
	}
}

// newFixtureServerWithPRs is newFixtureServer with caller-supplied pull
// requests, so the PR label check can be exercised end to end through
// Detail. The fixture is always the negative shape (no .clabot, neutral
// documents, no workflows) so Details depend only on the PR labels.
func newFixtureServerWithPRs(t *testing.T, prs []*github.PullRequest) (*github.Client, func() []string) {
	t.Helper()
	body, err := json.Marshal(prs)
	if err != nil {
		t.Fatalf("marshaling pull request fixture: %v", err)
	}
	server, client, recorded := startFixtureServerWithPulls(fixtureOwner, false, contributingContent, readmeContent, string(body))
	t.Cleanup(server.Close)
	return client, recorded
}

// TestDetailWithPRLabels verifies that Details.Tag follows the PR label
// contract end to end: unrelated labels leave Tag false, while either
// supported label on any inspected PR sets it true.
func TestDetailWithPRLabels(t *testing.T) {
	cases := []struct {
		name string
		prs  []*github.PullRequest
		want Details
	}{
		{
			name: "unrelated labels",
			prs:  []*github.PullRequest{fixturePR("bug", "cla: optional", "not-cla: yes")},
			want: Details{},
		},
		{
			name: "cla yes on first PR",
			prs:  []*github.PullRequest{fixturePR("cla: yes"), fixturePR("bug")},
			want: Details{Tag: true},
		},
		{
			name: "cla no on later PR",
			prs:  []*github.PullRequest{fixturePR("bug"), fixturePR("cla: no")},
			want: Details{Tag: true},
		},
		{
			name: "no pull requests",
			prs:  []*github.PullRequest{},
			want: Details{},
		},
		{
			name: "pull request without labels",
			prs:  []*github.PullRequest{fixturePR()},
			want: Details{},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			client, _ := newFixtureServerWithPRs(t, tc.prs)

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
