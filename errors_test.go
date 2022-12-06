/*
Copyright (c) 2021-2022 Progressive Casualty Insurance Company. All rights reserved.

Use of this source code is governed by an MIT license that can be found in
the LICENSE file at https://opensource.progressive.com/resources/license
*/

package needcla

import (
	"fmt"
	"testing"
)

func TestErrorsError(t *testing.T) {
	e := Errors{
		TagErr:            fmt.Errorf("this is the tag error"),
		BotFileErr:        fmt.Errorf("this is the bot file error"),
		InContributingErr: fmt.Errorf("this is the contrib error"),
		InREADMEErr:       fmt.Errorf("this is the readme error"),
		ActionErr:         fmt.Errorf("this is the action error"),
	}
	expected := "5 error(s) checking for CLA references:\n\t" +
		"* checking for CLA tag: this is the tag error\n\t" +
		"* checking for .clabot file: this is the bot file error\n\t" +
		"* checking for CLA references in CONTRIBUTING.md: this is the contrib error\n\t" +
		"* checking for CLA references in README.md: this is the readme error\n\t" +
		"* checking for cla-assistant Action: this is the action error"
	if e.Error() != expected {
		t.Errorf("unexpected error string,\nexpected:\n---\n%s\n---\n\ngot:\n---\n%s\n---", expected, e.Error())
	}
}
