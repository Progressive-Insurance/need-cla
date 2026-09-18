/*
Copyright (c) 2021-2022 Progressive Casualty Insurance Company. All rights reserved.

Use of this source code is governed by an MIT license that can be found in
the LICENSE file at https://opensource.progressive.com/resources/license
*/

package needcla

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/google/go-github/v43/github"
)

// claActionRepositories are the exact repository identities of the
// CLA Assistant GitHub Action. The second entry is the current upstream
// repository; the first is the legacy alias it was published under.
var claActionRepositories = []string{
	"cla-assistant/github-action",
	"contributor-assistant/github-action",
}

// Check determines whether a repository might need a CLA. See CheckWithContext.
func Check(client *github.Client, owner string, repo string) (bool, error) {
	return CheckWithContext(context.Background(), client, owner, repo)
}

// CheckWithContext determines whether a repository might need a CLA using the
// heuristics described in the package README.
//
// It returns true when at least one heuristic produced positive evidence. The
// returned error is nil when every heuristic completed; when some heuristics
// failed, the error is an *Errors value describing them, and the boolean only
// reflects the evidence that was successfully gathered. A false result with a
// non-nil error must be treated as an incomplete scan, not as proof that no
// CLA requirement exists.
func CheckWithContext(ctx context.Context, client *github.Client, owner string, repo string) (bool, error) {
	d, err := DetailWithContext(ctx, client, owner, repo)
	return d.Required(), err
}

// Detail gathers the individual heuristic results. See DetailWithContext.
func Detail(client *github.Client, owner string, repo string) (Details, error) {
	return DetailWithContext(context.Background(), client, owner, repo)
}

// DetailWithContext gathers the individual heuristic results for a repository.
// The returned Details reports only the evidence the heuristics established;
// when any heuristic failed, the error is an *Errors value identifying which
// checks could not be completed.
func DetailWithContext(ctx context.Context, client *github.Client, owner string, repo string) (Details, error) {
	if client == nil {
		return Details{}, fmt.Errorf("github client must not be nil")
	}
	owner, repo = strings.TrimSpace(owner), strings.TrimSpace(repo)
	if owner == "" || repo == "" {
		return Details{}, fmt.Errorf("owner and repo must both be nonempty")
	}

	// The rate lookup is the first network request and the only preflight:
	// it surfaces bad credentials (401) and transport failures up front, and
	// real rate-limit errors are then observed on the requests that actually
	// consume quota instead of guessing that a fixed remaining-quota
	// threshold is enough (a small scan can finish below any such threshold,
	// and a large workflow directory cannot be guaranteed by one).
	if _, _, err := client.RateLimits(ctx); err != nil {
		if apiErr := classifyAuthError(ctx, err, "rate limit lookup"); apiErr != nil {
			return Details{}, apiErr
		}
		return Details{}, fmt.Errorf("failed to get github rate limit: %w", err)
	}

	r, resp, err := client.Repositories.Get(ctx, owner, repo)
	if err != nil {
		// Inspect the error before touching resp or r: on transport
		// failures both are nil.
		if apiErr := classifyAuthError(ctx, err, "repository metadata"); apiErr != nil {
			return Details{}, apiErr
		}
		if resp != nil && resp.StatusCode == http.StatusNotFound {
			return Details{}, fmt.Errorf("%s/%s: %w", owner, repo, ErrNotFound)
		}
		// Preserve the original cause (HTTP status, JSON decode, transport)
		// instead of discarding it.
		return Details{}, fmt.Errorf("failed to get %s/%s metadata: %w", owner, repo, err)
	}
	def := r.GetDefaultBranch()
	if def == "" {
		return Details{}, fmt.Errorf("%s/%s has no default branch metadata", owner, repo)
	}

	c, err := newChecker(ctx, client, owner, repo, def)
	if c == nil {
		return Details{}, fmt.Errorf("failed to create checker: %w", err)
	}

	var (
		d = new(Details)
		e = new(Errors)
	)

	// The known-owner and PR-label heuristics do not depend on the Git tree,
	// so they always run. This preserves independent evidence (e.g. a known
	// CLA requirer) even when the tree could not be loaded.
	for result := range c.runChecks(ctx, c.independentChecks(ctx)) {
		d.merge(result.d)
		e.merge(result.e)
	}

	if err != nil {
		// The tree could not be loaded (R02): this is a partial scan, not a
		// fatal one. The tree-dependent heuristics could not run, so each of
		// their error fields carries the cause and the result is incomplete.
		treeErr := fmt.Errorf("failed to load the repository tree: %w", err)
		e.BotFileErr = treeErr
		e.InContributingErr = treeErr
		e.InREADMEErr = treeErr
		e.ActionErr = treeErr
		return *d, e.ErrOrNil()
	}

	// A complete tree is available: run the tree-dependent heuristics too.
	for result := range c.runChecks(ctx, c.treeDependentChecks(ctx)) {
		d.merge(result.d)
		e.merge(result.e)
	}

	return *d, e.ErrOrNil()
}

// classifyAuthError reports err as ErrInvalidToken when it is an
// authentication failure (HTTP 401) or the caller's context was cancelled.
// Permission failures (403), rate limiting (403/429), and transport errors
// are distinct conditions and are not reclassified: the caller returns the
// original typed error so they remain distinguishable.
func classifyAuthError(ctx context.Context, err error, what string) error {
	if ctxErr := ctx.Err(); ctxErr != nil {
		return fmt.Errorf("%s: %w", what, ctxErr)
	}
	var apiErr *github.ErrorResponse
	if errors.As(err, &apiErr) && apiErr.Response != nil && apiErr.Response.StatusCode == http.StatusUnauthorized {
		return fmt.Errorf("%s: %w", what, ErrInvalidToken)
	}
	return nil
}
