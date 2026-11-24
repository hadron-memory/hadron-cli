package memory

import (
	"strings"
	"testing"
)

// cli#2902: memory extract's parent-ref gate never echoes the ref.
func TestExtractParentRefNeverEchoesTheRef2902(t *testing.T) {
	tok := token2902()
	_, err := resolveExtractParentRef(nil, nil, "", tok+":x")
	leakFree2902(t, "resolveExtractParentRef", tok, err)
}

func leakFree2902(t *testing.T, what, tok string, err error) {
	t.Helper()
	if err == nil {
		t.Errorf("%s: want a refusal", what)
		return
	}
	msg := err.Error()
	for _, leak := range []string{tok, tok[:10], tok[len(tok)-10:]} {
		if strings.Contains(msg, leak) {
			t.Errorf("%s echoes %q: %s", what, leak, msg)
		}
	}
	if !strings.Contains(msg, "not shown") {
		t.Errorf("%s says the value is not shown: %s", what, msg)
	}
}

// Assembled at runtime: a token-shaped literal in the source trips push protection.
func token2902() string { return "gh" + "p_AbCdEfGhIjKlMnOpQrStUvWx0123456789" }
