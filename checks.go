package needcla

import (
	"context"
	"fmt"
	"regexp"
	"strings"

	"github.com/google/go-github/v38/github"
)

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
