/*
Copyright (c) 2021-2022 Progressive Casualty Insurance Company. All rights reserved.

Use of this source code is governed by an MIT license that can be found in
the LICENSE file at https://opensource.progressive.com/resources/license
*/

package needcla

import (
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/google/go-github/v43/github"
)

// exampleTransport serves a tiny deterministic repository (a README that
// references the CLA) entirely in-process, so the example runs offline in
// both short and normal mode and actually exercises the library.
type exampleTransport struct{}

func (exampleTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	status := http.StatusOK
	body := `{"message":"not found"}`
	switch req.URL.Path {
	case "/rate_limit":
		body = `{"resources":{"core":{"limit":5000,"remaining":1000}}}`
	case "/repos/example/project":
		body = `{"default_branch":"main"}`
	case "/repos/example/project/pulls":
		body = `[]`
	case "/repos/example/project/git/trees/main":
		body = `{"tree":[{"path":"README.md","type":"blob","sha":"readme"}]}`
	case "/repos/example/project/git/blobs/readme":
		// base64("Contributors must sign a CLA first.") — references the CLA.
		body = `{"encoding":"base64","content":"Q29udHJpYnV0b3JzIG11c3Qgc2lnbiBhIENMQSBmaXJzdC4="}`
	default:
		status = http.StatusNotFound
	}
	return &http.Response{
		StatusCode: status,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(strings.NewReader(body)),
		Request:    req,
	}, nil
}

func ExampleCheck() {
	client := github.NewClient(&http.Client{Transport: exampleTransport{}})
	r, err := Check(client, "example", "project")
	if err != nil {
		fmt.Println("error:", err)
		return
	}
	if r {
		fmt.Println("A CLA is required.")
		return
	}
	fmt.Println("A CLA is not required.")
	// Output: A CLA is required.
}
