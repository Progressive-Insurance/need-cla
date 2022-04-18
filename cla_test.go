/*
Copyright (c) 2021-2022 Progressive Casualty Insurance Company. All rights reserved.

Use of this source code is governed by an MIT license that can be found in
the LICENSE file at https://github.com/Progressive-Insurance/need-cla/blob/main/LICENSE.md
*/

package needcla

import (
	"context"
	"fmt"
	"testing"

	"github.com/google/go-github/v43/github"
)

func TestDetailWithContext(t *testing.T) {
	d, err := DetailWithContext(context.Background(), github.NewClient(nil), "google", "go-github")
	if err != nil {
		t.Errorf("error: %+v", err)
	}
	if !d.Required() {
		t.Errorf("expected a required CLA")
	}
}

func ExampleCheck() {
	if testing.Short() {
		fmt.Println("A CLA is required.")
		return
	}

	r, err := Check(github.NewClient(nil), "google", "go-github")
	if err != nil {
		fmt.Printf("error: %+v", err)
	}
	if r {
		fmt.Println("A CLA is required.")
		return
	}
	fmt.Println("A CLA is not required.")
	// Output: A CLA is required.
}
