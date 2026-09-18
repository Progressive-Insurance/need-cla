package needcla

import (
	"fmt"
	"reflect"
	"testing"
)

// detailsFieldNames lists the Details fields in struct order.
var detailsFieldNames = []string{"Known", "Tag", "BotFile", "InContributing", "InREADME", "Action"}

// setDetailsField sets the Details field at index i to true.
func setDetailsField(d *Details, i int) {
	switch i {
	case 0:
		d.Known = true
	case 1:
		d.Tag = true
	case 2:
		d.BotFile = true
	case 3:
		d.InContributing = true
	case 4:
		d.InREADME = true
	case 5:
		d.Action = true
	}
}

// TestDetailsMerge exhaustively merges every pair of single-field Details
// values. For each field this covers false+true, true+false, true+true, and
// disjoint populated inputs, and confirms accumulation never clears prior
// evidence.
func TestDetailsMerge(t *testing.T) {
	t.Run("empty+empty", func(t *testing.T) {
		var a, want Details
		a.merge(want)
		if !reflect.DeepEqual(a, want) {
			t.Errorf("merged incorrectly, got: %+v, wanted: %+v\n", a, want)
		}
	})

	for i := range detailsFieldNames {
		for j := range detailsFieldNames {
			a := Details{}
			setDetailsField(&a, i)
			b := Details{}
			setDetailsField(&b, j)
			want := Details{}
			setDetailsField(&want, i)
			setDetailsField(&want, j)
			t.Run(fmt.Sprintf("%s+%s", detailsFieldNames[i], detailsFieldNames[j]), func(t *testing.T) {
				a.merge(b)
				if !reflect.DeepEqual(a, want) {
					t.Errorf("merged incorrectly, got: %+v, wanted: %+v\n", a, want)
				}
			})
		}
	}
}
