## `need-cla` Command Line Utility

### Building

Requires:

- Go >= v1.18

```
$ git clone https://github.com/Progressive-Insurance/need-cla.git
$ cd need-cla/cmd/need-cla
$ go build
```

The build produces an executable named `need-cla` on Linux and macOS and
`need-cla.exe` on Windows.

### Usage

```
Usage of ./need-cla: need-cla [-h] [-token GITHUB_PERSONAL_ACCESS_TOKEN] owner repo
  -token string
        GitHub personal access token, can also be passed as CLA_TOKEN env var
```

`need-cla` takes exactly two nonempty positional arguments: the GitHub
repository `owner` and `repo`. Flags must be placed before the positional
arguments:

```
$ need-cla -token GITHUB_PERSONAL_ACCESS_TOKEN owner repo
```

`-h` (or `-help`) prints the usage to stdout and exits with status 0.

#### Authentication

Authentication is optional. If you need to authenticate to GitHub to perform
the CLA check, pass a GitHub Personal Access Token either with the `-token`
flag or with the `CLA_TOKEN` environment variable. When both are provided,
the explicit `-token` flag takes precedence.

#### Exit statuses

- `0`: the heuristic check completed, whether its conclusion is positive or
  negative.
- `1`: the check was fatal or incomplete.
- `2`: a usage error, such as the wrong number of arguments.

The result report is written to stdout and error diagnostics to stderr. When
some checks fail, the report is incomplete: each failed check is displayed as
unknown rather than as a negative finding, positive evidence from the checks
that did complete remains visible, and a partial result with no positive
evidence is inconclusive.
