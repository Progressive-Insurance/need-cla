/*
Copyright (c) 2021-2022 Progressive Casualty Insurance Company. All rights reserved.

Progressive-owned, no external contributions.
*/

package main

import (
	"context"
	"flag"
	"fmt"
	"net/http"
	"os"
	"strings"

	"github.com/google/go-github/v43/github"
	"github.com/peterbourgon/ff/v3"
	needcla "github.com/progressive-insurance/need-cla"
	"golang.org/x/oauth2"
)

var token string

func main() {
	fs := flag.NewFlagSet("need-cla", flag.ExitOnError)
	fs.StringVar(&token, "token", "", "GitHub personal access token")
	fs.Usage = func() {
		fmt.Println("Usage of ./need-cla: need-cla [-h] [-token GITHUB_PERSONAL_ACCESS_TOKEN] owner repo")
		fmt.Println("  -token string\n  \tGitHub personal access token, can also be passed as CLA_TOKEN env var")
	}
	ff.Parse(fs, os.Args[1:], ff.WithEnvVarPrefix("CLA"))
	// TODO
	// do we even need a token?
	// it doesn't make sense that we'd check for a CLA
	// for a private repository...
	var httpClient *http.Client
	if token == "" {
		httpClient = nil
	} else {
		ctx := context.Background()
		ts := oauth2.StaticTokenSource(
			&oauth2.Token{AccessToken: token},
		)
		httpClient = oauth2.NewClient(ctx, ts)
	}

	client := github.NewClient(httpClient)
	owner := fs.Arg(0)
	repo := fs.Arg(1)

	d, err := needcla.Detail(client, owner, repo)
	if err != nil {
		fmt.Println(err)
		if _, ok := err.(*needcla.Errors); !ok {
			os.Exit(1)
		}
		fmt.Println()
	}

	lines := []string{
		fmt.Sprintf("I found that %s/%s:", owner, repo),
		fmt.Sprintf("* %s %s a known CLA requirer", owner, is(d.Known)),
		fmt.Sprintf("* CONTRIBUTING.md %s reference a CLA", does(d.InContributing)),
		fmt.Sprintf("* README.md %s reference a CLA", does(d.InREADME)),
		fmt.Sprintf("* %s use the cla-bot Github Action", does(d.Action)),
		fmt.Sprintf("* PRs %s have \"cla\" tags", do(d.Tag)),
		fmt.Sprintf("* .clabot file %s exist", does(d.BotFile)),
	}

	fmt.Printf("[%s] I think %s/%s %s need a CLA signed before contributing.\n\n", symbol(d.Required()), owner, repo, does(d.Required()))
	fmt.Print(strings.Join(lines, "\n\t"))

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
