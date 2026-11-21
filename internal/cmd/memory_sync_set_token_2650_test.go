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
	// cli#2714 round 4: a message carrying the token in any covered spelling
	// is not partially redacted at all — the fixed refusal replaces it.
	if !strings.Contains(all, "the server refused the source token") {
		t.Fatalf("the server message must render the fixed refusal: %q", all)
	}
	if strings.Contains(all, tok) || strings.Contains(all, escaped) {
		t.Errorf("the token (literal or escaped) leaked: %q", all)
	}
}

// TestSetToken2650URNWithEscapedTokenNeverEchoesIt pins the blind P2 on
// cli#2683: the URN guard must catch the JSON-escaped spelling of the token as
// well as the raw one, or the receipt prints a token that unescapes back to the
// secret.
func TestSetToken2650URNWithEscapedTokenNeverEchoesIt(t *testing.T) {
	const tok = `s3cr"et\k-2650`
	esc, _ := json.Marshal(tok)
	escaped := strings.Trim(string(esc), `"`)
	urnJSON, _ := json.Marshal("hrn:mem:acme.com:" + escaped)
	resp := `{"data":{"setMemorySourceToken":{"id":"01a000000000000000000000000000aa","urn":` + string(urnJSON) + `}}}`
	for _, asJSON := range []bool{false, true} {
		args := []string{"acme.com:kb"}
		if asJSON {
			args = append(args, "--json")
		}
		out, errOut, _, err := runSetToken2511(t, tok+"\n", map[string]string{"SetMemorySourceToken": resp}, args...)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(out+errOut, escaped) || strings.Contains(out+errOut, tok) {
			t.Errorf("json=%v: the escaped token reached the receipt: %q", asJSON, out)
		}
		if !strings.Contains(out, "hrn:mem:acme.com:kb") {
			t.Errorf("json=%v: the receipt must name the typed ref: %q", asJSON, out)
		}
	}
}
