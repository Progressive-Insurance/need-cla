/*
Copyright (c) 2021-2022 Progressive Casualty Insurance Company. All rights reserved.

Use of this source code is governed by an MIT license that can be found in
the LICENSE file at https://opensource.progressive.com/resources/license
*/

package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"

	"github.com/google/go-github/v43/github"
	"github.com/peterbourgon/ff/v3"
	needcla "github.com/progressive-insurance/need-cla"
	"golang.org/x/oauth2"
)

// checkerFunc runs the CLA check for owner/repo, authenticating with token
// when it is nonempty.
type checkerFunc func(token, owner, repo string) (needcla.Details, error)

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr, defaultCheck))
}

// defaultCheck builds a GitHub client (authenticated when a token is present)
// and runs the CLA check.
func defaultCheck(token, owner, repo string) (needcla.Details, error) {
	var httpClient *http.Client
	if token != "" {
		ctx := context.Background()
		ts := oauth2.StaticTokenSource(
			&oauth2.Token{AccessToken: token},
		)
		httpClient = oauth2.NewClient(ctx, ts)
	}
	client := github.NewClient(httpClient)
	return needcla.Detail(client, owner, repo)
}

// run parses and validates the command line, runs the check, and renders the
// report. It returns the process exit status: 0 for a completed check whether
// its conclusion is positive or negative, 2 for usage failures, and 1 for
// fatal or incomplete checks. Help returns 0. Error diagnostics go to stderr;
// the report goes to stdout.
func run(args []string, stdout, stderr io.Writer, check checkerFunc) int {
	fs := flag.NewFlagSet("need-cla", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	var token string
	fs.StringVar(&token, "token", "", "GitHub personal access token")
	// The flag package invokes Usage for both -h and parse errors; keep it a
	// no-op so run can route usage to stdout (help) or stderr (failure).
	fs.Usage = func() {}
	if err := ff.Parse(fs, args, ff.WithEnvVarPrefix("CLA")); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			printUsage(stdout)
			return 0
		}
		fmt.Fprintln(stderr, err)
		printUsage(stderr)
		return 2
	}

	owner := fs.Arg(0)
	repo := fs.Arg(1)
	if fs.NArg() != 2 || owner == "" || repo == "" {
		fmt.Fprintln(stderr, "need-cla: expected exactly two nonempty arguments: owner and repo")
		printUsage(stderr)
		return 2
	}

	d, err := check(token, owner, repo)
	if err != nil {
		var agg *needcla.Errors
		if errors.As(err, &agg) {
			// Partial results: print the diagnostics once to stderr, then
			// render the completed evidence and the unknown checks.
			fmt.Fprintln(stderr, err)
			return renderReport(stdout, owner, repo, d, agg)
		}
		// Fatal, non-aggregate error: no normal result report.
		fmt.Fprintln(stderr, err)
		return 1
	}
	return renderReport(stdout, owner, repo, d, nil)
}

func printUsage(w io.Writer) {
	fmt.Fprintln(w, "Usage of ./need-cla: need-cla [-h] [-token GITHUB_PERSONAL_ACCESS_TOKEN] owner repo")
	fmt.Fprintln(w, "  -token string\n  \tGitHub personal access token, can also be passed as CLA_TOKEN env var")
}

// renderReport writes the result report to w and returns the exit status for
// the rendered outcome. agg is nil for a complete check; a non-nil agg marks
// the checks stored in its fields as failed.
func renderReport(w io.Writer, owner, repo string, d needcla.Details, agg *needcla.Errors) int {
	var (
		tagErr            error
		botFileErr        error
		inContributingErr error
		inREADMEErr       error
		actionErr         error
	)
	if agg != nil {
		tagErr = agg.TagErr
		botFileErr = agg.BotFileErr
		inContributingErr = agg.InContributingErr
		inREADMEErr = agg.InREADMEErr
		actionErr = agg.ActionErr
	}
	incomplete := tagErr != nil || botFileErr != nil || inContributingErr != nil || inREADMEErr != nil || actionErr != nil

	lines := []string{
		fmt.Sprintf("I found that %s/%s:", owner, repo),
		fmt.Sprintf("* %s %s a known CLA requirer", owner, is(d.Known)),
		checkLine(d.InContributing, inContributingErr,
			"CONTRIBUTING.md DOES reference a CLA",
			"CONTRIBUTING.md DOES NOT reference a CLA",
			"CONTRIBUTING.md UNKNOWN (check failed)"),
		checkLine(d.InREADME, inREADMEErr,
			"README.md DOES reference a CLA",
			"README.md DOES NOT reference a CLA",
			"README.md UNKNOWN (check failed)"),
		checkLine(d.Action, actionErr,
			"DOES use the cla-bot Github Action",
			"DOES NOT use the cla-bot Github Action",
			"cla-bot Github Action UNKNOWN (check failed)"),
		checkLine(d.Tag, tagErr,
			"PRs DO have \"cla\" tags",
			"PRs DO NOT have \"cla\" tags",
			"PR \"cla\" tags UNKNOWN (check failed)"),
		checkLine(d.BotFile, botFileErr,
			".clabot file DOES exist",
			".clabot file DOES NOT exist",
			".clabot file UNKNOWN (check failed)"),
	}

	fmt.Fprintln(w, conclusion(owner, repo, d.Required(), incomplete))
	fmt.Fprintln(w)
	fmt.Fprintln(w, strings.Join(lines, "\n\t"))
	if incomplete {
		return 1
	}
	return 0
}

// checkLine renders one heuristic line. A true detail is positive even when
// its check also failed; a false detail whose check failed is unknown; a
// false detail with no error is negative.
func checkLine(found bool, err error, positive, negative, unknown string) string {
	phrase := positive
	if !found {
		if err != nil {
			phrase = unknown
		} else {
			phrase = negative
		}
	}
	return "* " + phrase
}

// conclusion renders the overall verdict. A positive verdict is retained when
// any detail is true, with an incompleteness notice when checks failed. With
// no positive evidence, failed checks make the outcome inconclusive; only a
// complete all-false result is negative.
func conclusion(owner, repo string, positive, incomplete bool) string {
	switch {
	case positive && !incomplete:
		return fmt.Sprintf("[✓] I think %s/%s DOES need a CLA signed before contributing.", owner, repo)
	case positive && incomplete:
		return fmt.Sprintf("[✓] I think %s/%s DOES need a CLA signed before contributing (incomplete report: some checks failed).", owner, repo)
	case !positive && incomplete:
		return fmt.Sprintf("[?] Inconclusive: some checks failed and no positive evidence was found, so I cannot tell whether %s/%s needs a CLA signed before contributing.", owner, repo)
	default:
		return fmt.Sprintf("[✗] I think %s/%s DOES NOT need a CLA signed before contributing.", owner, repo)
	}
}

func is(b bool) string {
	if b {
		return "IS"
	}
	return "IS NOT"
}
