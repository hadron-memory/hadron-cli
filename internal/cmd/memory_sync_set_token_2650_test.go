package cmd

import (
	"encoding/json"
	"strings"
	"testing"
)

// TestSetToken2650ShortTokenInURNKeepsTheTypedRef pins cli#2650 item 1: a very
// short token that is a substring of the server's urn must not splice the
// receipt ("hrn:[REDACTED]e..."); the receipt names the typed ref instead.
func TestSetToken2650ShortTokenInURNKeepsTheTypedRef(t *testing.T) {
	for _, asJSON := range []bool{false, true} {
		args := []string{"acme.com:kb"}
		if asJSON {
			args = append(args, "--json")
		}
		out, errOut, _, err := runSetToken2511(t, "m\n", map[string]string{"SetMemorySourceToken": setTokenOK2511}, args...)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(out+errOut, "[REDACTED]") {
			t.Errorf("json=%v: the URN was spliced: %q", asJSON, out)
		}
		if !strings.Contains(out, "hrn:mem:acme.com:kb") {
			t.Errorf("json=%v: the receipt must name the typed ref: %q", asJSON, out)
		}
	}
}

// TestSetToken2650URNThatContainsTheTokenNeverEchoesIt pins cli#2650 item 4 for
// the URN-redaction guard: replacing it with `named = u` must fail this test.
func TestSetToken2650URNThatContainsTheTokenNeverEchoesIt(t *testing.T) {
	const tok = "ghp_SENTINEL_2650_urn"
	resp := `{"data":{"setMemorySourceToken":{"id":"01a000000000000000000000000000aa","urn":"hrn:mem:acme.com:` + tok + `"}}}`
	for _, asJSON := range []bool{false, true} {
		args := []string{"acme.com:kb"}
		if asJSON {
			args = append(args, "--json")
		}
		out, errOut, _, err := runSetToken2511(t, tok+"\n", map[string]string{"SetMemorySourceToken": resp}, args...)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(out+errOut, tok) {
			t.Errorf("json=%v: the token reached the output: %q %q", asJSON, out, errOut)
		}
	}
}

// TestSetToken2650EscapedEchoIsRedacted pins cli#2650 item 3: a server that
// echoes the token JSON-escaped (a quote or backslash in the token) must not
// leak it through the error message either.
func TestSetToken2650EscapedEchoIsRedacted(t *testing.T) {
	const tok = `s3cr"et\k-2650`
	esc, _ := json.Marshal(tok)
	escaped := strings.Trim(string(esc), `"`)
	msg, _ := json.Marshal("rejected " + escaped + " and " + tok)
	resp := `{"errors":[{"message":` + string(msg) + `,"extensions":{"code":"FORBIDDEN"}}],"data":null}`
	_, errOut, _, err := runSetToken2511(t, tok+"\n", map[string]string{"SetMemorySourceToken": resp}, "acme.com:kb")
	if err == nil {
		t.Fatal("want error")
	}
	all := err.Error() + errOut
	if strings.Contains(all, tok) || strings.Contains(all, escaped) {
		t.Errorf("the token (literal or escaped) leaked: %q", all)
	}
}
