package cmd

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

// cli#2758: a person holder's refused `secret reveal` prints the server's own
// SecretReadClient.revealUrl (cor:sec:060:02) byte for byte, found through the same
// read as `secret client list`. The CLI never composes the URL from the client id,
// never decides who a person holder is (it is the server's answer for the viewer),
// never says which cause applied, and a null or absent field leaves today's output
// byte-identical. Fake HTTP only.

const listOp2758 = "ListSecretReadClients" // the list and the link lookup; revoke keeps MySecretReadClients

const (
	url2758     = "https://hadron.example/secret-reveal/cl-1?x=1&y=%7E"
	rowHead2758 = `{"clientId":"cl-1","clientPrefix":"cl-1","grantId":"g1","secretId":"s1","secretName":"deploy","selfIssued":true,"endReason":null,"createdAt":"2026-11-20T00:00:00Z","destroyOnEnd":false`

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
		if c.Op == listOp2758 {
			n++
		}
	}
	return n
}

// client list carries the field in --json: a string when the server sent one, null
// when it did not (older server, or not a person holder).
func TestSecretClientListCarriesRevealUrl2758(t *testing.T) {
	srv, _ := attnServer(t, map[string]string{listOp2758: list2758(clientRow2758(withURL2758(url2758)), clientRow2758(""))})
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
		"RevealSecret": gqlErrorJSON(notFound2758),
		listOp2758:     list2758(clientRow2758(withURL2758(url2758))),
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
		if c.Op == listOp2758 && c.Session != "" {
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
		"RevealSecret": gqlErrorJSON(notFound2758),
		listOp2758:     list2758(clientRow2758(withURL2758(url2758))),
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
	// A literal, not a run of the same binary: dropping the "no link, no extra line" guard would
	// change the baseline and the cases alike and still compare equal.
	const wantText = "refused: SECRET_READ_CLIENT_NOT_FOUND; a read client is revealed by the worker it names, in that worker's session; a person reveals on the server's passkey page"
	baseline, _ := attnServer(t, map[string]string{"RevealSecret": gqlErrorJSON(notFound2758), listOp2758: list2758()})
	want := runSecretReveal2664(t, baseline.URL, true, "reveal", "cl-1").err
	if want == nil || want.Error() != wantText {
		t.Fatalf("the refusal today is exactly %q, got %v", wantText, want)
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
		"an id that merely starts with the requested one": list2758(strings.Replace(clientRow2758(withURL2758(url2758)), `"clientId":"cl-1"`, `"clientId":"cl-12"`, 1)),
	} {
		t.Run(name, func(t *testing.T) {
			srv, _ := attnServer(t, map[string]string{"RevealSecret": gqlErrorJSON(notFound2758), listOp2758: resp})
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
		"RevealSecret": gqlErrorJSON(notFound2758),
		listOp2758:     list2758(clientRow2758(withURL2758(bad))),
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
		srv, calls := attnServer(t, map[string]string{"RevealSecret": gqlErrorJSON(code), listOp2758: list2758(clientRow2758(withURL2758(url2758)))})
		r := runSecretReveal2664(t, srv.URL, true, "reveal", "cl-1")
		if r.err == nil || listCalls2758(calls) != 0 || strings.Contains(r.err.Error(), url2758) {
			t.Errorf("%s: no lookup and no link, got reads=%d err=%v", code, listCalls2758(calls), r.err)
		}
	}
	skipWindows2664(t)
	srv, calls := attnServer(t, map[string]string{"RevealSecret": gqlErrorJSON(notFound2758), listOp2758: list2758(clientRow2758(withURL2758(url2758)))})
	r := runSecretReveal2664(t, srv.URL, false, "exec", "--client", "cl-1", "--env", "TOKEN", "--", "true")
	if r.err == nil || listCalls2758(calls) != 0 || strings.Contains(r.err.Error(), url2758) {
		t.Errorf("exec is unchanged: reads=%d err=%v", listCalls2758(calls), r.err)
	}
}

// One short page is the whole list: the lookup stops there instead of paging on.
func TestSecretRevealLinkLookupStopsOnAShortPage2758(t *testing.T) {
	otherRow := strings.Replace(clientRow2758(withURL2758(url2758)), `"clientId":"cl-1"`, `"clientId":"cl-2"`, 1)
	srv, calls := attnServer(t, map[string]string{"RevealSecret": gqlErrorJSON(notFound2758), listOp2758: list2758(otherRow)})
	_ = runSecretReveal2664(t, srv.URL, true, "reveal", "cl-1")
	if n := listCalls2758(calls); n != 1 {
		t.Errorf("a short page ends the lookup, got %d list reads", n)
	}
}

// The lookup is the viewer's own and carries NO worker session, in a bound worktree too, while the
// reveal itself carries the worker's (LOCAL P2 on #2882): a lookup sent as the worker would make
// the server answer a person's list with nothing and the link would silently never print.
func TestSecretRevealLinkLookupCarriesNoWorkerSession2758(t *testing.T) {
	writeTeamBinding(t)
	srv, calls := attnServer(t, map[string]string{
		"RevealSecret":    gqlErrorJSON(notFound2758),
		listOp2758:        list2758(clientRow2758(withURL2758(url2758))),
		"TeamAppIdentity": teamAppIdentityJSON,
	})
	setTeamBindingServer(t, srv.URL)
	r := runSecretReveal2664(t, srv.URL, true, "reveal", "cl-1")
	if r.err == nil || !strings.Contains(r.err.Error(), url2758) {
		t.Fatalf("the link prints in a bound worktree too: %v", r.err)
	}
	var revealSession, lookups int
	for _, c := range *calls {
		switch c.Op {
		case "RevealSecret":
			if c.Session != "" {
				revealSession++
			}
		case listOp2758:
			lookups++
			if c.Session != "" {
				t.Errorf("the lookup must carry no worker session: %q", c.Session)
			}
		}
	}
	if revealSession != 1 || lookups != 1 {
		t.Errorf("the reveal carries the bound session once (%d) and the lookup is one read (%d)", revealSession, lookups)
	}
}

// A client on the second page is found: the offset advances by the rows seen.
func TestSecretRevealLinkLookupPagesToTheClient2758(t *testing.T) {
	var firstPage []string
	for i := 0; i < 200; i++ {
		row := strings.Replace(clientRow2758(""), `"clientId":"cl-1"`, fmt.Sprintf(`"clientId":"x-%03d"`, i), 1)
		firstPage = append(firstPage, row)
	}
	responses := map[string]string{"RevealSecret": gqlErrorJSON(notFound2758), listOp2758: list2758(firstPage...)}
	reads := 0
	srv, calls := attnServerHook(t, responses, func(op string) {
		// The hook runs before the answer is read: the first read keeps the full page, the second gets page two.
		if op == listOp2758 {
			reads++
			if reads == 2 {
				setAttnResponse(responses, listOp2758, list2758(clientRow2758(withURL2758(url2758))))
			}
		}
	})
	r := runSecretReveal2664(t, srv.URL, true, "reveal", "cl-1")
	if r.err == nil || !strings.Contains(r.err.Error(), url2758) {
		t.Fatalf("a client on page two is found: %v", r.err)
	}
	var offsets []any
	for _, c := range *calls {
		if c.Op == listOp2758 {
			offsets = append(offsets, c.Vars["offset"])
		}
	}
	if len(offsets) != 2 || offsets[0] != float64(0) || offsets[1] != float64(200) {
		t.Errorf("the second read starts after the 200 rows already seen: %v", offsets)
	}
}

// Only the server's own uniform code triggers the lookup: another not-found code does not.
func TestSecretRevealLinkOnlyForTheUniformCode2758(t *testing.T) {
	srv, calls := attnServer(t, map[string]string{"RevealSecret": gqlErrorJSON("NOT_FOUND"), listOp2758: list2758(clientRow2758(withURL2758(url2758)))})
	r := runSecretReveal2664(t, srv.URL, true, "reveal", "cl-1")
	if r.err == nil || listCalls2758(calls) != 0 || strings.Contains(r.err.Error(), url2758) {
		t.Errorf("a different not-found makes no lookup: reads=%d err=%v", listCalls2758(calls), r.err)
	}
}

// A server without revealUrl rejects the ListSecretReadClients document at validation naming only
// that field (Jorge's P2 on #2882): the list and the lookup fall back to the document without it.
const noRevealUrlField2758 = `{"errors":[{"message":"Cannot query field \"revealUrl\" on type \"SecretReadClient\".","extensions":{"code":"GRAPHQL_VALIDATION_FAILED"}}]}`

func TestSecretClientListOnAServerWithoutRevealUrl2758(t *testing.T) {
	srv, calls := attnServer(t, map[string]string{
		listOp2758:            noRevealUrlField2758,
		"MySecretReadClients": list2758(clientRow2758("")),
	})
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"--server", srv.URL, "secret", "client", "list", "--json"})
	if err := root.Execute(); err != nil {
		t.Fatalf("list must still work on such a server: %v", err)
	}
	var rows []map[string]any
	if err := json.Unmarshal([]byte(out.String()), &rows); err != nil || len(rows) != 1 || rows[0]["revealUrl"] != nil {
		t.Fatalf("one row with a null revealUrl: %v %q", err, out.String())
	}
	if ops := opsOf2631(calls); len(ops) != 2 || ops[0] != listOp2758 || ops[1] != "MySecretReadClients" {
		t.Errorf("the rejected document is tried once, then the one without the field: %v", ops)
	}
}

// Only a validation error naming ONLY revealUrl falls back: any other failure is the list's own.
func TestSecretClientListDoesNotFallBackOnOtherErrors2758(t *testing.T) {
	for name, body := range map[string]string{
		"another field": strings.Replace(noRevealUrlField2758, "revealUrl", "somethingElse", 1),
		"forbidden":     gqlErrorJSON("FORBIDDEN"),
	} {
		t.Run(name, func(t *testing.T) {
			srv, calls := attnServer(t, map[string]string{listOp2758: body, "MySecretReadClients": list2758(clientRow2758(""))})
			f, _ := testFactory(t)
			root := NewRootCmd(f)
			root.SetArgs([]string{"--server", srv.URL, "secret", "client", "list", "--json"})
			if err := root.Execute(); err == nil {
				t.Fatalf("must fail as the list's own error")
			}
			for _, op := range opsOf2631(calls) {
				if op == "MySecretReadClients" {
					t.Errorf("no fallback read for %s: %v", name, opsOf2631(calls))
				}
			}
		})
	}
}

// On such a server a refused person reveal is exactly today's refusal (no link, no error).
func TestSecretRevealOnAServerWithoutRevealUrlIsUnchanged2758(t *testing.T) {
	srv, _ := attnServer(t, map[string]string{
		"RevealSecret":        gqlErrorJSON(notFound2758),
		listOp2758:            noRevealUrlField2758,
		"MySecretReadClients": list2758(clientRow2758("")),
	})
	r := runSecretReveal2664(t, srv.URL, true, "reveal", "cl-1")
	const wantText = "refused: SECRET_READ_CLIENT_NOT_FOUND; a read client is revealed by the worker it names, in that worker's session; a person reveals on the server's passkey page"
	if r.err == nil || r.err.Error() != wantText {
		t.Errorf("the refusal is exactly today's: %v", r.err)
	}
}

// client revoke's exact-id lookup never selects revealUrl: a safety action does not depend on a display field.
func TestSecretClientRevokeNeverSelectsRevealUrl2758(t *testing.T) {
	srv, calls := attnServer(t, map[string]string{
		"MySecretReadClients":         readClients2680(clientRow2680("c1", "gr-c1")),
		"RevokeSecretReadClientGrant": revoked2680("REVOKED"),
	})
	if _, err := runRevoke2680(t, srv.URL, "c1", "--yes"); err != nil {
		t.Fatal(err)
	}
	for _, c := range *calls {
		if strings.Contains(c.Query, "revealUrl") {
			t.Errorf("%s selects revealUrl: revoke must not depend on it", c.Op)
		}
		if c.Op == listOp2758 {
			t.Errorf("revoke must use the lookup document, not the list one")
		}
	}
}

// The lookup is bounded: ten pages of 200, then the sentence alone (a client beyond them is not found).
func TestSecretRevealLinkLookupIsBounded2758(t *testing.T) {
	var page []string
	for i := 0; i < 200; i++ {
		page = append(page, strings.Replace(clientRow2758(""), `"clientId":"cl-1"`, fmt.Sprintf(`"clientId":"x-%03d"`, i), 1))
	}
	srv, calls := attnServer(t, map[string]string{"RevealSecret": gqlErrorJSON(notFound2758), listOp2758: list2758(page...)})
	r := runSecretReveal2664(t, srv.URL, true, "reveal", "cl-1")
	if r.err == nil || strings.Contains(r.err.Error(), "passkey page: ") {
		t.Fatalf("no link past the budget: %v", r.err)
	}
	if n := listCalls2758(calls); n != 10 {
		t.Errorf("the lookup reads exactly ten pages and stops, got %d", n)
	}
}

// The link rides only the uniform NOT_FOUND refusal: no other error envelope has the key.
func TestSecretRevealOtherEnvelopesHaveNoRevealUrl2758(t *testing.T) {
	for _, code := range []string{"SECRET_READ_CLIENT_SPENT", "FORBIDDEN", "SECRET_DELETED"} {
		srv, _ := attnServer(t, map[string]string{"RevealSecret": gqlErrorJSON(code), listOp2758: list2758(clientRow2758(withURL2758(url2758)))})
		f, out := testFactory(t)
		root := NewRootCmd(f)
		root.SetArgs([]string{"--server", srv.URL, "secret", "reveal", "cl-1", "--allow-non-terminal", "--json"})
		err := root.Execute()
		renderError(f, err)
		if strings.Contains(out.String(), "revealUrl") {
			t.Errorf("%s: the envelope must not carry revealUrl: %q", code, out.String())
		}
	}
}

// After the rejection the later pages go straight to the document without the field (one
// rejected request per command, not one per page).
func TestSecretClientListAllRemembersTheRejectedDocument2758(t *testing.T) {
	responses := map[string]string{listOp2758: noRevealUrlField2758, "MySecretReadClients": list2758(clientRow2758(""))}
	bases := 0
	srv, calls := attnServerHook(t, responses, func(op string) {
		if op == "MySecretReadClients" {
			bases++
			if bases == 2 {
				setAttnResponse(responses, "MySecretReadClients", list2758())
			}
		}
	})
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"--server", srv.URL, "secret", "client", "list", "--all", "--json"})
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	if ops := opsOf2631(calls); strings.Join(ops, ",") != listOp2758+",MySecretReadClients,MySecretReadClients" {
		t.Errorf("the rejected document is tried once, then each page is read without the field: %v (%q)", ops, out.String())
	}
}
