/*
Copyright (c) 2021-2022 Progressive Casualty Insurance Company. All rights reserved.

Use of this source code is governed by an MIT license that can be found in
the LICENSE file at https://opensource.progressive.com/resources/license
*/

package needcla

import (
	"context"
	"encoding/base64"
	"fmt"
	"net/url"
	"path"
	"regexp"
	"sort"
	"strings"
	"sync"

	"github.com/google/go-github/v43/github"
	"gopkg.in/yaml.v3"
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
	c := &checker{
		client: client,
		branch: branch,
		repo:   repo,
		owner:  owner,
	}
	tree, err := fetchRootTree(ctx, client, owner, repo, branch)
	if err != nil {
		// The checker is still usable for the heuristics that do not depend on
		// the Git tree (known owner, PR labels); the tree-dependent heuristics
		// are gated by the caller on this error. c.tree is left nil.
		return c, err
	}
	c.tree = tree
	return c, nil
}

// fetchRootTree fetches the recursive root tree for a branch. The branch name
// is encoded as URL path data so that legal Git references containing
// URL-significant characters keep their identity:
//
//   - "release#1" must not become a request for branch "release" with "1" as
//     a URL fragment;
//   - "release%stable" must not fail URL parsing;
//   - "release%61" must not be decoded to "releasea".
//
// The pinned SDK's Git.GetTree interpolates the ref into the path without
// escaping, so the request is issued directly against the git/trees endpoint
// with url.PathEscape applied exactly once. Escaping is applied here (and not
// again by the SDK) so a future SDK upgrade that starts escaping on its own
// would be the only thing that could double-escape — a change that would be
// caught by the branch-encoding regression tests.
func fetchRootTree(ctx context.Context, client *github.Client, owner, repo, branch string) (*github.Tree, error) {
	u := fmt.Sprintf("repos/%v/%v/git/trees/%v", owner, repo, url.PathEscape(branch))
	u += "?recursive=1"
	req, err := client.NewRequest("GET", u, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to build %s/%s tree request for %q: %w", owner, repo, branch, err)
	}
	tree := new(github.Tree)
	if _, err := client.Do(ctx, req, tree); err != nil {
		// Preserve the underlying cause: transport failures, rate limiting,
		// permission errors, and JSON problems all stay inspectable.
		return nil, fmt.Errorf("failed to get %s/%s tree for %q: %w", owner, repo, branch, err)
	}
	return tree, nil
}

func (c checker) isKnownCheck(ctx context.Context) result {
	return result{
		d: Details{
			Known: c.isKnown(),
		},
	}
}

// isKnown compares the owner case-insensitively: GitHub treats owner names as
// case-insensitive, and the known-owner table is intentionally lowercase.
func (c checker) isKnown() bool {
	owner := strings.ToLower(c.owner)
	for _, o := range knownOwners {
		if o == owner {
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
	for _, pr := range prs {
		for _, label := range pr.Labels {
			if matchesCLAPrLabel(label.GetName()) {
				return true, nil
			}
		}
	}

	return false, nil
}

// matchesCLAPrLabel reports whether a full PR label name is exactly "cla: yes"
// or "cla: no" (case-insensitive, surrounding whitespace tolerated). The
// matcher is a package-level compiled pattern, so a match error cannot
// occur at call time.
func matchesCLAPrLabel(name string) bool {
	return prLabelRe.MatchString(name)
}

var prLabelRe = regexp.MustCompile(`(?i)^cla:[[:space:]]*(yes|no)$`)

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

// hasCLABotFile reports whether a regular .clabot file exists in the repo
// root. A directory or submodule at that path is not a config file. Symlinks
// are accepted: GitHub stores them as blobs whose type is "blob".
func (c checker) hasCLABotFile(ctx context.Context) (bool, error) {
	te, err := c.find(".clabot")
	if err != nil {
		return false, err
	}
	if te == nil {
		return false, nil
	}
	if te.GetType() != "blob" {
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

// usesCLAAssistantAction reports whether any workflow file in
// .github/workflows uses the CLA Assistant action. Only .yml/.yaml blobs are
// scanned; other entries (README files, backups, subdirectories, submodules)
// are skipped without blob requests. Workflow files that fail to fetch or
// parse are reported as per-file errors rather than silent negatives.
//
// A repository whose .github/workflows directory is absent in a complete tree
// is a confirmed negative, not an error.
func (c checker) usesCLAAssistantAction(ctx context.Context) (bool, error) {
	workflowsEntry, err := c.find(".github/workflows")
	if err != nil {
		return false, fmt.Errorf("locating .github/workflows: %w", err)
	}
	if workflowsEntry == nil {
		// A complete tree with no workflows directory cannot contain
		// workflows: this is a negative result, not an error, and it must
		// not trigger a follow-up request.
		return false, nil
	}
	if workflowsEntry.GetType() != "tree" {
		// A non-directory at the workflows path cannot contain active
		// workflow files.
		return false, nil
	}
	workflowsTree, _, err := c.client.Git.GetTree(ctx, c.owner, c.repo, workflowsEntry.GetSHA(), false)
	if err != nil {
		return false, fmt.Errorf("failed to get %s/%s %s/.github/workflows tree: %w", c.owner, c.repo, c.branch, err)
	}
	if workflowsTree.GetTruncated() {
		return false, fmt.Errorf("%s/%s %s/.github/workflows tree is incomplete: %w", c.owner, c.repo, c.branch, ErrTruncatedTree)
	}

	errs := make(map[string]error)
	for _, e := range workflowsTree.Entries {
		// Only .yml/.yaml files at the top level of .github/workflows are
		// executable workflows. Subdirectories, submodules, and other file
		// types are skipped without blob requests.
		if e.GetType() != "blob" || !isWorkflowFile(e.GetPath()) {
			continue
		}
		content, err := c.contentAtSHA(ctx, e.GetSHA())
		if err != nil {
			errs[e.GetPath()] = err
			continue
		}
		uses, err := workflowUsesCLAAAction(content)
		if err != nil {
			errs[e.GetPath()] = err
			continue
		}
		if uses {
			return true, nil
		}
	}

	if len(errs) != 0 {
		return false, newFileErrors(errs)
	}

	return false, nil
}

// isWorkflowFile reports whether a path inside .github/workflows is a
// workflow definition file: a top-level .yml or .yaml file.
func isWorkflowFile(p string) bool {
	base := path.Base(p)
	if base != p {
		// Nested files are not executable workflows.
		return false
	}
	return strings.HasSuffix(base, ".yml") || strings.HasSuffix(base, ".yaml")
}

// workflowUsesCLAAAction parses a workflow file as YAML and reports whether
// any job step's `uses` field references one of the CLA Assistant action
// repositories. Comments and arbitrary string values (such as shell text in
// `run` blocks) never match. Malformed YAML is an error so a broken file is
// not reported as a clean negative.
func workflowUsesCLAAAction(content []byte) (bool, error) {
	var doc struct {
		Jobs map[string]struct {
			Steps []map[string]interface{} `yaml:"steps"`
		} `yaml:"jobs"`
	}
	if err := yaml.Unmarshal(content, &doc); err != nil {
		return false, fmt.Errorf("malformed workflow YAML: %w", err)
	}
	for _, job := range doc.Jobs {
		for _, step := range job.Steps {
			if uses, ok := step["uses"].(string); ok && isCLAAActionReference(uses) {
				return true, nil
			}
		}
	}
	return false, nil
}

// isCLAAActionReference reports whether a step's `uses` value references one
// of the known CLA Assistant action repositories. The reference may be
// "owner/repo" or "owner/repo@ref"; the repository identity must match
// exactly, so similarly named repositories do not qualify.
func isCLAAActionReference(uses string) bool {
	uses = strings.TrimSpace(uses)
	// Local actions ("./path" or "./path@ref") are never a remote action.
	if strings.HasPrefix(uses, "./") || strings.HasPrefix(uses, "/") {
		return false
	}
	identity := uses
	if i := strings.Index(uses, "@"); i >= 0 {
		identity = uses[:i]
	}
	for _, repo := range claActionRepositories {
		if strings.EqualFold(identity, repo) {
			return true
		}
	}
	return false
}

// fileErrors aggregates per-file errors in deterministic (path-sorted)
// order. It implements the multi-error Unwrap contract so that
// errors.Is/errors.As can reach every contained error.
type fileErrors struct {
	summary string
	errs    []error
}

func newFileErrors(errs map[string]error) *fileErrors {
	paths := make([]string, 0, len(errs))
	for p := range errs {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	var lines []string
	for _, p := range paths {
		lines = append(lines, fmt.Sprintf("* %s: %v", p, errs[p]))
	}
	fe := &fileErrors{
		summary: fmt.Sprintf("%d error(s) checking for cla-assistant action:\n\t%s", len(paths), strings.Join(lines, "\n\t")),
	}
	for _, p := range paths {
		fe.errs = append(fe.errs, errs[p])
	}
	return fe
}

func (f *fileErrors) Error() string { return f.summary }

// Unwrap exposes the contained per-file errors for errors.Is / errors.As.
func (f *fileErrors) Unwrap() []error { return f.errs }

// independentChecks are the heuristics that do not require the Git tree: the
// known-owner table and the PR label scan. They can run even when the tree
// could not be loaded (see R02).
func (c checker) independentChecks(ctx context.Context) []check {
	return []check{c.isKnownCheck, c.hasCLATagCheck}
}

// treeDependentChecks are the heuristics that read files from the repository
// tree. They must not run when the tree is unavailable.
func (c checker) treeDependentChecks(ctx context.Context) []check {
	return []check{
		c.hasCLABotFileCheck,
		c.referencesCLAInContributingCheck,
		c.referencesCLAInREADMECheck,
		c.usesCLAAssistantActionCheck,
	}
}

func (c checker) checkAll(ctx context.Context) chan result {
	return c.runChecks(ctx, append(append([]check{}, c.independentChecks(ctx)...), c.treeDependentChecks(ctx)...))
}

func (c checker) runChecks(ctx context.Context, checks []check) chan result {
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

// find locates a path in the (possibly recursive) root tree. When the tree
// is complete, absence is a confirmed negative and is reported as (nil, nil).
// When the tree was truncated, the path may have been missed: ErrTruncatedTree
// is returned so callers keep "unknown" distinct from "confirmed absent".
//
// A successful truncated-tree fallback (targeted nonrecursive traversal) is
// deliberately not implemented here: the public contract in this codebase
// treats a truncated tree as an explicit uncertainty (see the audit plan's
// R01), and the regression tests assert the ErrTruncatedTree sentinel is
// preserved for truncated trees.
func (c checker) find(path string) (*github.TreeEntry, error) {
	if c.tree == nil {
		// Defensive: the tree-dependent heuristics are gated on a loaded tree
		// by DetailWithContext, but a nil tree must never panic.
		return nil, fmt.Errorf("%s/%s: the repository tree is unavailable", c.owner, c.repo)
	}
	for _, e := range c.tree.Entries {
		if e.GetPath() == path {
			return e, nil
		}
	}
	if c.tree.GetTruncated() {
		return nil, ErrTruncatedTree
	}
	return nil, nil
}

// contentAtPath fetches the decoded content of a blob at the given path in
// the root tree. A truncated tree yields ErrTruncatedTree (wrapped), a
// non-blob entry yields a type error, and fetch/decode failures preserve
// their underlying cause.
func (c checker) contentAtPath(ctx context.Context, path string) ([]byte, error) {
	te, err := c.find(path)
	if err != nil {
		return nil, err
	}
	if te == nil {
		return nil, nil
	}
	if te.GetType() != "blob" {
		return nil, fmt.Errorf("%s wasn't a blob", path)
	}
	return c.contentAtSHA(ctx, te.GetSHA())
}

// contentAtSHA fetches and base64-decodes the blob with the given SHA. The
// base64 decode error is returned wrapped (with context) rather than
// discarded so a corrupted blob is distinguishable from a missing one.
func (c checker) contentAtSHA(ctx context.Context, sha string) ([]byte, error) {
	b, _, err := c.client.Git.GetBlob(ctx, c.owner, c.repo, sha)
	if err != nil {
		return nil, fmt.Errorf("error getting %s blob: %w", sha, err)
	}
	if b.GetEncoding() != "base64" {
		return nil, fmt.Errorf("blob is encoded %s, only base64 is supported", b.GetEncoding())
	}
	content, err := base64.StdEncoding.DecodeString(b.GetContent())
	if err != nil {
		return nil, fmt.Errorf("decoding %s blob: %w", sha, err)
	}
	return content, nil
}

// claAcronymRe matches the standalone "CLA" acronym with word boundaries, so
// "Please sign the CLA." matches while "CLASS" and "CLAMP" do not.
var claAcronymRe = regexp.MustCompile(`\bCLA\b`)

// referencesCLAInContent reports whether the document contains the standalone
// "CLA" acronym or the expanded "Contributor License Agreement" phrase.
// The matchers are compiled once at package level, so a match error cannot
// occur at call time; the function therefore never fails on matching.
func (c checker) referencesCLAInContent(content []byte) (bool, error) {
	if claAcronymRe.Match(content) {
		return true, nil
	}
	return strings.Contains(string(content), `Contributor License Agreement`), nil
}
