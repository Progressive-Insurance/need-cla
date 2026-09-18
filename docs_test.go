/*
Copyright (c) 2021-2022 Progressive Casualty Insurance Company. All rights reserved.

Use of this source code is governed by an MIT license that can be found in
the LICENSE file at https://github.com/Progressive-Insurance/need-cla/blob/main/LICENSE.md
*/

package needcla

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// goBlockPattern matches fenced Go code blocks in markdown.
var goBlockPattern = regexp.MustCompile("(?s)```go\r?\n(.*?)```")

// TestReadmeExampleCompiles extracts the documented library example from
// README.md and compiles it in an isolated temporary module that replaces
// this module with the local checkout. The example is built, never run, so
// no live GitHub request is made.
func TestReadmeExampleCompiles(t *testing.T) {
	goBin, err := exec.LookPath("go")
	if err != nil {
		t.Skip("go toolchain not available")
	}
	readme, err := os.ReadFile("README.md")
	if err != nil {
		t.Fatalf("reading README.md: %v", err)
	}
	var programs []string
	for _, m := range goBlockPattern.FindAllStringSubmatch(string(readme), -1) {
		if strings.Contains(m[1], "package main") {
			programs = append(programs, m[1])
		}
	}
	if len(programs) == 0 {
		t.Fatal("README.md does not document a complete Go program (a ```go block containing package main)")
	}
	repoRoot, err := os.Getwd()
	if err != nil {
		t.Fatalf("getting working directory: %v", err)
	}
	sum, err := os.ReadFile(filepath.Join(repoRoot, "go.sum"))
	if err != nil {
		t.Fatalf("reading go.sum: %v", err)
	}
	for i, program := range programs {
		t.Run(fmt.Sprintf("program-%d", i), func(t *testing.T) {
			compileDocumentedExample(t, goBin, repoRoot, sum, program)
		})
	}
}

// compileDocumentedExample writes program into an isolated temporary module
// that depends on this checkout via a local replace, then builds it.
func compileDocumentedExample(t *testing.T, goBin, repoRoot string, sum []byte, program string) {
	t.Helper()
	dir := t.TempDir()
	goMod := "module example.com/need-cla-example\n" +
		"\n" +
		"go 1.18\n" +
		"\n" +
		"require (\n" +
		"\tgithub.com/google/go-github/v43 v43.0.0\n" +
		"\tgithub.com/progressive-insurance/need-cla v0.0.0\n" +
		")\n" +
		"\n" +
		"require (\n" +
		"\tgithub.com/google/go-querystring v1.1.0 // indirect\n" +
		"\tgolang.org/x/crypto v0.0.0-20210817164053-32db794688a5 // indirect\n" +
		"\tgopkg.in/yaml.v3 v3.0.1 // indirect\n" +
		")\n" +
		"\n" +
		"replace github.com/progressive-insurance/need-cla => " + repoRoot + "\n"
	files := map[string][]byte{
		"go.mod":  []byte(goMod),
		"main.go": []byte(program),
		"go.sum":  sum,
	}
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(dir, name), content, 0o644); err != nil {
			t.Fatalf("writing %s: %v", name, err)
		}
	}
	cmd := exec.Command(goBin, "build", "./...")
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("documented example failed to compile: %v\n%s", err, out)
	}
}
