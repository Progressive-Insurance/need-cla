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
	"sync"
	"testing"

	"github.com/google/go-github/v43/github"
)

// clabotEntry builds a GitHub tree entry at the given path with the given Git
// type, so the .clabot file-existence heuristic can be exercised against
// specific entry shapes. An empty entryType leaves the type unset, modeling a
// present entry whose type is empty or otherwise unsupported.
func clabotEntry(path, entryType string) *github.TreeEntry {
	e := &github.TreeEntry{
		Path: github.String(path),
		SHA:  github.String("clabot-entry-sha"),
	}
	if entryType != "" {
		e.Type = github.String(entryType)
	}
	return e
}

// TestHasCLABotFile covers the acceptance cases for the root .clabot
// file-existence heuristic: only a blob at the exact root path counts as
// evidence, while trees, gitlinks, and other entry types do not. The checker
// is constructed directly with a tree, so no HTTP request is made and the
// blob is never downloaded.
func TestHasCLABotFile(t *testing.T) {
	cases := []struct {
		name      string
		entries   []*github.TreeEntry
		truncated bool
		want      bool
		wantErr   error
	}{
		{
			name:    "root blob is positive",
			entries: []*github.TreeEntry{clabotEntry(".clabot", "blob")},
			want:    true,
		},
		{
			name:    "root tree is negative",
			entries: []*github.TreeEntry{clabotEntry(".clabot", "tree")},
			want:    false,
		},
		{
			name:    "root gitlink is negative",
			entries: []*github.TreeEntry{clabotEntry(".clabot", "commit")},
			want:    false,
		},
		{
			name:    "complete tree without path is negative",
			entries: []*github.TreeEntry{clabotEntry("README.md", "blob")},
			want:    false,
		},
		{
			name:    "nested .config/.clabot does not count",
			entries: []*github.TreeEntry{clabotEntry(".config/.clabot", "blob")},
			want:    false,
		},
		{
			name:    "empty type is negative",
			entries: []*github.TreeEntry{clabotEntry(".clabot", "")},
			want:    false,
		},
		{
			name:    "unsupported type is negative",
			entries: []*github.TreeEntry{clabotEntry(".clabot", "submodule")},
			want:    false,
		},
		{
			name:      "truncated tree missing path keeps error",
			entries:   []*github.TreeEntry{clabotEntry("README.md", "blob")},
			truncated: true,
			want:      false,
			wantErr:   ErrTruncatedTree,
		},
		{
			name:      "truncated tree with blob stays positive",
			entries:   []*github.TreeEntry{clabotEntry(".clabot", "blob")},
			truncated: true,
			want:      true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := checker{
				tree: &github.Tree{
					Entries:   tc.entries,
					Truncated: github.Bool(tc.truncated),
				},
			}
			got, err := c.hasCLABotFile(context.Background())
			if tc.wantErr != nil {
				if !errors.Is(err, tc.wantErr) {
					t.Errorf("hasCLABotFile error = %v; want %v", err, tc.wantErr)
				}
			} else if err != nil {
				t.Errorf("hasCLABotFile returned unexpected error: %v", err)
			}
			if got != tc.want {
				t.Errorf("hasCLABotFile = %v; want %v", got, tc.want)
			}
		})
	}
}

// clabotEntryKind describes how the fixture models the root .clabot path in
// the repository tree.
const (
	clabotEntryAbsent  = "absent"
	clabotEntryBlob    = "blob"
	clabotEntryTree    = "tree"
	clabotEntryGitlink = "commit"
)

// startClabotFixtureServer starts an offline GitHub API fixture that models
// the root .clabot path according to entryKind and records every request it
// receives, so the public details aggregation and the no-blob-request
// guarantee can be verified. Routes that are not modeled return 500 so
// fixture mistakes surface as errors instead of passing silently.
func startClabotFixtureServer(t *testing.T, entryKind string) (*github.Client, func() []string) {
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

	entries := []string{
		`{"path":".github/workflows","sha":"` + workflowsTreeSHA + `","mode":"040000","type":"tree"}`,
		`{"path":"CONTRIBUTING.md","sha":"` + contributingBlobSHA + `","mode":"100644","type":"blob"}`,
		`{"path":"README.md","sha":"` + readmeBlobSHA + `","mode":"100644","type":"blob"}`,
	}
	switch entryKind {
	case clabotEntryBlob:
		entries = append(entries, `{"path":".clabot","sha":"`+clabotBlobSHA+`","mode":"100644","type":"blob"}`)
	case clabotEntryTree:
		entries = append(entries, `{"path":".clabot","sha":"`+clabotBlobSHA+`","mode":"040000","type":"tree"}`)
	case clabotEntryGitlink:
		entries = append(entries, `{"path":".clabot","sha":"`+clabotBlobSHA+`","mode":"160000","type":"commit"}`)
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
		writeJSON(w, `{"sha":"`+fixtureBranch+`-tree-sha","tree":[`+strings.Join(entries, ",")+`],"truncated":false}`)
	})
	mux.HandleFunc(repoPrefix+"/git/trees/"+workflowsTreeSHA, func(w http.ResponseWriter, r *http.Request) {
		record(r)
		writeJSON(w, `{"sha":"`+workflowsTreeSHA+`","tree":[],"truncated":false}`)
	})
	mux.HandleFunc(repoPrefix+"/git/blobs/", func(w http.ResponseWriter, r *http.Request) {
		record(r)
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

// TestDetailClabotEntryType verifies the public details aggregation for the
// root .clabot heuristic: a blob sets BotFile, while a tree or gitlink does
// not, and no blob is ever downloaded for the .clabot path.
func TestDetailClabotEntryType(t *testing.T) {
	cases := []struct {
		name      string
		entryKind string
		want      Details
	}{
		{name: "blob is positive", entryKind: clabotEntryBlob, want: Details{BotFile: true}},
		{name: "tree is negative", entryKind: clabotEntryTree, want: Details{}},
		{name: "gitlink is negative", entryKind: clabotEntryGitlink, want: Details{}},
		{name: "absent is negative", entryKind: clabotEntryAbsent, want: Details{}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			client, recorded := startClabotFixtureServer(t, tc.entryKind)

			details, err := Detail(client, fixtureOwner, fixtureRepo)
			if err != nil {
				t.Fatalf("Detail returned unexpected error: %v", err)
			}
			if details != tc.want {
				t.Errorf("Detail = %+v; want %+v", details, tc.want)
			}

			// The .clabot path must never be fetched as a blob, and no
			// request beyond the standard fixture set may be made.
			if got := recorded(); !samePathSet(got, expectedFixturePaths()) {
				t.Errorf("recorded requests %v; want %v", got, expectedFixturePaths())
			}
			for _, p := range recorded() {
				if strings.Contains(p, "/git/blobs/"+clabotBlobSHA) {
					t.Errorf("recorded a blob download for .clabot: %v", recorded())
				}
			}
		})
	}
}
