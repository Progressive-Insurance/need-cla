/*
Copyright (c) 2021-2022 Progressive Casualty Insurance Company. All rights reserved.

Use of this source code is governed by an MIT license that can be found in
the LICENSE file at https://opensource.progressive.com/resources/license
*/

package needcla

import (
	"errors"
	"fmt"
	"strings"
)

var ErrTruncatedTree = errors.New("git tree was truncated and path was possibly missed")
var ErrInvalidToken = errors.New("invalid personal access token")
var ErrNotFound = errors.New("not found")

// Errors returns errors from checking for CLA references
// adapted from hashicorp/go-multierror
// https://github.com/hashicorp/go-multierror/blob/9974e9ec57696378079ecc3accd3d6f29401b3a0/format.go#L14
type Errors struct {
	// TagErr is non-nil if there was an error checking for `Details.Tag`
	TagErr error
	// BotFileError is non-nil if there was an eror checking for `Details.BotFile`
	BotFileErr error
	// InContributingErr is non-nil if there was an error checking for `Details.InContributing`
	InContributingErr error
	// InREADMEErr is non-nil if there was an error checking for `Deatails.InREADME`
	InREADMEErr error
	// ActionErr is non-nil if there was an error checking for `Details.Action`
	ActionErr error
}

func (e *Errors) merge(errors Errors) {
	if errors.TagErr != nil {
		e.TagErr = errors.TagErr
	}
	if errors.BotFileErr != nil {
		e.BotFileErr = errors.BotFileErr
	}
	if errors.InContributingErr != nil {
		e.InContributingErr = errors.InContributingErr
	}
	if errors.InREADMEErr != nil {
		e.InREADMEErr = errors.InREADMEErr
	}
	if errors.ActionErr != nil {
		e.ActionErr = errors.ActionErr
	}
}

// errors is the ordered list of non-nil per-check errors. It is the single
// source of truth for Error(), ErrOrNil(), Is(), and As().
func (e Errors) errors() []error {
	var errs []error
	if e.TagErr != nil {
		errs = append(errs, e.TagErr)
	}
	if e.BotFileErr != nil {
		errs = append(errs, e.BotFileErr)
	}
	if e.InContributingErr != nil {
		errs = append(errs, e.InContributingErr)
	}
	if e.InREADMEErr != nil {
		errs = append(errs, e.InREADMEErr)
	}
	if e.ActionErr != nil {
		errs = append(errs, e.ActionErr)
	}
	return errs
}

func (e Errors) Error() string {
	errs := e.errors()
	if len(errs) == 0 {
		return "0 error(s) checking for CLA references"
	}
	var lines []string
	if e.TagErr != nil {
		lines = append(lines, fmt.Sprintf("* checking for CLA tag: %v", e.TagErr))
	}
	if e.BotFileErr != nil {
		lines = append(lines, fmt.Sprintf("* checking for .clabot file: %v", e.BotFileErr))
	}
	if e.InContributingErr != nil {
		lines = append(lines, fmt.Sprintf("* checking for CLA references in CONTRIBUTING.md: %v", e.InContributingErr))
	}
	if e.InREADMEErr != nil {
		lines = append(lines, fmt.Sprintf("* checking for CLA references in README.md: %v", e.InREADMEErr))
	}
	if e.ActionErr != nil {
		lines = append(lines, fmt.Sprintf("* checking for cla-assistant Action: %v", e.ActionErr))
	}
	return fmt.Sprintf("%d error(s) checking for CLA references:\n\t%s", len(lines), strings.Join(lines, "\n\t"))
}

// ErrOrNil returns nil when every heuristic completed, and an error
// describing the failed heuristics otherwise. The returned error preserves
// the per-check fields and is inspectable with errors.Is / errors.As: both
// delegate to every contained per-check error, so a caller can reach the
// original transport failure, rate-limit error, or sentinel (such as
// ErrTruncatedTree) without parsing the message.
func (e *Errors) ErrOrNil() error {
	if len(e.errors()) == 0 {
		return nil
	}
	return e
}

// Is implements errors.Is for the aggregate: target matches when it matches
// any of the contained per-check errors (or their own wrapped causes).
func (e Errors) Is(target error) bool {
	for _, err := range e.errors() {
		if errors.Is(err, target) {
			return true
		}
	}
	return false
}

// As implements errors.As for the aggregate: target is filled when any of the
// contained per-check errors (or their causes) matches it.
func (e Errors) As(target interface{}) bool {
	for _, err := range e.errors() {
		if errors.As(err, target) {
			return true
		}
	}
	return false
}
