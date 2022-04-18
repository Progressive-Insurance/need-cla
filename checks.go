package needcla

import (
	"context"
	"encoding/base64"
	"fmt"
	"regexp"
	"strings"
	"sync"

	"github.com/google/go-github/v38/github"
)

type check = func(context.Context) result
type result struct {
	d Details
	e Errors
}
type checker struct {
	branch string
	repo   string
	owner  string

	client *github.Client

	tree *github.Tree
}

func newChecker(ctx context.Context, client *github.Client, owner, repo, branch string) (*checker, error) {
	tree, _, err := client.Git.GetTree(ctx, owner, repo, branch, true)

	if err != nil {
		return nil, fmt.Errorf("failed to get %s/%s tree: %v", owner, repo, err)
	}

	return &checker{
		client: client,
		branch: branch,
		repo:   repo,
		owner:  owner,
		tree:   tree,
	}, nil
}

func (c checker) isKnownCheck(ctx context.Context) result {
	return result{
		d: Details{
			Known: c.isKnown(),
		},
	}
}

func (c checker) isKnown() bool {
	for _, o := range knownOwners {
		if o == c.owner {
			return true
		}
	}
	return false
}

func (c checker) hasCLATagCheck(ctx context.Context) result {
	r, err := c.hasCLATag(ctx)
	return result{
		d: Details{
			Tag: r,
		},
		e: Errors{
			TagErr: err,
		},
	}
}

func (c checker) hasCLATag(ctx context.Context) (bool, error) {
	opts := &github.PullRequestListOptions{
		ListOptions: github.ListOptions{
			PerPage: 100,
		},
		State: "all",
	}
	prs, _, err := c.client.PullRequests.List(ctx, c.owner, c.repo, opts)
	if err != nil {
		return false, fmt.Errorf("error getting %s/%s PRs: %v", c.owner, c.repo, err)
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

func (c checker) hasCLABotFileCheck(ctx context.Context) result {
	r, err := c.hasCLABotFile(ctx)
	return result{
		d: Details{
			BotFile: r,
		},
		e: Errors{
			BotFileErr: err,
		},
	}
}

func (c checker) hasCLABotFile(ctx context.Context) (bool, error) {
	te, err := c.find(".clabot")
	if te == nil || err != nil {
		return false, err
	}
	return true, nil
}

func (c checker) referencesCLAInContributingCheck(ctx context.Context) result {
	r, err := c.referencesCLAInContributing(ctx)
	return result{
		d: Details{
			InContributing: r,
		},
		e: Errors{
			InContributingErr: err,
		},
	}
}

func (c checker) referencesCLAInContributing(ctx context.Context) (bool, error) {
	content, err := c.contentAtPath(ctx, "CONTRIBUTING.md")
	if err != nil {
		return false, fmt.Errorf("failed to check CONTRIBUTING.md: %v", err)
	}
	return c.referencesCLAInContent(content)
}

func (c checker) referencesCLAInREADMECheck(ctx context.Context) result {
	r, err := c.referencesCLAInREADME(ctx)
	return result{
		d: Details{
			InREADME: r,
		},
		e: Errors{
			InREADMEErr: err,
		},
	}
}

func (c checker) usesCLAAssistantActionCheck(ctx context.Context) result {
	r, err := c.usesCLAAssistantAction(ctx)
	return result{
		d: Details{
			Action: r,
		},
		e: Errors{
			ActionErr: err,
		},
	}
}

func (c checker) referencesCLAInREADME(ctx context.Context) (bool, error) {
	content, err := c.contentAtPath(ctx, "README.md")
	if err != nil {
		return false, fmt.Errorf("failed to check README.md: %v", err)
	}
	return c.referencesCLAInContent(content)
}

func (c checker) usesCLAAssistantAction(ctx context.Context) (bool, error) {
	workflowsEntry, err := c.find(".github/workflows")
	if err != nil {
		if err == ErrTruncatedTree {
			return false, fmt.Errorf("tree was truncated and .github/workflows was possibly missed")
		}
		return false, err
	}
	workflowsTree, _, err := c.client.Git.GetTree(ctx, c.owner, c.repo, workflowsEntry.GetSHA(), false)
	if err != nil {
		return false, fmt.Errorf("failed to get %s/%s/master/.github/workflows tree: %v", c.owner, c.repo, err)
	}

	errs := make(map[string]error)
	for _, e := range workflowsTree.Entries {
		content, err := c.contentAtSHA(ctx, e.GetSHA())
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

func (c checker) checkAll(ctx context.Context) chan result {
	checks := []check{
		c.isKnownCheck,
		c.hasCLATagCheck,
		c.hasCLABotFileCheck,
		c.referencesCLAInContributingCheck,
		c.referencesCLAInREADMECheck,
		c.usesCLAAssistantActionCheck,
	}
	results := make(chan result)
	var wg sync.WaitGroup
	wg.Add(len(checks))

	go func() {
		wg.Wait()
		close(results)
	}()

	for _, chk := range checks {
		go func(chk check) {
			defer wg.Done()
			results <- chk(ctx)
		}(chk)
	}

	return results
}

func (c checker) find(path string) (*github.TreeEntry, error) {
	// if tree.GetTruncated() {
	// TODO
	// response is too large
	// need to do our own recursion to the path
	// }
	for _, e := range c.tree.Entries {
		if e.GetPath() == path {
			return e, nil
		}
	}
	if c.tree.GetTruncated() {
		// TODO
		// remove once we're handling truncated trees properly
		return nil, ErrTruncatedTree
	}
	return nil, nil
}

func (c checker) contentAtPath(ctx context.Context, path string) ([]byte, error) {
	te, err := c.find(path)
	if te == nil {
		if err == ErrTruncatedTree {
			return nil, fmt.Errorf("tree was truncated and %s was possibly missed", path)
		}
		return nil, err
	}
	if te.GetType() != "blob" {
		return nil, fmt.Errorf("%s wasn't a blob", path)
	}
	b, _, err := c.client.Git.GetBlob(ctx, c.owner, c.repo, te.GetSHA())
	if err != nil {
		return nil, fmt.Errorf("error getting %s blob: %v", path, err)
	}
	if b.GetEncoding() != "base64" {
		return nil, fmt.Errorf("blob is encoded %s, only base64 is supported", b.GetEncoding())
	}
	return base64.StdEncoding.DecodeString(b.GetContent())
}

func (c checker) contentAtSHA(ctx context.Context, sha string) ([]byte, error) {
	b, _, err := c.client.Git.GetBlob(ctx, c.owner, c.repo, sha)
	if err != nil {
		return nil, fmt.Errorf("error getting %s blob: %v", sha, err)
	}
	if b.GetEncoding() != "base64" {
		return nil, fmt.Errorf("blob is encoded %s, only base64 is supported", b.GetEncoding())
	}
	return base64.StdEncoding.DecodeString(b.GetContent())
}

func (c checker) referencesCLAInContent(content []byte) (bool, error) {
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
