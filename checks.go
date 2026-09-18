/*
Copyright (c) 2021-2022 Progressive Casualty Insurance Company. All rights reserved.

Use of this source code is governed by an MIT license that can be found in
the LICENSE file at https://opensource.progressive.com/resources/license
*/

package needcla

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"sync"

	"github.com/google/go-github/v43/github"
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
	// rootTreeSHA is the immutable SHA of the root tree returned by the
	// initial recursive retrieval. Truncated-tree recovery walks
	// non-recursive trees from this SHA so a single answer never mixes
	// different commits.
	rootTreeSHA string
}

// errorGroup is a small internal aggregate of the individual failures of a
// single check. It keeps every original cause so errors.Is and errors.As
// can traverse the group, and renders the failures in the order given.
type errorGroup struct {
	summary string
	errs    []error
}

func (g errorGroup) Error() string {
	lines := make([]string, 0, len(g.errs))
	for _, err := range g.errs {
		lines = append(lines, "* "+err.Error())
	}
	return fmt.Sprintf("%s:\n\t%s", g.summary, strings.Join(lines, "\n\t"))
}

// Is reports whether any failure in the group matches target.
func (g errorGroup) Is(target error) bool {
	for _, err := range g.errs {
		if errors.Is(err, target) {
			return true
		}
	}
	return false
}

// As reports whether any failure in the group matches target.
func (g errorGroup) As(target interface{}) bool {
	for _, err := range g.errs {
		if errors.As(err, target) {
			return true
		}
	}
	return false
}

func newChecker(ctx context.Context, client *github.Client, owner, repo, branch string) (*checker, error) {
	tree, _, err := client.Git.GetTree(ctx, owner, repo, branch, true)

	if err != nil {
		return nil, fmt.Errorf("failed to get %s/%s tree: %w", owner, repo, err)
	}

	return &checker{
		client:      client,
		branch:      branch,
		repo:        repo,
		owner:       owner,
		tree:        tree,
		rootTreeSHA: tree.GetSHA(),
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
		// GitHub owner names are case-insensitive, so compare without
		// distinguishing upper from lower case.
		if strings.EqualFold(o, c.owner) {
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
		return false, fmt.Errorf("error getting %s/%s PRs: %w", c.owner, c.repo, err)
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
		return false, errorGroup{
			summary: fmt.Sprintf("%d errors(s) checking recent PR labels", len(errs)),
			errs:    errs,
		}
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
	te, err := c.find(ctx, ".clabot")
	if te == nil || err != nil {
		return false, err
	}
	if te.GetType() != "blob" {
		// A directory or gitlink named .clabot is not a configuration file.
		return false, nil
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
		return false, fmt.Errorf("failed to check CONTRIBUTING.md: %w", err)
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
		return false, fmt.Errorf("failed to check README.md: %w", err)
	}
	return c.referencesCLAInContent(content)
}

// isWorkflowFile reports whether path names an immediate workflow file: a
// top-level name with no directory separator that ends in one of the exact
// supported extensions, .yml or .yaml.
func isWorkflowFile(path string) bool {
	if strings.Contains(path, "/") {
		return false
	}
	return strings.HasSuffix(path, ".yml") || strings.HasSuffix(path, ".yaml")
}

func (c checker) usesCLAAssistantAction(ctx context.Context) (bool, error) {
	workflowsEntry, err := c.find(ctx, ".github/workflows")
	if err != nil {
		return false, err
	}
	if workflowsEntry == nil {
		// A complete tree with no .github/workflows entry has no workflows.
		return false, nil
	}
	if workflowsEntry.GetType() != "tree" {
		// A blob or gitlink at .github/workflows cannot contain workflows.
		return false, nil
	}
	workflowsTree, _, err := c.client.Git.GetTree(ctx, c.owner, c.repo, workflowsEntry.GetSHA(), false)
	if err != nil {
		return false, fmt.Errorf("failed to get %s/%s/%s/.github/workflows tree: %w", c.owner, c.repo, c.branch, err)
	}

	// The truncation flag on the workflow-directory listing itself decides
	// whether a negative result is complete evidence. The listing is
	// requested exactly once; it is never re-requested hoping to become
	// complete.
	truncated := workflowsTree.GetTruncated()
	errs := make(map[string]error)
	for _, e := range workflowsTree.Entries {
		if e.GetType() != "blob" || !isWorkflowFile(e.GetPath()) {
			// Directories, gitlinks, and non-YAML files cannot be workflows.
			continue
		}
		content, err := c.contentAtSHA(ctx, e.GetSHA())
		if err != nil {
			errs[e.GetPath()] = err
			continue
		}
		match, err := workflowYAMLHasCLAAction(content)
		if err != nil {
			errs[e.GetPath()] = err
			continue
		}
		if match {
			// Positive evidence resolves the check affirmatively even when
			// the listing is incomplete.
			return true, nil
		}
	}

	if len(errs) != 0 || truncated {
		// Sort the paths so the diagnostics render and traverse in a
		// stable order regardless of map iteration order.
		paths := make([]string, 0, len(errs))
		for path := range errs {
			paths = append(paths, path)
		}
		sort.Strings(paths)
		count := len(errs)
		if truncated {
			count++
		}
		group := errorGroup{summary: fmt.Sprintf("%d error(s) checking for cla-assistant action", count)}
		for _, path := range paths {
			group.errs = append(group.errs, fmt.Errorf("%s: %w", path, errs[path]))
		}
		if truncated {
			// No positive match and the listing is incomplete: report the
			// path-qualified truncation cause alongside the retained file
			// failures.
			group.errs = append(group.errs, fmt.Errorf(".github/workflows: %w", ErrTruncatedTree))
		}
		return false, group
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

// find returns the entry at path. An entry present in the original recursive
// listing is the fast path and is returned without further requests. A path
// absent from a complete listing is ordinary absence. A path omitted from a
// truncated listing is recovered by walking non-recursive trees from the
// immutable root tree SHA; if it still cannot be resolved, find returns an
// error wrapping ErrTruncatedTree. Transport and context errors from the
// recovery requests are returned as their original causes.
func (c checker) find(ctx context.Context, path string) (*github.TreeEntry, error) {
	for _, e := range c.tree.Entries {
		if e.GetPath() == path {
			return e, nil
		}
	}
	if !c.tree.GetTruncated() {
		return nil, nil
	}
	return c.findInTruncatedTree(ctx, path)
}

// findInTruncatedTree resolves path after the recursive listing omitted it
// while truncated. It requests non-recursive trees at the immutable root
// tree SHA and then at each matching intermediate directory SHA, traversing
// only the requested components. A matching component may be followed even
// when its containing listing is truncated. A component absent from a
// complete listing is ordinary absence; a component absent from an incomplete
// listing, or an unavailable root tree SHA, leaves the path unresolved and
// wraps ErrTruncatedTree. The fallback never restarts from the mutable
// default-branch name, so a single answer never mixes different commits.
func (c checker) findInTruncatedTree(ctx context.Context, path string) (*github.TreeEntry, error) {
	rootSHA := c.rootTreeSHA
	if rootSHA == "" {
		return nil, fmt.Errorf("tree was truncated and %s was possibly missed: %w", path, ErrTruncatedTree)
	}
	components := strings.Split(path, "/")
	sha := rootSHA
	var entry *github.TreeEntry
	for i, component := range components {
		tree, _, err := c.client.Git.GetTree(ctx, c.owner, c.repo, sha, false)
		if err != nil {
			return nil, fmt.Errorf("failed to get %s/%s tree %s: %w", c.owner, c.repo, sha, err)
		}
		entry = nil
		for _, e := range tree.Entries {
			if e.GetPath() == component {
				entry = e
				break
			}
		}
		if entry == nil {
			if tree.GetTruncated() {
				return nil, fmt.Errorf("tree was truncated and %s was possibly missed: %w", path, ErrTruncatedTree)
			}
			return nil, nil
		}
		// An intermediate component must be a directory to traverse into; a
		// blob or gitlink cannot contain the remaining path components, so
		// the path is absent regardless of the listing's completeness.
		if i < len(components)-1 && entry.GetType() != "tree" {
			return nil, nil
		}
		sha = entry.GetSHA()
	}
	return entry, nil
}

func (c checker) contentAtPath(ctx context.Context, path string) ([]byte, error) {
	te, err := c.find(ctx, path)
	if te == nil {
		return nil, err
	}
	if te.GetType() != "blob" {
		return nil, fmt.Errorf("%s wasn't a blob", path)
	}
	b, _, err := c.client.Git.GetBlob(ctx, c.owner, c.repo, te.GetSHA())
	if err != nil {
		return nil, fmt.Errorf("error getting %s blob: %w", path, err)
	}
	if b.GetEncoding() != "base64" {
		return nil, fmt.Errorf("blob is encoded %s, only base64 is supported", b.GetEncoding())
	}
	return base64.StdEncoding.DecodeString(b.GetContent())
}

func (c checker) contentAtSHA(ctx context.Context, sha string) ([]byte, error) {
	b, _, err := c.client.Git.GetBlob(ctx, c.owner, c.repo, sha)
	if err != nil {
		return nil, fmt.Errorf("error getting %s blob: %w", sha, err)
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
			return false, fmt.Errorf(`error matching against "%s": %w`, matcher, err)
		}
		if match {
			return true, nil
		}
	}

	return false, nil
}
