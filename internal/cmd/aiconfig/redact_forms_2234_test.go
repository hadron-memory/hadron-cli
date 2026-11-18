package aiconfig

import (
	"strings"
	"testing"
)

// cli#2234 item 3: one control per escaped spelling of the submitted key. The
// two keys are chosen so each spelling is distinct from the literal AND from
// the other encoder's spelling, so deleting one form from redactSubmittedKey
// fails exactly its own control:
//   - jsonOnly: encoding/json escapes the angle brackets and the ampersand
//     (HTML-safe unicode escapes); strconv.Quote leaves them alone.
//   - goOnly: strconv.Quote escapes DEL as backslash-x7f; encoding/json leaves
//     it raw.
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
		if got := redactSubmittedKey("a[redacted]b", "redacted"); strings.Contains(got, "redacted") {
			t.Fatalf("got %q", got)
		}
	})
}
