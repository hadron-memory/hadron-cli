package cmd

import (
	"encoding/json"
	"strings"
	"testing"
)

// cli#2758: a person holder's refused `secret reveal` prints the server's own
// SecretReadClient.revealUrl (cor:sec:060:02) byte for byte, found through the same
// read as `secret client list`. The CLI never composes the URL from the client id,
// never decides who a person holder is (it is the server's answer for the viewer),
// never says which cause applied, and a null or absent field leaves today's output
// byte-identical. Fake HTTP only.

const (
	url2758     = "https://hadron.example/secret-reveal/cl-1?x=1&y=%7E"
	rowHead2758 = `{"clientId":"cl-1","clientPrefix":"cl-1","grantId":"g1","secretId":"s1","secretName":"deploy","selfIssued":true,"endReason":null,"createdAt":"2026-11-20T00:00:00Z"`

	notFound2758 = "SECRET_READ_CLIENT_NOT_FOUND"
)

func clientRow2758(extra string) string { return rowHead2758 + extra + `}` }
func list2758(rows ...string) string {
	return `{"data":{"mySecretReadClients":[` + strings.Join(rows, ",") + `]}}`
}
func withURL2758(u string) string { return `,"revealUrl":` + mustJSON2631(u) }

func listCalls2758(calls *[]attnCall) int {
	n := 0
	for _, c := range *calls {
		if c.Op == "MySecretReadClients" {
			n++
		}
	}
	return n
}

// client list carries the field in --json: a string when the server sent one, null
// when it did not (older server, or not a person holder).
func TestSecretClientListCarriesRevealUrl2758(t *testing.T) {
	srv, _ := attnServer(t, map[string]string{"MySecretReadClients": list2758(clientRow2758(withURL2758(url2758)), clientRow2758(""))})
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"--server", srv.URL, "secret", "client", "list", "--json"})
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	var rows []map[string]any
	if err := json.Unmarshal([]byte(out.String()), &rows); err != nil || len(rows) != 2 {
		t.Fatalf("rows: %v %q", err, out.String())
	}
	if rows[0]["revealUrl"] != url2758 {
		t.Errorf("revealUrl = %v, want the server's string byte for byte", rows[0]["revealUrl"])
	}
	if v, has := rows[1]["revealUrl"]; !has || v != nil {
		t.Errorf("an absent field must be present and null, got %v present=%v", v, has)
	}
}

// The acceptance pin: before this change a refused person reveal prints no URL.
func TestSecretRevealRefusedPersonPrintsTheServersLink2758(t *testing.T) {
	srv, calls := attnServer(t, map[string]string{
		"RevealSecret":        gqlErrorJSON(notFound2758),
		"MySecretReadClients": list2758(clientRow2758(withURL2758(url2758))),
	})
	r := runSecretReveal2664(t, srv.URL, true, "reveal", "cl-1")
	if exitCodeFor(r.err) != 4 || revealCalls2664(calls) != 1 {
		t.Fatalf("exit=%d reveals=%d err=%v", exitCodeFor(r.err), revealCalls2664(calls), r.err)
	}
	if !strings.Contains(r.err.Error(), "revealed by the worker it names") || !strings.Contains(r.err.Error(), "passkey page") {
		t.Errorf("the uniform sentence stays: %v", r.err)
	}
	if !strings.Contains(r.err.Error(), url2758) {
		t.Errorf("the server's link must be printed byte for byte: %v", r.err)
	}
	// The lookup is a read and never a reveal: the reveal is still sent exactly once.
	if listCalls2758(calls) < 1 {
		t.Errorf("the link comes from the client-list read")
	}
	for _, c := range *calls {
		if c.Op == "MySecretReadClients" && c.Session != "" {
			t.Errorf("the lookup is the viewer's own (no worker session header): %q", c.Session)
		}
	}
	for _, hint := range []string{"you are a person", "you are not", "does not exist", "unknown client"} {
		if strings.Contains(strings.ToLower(r.err.Error()), hint) {
			t.Errorf("the answer must not say which cause applied (%q): %v", hint, r.err)
		}
	}
}

// --json: the envelope carries the URL exactly in its own key, so a script can hand
// it to the person; the message still has the one uniform sentence.
func TestSecretRevealRefusedPersonLinkInTheJSONEnvelope2758(t *testing.T) {
	srv, _ := attnServer(t, map[string]string{
		"RevealSecret":        gqlErrorJSON(notFound2758),
		"MySecretReadClients": list2758(clientRow2758(withURL2758(url2758))),
	})
	f, out := testFactory(t)
	var errOut strings.Builder
	f.IOStreams.ErrOut = &errOut
	root := NewRootCmd(f)
	root.SetArgs([]string{"--server", srv.URL, "secret", "reveal", "cl-1", "--allow-non-terminal", "--json"})
	err := root.Execute()
	if err == nil || exitCodeFor(err) != 4 {
		t.Fatalf("exit=%d err=%v", exitCodeFor(err), err)
	}
	renderError(f, err)
	var env struct {
		Error struct {
			Code      int    `json:"code"`
			Message   string `json:"message"`
			RevealURL string `json:"revealUrl"`
		} `json:"error"`
	}
	if jerr := json.Unmarshal([]byte(out.String()), &env); jerr != nil {
		t.Fatalf("envelope: %v %q", jerr, out.String())
	}
	if env.Error.RevealURL != url2758 {
		t.Errorf("error.revealUrl = %q, want the server's URL exactly", env.Error.RevealURL)
	}
	if !strings.Contains(env.Error.Message, "passkey page") {
		t.Errorf("the message keeps the uniform sentence: %q", env.Error.Message)
	}
	if strings.Contains(env.Error.Message, url2758) {
		t.Errorf("the exact URL lives in error.revealUrl only, not duplicated into the message: %q", env.Error.Message)
	}
}

// A null or absent field, a row for another client, a row with no full client id and a
// failed lookup all leave the refusal exactly as it was: the same text, the same code.
func TestSecretRevealRefusalIsUnchangedWithoutALink2758(t *testing.T) {
	baseline, _ := attnServer(t, map[string]string{"RevealSecret": gqlErrorJSON(notFound2758), "MySecretReadClients": list2758()})
	want := runSecretReveal2664(t, baseline.URL, true, "reveal", "cl-1").err
	if want == nil {
		t.Fatal("baseline must refuse")
	}
	other := strings.Replace(clientRow2758(withURL2758(url2758)), `"clientId":"cl-1"`, `"clientId":"cl-2"`, 1)
	prefixOnly := strings.Replace(clientRow2758(withURL2758(url2758)), `"clientId":"cl-1"`, `"clientId":null`, 1)
	for name, resp := range map[string]string{
		"field absent":      list2758(clientRow2758("")),
		"field null":        list2758(clientRow2758(`,"revealUrl":null`)),
		"another client":    list2758(other),
		"no full client id": list2758(prefixOnly),
		"lookup refused":    gqlErrorJSON("FORBIDDEN"),
		"empty url":         list2758(clientRow2758(`,"revealUrl":""`)),
	} {
		t.Run(name, func(t *testing.T) {
			srv, _ := attnServer(t, map[string]string{"RevealSecret": gqlErrorJSON(notFound2758), "MySecretReadClients": resp})
			got := runSecretReveal2664(t, srv.URL, true, "reveal", "cl-1").err
			if got == nil || got.Error() != want.Error() || exitCodeFor(got) != exitCodeFor(want) {
				t.Errorf("a refusal without a link must be byte-identical to today's: got %v want %v", got, want)
			}
		})
	}
}

// Text mode refuses a URL carrying control or format characters (as the ceremony URL
// does) and says where to read it exactly; --json still carries the exact bytes.
func TestSecretRevealLinkWithControlCharactersIsNotPrintedAsText2758(t *testing.T) {
	bad := "https://hadron.example/secret-reveal/cl-1\u001b[31m?x=\u202e"
	srv, _ := attnServer(t, map[string]string{
		"RevealSecret":        gqlErrorJSON(notFound2758),
		"MySecretReadClients": list2758(clientRow2758(withURL2758(bad))),
	})
	r := runSecretReveal2664(t, srv.URL, true, "reveal", "cl-1")
	if r.err == nil || exitCodeFor(r.err) != 4 {
		t.Fatalf("exit=%d err=%v", exitCodeFor(r.err), r.err)
	}
	if strings.ContainsAny(r.err.Error(), "\u001b\u202e") || strings.Contains(r.err.Error(), "secret-reveal/cl-1") {
		t.Errorf("an unsafe URL must not be printed as text: %q", r.err.Error())
	}
	if !strings.Contains(r.err.Error(), "--json") {
		t.Errorf("say where to read it exactly: %q", r.err.Error())
	}
	f, out := testFactory(t)
	var errOut strings.Builder
	f.IOStreams.ErrOut = &errOut
	root := NewRootCmd(f)
	root.SetArgs([]string{"--server", srv.URL, "secret", "reveal", "cl-1", "--allow-non-terminal", "--json"})
	err := root.Execute()
	renderError(f, err)
	var env struct {
		Error struct {
			RevealURL string `json:"revealUrl"`
		} `json:"error"`
	}
	if jerr := json.Unmarshal([]byte(out.String()), &env); jerr != nil || env.Error.RevealURL != bad {
		t.Errorf("--json keeps the exact bytes: %v %q", jerr, out.String())
	}
}

// The link is looked up only for the uniform NOT_FOUND refusal: any other refusal, and
// a lost answer, make no extra read; and `secret exec` (not in this issue) is unchanged.
func TestSecretRevealLinkLookupOnlyForNotFound2758(t *testing.T) {
	for _, code := range []string{"SECRET_READ_CLIENT_SPENT", "FORBIDDEN", "SECRET_DELETED"} {
		srv, calls := attnServer(t, map[string]string{"RevealSecret": gqlErrorJSON(code), "MySecretReadClients": list2758(clientRow2758(withURL2758(url2758)))})
		r := runSecretReveal2664(t, srv.URL, true, "reveal", "cl-1")
		if r.err == nil || listCalls2758(calls) != 0 || strings.Contains(r.err.Error(), url2758) {
			t.Errorf("%s: no lookup and no link, got reads=%d err=%v", code, listCalls2758(calls), r.err)
		}
	}
	skipWindows2664(t)
	srv, calls := attnServer(t, map[string]string{"RevealSecret": gqlErrorJSON(notFound2758), "MySecretReadClients": list2758(clientRow2758(withURL2758(url2758)))})
	r := runSecretReveal2664(t, srv.URL, false, "exec", "--client", "cl-1", "--env", "TOKEN", "--", "true")
	if r.err == nil || listCalls2758(calls) != 0 || strings.Contains(r.err.Error(), url2758) {
		t.Errorf("exec is unchanged: reads=%d err=%v", listCalls2758(calls), r.err)
	}
}

// One short page is the whole list: the lookup stops there instead of paging on.
func TestSecretRevealLinkLookupStopsOnAShortPage2758(t *testing.T) {
	otherRow := strings.Replace(clientRow2758(withURL2758(url2758)), `"clientId":"cl-1"`, `"clientId":"cl-2"`, 1)
	srv, calls := attnServer(t, map[string]string{"RevealSecret": gqlErrorJSON(notFound2758), "MySecretReadClients": list2758(otherRow)})
	_ = runSecretReveal2664(t, srv.URL, true, "reveal", "cl-1")
	if n := listCalls2758(calls); n != 1 {
		t.Errorf("a short page ends the lookup, got %d list reads", n)
	}
}
