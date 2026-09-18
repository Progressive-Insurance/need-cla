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

	"github.com/google/go-github/v43/github"
)

var stringMatchers = []string{`\bCLA\b`, "Contributor License Agreement"}
var prLabelMatcher = `^cla:[[:space:]]*(yes|no)$`

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
	r, resp, err := client.Repositories.Get(ctx, owner, repo)
	if err != nil {
		return Details{}, repositoryError(owner, repo, resp, err)
	}
	if r == nil {
		return Details{}, fmt.Errorf("%s/%s: repository response did not contain a repository", owner, repo)
	}
	def := r.GetDefaultBranch()
	if def == "" {
		return Details{}, fmt.Errorf("%s/%s: repository response did not contain a usable default branch", owner, repo)
	}

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

// repositoryError classifies a repository lookup failure while preserving
// its cause. Only a 401 or a 404 from a real HTTP response maps to the
// library sentinels; throttling and permission 403s keep their original
// error so callers can recognize them.
func repositoryError(owner, repo string, resp *github.Response, err error) error {
	var status int
	if resp != nil {
		status = resp.StatusCode
	}
	var ge *github.ErrorResponse
	sentinel := error(nil)
	switch {
	case status == http.StatusNotFound && errors.As(err, &ge):
		sentinel = ErrNotFound
	case status == http.StatusUnauthorized && errors.As(err, &ge):
		sentinel = ErrInvalidToken
	}
	return &repositoryLookupError{
		owner:    owner,
		repo:     repo,
		sentinel: sentinel,
		cause:    err,
	}
}

// repositoryLookupError wraps a repository lookup failure so it carries both
// an optional sentinel and the original error.
type repositoryLookupError struct {
	owner    string
	repo     string
	sentinel error
	cause    error
}

func (e *repositoryLookupError) Error() string {
	msg := fmt.Sprintf("%s/%s: failed to get repository", e.owner, e.repo)
	if e.cause != nil {
		msg += ": " + e.cause.Error()
	}
	return msg
}

func (e *repositoryLookupError) Is(target error) bool {
	return e.sentinel != nil && errors.Is(e.sentinel, target)
}

func (e *repositoryLookupError) Unwrap() error {
	return e.cause
}
