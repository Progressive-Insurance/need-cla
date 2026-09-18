/*
Copyright (c) 2021-2022 Progressive Casualty Insurance Company. All rights reserved.

Use of this source code is governed by an MIT license that can be found in
the LICENSE file at https://opensource.progressive.com/resources/license
*/

package needcla

// Details contains the results for CLA requirement using various hueristics
type Details struct {
	// Known is true if the owner of a repo is a known CLA requiror
	Known bool
	// Tag is true if a sample of PRs in the repo use a 'cla: yes' and/or 'cla: no' label
	Tag bool
	// BotFile is true if a .clabot config file is present in root
	BotFile bool
	// InContributing is true if the repo's CONTRIBUTING.md exists and refrences the CLA string matchers
	InContributing bool
	// InREADME is true if the repo's README.md exists and references the CLA string matchers
	InREADME bool
	// Action is true if a .github/workflow file has a 'uses: cla-assistant/github-action' line
	Action bool
}

func (d *Details) Required() bool {
	return d.Known || d.Tag || d.BotFile || d.InContributing || d.InREADME || d.Action
}

func (d *Details) merge(details Details) {
	d.Action = d.Action || details.Action
	d.BotFile = d.BotFile || details.BotFile
	d.InContributing = d.InContributing || details.InContributing
	d.InREADME = d.InREADME || details.InREADME
	d.Known = d.Known || details.Known
	d.Tag = d.Tag || details.Tag
}
