package aiconfig

import "testing"

// cli#2234 item 3: one control per escaped spelling of the submitted key. The
// two keys are chosen so each spelling is distinct from the literal AND from
// the other encoder's spelling, so deleting one form from redactSubmittedKey
// fails exactly its own control:
//   - jsonOnly: encoding/json escapes the angle brackets and the ampersand
//     (HTML-safe unicode escapes); strconv.Quote leaves them alone.
//   - goOnly: strconv.Quote escapes DEL as backslash-x7f; encoding/json leaves
//     it raw.
//
// jsonOnlyEnc2234 is assembled from fragments so no tool or formatter can
// turn the six-character unicode escapes back into the characters they spell.
const (
	jsonOnlyKey2234 = "sk-2234-<tag>&"
	jsonOnlyEnc2234 = `sk-2234-\u` + `003ctag\u` + `003e\u` + `0026`
	goOnlyKey2234   = "sk-2234-\x7f-tail"
	goOnlyEnc2234   = `sk-2234-\x7f-tail`
)

func TestRedactSubmittedKeyIndividualForms2234(t *testing.T) {
	if jsonOnlyEnc2234 == jsonOnlyKey2234 || goOnlyEnc2234 == goOnlyKey2234 {
		t.Fatal("fixture spellings must differ from the literal key")
	}
	for _, tc := range []struct{ name, key, spelling string }{
		{"json-only spelling", jsonOnlyKey2234, jsonOnlyEnc2234},
		{"go-only spelling", goOnlyKey2234, goOnlyEnc2234},
		{"literal", jsonOnlyKey2234, jsonOnlyKey2234},
		{"literal with DEL", goOnlyKey2234, goOnlyKey2234},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := redactSubmittedKey("echo "+tc.spelling+" safe-tail", tc.key)
			if got != "echo [redacted] safe-tail" {
				t.Fatalf("got %q", got)
			}
		})
	}
	t.Run("the other encoder's spelling is not this key's", func(t *testing.T) {
		in := "echo " + goOnlyEnc2234 + " " + jsonOnlyEnc2234
		if got := redactSubmittedKey(in, "unrelated"); got != in {
			t.Fatalf("unrelated key changed the message: %q", got)
		}
	})
	t.Run("a replacement that contains the key is empty", func(t *testing.T) {
		if got := redactSubmittedKey("a[redacted]b", "redacted"); got != "a[]b" {
			t.Fatalf("got %q, want %q", got, "a[]b")
		}
	})
}

// cli#2234 (BLIND P2): the documented tradeoff is behavior, not prose. Masking
// is by exact value, so a short or common key masks it wherever it occurs,
// inside a longer word included. A length or word-boundary exemption would
// let the key through on some surface (cor:dmo:050:10) and must fail here.
func TestRedactSubmittedKeyMasksSubstringsOfLegitimateValues2234(t *testing.T) {
	for _, tc := range []struct{ name, key, in, want string }{
		{"inside a model name", "gpt", "gpt-4o-mini", "[redacted]-4o-mini"},
		{"inside a longer word", "ok", "token ok-ish broken", "t[redacted]en [redacted]-ish br[redacted]en"},
		{"a one-character key", "x", "next box", "ne[redacted]t bo[redacted]"},
		{"every occurrence", "m-19", "m-1959 and m-1960 and m-19", "[redacted]59 and [redacted]60 and [redacted]"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := redactSubmittedKey(tc.in, tc.key); got != tc.want {
				t.Fatalf("got %q, want %q", got, tc.want)
			}
		})
	}
}
