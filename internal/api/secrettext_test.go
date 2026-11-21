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
	// mixed-case re-encode, one of the named limits.
	for _, echo := range []string{secret, strings.ToUpper(secret), strings.ToLower(secret), url.QueryEscape(secret), url.PathEscape(secret), lowercasePercentHex(url.QueryEscape(secret))} {
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
