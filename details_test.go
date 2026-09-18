package needcla_test

import (
	"fmt"
	"testing"

	needcla "github.com/progressive-insurance/need-cla"
)

// TestDetailsRequired deterministically exercises all 64 boolean states of
// the six Details fields. Bit i of the mask maps to the i-th field in struct
// order, so dropping any operand from Required() fails that field's one-hot
// state, and the zero and all-true states are covered explicitly.
func TestDetailsRequired(t *testing.T) {
	for mask := 0; mask < 64; mask++ {
		d := needcla.Details{
			Known:          mask&0b000001 != 0,
			Tag:            mask&0b000010 != 0,
			BotFile:        mask&0b000100 != 0,
			InContributing: mask&0b001000 != 0,
			InREADME:       mask&0b010000 != 0,
			Action:         mask&0b100000 != 0,
		}
		want := mask != 0
		t.Run(fmt.Sprintf("mask%02d", mask), func(t *testing.T) {
			if got := d.Required(); got != want {
				t.Errorf("Required() = %v for mask %02b (%+v); want %v", got, mask, d, want)
			}
		})
	}
}
