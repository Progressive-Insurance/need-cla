/*
Copyright (c) 2021-2022 Progressive Casualty Insurance Company. All rights reserved.

Use of this source code is governed by an MIT license that can be found in
the LICENSE file at https://github.com/Progressive-Insurance/need-cla/blob/main/LICENSE.md
*/

package needcla

import (
	"fmt"
	"strings"
	"unicode"

	"gopkg.in/yaml.v3"
)

// claActionRefPrefix is the exact, case-sensitive repository component of
// the cla-assistant action reference, including the '@' that must follow it
// immediately.
const claActionRefPrefix = "cla-assistant/github-action@"

// isCLAActionRef reports whether ref is a step action reference to
// cla-assistant/github-action: the exact repository component followed
// immediately by '@' and a nonempty reference containing no whitespace.
// Tags, branch references containing '/', and commit SHAs are accepted
// without network resolution. Surrounding whitespace, repository
// prefixes/suffixes, subdirectory action paths, and missing or empty
// references are rejected.
func isCLAActionRef(ref string) bool {
	if ref != strings.TrimSpace(ref) {
		return false
	}
	if !strings.HasPrefix(ref, claActionRefPrefix) {
		return false
	}
	after := ref[len(claActionRefPrefix):]
	if after == "" {
		return false
	}
	for _, r := range after {
		if unicode.IsSpace(r) {
			return false
		}
	}
	return true
}

// workflowYAMLHasCLAAction reports whether the workflow YAML content
// references cla-assistant/github-action in a step's `uses` field. Only
// scalar string values at jobs.<job_id>.steps[*].uses are inspected; other
// fields are tolerated without validating the full Actions schema. An empty
// document supplies no evidence. Malformed YAML or incompatible types in
// the inspected structure produce an error.
func workflowYAMLHasCLAAction(content []byte) (bool, error) {
	var doc map[string]interface{}
	if err := yaml.Unmarshal(content, &doc); err != nil {
		return false, fmt.Errorf("invalid workflow YAML: %w", err)
	}
	if doc == nil {
		// An empty document supplies no evidence.
		return false, nil
	}

	jobs, ok := doc["jobs"]
	if !ok {
		return false, nil
	}
	jobsMap, ok := jobs.(map[string]interface{})
	if !ok {
		return false, fmt.Errorf("jobs is not a mapping")
	}

	for jobID, job := range jobsMap {
		jobMap, ok := job.(map[string]interface{})
		if !ok {
			return false, fmt.Errorf("job %q is not a mapping", jobID)
		}
		steps, ok := jobMap["steps"]
		if !ok {
			continue
		}
		stepList, ok := steps.([]interface{})
		if !ok {
			return false, fmt.Errorf("steps in job %q is not a sequence", jobID)
		}
		for i, step := range stepList {
			stepMap, ok := step.(map[string]interface{})
			if !ok {
				return false, fmt.Errorf("step %d in job %q is not a mapping", i, jobID)
			}
			uses, ok := stepMap["uses"]
			if !ok {
				continue
			}
			usesStr, ok := uses.(string)
			if !ok {
				return false, fmt.Errorf("uses in step %d of job %q is not a scalar string", i, jobID)
			}
			if isCLAActionRef(usesStr) {
				return true, nil
			}
		}
	}

	return false, nil
}
