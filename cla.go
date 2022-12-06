/*
Copyright (c) 2021-2022 Progressive Casualty Insurance Company. All rights reserved.

Use of this source code is governed by an MIT license that can be found in
the LICENSE file at https://opensource.progressive.com/resources/license
*/

package needcla

import (
	"context"
	"fmt"
	"net/http"

	"github.com/google/go-github/v43/github"
)

var stringMatchers = []string{"\bCLA\b", "Contributor License Agreement"}
var actionMatcher = "uses:[[:space:]]*?cla-assistant/github-action"
var prLabelMatcher = "cla:[[:space:]]*?[yes|no]"

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
	if limits.Core.Remaining < 10 {
		// TODO: count actual API calls we'll make
		return Details{}, fmt.Errorf("remaining github rate limit too low")
	}

	r, resp, _ := client.Repositories.Get(ctx, owner, repo)
	if resp.StatusCode == http.StatusNotFound {
		return Details{}, fmt.Errorf("%s/%s: %w", owner, repo, ErrNotFound)
	}
	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		return Details{}, ErrInvalidToken
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
