/*
Copyright (c) 2021-2022 Progressive Casualty Insurance Company. All rights reserved.

Use of this source code is governed by an MIT license that can be found in
the LICENSE file at https://github.com/Progressive-Insurance/need-cla/blob/main/LICENSE.md
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

func (e Errors) Error() string {
	lines := []string{}
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

func (e *Errors) ErrOrNil() error {
	if e.TagErr == nil && e.BotFileErr == nil && e.InContributingErr == nil && e.InREADMEErr == nil && e.ActionErr == nil {
		return nil
	}
	return e
}
