/*
Copyright (c) 2021-2022 Progressive Casualty Insurance Company. All rights reserved.

Use of this source code is governed by an MIT license that can be found in
the LICENSE file at https://github.com/Progressive-Insurance/need-cla/blob/main/LICENSE.md
*/

package needcla

import (
	"context"
	"encoding/base64"
	"fmt"
	"net/http"
	"regexp"
	"sync"

	"github.com/google/go-github/v38/github"
)

// Details contains the results for CLA requirement using various hueristics
type Details struct {
	// Known is true if the owner of a repo is a known CLA requiror
	Known bool
	// Tag is true if a sample of PRs in the repo use a 'cla: yes' and/or 'cla: no' label
	Tag bool
	// BotFile is true if a .clabot config file is present in root
	BotFile bool
	// InContributing is true if the repo's CONTRIBUTING.md exists and refrences the CLA string matchers
	InContributing bool
	// InREADME is true if the repo's README.md exists and references the CLA string matchers
	InREADME bool
	// Action is true if a .github/workflow file has a 'uses: cla-assistant/github-action' line
	Action bool
}

var stringMatchers = []string{"\bCLA\b", "Contributor License Agreement"}
var actionMatcher = "uses:[[:space:]]*?cla-assistant/github-action"
var prLabelMatcher = "cla:[[:space:]]*?[yes|no]"

var tree *github.Tree

func (d *Details) Required() bool {
	if d.Tag || d.BotFile || d.InContributing || d.InREADME || d.Action {
		return true
	}
	return false
}

func Check(client *github.Client, owner string, repo string) (bool, error) {
	return CheckWithContext(context.Background(), client, owner, repo)
}

func CheckWithContext(ctx context.Context, client *github.Client, owner string, repo string) (bool, error) {
	d, err := DetailWithContext(ctx, client, owner, repo)
	return d.Required(), err
}

func Detail(client *github.Client, owner string, repo string) (*Details, error) {
	return DetailWithContext(context.Background(), client, owner, repo)
}

func DetailWithContext(ctx context.Context, client *github.Client, owner string, repo string) (*Details, error) {
	r, resp, _ := client.Repositories.Get(ctx, owner, repo)
	if resp.StatusCode == http.StatusNotFound {
		return &Details{}, fmt.Errorf("%s/%s: %w", owner, repo, ErrNotFound)
	}
	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		return &Details{}, ErrInvalidToken
	}
	def := r.GetDefaultBranch()

	type check = func(wg *sync.WaitGroup, d *Details, e *Errors)
	checks := []check{
		func(wg *sync.WaitGroup, d *Details, e *Errors) {
			defer wg.Done()
			d.Known = isKnown(owner)
		},
		func(wg *sync.WaitGroup, d *Details, e *Errors) {
			defer wg.Done()
			d.Tag, e.TagErr = hasCLATag(ctx, client, owner, repo)
		},
		func(wg *sync.WaitGroup, d *Details, e *Errors) {
			defer wg.Done()
			d.BotFile, e.BotFileErr = hasCLABotFile(ctx, client, owner, repo, def)
		},
		func(wg *sync.WaitGroup, d *Details, e *Errors) {
			defer wg.Done()
			d.InContributing, e.InContributingErr = referencesCLAInContributing(ctx, client, owner, repo, def)
		},
		func(wg *sync.WaitGroup, d *Details, e *Errors) {
			defer wg.Done()
			d.InREADME, e.InREADMEErr = referencesCLAInREADME(ctx, client, owner, repo, def)
		},
		func(wg *sync.WaitGroup, d *Details, e *Errors) {
			defer wg.Done()
			d.Action, e.ActionErr = usesCLAAssistantAction(ctx, client, owner, repo, def)
		},
	}
	var wg sync.WaitGroup
	wg.Add(len(checks))

	var (
		d Details
		e Errors
	)
	for _, c := range checks {
		// c(&wg, &d, &e)
		go c(&wg, &d, &e)
	}
	wg.Wait()

	return &d, e.ErrOrNil()
}

func find(ctx context.Context, client *github.Client, owner, repo, branch, path string) (*github.TreeEntry, error) {
	tree, _, err := client.Git.GetTree(ctx, owner, repo, branch, true)
	
	if err != nil {
		return nil, fmt.Errorf("failed to get %s/%s tree: %v", owner, repo, err)
	}
	// if tree.GetTruncated() {
	// TODO
	// response is too large
	// need to do our own recursion to the path
	// }
	for _, e := range tree.Entries {
		if e.GetPath() == path {
			return e, nil
		}
	}
	if tree.GetTruncated() {
		// TODO
		// remove once we're handling truncated trees properly
		return nil, ErrTruncatedTree
	}
	return nil, nil
}

func contentAtPath(ctx context.Context, client *github.Client, owner, repo, branch, path string) ([]byte, error) {
	te, err := find(ctx, client, owner, repo, branch, path)
	if te == nil {
		if err == ErrTruncatedTree {
			return nil, fmt.Errorf("tree was truncated and %s was possibly missed", path)
		}
		return nil, err
	}
	if te.GetType() != "blob" {
		return nil, fmt.Errorf("%s wasn't a blob", path)
	}
	b, _, err := client.Git.GetBlob(ctx, owner, repo, te.GetSHA())
	if err != nil {
		return nil, fmt.Errorf("error getting %s blob: %v", path, err)
	}
	if b.GetEncoding() != "base64" {
		return nil, fmt.Errorf("blob is encoded %s, only base64 is supported", b.GetEncoding())
	}
	return base64.StdEncoding.DecodeString(b.GetContent())
}

func contentAtSHA(ctx context.Context, client *github.Client, owner, repo, sha string) ([]byte, error) {
	b, _, err := client.Git.GetBlob(ctx, owner, repo, sha)
	if err != nil {
		return nil, fmt.Errorf("error getting %s blob: %v", sha, err)
	}
	if b.GetEncoding() != "base64" {
		return nil, fmt.Errorf("blob is encoded %s, only base64 is supported", b.GetEncoding())
	}
	return base64.StdEncoding.DecodeString(b.GetContent())
}

func referencesCLAInContent(content []byte) (bool, error) {
	var match bool
	var err error
	for _, matcher := range stringMatchers {
		match, err = regexp.Match(matcher, content)
		if err != nil {
			return false, fmt.Errorf(`error matching against "%s": %v`, matcher, err)
		}
		if match {
			return true, nil
		}
	}

	return false, nil
}
