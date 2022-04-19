package needcla

import (
	"errors"
	"reflect"
	"testing"
)

func TestErrorsMerge(t *testing.T) {
	err := errors.New("error")
	type tc struct {
		a    Errors
		b    Errors
		want Errors
	}
	tests := []tc{
		{
			Errors{},
			Errors{},
			Errors{},
		},
		{
			Errors{},
			Errors{InContributingErr: err},
			Errors{InContributingErr: err},
		},
		{
			Errors{InContributingErr: err},
			Errors{},
			Errors{InContributingErr: err},
		},
		{
			Errors{InContributingErr: err},
			Errors{InREADMEErr: err},
			Errors{InContributingErr: err, InREADMEErr: err},
		},
	}
	for _, tt := range tests {
		tt.a.merge(tt.b)
		if !reflect.DeepEqual(tt.a, tt.want) {
			t.Errorf("merged incorrectly, got: %+v, wanted: %+v\n", tt.a, tt.want)
		}
	}
}
