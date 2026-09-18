/*
Copyright (c) 2021-2022 Progressive Casualty Insurance Company. All rights reserved.

Use of this source code is governed by an MIT license that can be found in
the LICENSE file at https://github.com/Progressive-Insurance/need-cla/blob/main/LICENSE.md
*/

package needcla

import (
	"strings"
	"testing"
)

// TestIsCLAActionRef covers the reference boundary rules: the exact,
// case-sensitive repository component followed immediately by '@' and a
// nonempty, whitespace-free reference.
func TestIsCLAActionRef(t *testing.T) {
	for _, tc := range []struct {
		ref  string
		want bool
	}{
		// Accepted: tags, branch references containing '/', and commit SHAs.
		{"cla-assistant/github-action@v2", true},
		{"cla-assistant/github-action@v2.1.0", true},
		{"cla-assistant/github-action@main", true},
		{"cla-assistant/github-action@feature/branch", true},
		{"cla-assistant/github-action@abc123def456789012345678901234567890123456", true},

		// Rejected: similarly named repositories and other owners.
		{"cla-assistant/github-action-helper@v1", false},
		{"cla-assistant/github-actionx@v1", false},
		{"other-owner/github-action@v1", false},
		{"cla-assistant/other-action@v1", false},
		{"cla-assistant/github-action/v2", false},

		// Rejected: subdirectory action paths.
		{"cla-assistant/github-action/subdir@v1", false},

		// Rejected: missing or empty references.
		{"cla-assistant/github-action", false},
		{"cla-assistant/github-action@", false},

		// Rejected: surrounding or internal whitespace.
		{"cla-assistant/github-action@   ", false},
		{" cla-assistant/github-action@v2", false},
		{"cla-assistant/github-action@v2 ", false},
		{"cla-assistant/github-action@v 2", false},
		{"cla-assistant/github-action@\t", false},

		// Rejected: case sensitivity.
		{"CLA-ASSISTANT/github-action@v2", false},
		{"cla-assistant/GitHub-Action@v2", false},
	} {
		if got := isCLAActionRef(tc.ref); got != tc.want {
			t.Errorf("isCLAActionRef(%q) = %v; want %v", tc.ref, got, tc.want)
		}
	}
}

// TestWorkflowYAMLHasCLAAction is the parser-focused table: it exercises the
// YAML structure inspection and the acceptance cases for quoting, unrelated
// fields, non-matching text, malformed input, and empty documents.
func TestWorkflowYAMLHasCLAAction(t *testing.T) {
	for _, tc := range []struct {
		name    string
		content string
		want    bool
		wantErr bool
	}{
		// Acceptance case 1: quoting styles match identically.
		{
			name:    "plain",
			content: "jobs:\n  cla:\n    steps:\n      - uses: cla-assistant/github-action@v2\n",
			want:    true,
		},
		{
			name:    "single-quoted",
			content: "jobs:\n  cla:\n    steps:\n      - uses: 'cla-assistant/github-action@v2'\n",
			want:    true,
		},
		{
			name:    "double-quoted",
			content: "jobs:\n  cla:\n    steps:\n      - uses: \"cla-assistant/github-action@v2\"\n",
			want:    true,
		},
		{
			name:    "flow-style",
			content: "jobs:\n  cla:\n    steps: [{uses: cla-assistant/github-action@v2}]\n",
			want:    true,
		},

		// Acceptance case 2: multiple jobs and steps; unrelated fields are
		// accepted.
		{
			name: "multiple-jobs",
			content: "jobs:\n" +
				"  build:\n" +
				"    steps:\n" +
				"      - uses: actions/checkout@v3\n" +
				"  cla:\n" +
				"    steps:\n" +
				"      - uses: cla-assistant/github-action@v2\n",
			want: true,
		},
		{
			name: "multiple-steps",
			content: "jobs:\n" +
				"  cla:\n" +
				"    steps:\n" +
				"      - uses: actions/checkout@v3\n" +
				"      - run: echo hello\n" +
				"      - uses: cla-assistant/github-action@v2\n",
			want: true,
		},
		{
			name: "unrelated-fields",
			content: "name: cla\n" +
				"on: push\n" +
				"jobs:\n" +
				"  cla:\n" +
				"    runs-on: ubuntu-latest\n" +
				"    env:\n" +
				"      FOO: bar\n" +
				"    steps:\n" +
				"      - name: Check CLA\n" +
				"        uses: cla-assistant/github-action@v2\n" +
				"        with:\n" +
				"          path-to-cla-file: .cla\n",
			want: true,
		},

		// Acceptance case 3: comments, descriptions, environment values, and
		// block-scalar scripts containing the action text do not match.
		{
			name: "comment",
			content: "jobs:\n" +
				"  cla:\n" +
				"    steps:\n" +
				"      - uses: actions/checkout@v3 # uses: cla-assistant/github-action@v2\n",
			want: false,
		},
		{
			name: "description",
			content: "jobs:\n" +
				"  cla:\n" +
				"    steps:\n" +
				"      - name: cla-assistant/github-action@v2\n" +
				"        uses: actions/checkout@v3\n",
			want: false,
		},
		{
			name: "env-value",
			content: "jobs:\n" +
				"  cla:\n" +
				"    env:\n" +
				"      ACTION: cla-assistant/github-action@v2\n" +
				"    steps:\n" +
				"      - uses: actions/checkout@v3\n",
			want: false,
		},
		{
			name: "block-scalar-script",
			content: "jobs:\n" +
				"  cla:\n" +
				"    steps:\n" +
				"      - run: |\n" +
				"          echo uses: cla-assistant/github-action@v2\n",
			want: false,
		},

		// Acceptance case 4: similarly named repositories, other owners,
		// subdirectory paths, and missing/empty/whitespace references do not
		// match.
		{
			name: "helper-repository",
			content: "jobs:\n" +
				"  cla:\n" +
				"    steps:\n" +
				"      - uses: cla-assistant/github-action-helper@v1\n",
			want: false,
		},
		{
			name: "other-owner",
			content: "jobs:\n" +
				"  cla:\n" +
				"    steps:\n" +
				"      - uses: other-owner/github-action@v1\n",
			want: false,
		},
		{
			name: "subdirectory-path",
			content: "jobs:\n" +
				"  cla:\n" +
				"    steps:\n" +
				"      - uses: cla-assistant/github-action/subdir@v1\n",
			want: false,
		},
		{
			name: "missing-at",
			content: "jobs:\n" +
				"  cla:\n" +
				"    steps:\n" +
				"      - uses: cla-assistant/github-action\n",
			want: false,
		},
		{
			name: "empty-reference",
			content: "jobs:\n" +
				"  cla:\n" +
				"    steps:\n" +
				"      - uses: cla-assistant/github-action@\n",
			want: false,
		},
		{
			name: "whitespace-reference",
			content: "jobs:\n" +
				"  cla:\n" +
				"    steps:\n" +
				"      - uses: 'cla-assistant/github-action@   '\n",
			want: false,
		},

		// Acceptance case 5: malformed YAML and nonscalar uses produce
		// errors; an empty document yields false without error.
		{
			name:    "malformed-yaml",
			content: "jobs: [unclosed\n",
			wantErr: true,
		},
		{
			name: "nonscalar-uses-integer",
			content: "jobs:\n" +
				"  cla:\n" +
				"    steps:\n" +
				"      - uses: 123\n",
			wantErr: true,
		},
		{
			name: "nonscalar-uses-sequence",
			content: "jobs:\n" +
				"  cla:\n" +
				"    steps:\n" +
				"      - uses: [a, b]\n",
			wantErr: true,
		},
		{
			name: "nonscalar-uses-mapping",
			content: "jobs:\n" +
				"  cla:\n" +
				"    steps:\n" +
				"      - uses:\n" +
				"          a: b\n",
			wantErr: true,
		},
		{
			name: "null-uses",
			content: "jobs:\n" +
				"  cla:\n" +
				"    steps:\n" +
				"      - uses:\n",
			wantErr: true,
		},
		{
			name:    "empty-document",
			content: "",
			want:    false,
		},
		{
			name:    "comment-only-document",
			content: "# just a comment\n",
			want:    false,
		},
		{
			name:    "no-jobs-key",
			content: "name: cla\non: push\n",
			want:    false,
		},
		{
			name:    "job-without-steps",
			content: "jobs:\n  cla:\n    runs-on: ubuntu-latest\n",
			want:    false,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := workflowYAMLHasCLAAction([]byte(tc.content))
			if tc.wantErr {
				if err == nil {
					t.Fatalf("workflowYAMLHasCLAAction returned nil error; want an error")
				}
				return
			}
			if err != nil {
				t.Fatalf("workflowYAMLHasCLAAction returned unexpected error: %v", err)
			}
			if got != tc.want {
				t.Errorf("workflowYAMLHasCLAAction = %v; want %v", got, tc.want)
			}
		})
	}
}

// workflowYAMLBlobContents are the blob contents used by the offline
// integration tests below.
const (
	quotedWorkflowContent    = "name: cla\njobs:\n  cla:\n    steps:\n      - uses: \"cla-assistant/github-action@v2\"\n"
	commentWorkflowContent   = "name: cla\njobs:\n  cla:\n    steps:\n      - uses: actions/checkout@v3 # uses: cla-assistant/github-action@v2\n"
	scriptWorkflowContent    = "name: cla\njobs:\n  cla:\n    steps:\n      - run: |\n          echo uses: cla-assistant/github-action@v2\n"
	helperWorkflowContent    = "name: cla\njobs:\n  cla:\n    steps:\n      - uses: cla-assistant/github-action-helper@v1\n"
	malformedWorkflowContent = "jobs: [unclosed\n"
	nonscalarWorkflowContent = "jobs:\n  cla:\n    steps:\n      - uses: 123\n"
)

// TestUsesCLAAssistantActionQuotedReference is the regression for the missed
// quoted value: a double-quoted step reference is recognized end to end.
func TestUsesCLAAssistantActionQuotedReference(t *testing.T) {
	client, recorded := startWorkflowsFixtureServer(t, workflowsFixtureConfig{
		entry: workflowsEntryTree,
		treeEntries: []string{
			`{"path":"cla.yml","sha":"` + workflowBlobSHA + `","mode":"100644","type":"blob"}`,
		},
		treeBlobs: map[string]string{workflowBlobSHA: quotedWorkflowContent},
	})

	details, err := Detail(client, fixtureOwner, fixtureRepo)
	if err != nil {
		t.Fatalf("Detail returned unexpected error: %v", err)
	}
	if !details.Action {
		t.Errorf("Action = false; want true for a double-quoted reference")
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

// TestUsesCLAAssistantActionNonStepText covers acceptance case 3 end to end:
// the action text appearing only in a comment, a block-scalar script, or a
// similarly named repository does not set Details.Action.
func TestUsesCLAAssistantActionNonStepText(t *testing.T) {
	for _, tc := range []struct {
		name    string
		content string
	}{
		{name: "comment", content: commentWorkflowContent},
		{name: "block-scalar-script", content: scriptWorkflowContent},
		{name: "helper-repository", content: helperWorkflowContent},
	} {
		t.Run(tc.name, func(t *testing.T) {
			client, _ := startWorkflowsFixtureServer(t, workflowsFixtureConfig{
				entry: workflowsEntryTree,
				treeEntries: []string{
					`{"path":"cla.yml","sha":"` + workflowBlobSHA + `","mode":"100644","type":"blob"}`,
				},
				treeBlobs: map[string]string{workflowBlobSHA: tc.content},
			})

			details, err := Detail(client, fixtureOwner, fixtureRepo)
			if err != nil {
				t.Fatalf("Detail returned unexpected error: %v", err)
			}
			if details.Action {
				t.Errorf("Action = true; want false for %s", tc.name)
			}
		})
	}
}

// TestUsesCLAAssistantActionBrokenPlusMatch covers acceptance case 6: one
// broken workflow plus a valid matching workflow remains positive with no
// error.
func TestUsesCLAAssistantActionBrokenPlusMatch(t *testing.T) {
	client, _ := startWorkflowsFixtureServer(t, workflowsFixtureConfig{
		entry: workflowsEntryTree,
		treeEntries: []string{
			`{"path":"broken.yml","sha":"` + failingBlobSHA + `","mode":"100644","type":"blob"}`,
			`{"path":"cla.yml","sha":"` + workflowBlobSHA + `","mode":"100644","type":"blob"}`,
		},
		treeBlobs: map[string]string{
			failingBlobSHA:  malformedWorkflowContent,
			workflowBlobSHA: quotedWorkflowContent,
		},
	})

	details, err := Detail(client, fixtureOwner, fixtureRepo)
	if err != nil {
		t.Fatalf("Detail returned unexpected error: %v", err)
	}
	if !details.Action {
		t.Errorf("Action = false; want true (a valid matching workflow is present)")
	}
}

// TestUsesCLAAssistantActionBrokenNoMatch covers acceptance case 6: broken
// workflows with no match produce aggregated errors with filename context.
func TestUsesCLAAssistantActionBrokenNoMatch(t *testing.T) {
	for _, tc := range []struct {
		name    string
		content string
	}{
		{name: "malformed-yaml", content: malformedWorkflowContent},
		{name: "nonscalar-uses", content: nonscalarWorkflowContent},
	} {
		t.Run(tc.name, func(t *testing.T) {
			client, _ := startWorkflowsFixtureServer(t, workflowsFixtureConfig{
				entry: workflowsEntryTree,
				treeEntries: []string{
					`{"path":"broken.yml","sha":"` + failingBlobSHA + `","mode":"100644","type":"blob"}`,
				},
				treeBlobs: map[string]string{failingBlobSHA: tc.content},
			})

			details, err := Detail(client, fixtureOwner, fixtureRepo)
			if err == nil {
				t.Fatalf("Detail returned nil error; want the workflow inspection error")
			}
			if details.Action {
				t.Errorf("Action = true; want false")
			}
			msg := err.Error()
			if !strings.Contains(msg, "broken.yml") {
				t.Errorf("error does not mention the failing workflow path: %v", err)
			}
			if !strings.Contains(msg, "cla-assistant action") {
				t.Errorf("error does not mention the cla-assistant action check: %v", err)
			}
		})
	}
}

// TestUsesCLAAssistantActionEmptyFile covers acceptance case 5: an empty
// workflow file yields false without error.
func TestUsesCLAAssistantActionEmptyFile(t *testing.T) {
	client, _ := startWorkflowsFixtureServer(t, workflowsFixtureConfig{
		entry: workflowsEntryTree,
		treeEntries: []string{
			`{"path":"empty.yml","sha":"` + workflowBlobSHA + `","mode":"100644","type":"blob"}`,
		},
		treeBlobs: map[string]string{workflowBlobSHA: ""},
	})

	details, err := Detail(client, fixtureOwner, fixtureRepo)
	if err != nil {
		t.Fatalf("Detail returned unexpected error: %v", err)
	}
	if details.Action {
		t.Errorf("Action = true; want false for an empty workflow file")
	}
}
