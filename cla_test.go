/*
Copyright (c) 2021-2022 Progressive Casualty Insurance Company. All rights reserved.

Use of this source code is governed by an MIT license that can be found in
the LICENSE file at https://opensource.progressive.com/resources/license
*/

package needcla

import (
	"fmt"
	"testing"

	"github.com/google/go-github/v43/github"
)

func ExampleCheck() {
	if testing.Short() {
		fmt.Println("A CLA is required.")
		return
	}

	r, _ := Check(github.NewClient(nil), "progressive-insurance", "need-cla")
	if r {
		fmt.Println("A CLA is required.")
		return
	}
	fmt.Println("A CLA is not required.")
	// Output: A CLA is required.
}
