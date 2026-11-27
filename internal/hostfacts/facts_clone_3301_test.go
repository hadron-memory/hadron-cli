package hostfacts

import (
	"reflect"
	"testing"
)

// hadron-cli#3301 BLIND P2-5: Clone is a field-by-field copy, which isolates pollers only while every field is a
// function value or a string. A field of any other kind (a map, a slice, a pointer, a mutex, a struct holding
// one) could be shared between copies, so it fails here until Clone copies it deeply.
func TestFactsFieldsAreFuncsOrStrings3301(t *testing.T) {
	ty := reflect.TypeFor[Facts]()
	for i := range ty.NumField() {
		f := ty.Field(i)
		if k := f.Type.Kind(); k != reflect.Func && k != reflect.String {
			t.Errorf("Facts.%s is a %s: Clone shares it between pollers; copy it deeply in Clone, then widen this pin", f.Name, k)
		}
	}
}

func TestFactsCloneIsolatesItsFields3301(t *testing.T) {
	f := New(nil)
	c := f.Clone()
	if c == f {
		t.Fatal("Clone returns a new Facts")
	}
	c.Home, c.GOOS = "/elsewhere", "plan9"
	c.Getenv = func(string) string { return "clone" }
	if f.Home == "/elsewhere" || f.GOOS == "plan9" || f.Getenv("HOME") == "clone" {
		t.Fatal("a change to the clone reaches the original")
	}
}
