package output

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

func TestSafeJSONKeepsTheValueAndEscapesUnsafeRunes(t *testing.T) {
	// A bidi override, a zero-width space, a line separator, a tag character
	// above the BMP, and an escaped quote and backslash that must not confuse
	// the string tracking.
	raw := []byte("{\"k\u202e\":\"a\u200bb\u2028c\U000E0041 \\\" \\\\\",\"n\":[1,{\"x\":null}]}")
	got, err := SafeJSON(raw)
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range got {
		if r == '\u202e' || r == '\u200b' || r == '\u2028' || r == '\U000E0041' {
			t.Fatalf("unsafe rune %U printed raw:\n%s", r, got)
		}
	}
	for _, want := range []string{"\\u202e", "\\u200b", "\\u2028", "\\udb40\\udc41", "\n  \"n\": ["} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in:\n%s", want, got)
		}
	}
	var a, b any
	if err := json.Unmarshal(raw, &a); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal([]byte(got), &b); err != nil {
		t.Fatalf("output is not valid JSON: %v\n%s", err, got)
	}
	if !reflect.DeepEqual(a, b) {
		t.Errorf("value changed:\n%v\n%v", a, b)
	}
}

func TestSafeJSONRefusesInvalid(t *testing.T) {
	if _, err := SafeJSON([]byte(`{"a":`)); err == nil {
		t.Error("invalid JSON must be an error")
	}
}
