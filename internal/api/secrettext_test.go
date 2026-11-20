package api

import (
	"net/url"
	"strings"
	"testing"
)

func TestRedactSecretTextCoversEverySpellingAndSkipsEmpty(t *testing.T) {
	secret := "xoxb-a/b+c=d secret"
	for _, echo := range []string{secret, url.QueryEscape(secret), url.PathEscape(secret), strings.ToLower(url.QueryEscape(secret))} {
		in := "bad " + echo + " end"
		if !HoldsSecret(in, []string{secret}) {
			t.Errorf("HoldsSecret missed %q", echo)
		}
		if out := RedactSecretText(in, []string{secret}); strings.Contains(out, echo) || !strings.Contains(out, "[redacted]") {
			t.Errorf("not redacted: %q", out)
		}
	}
	if got := RedactSecretText("plain", []string{"", ""}); got != "plain" {
		t.Errorf("an empty secret must not match everywhere: %q", got)
	}
	if HoldsSecret("plain", []string{""}) {
		t.Error("an empty secret is never held")
	}
}
