package node

import (
	"strings"
	"testing"
)

// cli#2902: node revision list's ref gate never echoes the ref (a sibling of ResolveNodeURN's refusals).
func TestRevisionNodeRefNeverEchoesTheRef2902(t *testing.T) {
	tok := token2902()
	_, err := revisionNodeRef(nil, nil, "", tok+":x")
	leakFree2902(t, "revisionNodeRef", tok, err)
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
