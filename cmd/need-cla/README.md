## `need-cla` Command Line Utility

### Building

Requires:

- Go >= v1.21

```
$ git clone https://github.com/Progressive-Insurance/need-cla
$ cd need-cla/cmd/need-cla
$ go build
```

### Usage

```
Usage of ./need-cla: need-cla [-h] [-token GITHUB_PERSONAL_ACCESS_TOKEN] [-timeout DURATION] owner repo
  -token string
        GitHub personal access token, can also be passed as CLA_TOKEN env var
  -timeout duration
        overall request deadline, can also be passed as CLA_TIMEOUT (default 1m)
```

`owner` and `repo` are both required. Any other argument count (including an
empty argument) fails locally with a usage message and a non-zero exit status
before any network request is made.

#### Exit status

The command uses a documented exit-status contract so automation can tell a
complete scan from an incomplete one:

- `0` — the scan completed and the printed verdict is reliable.
- `1` — a fatal startup or transport failure (bad arguments, invalid
  credentials, network error, or the repository metadata could not be
  loaded). No verdict is printed.
- `3` — the scan ran but at least one heuristic failed. The evidence that was
  gathered is printed, the failed heuristics are marked "could not be
  checked" (never as a negative finding), and the verdict is reported as
  incomplete. Diagnostics go to stderr.

A scan with no positive evidence and a failed heuristic is therefore never
presented as a successful negative scan.

#### Authentication

If, for some reason, you need to authenticate to GitHub to perform the CLA check, you can pass a GitHub Personal Access Token.
Either pass it with the `-token` flag, or use the `CLA_TOKEN` environment variable.
