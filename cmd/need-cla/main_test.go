/*
Copyright (c) 2021-2022 Progressive Casualty Insurance Company. All rights reserved.

Use of this source code is governed by an MIT license that can be found in
the LICENSE file at https://github.com/Progressive-Insurance/need-cla/blob/main/LICENSE.md
*/

package main

import (
	"bytes"
	"errors"
	"fmt"
	"strings"
	"testing"

	needcla "github.com/progressive-insurance/need-cla"
)

// fakeChecker records every call and returns a fixed result, so the CLI can
// be exercised without any GitHub request.
type fakeChecker struct {
	details needcla.Details
	err     error
	calls   int
	tokens  []string
	owners  []string
	repos   []string
}

func (f *fakeChecker) check(token, owner, repo string) (needcla.Details, error) {
	f.calls++
	f.tokens = append(f.tokens, token)
	f.owners = append(f.owners, owner)
	f.repos = append(f.repos, repo)
	return f.details, f.err
}

// runCLI runs the CLI with the given arguments and captured writers, returning
// stdout, stderr, and the exit status.
func runCLI(t *testing.T, args []string, fc *fakeChecker) (string, string, int) {
	t.Helper()
	var out, errb bytes.Buffer
	status := run(args, &out, &errb, fc.check)
	return out.String(), errb.String(), status
}

// TestRunUsage covers task 13's argument validation: every malformed invocation
// returns 2, prints a diagnostic and usage to stderr, and never invokes the
// checker.
func TestRunUsage(t *testing.T) {
	cases := []struct {
		name string
		args []string
	}{
		{"zero positionals", []string{}},
		{"one positional", []string{"owner"}},
		{"three positionals", []string{"owner", "repo", "extra"}},
		{"empty owner", []string{"", "repo"}},
		{"empty repo", []string{"owner", ""}},
		{"unknown flag", []string{"-bogus", "owner", "repo"}},
		{"missing token value", []string{"-token"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fc := &fakeChecker{}
			stdout, stderr, status := runCLI(t, tc.args, fc)
			if status != 2 {
				t.Errorf("status = %d, want 2", status)
			}
			if fc.calls != 0 {
				t.Errorf("checker called %d times, want 0", fc.calls)
			}
			if !strings.Contains(stderr, "Usage of ./need-cla") {
				t.Errorf("stderr missing usage:\n%s", stderr)
			}
			if stdout != "" {
				t.Errorf("stdout = %q, want empty", stdout)
			}
		})
	}
}

// TestRunHelp verifies that -h and -help print usage to stdout, return 0, and
// never invoke the checker or write to stderr.
func TestRunHelp(t *testing.T) {
	for _, arg := range []string{"-h", "-help"} {
		t.Run(arg, func(t *testing.T) {
			fc := &fakeChecker{}
			stdout, stderr, status := runCLI(t, []string{arg}, fc)
			if status != 0 {
				t.Errorf("status = %d, want 0", status)
			}
			if fc.calls != 0 {
				t.Errorf("checker called %d times, want 0", fc.calls)
			}
			if !strings.Contains(stdout, "Usage of ./need-cla") {
				t.Errorf("stdout missing usage:\n%s", stdout)
			}
			if stderr != "" {
				t.Errorf("stderr = %q, want empty", stderr)
			}
		})
	}
}

// TestRunValidPositionals verifies that two valid positionals invoke the
// checker exactly once with the exact values and produce a report on stdout.
func TestRunValidPositionals(t *testing.T) {
	fc := &fakeChecker{details: needcla.Details{InREADME: true}}
	stdout, stderr, status := runCLI(t, []string{"myowner", "myrepo"}, fc)
	if status != 0 {
		t.Errorf("status = %d, want 0", status)
	}
	if fc.calls != 1 {
		t.Fatalf("checker called %d times, want 1", fc.calls)
	}
	if fc.owners[0] != "myowner" {
		t.Errorf("owner = %q, want myowner", fc.owners[0])
	}
	if fc.repos[0] != "myrepo" {
		t.Errorf("repo = %q, want myrepo", fc.repos[0])
	}
	if stderr != "" {
		t.Errorf("stderr = %q, want empty", stderr)
	}
	if !strings.Contains(stdout, "myowner/myrepo") {
		t.Errorf("stdout missing owner/repo:\n%s", stdout)
	}
}

// TestRunToken verifies token parsing: explicit flag, environment variable,
// explicit-over-env precedence, and no shared token state across runs.
func TestRunToken(t *testing.T) {
	t.Run("explicit token", func(t *testing.T) {
		fc := &fakeChecker{}
		_, _, status := runCLI(t, []string{"-token", "explicit", "owner", "repo"}, fc)
		if status != 0 {
			t.Errorf("status = %d, want 0", status)
		}
		if fc.calls != 1 || fc.tokens[0] != "explicit" {
			t.Errorf("token = %v, want [explicit]", fc.tokens)
		}
	})
	t.Run("env token", func(t *testing.T) {
		t.Setenv("CLA_TOKEN", "envtok")
		fc := &fakeChecker{}
		_, _, status := runCLI(t, []string{"owner", "repo"}, fc)
		if status != 0 {
			t.Errorf("status = %d, want 0", status)
		}
		if fc.calls != 1 || fc.tokens[0] != "envtok" {
			t.Errorf("token = %v, want [envtok]", fc.tokens)
		}
	})
	t.Run("explicit overrides env", func(t *testing.T) {
		t.Setenv("CLA_TOKEN", "envtok")
		fc := &fakeChecker{}
		_, _, status := runCLI(t, []string{"-token", "explicit", "owner", "repo"}, fc)
		if status != 0 {
			t.Errorf("status = %d, want 0", status)
		}
		if fc.calls != 1 || fc.tokens[0] != "explicit" {
			t.Errorf("token = %v, want [explicit]", fc.tokens)
		}
	})
	t.Run("no shared state", func(t *testing.T) {
		fc1 := &fakeChecker{}
		_, _, _ = runCLI(t, []string{"-token", "tok1", "owner", "repo"}, fc1)
		fc2 := &fakeChecker{}
		_, _, _ = runCLI(t, []string{"-token", "tok2", "owner", "repo"}, fc2)
		if fc1.tokens[0] != "tok1" {
			t.Errorf("first token = %q, want tok1", fc1.tokens[0])
		}
		if fc2.tokens[0] != "tok2" {
			t.Errorf("second token = %q, want tok2", fc2.tokens[0])
		}
	})
}

// TestRunPartialErrorFields covers each partial-error field independently: the
// failed check is rendered UNKNOWN (never as a negative finding), the all-false
// outcome is inconclusive, the diagnostic goes to stderr, and the status is 1.
func TestRunPartialErrorFields(t *testing.T) {
	cases := []struct {
		name     string
		agg      *needcla.Errors
		unknown  string
		negative string
		errText  string
	}{
		{
			name:     "TagErr",
			agg:      &needcla.Errors{TagErr: errors.New("tag failure")},
			unknown:  "PR \"cla\" tags UNKNOWN (check failed)",
			negative: "PRs DO NOT have",
			errText:  "checking for CLA tag: tag failure",
		},
		{
			name:     "BotFileErr",
			agg:      &needcla.Errors{BotFileErr: errors.New("botfile failure")},
			unknown:  ".clabot file UNKNOWN (check failed)",
			negative: ".clabot file DOES NOT exist",
			errText:  "checking for .clabot file: botfile failure",
		},
		{
			name:     "InContributingErr",
			agg:      &needcla.Errors{InContributingErr: errors.New("contributing failure")},
			unknown:  "CONTRIBUTING.md UNKNOWN (check failed)",
			negative: "CONTRIBUTING.md DOES NOT reference",
			errText:  "checking for CLA references in CONTRIBUTING.md: contributing failure",
		},
		{
			name:     "InREADMEErr",
			agg:      &needcla.Errors{InREADMEErr: errors.New("readme failure")},
			unknown:  "README.md UNKNOWN (check failed)",
			negative: "README.md DOES NOT reference",
			errText:  "checking for CLA references in README.md: readme failure",
		},
		{
			name:     "ActionErr",
			agg:      &needcla.Errors{ActionErr: errors.New("action failure")},
			unknown:  "cla-bot Github Action UNKNOWN (check failed)",
			negative: "DOES NOT use the cla-bot Github Action",
			errText:  "checking for cla-assistant Action: action failure",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fc := &fakeChecker{details: needcla.Details{}, err: tc.agg}
			stdout, stderr, status := runCLI(t, []string{"owner", "repo"}, fc)
			if status != 1 {
				t.Errorf("status = %d, want 1", status)
			}
			if !strings.Contains(stdout, tc.unknown) {
				t.Errorf("stdout missing UNKNOWN line %q:\n%s", tc.unknown, stdout)
			}
			if strings.Contains(stdout, tc.negative) {
				t.Errorf("stdout contains negative claim %q for a failed check:\n%s", tc.negative, stdout)
			}
			if !strings.Contains(stdout, "Inconclusive") {
				t.Errorf("stdout missing inconclusive conclusion:\n%s", stdout)
			}
			if !strings.Contains(stderr, tc.errText) {
				t.Errorf("stderr missing diagnostic %q:\n%s", tc.errText, stderr)
			}
			if got := strings.Count(stderr, "error(s) checking for CLA references"); got != 1 {
				t.Errorf("diagnostics printed %d times, want 1:\n%s", got, stderr)
			}
		})
	}
}

// TestRunMultipleFailures covers several failed checks together: each failed
// check is UNKNOWN, the completed checks keep their negative findings, the
// outcome is inconclusive, and the diagnostics are printed once.
func TestRunMultipleFailures(t *testing.T) {
	agg := &needcla.Errors{
		TagErr:      errors.New("tag failure"),
		BotFileErr:  errors.New("botfile failure"),
		InREADMEErr: errors.New("readme failure"),
	}
	fc := &fakeChecker{details: needcla.Details{}, err: agg}
	stdout, stderr, status := runCLI(t, []string{"owner", "repo"}, fc)
	if status != 1 {
		t.Errorf("status = %d, want 1", status)
	}
	for _, frag := range []string{
		"PR \"cla\" tags UNKNOWN (check failed)",
		".clabot file UNKNOWN (check failed)",
		"README.md UNKNOWN (check failed)",
	} {
		if !strings.Contains(stdout, frag) {
			t.Errorf("stdout missing %q:\n%s", frag, stdout)
		}
	}
	// Completed checks keep their negative findings.
	for _, frag := range []string{
		"CONTRIBUTING.md DOES NOT reference a CLA",
		"DOES NOT use the cla-bot Github Action",
	} {
		if !strings.Contains(stdout, frag) {
			t.Errorf("stdout missing completed negative line %q:\n%s", frag, stdout)
		}
	}
	if !strings.Contains(stdout, "Inconclusive") {
		t.Errorf("stdout missing inconclusive conclusion:\n%s", stdout)
	}
	if got := strings.Count(stderr, "error(s) checking for CLA references"); got != 1 {
		t.Errorf("diagnostics printed %d times, want 1:\n%s", got, stderr)
	}
}

// TestRunPositiveElsewhere verifies that positive evidence elsewhere keeps the
// positive conclusion, marked incomplete, with status 1.
func TestRunPositiveElsewhere(t *testing.T) {
	agg := &needcla.Errors{TagErr: errors.New("tag failure")}
	fc := &fakeChecker{details: needcla.Details{InREADME: true}, err: agg}
	stdout, stderr, status := runCLI(t, []string{"owner", "repo"}, fc)
	if status != 1 {
		t.Errorf("status = %d, want 1", status)
	}
	if !strings.Contains(stdout, "README.md DOES reference a CLA") {
		t.Errorf("stdout missing positive README line:\n%s", stdout)
	}
	if !strings.Contains(stdout, "PR \"cla\" tags UNKNOWN (check failed)") {
		t.Errorf("stdout missing UNKNOWN tag line:\n%s", stdout)
	}
	if !strings.Contains(stdout, "DOES need a CLA signed before contributing (incomplete report: some checks failed)") {
		t.Errorf("stdout missing positive-with-notice conclusion:\n%s", stdout)
	}
	if strings.Contains(stdout, "Inconclusive") {
		t.Errorf("stdout should not be inconclusive when positive evidence exists:\n%s", stdout)
	}
	if !strings.Contains(stderr, "tag failure") {
		t.Errorf("stderr missing diagnostic:\n%s", stderr)
	}
}

// TestRunTrueWithOwnError verifies that a true detail stays positive even when
// its own check returned an error, and the report is flagged incomplete.
func TestRunTrueWithOwnError(t *testing.T) {
	agg := &needcla.Errors{InREADMEErr: errors.New("readme failure")}
	fc := &fakeChecker{details: needcla.Details{InREADME: true}, err: agg}
	stdout, stderr, status := runCLI(t, []string{"owner", "repo"}, fc)
	if status != 1 {
		t.Errorf("status = %d, want 1", status)
	}
	if !strings.Contains(stdout, "README.md DOES reference a CLA") {
		t.Errorf("stdout missing positive README line (true detail with its own error):\n%s", stdout)
	}
	if strings.Contains(stdout, "README.md UNKNOWN") {
		t.Errorf("stdout should not mark a true detail as unknown:\n%s", stdout)
	}
	if !strings.Contains(stdout, "DOES need a CLA signed before contributing (incomplete report: some checks failed)") {
		t.Errorf("stdout missing positive-with-notice conclusion:\n%s", stdout)
	}
	if !strings.Contains(stderr, "readme failure") {
		t.Errorf("stderr missing diagnostic:\n%s", stderr)
	}
}

// TestRunWrappedAggregate verifies that an aggregate wrapped with context is
// recognized like a direct aggregate.
func TestRunWrappedAggregate(t *testing.T) {
	inner := &needcla.Errors{InREADMEErr: errors.New("readme failure")}
	wrapped := fmt.Errorf("checking owner/repo: %w", inner)
	fc := &fakeChecker{details: needcla.Details{}, err: wrapped}
	stdout, stderr, status := runCLI(t, []string{"owner", "repo"}, fc)
	if status != 1 {
		t.Errorf("status = %d, want 1", status)
	}
	if !strings.Contains(stdout, "README.md UNKNOWN (check failed)") {
		t.Errorf("stdout missing UNKNOWN README line:\n%s", stdout)
	}
	if !strings.Contains(stdout, "Inconclusive") {
		t.Errorf("stdout missing inconclusive conclusion:\n%s", stdout)
	}
	if !strings.Contains(stderr, "readme failure") {
		t.Errorf("stderr missing diagnostic:\n%s", stderr)
	}
}

// TestRunFatalError verifies that a fatal non-aggregate error returns 1, prints
// the diagnostic to stderr, and produces no result report on stdout.
func TestRunFatalError(t *testing.T) {
	fc := &fakeChecker{details: needcla.Details{}, err: errors.New("fatal failure")}
	stdout, stderr, status := runCLI(t, []string{"owner", "repo"}, fc)
	if status != 1 {
		t.Errorf("status = %d, want 1", status)
	}
	if !strings.Contains(stderr, "fatal failure") {
		t.Errorf("stderr missing diagnostic:\n%s", stderr)
	}
	if stdout != "" {
		t.Errorf("stdout = %q, want empty (no report for a fatal error)", stdout)
	}
}

// TestRunCompletePositive verifies that a complete positive result returns 0
// with a positive conclusion and no stderr output.
func TestRunCompletePositive(t *testing.T) {
	fc := &fakeChecker{details: needcla.Details{InREADME: true}, err: nil}
	stdout, stderr, status := runCLI(t, []string{"owner", "repo"}, fc)
	if status != 0 {
		t.Errorf("status = %d, want 0", status)
	}
	if !strings.Contains(stdout, "README.md DOES reference a CLA") {
		t.Errorf("stdout missing positive README line:\n%s", stdout)
	}
	if !strings.Contains(stdout, "[✓] I think owner/repo DOES need a CLA signed before contributing.") {
		t.Errorf("stdout missing positive conclusion:\n%s", stdout)
	}
	if stderr != "" {
		t.Errorf("stderr = %q, want empty", stderr)
	}
}

// TestRunCompleteNegative verifies that a complete all-false result returns 0
// with a negative conclusion and no stderr output.
func TestRunCompleteNegative(t *testing.T) {
	fc := &fakeChecker{details: needcla.Details{}, err: nil}
	stdout, stderr, status := runCLI(t, []string{"owner", "repo"}, fc)
	if status != 0 {
		t.Errorf("status = %d, want 0", status)
	}
	if !strings.Contains(stdout, "[✗] I think owner/repo DOES NOT need a CLA signed before contributing.") {
		t.Errorf("stdout missing negative conclusion:\n%s", stdout)
	}
	if !strings.Contains(stdout, "README.md DOES NOT reference a CLA") {
		t.Errorf("stdout missing negative README line:\n%s", stdout)
	}
	if stderr != "" {
		t.Errorf("stderr = %q, want empty", stderr)
	}
}

// TestRunKnownWithFailedChecks verifies that a positive Known detail (which
// has no error field) keeps the positive conclusion even when other checks
// failed, marked incomplete.
func TestRunKnownWithFailedChecks(t *testing.T) {
	agg := &needcla.Errors{InREADMEErr: errors.New("readme failure")}
	fc := &fakeChecker{details: needcla.Details{Known: true}, err: agg}
	stdout, stderr, status := runCLI(t, []string{"owner", "repo"}, fc)
	if status != 1 {
		t.Errorf("status = %d, want 1", status)
	}
	if !strings.Contains(stdout, "owner IS a known CLA requirer") {
		t.Errorf("stdout missing known line:\n%s", stdout)
	}
	if !strings.Contains(stdout, "README.md UNKNOWN (check failed)") {
		t.Errorf("stdout missing UNKNOWN README line:\n%s", stdout)
	}
	if !strings.Contains(stdout, "DOES need a CLA signed before contributing (incomplete report: some checks failed)") {
		t.Errorf("stdout missing positive-with-notice conclusion:\n%s", stdout)
	}
	if strings.Contains(stdout, "Inconclusive") {
		t.Errorf("stdout should not be inconclusive when Known is positive:\n%s", stdout)
	}
	if !strings.Contains(stderr, "readme failure") {
		t.Errorf("stderr missing diagnostic:\n%s", stderr)
	}
}

// TestRunKnownPositive verifies that the Known detail (which has no error
// field) yields a positive, complete result.
func TestRunKnownPositive(t *testing.T) {
	fc := &fakeChecker{details: needcla.Details{Known: true}, err: nil}
	stdout, _, status := runCLI(t, []string{"owner", "repo"}, fc)
	if status != 0 {
		t.Errorf("status = %d, want 0", status)
	}
	if !strings.Contains(stdout, "owner IS a known CLA requirer") {
		t.Errorf("stdout missing known line:\n%s", stdout)
	}
	if !strings.Contains(stdout, "[✓] I think owner/repo DOES need a CLA signed before contributing.") {
		t.Errorf("stdout missing positive conclusion:\n%s", stdout)
	}
}
