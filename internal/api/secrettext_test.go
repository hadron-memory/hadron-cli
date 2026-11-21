package api

import (
	"net/url"
	"strings"
	"testing"
)

func TestRedactSecretTextCoversEverySpellingAndSkipsEmpty(t *testing.T) {
	// Mixed-case on purpose (#2827 review F2): exact, all-lower and all-upper
	// are three distinct strings, so dropping either flip arm from
	// secretSpellings fails here.
	secret := "Xoxb-A/b+c=d Secret"
	// cli#2756 P3-4: an upper- or lower-casing gateway (the case flip of the
	// token itself) is redacted too; a mixed-case flip or partial echo is a
	// named limit, not a covered spelling. The last arm is the lower-hex
	// escape, the covered variant; a full ToLower of the escape is a
	// mixed-case re-encode, one of the named limits. It is spelled as the
	// PRECOMPUTED literal of url.QueryEscape(secret) with its percent-hex
	// lowered — not built by calling lowercasePercentHex, which is the
	// function under test through secretSpellings: a test spelling built with
	// the same function is circular, and a mutant that breaks it (an identity
	// lowercasePercentHex duplicates the plain escape instead) would pass
	// (#2827 review, Jonas N2).
	for _, echo := range []string{secret, strings.ToUpper(secret), strings.ToLower(secret), url.QueryEscape(secret), url.PathEscape(secret), "Xoxb-A%2fb%2bc%3dd+Secret"} {
		in := "bad " + echo + " end"
		if !ContainsSecret(in, []string{secret}) {
			t.Errorf("ContainsSecret missed %q", echo)
		}
		if out := RedactSecretText(in, []string{secret}); strings.Contains(out, echo) || !strings.Contains(out, "[redacted]") {
			t.Errorf("not redacted: %q", out)
		}
	}
	if got := RedactSecretText("plain", []string{"", ""}); got != "plain" {
		t.Errorf("an empty secret must not match everywhere: %q", got)
	}
	if ContainsSecret("plain", []string{""}) {
		t.Error("an empty secret is never held")
	}
}
