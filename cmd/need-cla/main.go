/*
Copyright (c) 2021-2022 Progressive Casualty Insurance Company. All rights reserved.

Use of this source code is governed by an MIT license that can be found in
the LICENSE file at https://opensource.progressive.com/resources/license
*/

package main

import (
	"context"
	"flag"
	"fmt"
	"net/http"
	"os"
	"time"

	"github.com/google/go-github/v43/github"
	"github.com/peterbourgon/ff/v3"
	needcla "github.com/progressive-insurance/need-cla"
	"golang.org/x/oauth2"
)

// Exit status contract:
//
//	0  the scan completed; the verdict (needs a CLA / does not) is reliable
//	1  fatal startup or transport failure: bad arguments, invalid credentials,
//	   network error, or the repository/tree metadata could not be loaded.
//	   No verdict is printed.
//	3  the scan ran but at least one heuristic failed (partial evidence). The
//	   verdict is printed as "incomplete" and is NOT a reliable negative: a
//	   failed heuristic is reported as unknown, never as a negative finding.
const (
	exitComplete   = 0
	exitFatal      = 1
	exitIncomplete = 3
)

const defaultTimeout = 60 * time.Second

var token string

func main() {
	os.Exit(run(os.Args[1:]))
}

func run(args []string) int {
	fs := flag.NewFlagSet("need-cla", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	fs.Usage = func() {
		fmt.Fprintln(os.Stderr, "Usage of ./need-cla: need-cla [-h] [-token GITHUB_PERSONAL_ACCESS_TOKEN] [-timeout DURATION] owner repo")
		fmt.Fprintln(os.Stderr, "  -token string\n  \tGitHub personal access token, can also be passed as CLA_TOKEN env var")
		fmt.Fprintln(os.Stderr, "  -timeout duration\n  \tOverall request deadline, can also be passed as CLA_TIMEOUT (default 1m)")
	}
	fs.StringVar(&token, "token", "", "GitHub personal access token")
	var timeout time.Duration
	fs.DurationVar(&timeout, "timeout", defaultTimeout, "overall request deadline")
	// ff.Parse maps CLA_TOKEN -> -token and CLA_TIMEOUT -> -timeout.
	if err := ff.Parse(fs, args, ff.WithEnvVarPrefix("CLA")); err != nil {
		// A parse error (including -h) already printed usage/diagnostics to
		// stderr; fail locally without any network access.
		return exitFatal
	}

	// Validate the required positional arguments before constructing the
	// client or issuing any request.
	if fs.NArg() != 2 {
		fmt.Fprintf(os.Stderr, "need-cla: expected exactly 2 arguments (owner repo), got %d\n", fs.NArg())
		fs.Usage()
		return exitFatal
	}
	owner, repo := fs.Arg(0), fs.Arg(1)
	if owner == "" || repo == "" {
		fmt.Fprintln(os.Stderr, "need-cla: owner and repo must both be nonempty")
		fs.Usage()
		return exitFatal
	}

	var httpClient *http.Client
	if token != "" {
		ts := oauth2.StaticTokenSource(
			&oauth2.Token{AccessToken: token},
		)
		httpClient = oauth2.NewClient(context.Background(), ts)
	}
	client := github.NewClient(httpClient)

	// Every request made by the scan is bound by this deadline so the
	// command cannot hang indefinitely on a stalled connection. Library
	// callers keep full control of their own context and client.
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	d, err := needcla.DetailWithContext(ctx, client, owner, repo)
	if err != nil {
		if partial, ok := err.(*needcla.Errors); ok {
			reportPartial(os.Stdout, os.Stderr, owner, repo, d, partial)
			return exitIncomplete
		}
		fmt.Fprintf(os.Stderr, "need-cla: %v\n", err)
		return exitFatal
	}

	out := os.Stdout
	fmt.Fprintf(out, "I found that %s/%s:\n", owner, repo)
	fmt.Fprintf(out, "\t* %s %s a known CLA requirer\n", owner, is(d.Known))
	fmt.Fprintf(out, "\t* CONTRIBUTING.md %s reference a CLA\n", does(d.InContributing))
	fmt.Fprintf(out, "\t* README.md %s reference a CLA\n", does(d.InREADME))
	fmt.Fprintf(out, "\t* workflows %s use the CLA Assistant GitHub Action\n", does(d.Action))
	fmt.Fprintf(out, "\t* PRs %s have \"cla\" tags\n", do(d.Tag))
	fmt.Fprintf(out, "\t* .clabot file %s exist\n", does(d.BotFile))
	fmt.Fprintf(out, "[%s] I think %s/%s %s need a CLA signed before contributing.\n", symbol(d.Required()), owner, repo, does(d.Required()))
	return exitComplete
}

// reportPartial prints the evidence that WAS established, marks the failed
// heuristics as unknown (never negative), and sends diagnostics to stderr.
// A scan with no positive evidence and a failed heuristic is therefore
// reported as incomplete, not as a successful negative scan.
func reportPartial(out, errOut *os.File, owner, repo string, d needcla.Details, e *needcla.Errors) {
	fmt.Fprintln(errOut, e)
	fmt.Fprintln(errOut)

	fmt.Fprintf(out, "I found that %s/%s (scan incomplete, see diagnostics):\n", owner, repo)
	fmt.Fprintf(out, "\t* %s %s a known CLA requirer\n", owner, is(d.Known))
	fmt.Fprintf(out, "\t* CONTRIBUTING.md %s\n", outcome(d.InContributing, e.InContributingErr != nil, "reference a CLA"))
	fmt.Fprintf(out, "\t* README.md %s\n", outcome(d.InREADME, e.InREADMEErr != nil, "reference a CLA"))
	fmt.Fprintf(out, "\t* workflows %s\n", outcome(d.Action, e.ActionErr != nil, "use the CLA Assistant GitHub Action"))
	fmt.Fprintf(out, "\t* PRs %s\n", outcome(d.Tag, e.TagErr != nil, "have \"cla\" tags"))
	fmt.Fprintf(out, "\t* .clabot file %s\n", outcome(d.BotFile, e.BotFileErr != nil, "exist"))
	switch {
	case d.Required():
		// Positive evidence is still valid: the repository needs a CLA.
		fmt.Fprintf(out, "[✓] I think %s/%s DOES need a CLA signed before contributing (positive evidence was found), but the scan is INCOMPLETE: at least one heuristic failed, so additional evidence may exist.\n", owner, repo)
	default:
		// No positive evidence plus a failed heuristic is NOT a reliable
		// negative result: the failed heuristic could have found evidence.
		fmt.Fprintf(out, "[✗] The scan for %s/%s is INCOMPLETE: no positive evidence was found, but at least one heuristic failed, so this is not a reliable negative result.\n", owner, repo)
	}
}

// outcome renders one heuristic's line. A failed check is "unknown" even when
// its boolean is false; a successful check keeps its positive/negative word.
func outcome(positive, failed bool, phrase string) string {
	switch {
	case failed:
		return "could not be checked"
	case positive:
		return "DOES " + phrase
	default:
		return "DOES NOT " + phrase
	}
}

func symbol(b bool) string {
	if b {
		return "✓"
	}
	return "✗"
}

func is(b bool) string {
	if b {
		return "IS"
	}
	return "IS NOT"
}

func does(b bool) string {
	if b {
		return "DOES"
	}
	return "DOES NOT"
}

func do(b bool) string {
	if b {
		return "DO"
	}
	return "DO NOT"
}
