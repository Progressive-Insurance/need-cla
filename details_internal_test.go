package needcla

import (
	"reflect"
	"testing"
)

func TestDetailsMerge(t *testing.T) {
	type tc struct {
		a    Details
		b    Details
		want Details
	}
	tests := []tc{
		{
			Details{},
			Details{},
			Details{},
		},
		{
			Details{},
			Details{Known: true},
			Details{Known: true},
		},
		{
			Details{InContributing: true},
			Details{},
			Details{InContributing: true},
		},
		{
			Details{InContributing: true},
			Details{InREADME: true},
			Details{InContributing: true, InREADME: true},
		},
	}
	for _, tt := range tests {
		tt.a.merge(tt.b)
		if !reflect.DeepEqual(tt.a, tt.want) {
			t.Errorf("merged incorrectly, got: %+v, wanted: %+v\n", tt.a, tt.want)
		}
	}
}
