package needcla

import (
	"errors"
	"fmt"
	"reflect"
	"testing"
)

// errorFieldNames lists the Errors fields in struct order.
var errorFieldNames = []string{"TagErr", "BotFileErr", "InContributingErr", "InREADMEErr", "ActionErr"}

// errorSentinels holds one distinct sentinel per Errors field.
var errorSentinels = []error{
	errors.New("tag sentinel"),
	errors.New("bot file sentinel"),
	errors.New("in contributing sentinel"),
	errors.New("in readme sentinel"),
	errors.New("action sentinel"),
}

// setErrorField sets the Errors field at index i to err.
func setErrorField(e *Errors, i int, err error) {
	switch i {
	case 0:
		e.TagErr = err
	case 1:
		e.BotFileErr = err
	case 2:
		e.InContributingErr = err
	case 3:
		e.InREADMEErr = err
	case 4:
		e.ActionErr = err
	}
}

// getErrorField returns the Errors field at index i.
func getErrorField(e *Errors, i int) error {
	switch i {
	case 0:
		return e.TagErr
	case 1:
		return e.BotFileErr
	case 2:
		return e.InContributingErr
	case 3:
		return e.InREADMEErr
	case 4:
		return e.ActionErr
	}
	return nil
}

// TestErrorsMerge exercises all error fields with distinct sentinels. For
// each field it verifies empty input preserves existing errors, nonnil input
// fills missing errors, and a new error replaces an existing one in the same
// field without altering other fields.
func TestErrorsMerge(t *testing.T) {
	t.Run("empty+empty", func(t *testing.T) {
		var a, want Errors
		a.merge(want)
		if !reflect.DeepEqual(a, want) {
			t.Errorf("merged incorrectly, got: %+v, wanted: %+v\n", a, want)
		}
	})

	for i := range errorFieldNames {
		// Empty input preserves existing errors.
		t.Run(fmt.Sprintf("%s+empty", errorFieldNames[i]), func(t *testing.T) {
			a := Errors{}
			setErrorField(&a, i, errorSentinels[i])
			want := Errors{}
			setErrorField(&want, i, errorSentinels[i])
			a.merge(Errors{})
			if !reflect.DeepEqual(a, want) {
				t.Errorf("merged incorrectly, got: %+v, wanted: %+v\n", a, want)
			}
		})

		// Nonnil input fills missing errors (disjoint fields).
		for j := range errorFieldNames {
			if i == j {
				continue
			}
			t.Run(fmt.Sprintf("%s+%s", errorFieldNames[i], errorFieldNames[j]), func(t *testing.T) {
				a := Errors{}
				setErrorField(&a, i, errorSentinels[i])
				b := Errors{}
				setErrorField(&b, j, errorSentinels[j])
				want := Errors{}
				setErrorField(&want, i, errorSentinels[i])
				setErrorField(&want, j, errorSentinels[j])
				a.merge(b)
				if !reflect.DeepEqual(a, want) {
					t.Errorf("merged incorrectly, got: %+v, wanted: %+v\n", a, want)
				}
			})
		}

		// A new error replaces an existing error in the same field without
		// altering other fields.
		t.Run(fmt.Sprintf("%s-replace", errorFieldNames[i]), func(t *testing.T) {
			other := (i + 1) % len(errorFieldNames)
			replacement := errors.New("replacement for " + errorFieldNames[i])
			a := Errors{}
			setErrorField(&a, i, errorSentinels[i])
			setErrorField(&a, other, errorSentinels[other])
			b := Errors{}
			setErrorField(&b, i, replacement)
			want := Errors{}
			setErrorField(&want, i, replacement)
			setErrorField(&want, other, errorSentinels[other])
			a.merge(b)
			if !reflect.DeepEqual(a, want) {
				t.Errorf("merged incorrectly, got: %+v, wanted: %+v\n", a, want)
			}
		})
	}
}

// TestErrorsErrOrNil verifies ErrOrNil returns a literal nil only for an
// aggregate with no errors, and otherwise returns the aggregate itself,
// preserving identity and the original field values.
func TestErrorsErrOrNil(t *testing.T) {
	t.Run("empty", func(t *testing.T) {
		e := &Errors{}
		if err := e.ErrOrNil(); err != nil {
			t.Errorf("ErrOrNil on an empty aggregate = %v; want nil", err)
		}
	})

	// Each field populated independently.
	for i := range errorFieldNames {
		t.Run(errorFieldNames[i], func(t *testing.T) {
			e := &Errors{}
			setErrorField(e, i, errorSentinels[i])
			err := e.ErrOrNil()
			if err == nil {
				t.Fatalf("ErrOrNil with %s populated = nil; want non-nil", errorFieldNames[i])
			}
			got, ok := err.(*Errors)
			if !ok {
				t.Fatalf("ErrOrNil returned %T; want *Errors", err)
			}
			if got != e {
				t.Errorf("ErrOrNil = %p; want aggregate identity %p", got, e)
			}
			if gotField := getErrorField(got, i); gotField != errorSentinels[i] {
				t.Errorf("field %s = %v; want original sentinel %v", errorFieldNames[i], gotField, errorSentinels[i])
			}
		})
	}

	// All fields populated.
	t.Run("all", func(t *testing.T) {
		e := &Errors{}
		for i := range errorFieldNames {
			setErrorField(e, i, errorSentinels[i])
		}
		err := e.ErrOrNil()
		if err == nil {
			t.Fatalf("ErrOrNil with all fields populated = nil; want non-nil")
		}
		got, ok := err.(*Errors)
		if !ok {
			t.Fatalf("ErrOrNil returned %T; want *Errors", err)
		}
		if got != e {
			t.Errorf("ErrOrNil = %p; want aggregate identity %p", got, e)
		}
		for i := range errorFieldNames {
			if gotField := getErrorField(got, i); gotField != errorSentinels[i] {
				t.Errorf("field %s = %v; want original sentinel %v", errorFieldNames[i], gotField, errorSentinels[i])
			}
		}
	})
}
