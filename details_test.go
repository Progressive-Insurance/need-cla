package needcla_test

import (
	"testing"

	needcla "github.com/progressive-insurance/need-cla"
)

func TestDetailsRequired(t *testing.T) {
	t.Run("NotRequired", func(t *testing.T) {
		d := needcla.Details{}
		if d.Required() {
			t.Errorf("got required from zero value")
		}
	})

	// Deterministic single-flag coverage: each heuristic flag independently
	// establishes that a CLA is required. This replaces the previous
	// randomized test so every flag is proven on its own (including
	// all-true) rather than by chance.
	flags := []struct {
		name string
		set  func(d *needcla.Details)
	}{
		{"Known", func(d *needcla.Details) { d.Known = true }},
		{"Tag", func(d *needcla.Details) { d.Tag = true }},
		{"BotFile", func(d *needcla.Details) { d.BotFile = true }},
		{"InContributing", func(d *needcla.Details) { d.InContributing = true }},
		{"InREADME", func(d *needcla.Details) { d.InREADME = true }},
		{"Action", func(d *needcla.Details) { d.Action = true }},
	}
	for _, f := range flags {
		t.Run("single/"+f.name, func(t *testing.T) {
			d := needcla.Details{}
			f.set(&d)
			if !d.Required() {
				t.Errorf("got not required from %+v", d)
			}
		})
	}

	t.Run("all", func(t *testing.T) {
		d := needcla.Details{
			Known: true, Tag: true, BotFile: true,
			InContributing: true, InREADME: true, Action: true,
		}
		if !d.Required() {
			t.Errorf("got not required from all-true %+v", d)
		}
	})
}
