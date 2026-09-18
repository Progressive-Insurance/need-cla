# need-cla

[![Run Go tests](https://github.com/Progressive-Insurance/need-cla/actions/workflows/test.yml/badge.svg)](https://github.com/Progressive-Insurance/need-cla/actions/workflows/test.yml)

> Go library and command line utility to check if a GitHub repository might need a CLA signed before contributing

**Please note,** until GitHub provides an official "CLA Required" API endpoint, the best we can make are guesses.

This library uses a few heuristics to determine if a repository requires a CLA:

- if the repo is owned by a list of [known CLA requirers from Wikipedia](https://en.wikipedia.org/wiki/Contributor_License_Agreement#Users) (matched case-insensitively)
- if the repo's `CONTRIBUTING.md` or `README.md` reference "CLA" (as a standalone word) or "Contributor License Agreement"
- if any `.yml`/`.yaml` file in `.github/workflows` has a job step whose `uses` references the CLA Assistant action (`cla-assistant/github-action` or the current `contributor-assistant/github-action`), parsed as YAML so comments and shell text are not matched
- if any of the most recent 100 PRs have a Google-style `cla: yes` or `cla: no` label
- if a `.clabot` file (a regular file, not a directory) exists in the repo root

More methods to denote CLA requirements probably exist.
If you know of a good way to check for CLA requirements, please [contribute](./CONTRIBUTING.md)!

## Library

### Installation

```
go get github.com/progressive-insurance/need-cla
```

### Usage

First, import the library:

```go
import needcla "github.com/progressive-insurance/need-cla"
```

Then, create a GitHub client and check if a repository needs a CLA.
The client type is `*github.Client`, where `github` is the same major
version of the SDK this library depends on — `go-github/v43`. The example
below compiles against that version:

```go
package main

import (
	"context"
	"fmt"

	"github.com/google/go-github/v43/github"
	needcla "github.com/progressive-insurance/need-cla"
)

func main() {
	client := github.NewClient(nil) // unauthenticated; use an oauth2 client to raise rate limits
	needCla, err := needcla.CheckWithContext(context.Background(), client, "google", "go-github")
	if err != nil {
		// A nil error means every heuristic completed. A non-nil error is a
		// partial *needcla.Errors value: the boolean only reflects the
		// evidence that was gathered, so a false result is not proof that no
		// CLA requirement exists.
		fmt.Println("scan incomplete:", err)
	}
	if needCla {
		fmt.Println("it needs a CLA signed!")
	}
}
```

## `need-cla` Command Line Utility

[See the executable's README.md](./cmd/need-cla/README.md)
