# need-cla

[![Run Go tests](https://github.com/Progressive-Insurance/need-cla/actions/workflows/test.yml/badge.svg)](https://github.com/Progressive-Insurance/need-cla/actions/workflows/test.yml)

> Go library and command line utility to check if a GitHub repository might need a CLA signed before contributing

**Please note,** until GitHub provides an official "CLA Required" API endpoint, the best we can make are guesses.

This library uses a few heuristics to determine if a repository requires a CLA:

- if the repo is owned by a list of [known CLA requirers from Wikipedia](https://en.wikipedia.org/wiki/Contributor_License_Agreement#Users)
- if the repo's `CONTRIBUTING.md` or `README.md` reference "CLA" or "Contributor License Agreement"
- if any of the repo's workflows have a `uses: cla-assistant/github-action` line
- if any of the most recent 100 PRs have a Google-style `cla: yes` or `cla: no` tag
- if a `.clabot` file exists in the repo root

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

Then, create a GitHub client and check if a repository needs a CLA:

```go
client := github.NewClient(nil) // this typically comes from github.com/google/go-github/v38/github
needCla, err := needcla.Check(client, "google", "go-github")
if err != nil {
  // handle
}
if needCla {
  fmt.Println("it needs a CLA signed!")
}
```

## `need-cla` Command Line Utility

[See the executable's README.md](./cmd/need-cla/README.md)
