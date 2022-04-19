## `need-cla` Command Line Utility

### Building

Requires:

- Go >= v1.18

```
$ git clone github.com/Progressive/need-cla
$ cd need-cla/cmd/need-cla
$ go build
```

### Usage

```
Usage of ./need-cla: need-cla [-h] [-token GITHUB_PERSONAL_ACCESS_TOKEN] owner repo
  -token string
        GitHub personal access token, can also be passed as CLA_TOKEN env var
```

#### Authentication

If, for some reason, you need to authenticate to GitHub to perform the CLA check, you can pass a GitHub Personal Access Token.
Either pass it with the `-token` flag, or use the `CLA_TOKEN` environment variable.
