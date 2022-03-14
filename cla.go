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
	"strings"

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
	known := isKnown(owner)

	def := r.GetDefaultBranch()

	// TODO: parallelize
	tag, tagerr := hasCLATag(ctx, client, owner, repo)
	bot, boterr := hasCLABotFile(ctx, client, owner, repo, def)
	contrib, contriberr := referencesCLAInContributing(ctx, client, owner, repo, def)
	readme, readmeerr := referencesCLAInREADME(ctx, client, owner, repo, def)
	act, acterr := usesCLAAssistantAction(ctx, client, owner, repo, def)

	d := &Details{
		Known:          known,
		Tag:            tag,
		BotFile:        bot,
		InContributing: contrib,
		InREADME:       readme,
		Action:         act,
	}

	e := &Errors{
		TagErr:            tagerr,
		BotFileErr:        boterr,
		InContributingErr: contriberr,
		InREADMEErr:       readmeerr,
		ActionErr:         acterr,
	}

	return d, e.ErrOrNil()
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

func isKnown(owner string) bool {
	for _, o := range knownOwners {
		if o == owner {
			return true
		}
	}
	return false
}

func hasCLATag(ctx context.Context, client *github.Client, owner, repo string) (bool, error) {
	opts := &github.PullRequestListOptions{
		ListOptions: github.ListOptions{
			PerPage: 100,
		},
		State: "all",
	}
	prs, _, err := client.PullRequests.List(ctx, owner, repo, opts)
	if err != nil {
		return false, fmt.Errorf("error getting %s/%s PRs: %v", owner, repo, err)
	}
	errs := make([]error, 0, 100)
	for _, pr := range prs {
		for _, label := range pr.Labels {
			match, err := regexp.Match(prLabelMatcher, []byte(label.GetName()))
			if err != nil {
				errs = append(errs, err)
			}
			if match {
				return true, nil
			}
		}
	}
	if len(errs) != 0 {
		var lines []string
		for _, err := range errs {
			lines = append(lines, fmt.Sprintf("* %v", err))
		}
		return false, fmt.Errorf("%d errors(s) checking recent PR labels:\n\t%s", len(errs), strings.Join(lines, "\n\t"))
	}

	return false, nil
}

func hasCLABotFile(ctx context.Context, client *github.Client, owner, repo, branch string) (bool, error) {
	te, err := find(ctx, client, owner, repo, branch, ".clabot")
	if te == nil || err != nil {
		return false, err
	}
	return true, nil
}

func referencesCLAInContributing(ctx context.Context, client *github.Client, owner, repo, branch string) (bool, error) {
	content, err := contentAtPath(ctx, client, owner, repo, branch, "CONTRIBUTING.md")
	if err != nil {
		return false, fmt.Errorf("failed to check CONTRIBUTING.md: %v", err)
	}
	return referencesCLAInContent(content)
}

func referencesCLAInREADME(ctx context.Context, client *github.Client, owner, repo, branch string) (bool, error) {
	content, err := contentAtPath(ctx, client, owner, repo, branch, "README.md")
	if err != nil {
		return false, fmt.Errorf("failed to check README.md: %v", err)
	}
	return referencesCLAInContent(content)
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

func usesCLAAssistantAction(ctx context.Context, client *github.Client, owner, repo, branch string) (bool, error) {
	workflowsEntry, err := find(ctx, client, owner, repo, branch, ".github/workflows")
	if err != nil {
		if err == ErrTruncatedTree {
			return false, fmt.Errorf("tree was truncated and .github/workflows was possibly missed")
		}
		return false, err
	}
	workflowsTree, _, err := client.Git.GetTree(ctx, owner, repo, workflowsEntry.GetSHA(), false)
	if err != nil {
		return false, fmt.Errorf("failed to get %s/%s/master/.github/workflows tree: %v", owner, repo, err)
	}

	errs := make(map[string]error)
	for _, e := range workflowsTree.Entries {
		content, err := contentAtSHA(ctx, client, owner, repo, e.GetSHA())
		if err != nil {
			errs[e.GetPath()] = err
			continue
		}
		match, err := regexp.Match(actionMatcher, content)
		if err != nil {
			errs[e.GetPath()] = err
			continue
		}
		if match {
			return true, nil
		}
	}

	if len(errs) != 0 {
		var lines []string
		for path, err := range errs {
			lines = append(lines, fmt.Sprintf("* %s: %v", path, err))
		}
		return false, fmt.Errorf("%d error(s) checking for cla-assistant action:\n\t%s", len(errs), strings.Join(lines, "\n\t"))
	}

	return false, nil
}
