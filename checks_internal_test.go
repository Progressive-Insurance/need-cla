/*
Copyright (c) 2021-2022 Progressive Casualty Insurance Company. All rights reserved.

Use of this source code is governed by an MIT license that can be found in
the LICENSE file at https://github.com/Progressive-Insurance/need-cla/blob/main/LICENSE.md
*/

package needcla

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"sync/atomic"
	"testing"

	"github.com/google/go-github/v43/github"
)

// TestUsesCLAAssistantActionNoWorkflowsDir ensures a repo without a
// .github/workflows directory returns (false, nil) rather than issuing a
// GetTree call with an empty SHA (which 404s) and surfacing a spurious
// ActionErr.
func TestUsesCLAAssistantActionNoWorkflowsDir(t *testing.T) {
	c := checker{
		branch: "main",
		repo:   "some-repo",
		owner:  "some-owner",
		// A tree with entries, but no .github/workflows directory.
		tree: &github.Tree{
			Entries: []*github.TreeEntry{
				{Path: github.String("README.md"), Type: github.String("blob"), SHA: github.String("abc")},
				{Path: github.String(".clabot"), Type: github.String("blob"), SHA: github.String("def")},
			},
		},
	}

	got, err := c.usesCLAAssistantAction(context.Background())
	if err != nil {
		t.Fatalf("expected no error for a repo without .github/workflows, got: %v", err)
	}
	if got {
		t.Errorf("expected Action=false when there is no .github/workflows dir, got true")
	}
}

// TestReferencesCLAInContentCaseInsensitive ensures the CLA string matchers are
// case-insensitive (and accept the British "Licence" spelling), so references
// written in different cases are still detected.
func TestReferencesCLAInContentCaseInsensitive(t *testing.T) {
	c := checker{}
	for _, content := range []string{
		"contributor license agreement",
		"CONTRIBUTOR LICENSE AGREEMENT",
		"Contributor Licence Agreement",
		"By contributing you agree to the CLA.",
		"cla",
	} {
		got, err := c.referencesCLAInContent([]byte(content))
		if err != nil {
			t.Fatalf("referencesCLAInContent(%q) returned error: %v", content, err)
		}
		if !got {
			t.Errorf("referencesCLAInContent(%q) = false, want true", content)
		}
	}

	got, err := c.referencesCLAInContent([]byte("This project welcomes contributions."))
	if err != nil {
		t.Fatalf("referencesCLAInContent returned error: %v", err)
	}
	if got {
		t.Errorf("referencesCLAInContent(no CLA) = true, want false")
	}
}

// TestPrLabelMatcher ensures the PR label matcher is case-insensitive and
// anchored: it matches "cla: yes"/"cla: no" in any case, but not labels that
// merely end in "cla:".
func TestPrLabelMatcher(t *testing.T) {
	cases := []struct {
		label string
		want  bool
	}{
		{"cla: yes", true},
		{"cla: no", true},
		{"cla:yes", true},
		{"CLA: yes", true},
		{"cla: YES", true},
		{"not-a-cla: no", false},
		{"My-CLA: yes", false},
		{"other", false},
	}
	for _, tc := range cases {
		got, err := regexp.Match(prLabelMatcher, []byte(tc.label))
		if err != nil {
			t.Fatalf("prLabelMatcher error on %q: %v", tc.label, err)
		}
		if got != tc.want {
			t.Errorf("prLabelMatcher(%q) = %v, want %v", tc.label, got, tc.want)
		}
	}
}

// TestFindCaseInsensitive ensures find matches tree entries regardless of the
// case of the file name in the tree.
func TestFindCaseInsensitive(t *testing.T) {
	c := checker{
		tree: &github.Tree{
			Entries: []*github.TreeEntry{
				{Path: github.String("Readme.md"), Type: github.String("blob"), SHA: github.String("abc")},
				{Path: github.String("contributing.md"), Type: github.String("blob"), SHA: github.String("def")},
			},
		},
	}
	te, err := c.find("README.md")
	if err != nil {
		t.Fatalf("find(README.md) returned error: %v", err)
	}
	if te == nil {
		t.Fatal("find(README.md) = nil, want the Readme.md entry")
	}
	if te.GetSHA() != "abc" {
		t.Errorf("find(README.md) sha = %q, want abc", te.GetSHA())
	}
	if _, err := c.find("CONTRIBUTING.MD"); err != nil {
		t.Fatalf("find(CONTRIBUTING.MD) returned error: %v", err)
	}
}

// TestDecodeBlob ensures the blob decoder handles base64, raw text, and a missing
// encoding, and rejects unknown encodings.
func TestDecodeBlob(t *testing.T) {
	b64 := base64.StdEncoding.EncodeToString([]byte("hello"))
	cases := []struct {
		name    string
		blob    *github.Blob
		want    string
		wantErr bool
	}{
		{"base64", &github.Blob{Content: github.String(b64), Encoding: github.String("base64")}, "hello", false},
		{"text", &github.Blob{Content: github.String("raw"), Encoding: github.String("text")}, "raw", false},
		{"no encoding", &github.Blob{Content: github.String("raw2")}, "raw2", false},
		{"unknown encoding", &github.Blob{Content: github.String("x"), Encoding: github.String("utf-8")}, "", true},
	}
	for _, tc := range cases {
		got, err := decodeBlob(tc.blob, "label")
		if tc.wantErr {
			if err == nil {
				t.Errorf("decodeBlob(%s) = no error, want error", tc.name)
			}
			continue
		}
		if err != nil {
			t.Errorf("decodeBlob(%s) returned error: %v", tc.name, err)
			continue
		}
		if string(got) != tc.want {
			t.Errorf("decodeBlob(%s) = %q, want %q", tc.name, got, tc.want)
		}
	}
}

// TestHasCLATagPaginationCap ensures hasCLATag stops after maxCLATagPages list
// calls even when every page is full, rather than paging through every PR in the
// repo (which would defeat the rate-limit guard).
func TestHasCLATagPaginationCap(t *testing.T) {
	var prCalls int64
	mux := http.NewServeMux()
	mux.HandleFunc("/repos/o/r/pulls", func(w http.ResponseWriter, _ *http.Request) {
		atomic.AddInt64(&prCalls, 1)
		prs := make([]*github.PullRequest, 100)
		for i := range prs {
			prs[i] = &github.PullRequest{Number: github.Int(i)}
		}
		_ = json.NewEncoder(w).Encode(prs)
	})
	server := httptest.NewServer(mux)
	defer server.Close()

	client := github.NewClient(server.Client())
	u, _ := url.Parse(server.URL + "/")
	client.BaseURL = u

	c := checker{owner: "o", repo: "r", branch: "main", client: client}
	got, err := c.hasCLATag(context.Background())
	if err != nil {
		t.Fatalf("hasCLATag returned error: %v", err)
	}
	if got {
		t.Errorf("hasCLATag = true, want false (no labels present)")
	}
	if calls := atomic.LoadInt64(&prCalls); calls != maxCLATagPages {
		t.Errorf("expected %d PR list calls, got %d", maxCLATagPages, calls)
	}
}
