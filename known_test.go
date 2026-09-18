/*
Copyright (c) 2021-2022 Progressive Casualty Insurance Company. All rights reserved.

Use of this source code is governed by an MIT license that can be found in
the LICENSE file at https://github.com/Progressive-Insurance/need-cla/blob/main/LICENSE.md
*/

package needcla

import (
	"net/http/httptest"
	"testing"

	"github.com/google/go-github/v43/github"
)

// TestIsKnown pins case-insensitive matching of the known-owner allowlist:
// capitalization variants of an existing entry must match, while unknown
// names, partial names, and padded names must not.
func TestIsKnown(t *testing.T) {
	for _, tc := range []struct {
		owner string
		want  bool
	}{
		{owner: "google", want: true},
		{owner: "Google", want: true},
		{owner: "GOOGLE", want: true},
		{owner: "progressive-insurance", want: true},
		{owner: "Progressive-Insurance", want: true},
		{owner: "PROGRESSIVE-INSURANCE", want: true},
		{owner: "kubernetes", want: true},
		{owner: "Kubernetes", want: true},
		{owner: "fixture-owner", want: false},
		{owner: "Fixture-Owner", want: false},
		{owner: "unknown-org", want: false},
		{owner: "Unknown-Org", want: false},
		{owner: "", want: false},
		{owner: "google-tools", want: false},
		{owner: "mygoogle", want: false},
		{owner: " google", want: false},
		{owner: "google ", want: false},
	} {
		t.Run(tc.owner, func(t *testing.T) {
			c := checker{owner: tc.owner}
			if got := c.isKnown(); got != tc.want {
				t.Errorf("isKnown(%q) = %v; want %v", tc.owner, got, tc.want)
			}
		})
	}
}

// newKnownOwnerFixtureServer starts the offline GitHub API fixture for the
// given owner with every content-based heuristic negative: no .clabot file,
// no CLA references in CONTRIBUTING.md or README.md, an empty workflows
// directory, and no pull requests. Only the owner allowlist can make the
// scan report a CLA requirement.
func newKnownOwnerFixtureServer(t *testing.T, owner string) (*httptest.Server, *github.Client, func() []string) {
	t.Helper()
	server, client, recorded := startFixtureServerWithPulls(owner, false, contributingContent, readmeContent, `[]`)
	t.Cleanup(server.Close)
	return server, client, recorded
}

// TestDetailKnownOwnerCaseInsensitive exercises the public API end to end:
// a mixed-case known owner must set Details.Known and drive Required() even
// when every other heuristic is negative, while an unknown owner must not.
// The recorded request set proves owner matching introduces no new network
// requests.
func TestDetailKnownOwnerCaseInsensitive(t *testing.T) {
	for _, tc := range []struct {
		name  string
		owner string
		want  Details
	}{
		{name: "mixed-case known owner", owner: "Google", want: Details{Known: true}},
		{name: "uppercase known owner", owner: "GOOGLE", want: Details{Known: true}},
		{name: "canonical known owner", owner: "Progressive-Insurance", want: Details{Known: true}},
		{name: "unknown owner", owner: "fixture-owner", want: Details{}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, client, recorded := newKnownOwnerFixtureServer(t, tc.owner)

			details, err := Detail(client, tc.owner, fixtureRepo)
			if err != nil {
				t.Fatalf("Detail returned unexpected error: %v", err)
			}
			if details != tc.want {
				t.Errorf("Detail = %+v; want %+v", details, tc.want)
			}
			if got := details.Required(); got != tc.want.Required() {
				t.Errorf("Required() = %v; want %v", got, tc.want.Required())
			}

			required, err := Check(client, tc.owner, fixtureRepo)
			if err != nil {
				t.Fatalf("Check returned unexpected error: %v", err)
			}
			if required != tc.want.Required() {
				t.Errorf("Check = %v; want %v", required, tc.want.Required())
			}

			repoPrefix := "/repos/" + tc.owner + "/" + fixtureRepo
			want := []string{
				repoPrefix,
				repoPrefix + "/git/trees/" + fixtureBranch + "?recursive=1",
				repoPrefix + "/git/trees/" + workflowsTreeSHA,
				repoPrefix + "/git/blobs/" + contributingBlobSHA,
				repoPrefix + "/git/blobs/" + readmeBlobSHA,
				repoPrefix + "/pulls?per_page=100&state=all",
			}
			if got := recorded(); !samePathSet(got, want) {
				t.Errorf("recorded requests %v; want %v", got, want)
			}
		})
	}
}
