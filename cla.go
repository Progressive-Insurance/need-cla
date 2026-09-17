/*
Copyright (c) 2021-2022 Progressive Casualty Insurance Company. All rights reserved.

Use of this source code is governed by an MIT license that can be found in
the LICENSE file at https://github.com/Progressive-Insurance/need-cla/blob/main/LICENSE.md
*/

package needcla

import (
	"context"
	"fmt"
	"net/http"

	"github.com/google/go-github/v43/github"
)

// stringMatchers are the patterns searched for in CONTRIBUTING.md and README.md.
// Both are case-insensitive: the first is a whole-word match on "CLA", the second
// on "Contributor License Agreement" (also matching the British "Licence" spelling).
// They are raw strings: in a Go double-quoted string "\b" is the backspace
// character, not a regex word boundary, so the \b boundary in the first pattern
// must be written inside a raw string.
var stringMatchers = []string{`(?i)\bCLA\b`, `(?i)Contributor Licen[cs]e Agreement`}
var actionMatcher = "uses:[[:space:]]*?cla-assistant/github-action"
// prLabelMatcher matches a "cla: yes" or "cla: no" PR label, case-insensitively.
// It is anchored at the start (^) so labels that merely end in "cla:"
// (e.g. "not-a-cla: no") do not match, and the alternatives are grouped (yes|no)
// since [yes|no] is a character class that matches a single char.
var prLabelMatcher = "(?i)^cla:[[:space:]]*?(yes|no)\\b"

// minCoreRateLimit is a lower bound on the core API calls DetailWithContext makes:
// one repo get, one recursive tree get, and at least one PR list page, plus a blob
// get for each of CONTRIBUTING.md and README.md, the workflows tree, and one blob
// per workflow file. Repos with many PRs or workflow files use more, so treat this
// as a heuristic lower bound rather than an exact budget.
const minCoreRateLimit = 20

func Check(client *github.Client, owner string, repo string) (bool, error) {
	return CheckWithContext(context.Background(), client, owner, repo)
}

func CheckWithContext(ctx context.Context, client *github.Client, owner string, repo string) (bool, error) {
	d, err := DetailWithContext(ctx, client, owner, repo)
	return d.Required(), err
}

func Detail(client *github.Client, owner string, repo string) (Details, error) {
	return DetailWithContext(context.Background(), client, owner, repo)
}

func DetailWithContext(ctx context.Context, client *github.Client, owner string, repo string) (Details, error) {
	limits, _, err := client.RateLimits(ctx)
	if err != nil {
		return Details{}, fmt.Errorf("failed to get github rate limit: %w", err)
	}
	if limits.Core.Remaining < minCoreRateLimit {
		return Details{}, fmt.Errorf("remaining github rate limit too low")
	}

	r, resp, err := client.Repositories.Get(ctx, owner, repo)
	if err != nil {
		// On a transport failure (DNS, connection refused, timeout) go-github
		// returns a nil *http.Response, so the error must be checked before
		// dereferencing resp. A non-nil error with a nil resp is a real failure.
		if resp == nil {
			return Details{}, fmt.Errorf("failed to get %s/%s: %w", owner, repo, err)
		}
		if resp.StatusCode == http.StatusNotFound {
			return Details{}, fmt.Errorf("%s/%s: %w", owner, repo, ErrNotFound)
		}
		if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
			return Details{}, ErrInvalidToken
		}
		return Details{}, fmt.Errorf("failed to get %s/%s (status %d): %w", owner, repo, resp.StatusCode, err)
	}
	def := r.GetDefaultBranch()

	c, err := newChecker(ctx, client, owner, repo, def)
	if err != nil {
		return Details{}, fmt.Errorf("failed to create checker: %w", err)
	}
	var (
		d = new(Details)
		e = new(Errors)
	)
	results := c.checkAll(ctx)
	for result := range results {
		d.merge(result.d)
		e.merge(result.e)
	}

	return *d, e.ErrOrNil()
}
