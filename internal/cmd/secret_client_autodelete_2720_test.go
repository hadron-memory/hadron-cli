package cmd

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/api/gen"
	"github.com/hadron-memory/hadron-cli/internal/exitcode"
)

// cli#2720: the auto-delete mark on `secret client issue` is passed to the server as given and
// the server's refusals come back unchanged; `secret client list` shows the mark.

// serverRefusal2720 is a coded refusal as the server sends it: the code, and a message of its own.
func serverRefusal2720(code, message string) string {
	return `{"errors":[{"message":"` + message + `","extensions":{"code":"` + code + `"}}]}`
}

// Acceptance 1 (red where a code is unmapped today: SECRET_AUTO_DELETE_NOT_ALLOWED fell to exit
// 1): each of the four refusals the issue names, in the server's order, yields its mapped exit
// code and the server's own message unchanged. There is NO client-side check: --auto-delete with
// no window and no budget is still sent (one issue request), and no signing request follows a
// refused issue.
func TestSecretClientIssueAutoDeleteRefusalsPassThrough2720(t *testing.T) {
	for _, tc := range []struct {
		code, message string
		exit          int
	}{
		{"SECRET_AUTO_DELETE_NEEDS_LIMIT", "An auto-delete mark needs a window or a read budget.", exitcode.Usage},
		{"SECRET_AUTO_DELETE_NOT_ALLOWED", "You may not set an auto-delete mark for this secret.", exitcode.Forbidden},
		{"GRANT_HOLDER_NOT_ALLOWED", "A read client is held by a person or a worker.", exitcode.Forbidden},
		{"BAD_USER_INPUT", "A time is not a valid date.", exitcode.Usage},
	} {
		t.Run(tc.code, func(t *testing.T) {
			srv, calls := attnServer(t, map[string]string{"IssueSecretReadClient": serverRefusal2720(tc.code, tc.message)})
			_, _, err := runSecretClient2631(t, srv.URL, "issue", "s1", "--reason", "r", "--auto-delete")
			if err == nil || exitCodeFor(err) != tc.exit {
				t.Fatalf("exit=%d want %d: %v", exitCodeFor(err), tc.exit, err)
			}
			if !strings.Contains(err.Error(), tc.message) {
				t.Errorf("the server's message must come through unchanged: %q does not contain %q", err.Error(), tc.message)
			}
			if got := opsOf2631(calls); len(got) != 1 || got[0] != "IssueSecretReadClient" {
				t.Errorf("a refused issue is one request and no signing request follows: %v", got)
			}
			if in := (*calls)[0].Vars["input"].(map[string]any); in["autoDelete"] != true {
				t.Errorf("--auto-delete without a window or budget must still be sent (no client pre-check): %v", in)
			}
		})
	}
}

// Acceptance 2: without --auto-delete the issue request is byte-identical to what the command
// sent before the flag was documented (autoDelete false, nothing else added).
func TestSecretClientIssueWithoutAutoDeleteIsByteIdentical2720(t *testing.T) {
	srv, calls := attnServer(t, issueResponses2631("OPEN"))
	if _, _, err := runSecretClient2631(t, srv.URL, "issue", "s1", "--reason", "r"); err != nil {
		t.Fatal(err)
	}
	const want = `{"input":{"autoDelete":false,"holderKind":"USER","reason":"r","secretRef":"s1"}}`
	if got := string((*calls)[0].RawVars); got != want {
		t.Fatalf("the request without --auto-delete changed:\n got %s\nwant %s", got, want)
	}
}

// Acceptance 3: `secret client list` shows the mark in an AUTO-DELETE column, and --json carries
// destroyOnEnd (a boolean, never null) on every row.
func TestSecretClientListShowsTheAutoDeleteMark2720(t *testing.T) {
	marked := `{"clientId":"c1","clientPrefix":"c1","grantId":"g1","secretId":"s1","secretName":"tok-marked","selfIssued":true,"endReason":null,"createdAt":"2026-11-20T00:00:00Z","destroyOnEnd":true}`
	plain := `{"clientId":"c2","clientPrefix":"c2","grantId":"g2","secretId":"s1","secretName":"tok-plain","selfIssued":true,"endReason":null,"createdAt":"2026-11-20T00:00:00Z","destroyOnEnd":false}`
	srv, _ := attnServer(t, map[string]string{"ListSecretReadClients": readClients2680(marked, plain)})
	out, _, err := runSecretClient2631(t, srv.URL, "list", "--json")
	if err != nil {
		t.Fatal(err)
	}
	var rows []map[string]any
	if e := json.Unmarshal([]byte(out), &rows); e != nil || len(rows) != 2 {
		t.Fatalf("json: %v\n%s", e, out)
	}
	if rows[0]["destroyOnEnd"] != true || rows[1]["destroyOnEnd"] != false {
		t.Errorf("--json must carry destroyOnEnd per row: %v", rows)
	}
	text, _, err := runSecretClient2631(t, srv.URL, "list")
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(text), "\n")
	if len(lines) != 3 || !strings.Contains(lines[0], "AUTO-DELETE") {
		t.Fatalf("the table needs an AUTO-DELETE column:\n%s", text)
	}
	if !strings.Contains(lines[1], "tok-marked") || !strings.Contains(lines[1], "yes") || !strings.Contains(lines[2], "tok-plain") || !strings.Contains(lines[2], "no") || strings.Contains(lines[2], "yes") {
		t.Errorf("the column must read yes for the marked row and no for the other:\n%s", text)
	}
}

// Acceptance 4 (help): the help states the shipped behaviour (all three stale spots), the FIT
// conditions (revoking a marked client does NOT cancel the deletion; "writer" is the writer of
// the secret's current version), and no longer says the server refuses the mark.
func TestSecretClientIssueHelpStatesTheShippedAutoDelete2720(t *testing.T) {
	f, _ := testFactory(t)
	c, _, err := NewRootCmd(f).Find([]string{"secret", "client", "issue"})
	if err != nil || c.Name() != "issue" {
		t.Fatalf("no issue command: %v", err)
	}
	long := strings.Join(strings.Fields(c.Long), " ")
	usage := strings.Join(strings.Fields(c.Flags().Lookup("auto-delete").Usage), " ")
	for _, stale := range []string{"until it is built", "not yet available", "refuses until"} {
		if strings.Contains(long, stale) || strings.Contains(usage, stale) {
			t.Errorf("the help still carries the stale %q:\n%s\n%s", stale, long, usage)
		}
	}
	for _, want := range []string{
		"deletes the secret", "last read", "when its window ends", "--expires-at or --max-reads",
		"With --expires-at the deletion is a signed deadline",
		"revoking a marked client (or the holder leaving) does not cancel the deletion",
		"With only --max-reads there is no deadline",
		"revoking cancels the deletion",
		"writer of the secret's current version",
		`the refusal is the same "not permitted" (exit 8), never "not found"`,
	} {
		if !strings.Contains(long, want) {
			t.Errorf("the help lacks %q:\n%s", want, long)
		}
	}
	// Jessie's P2: "revoking does not cancel" is only true with a window. It must NOT be
	// stated unconditionally anywhere (the issue help, the flag usage).
	for _, over := range []string{"revoking a marked client does not cancel the deletion.", "once signed, revoking the client does not cancel it"} {
		if strings.Contains(long, over) || strings.Contains(usage, over) {
			t.Errorf("the help states unconditionally %q: it holds only with --expires-at", over)
		}
	}
	for _, want := range []string{"with --expires-at a signed deletion survives revoking the client", "with only --max-reads revoking cancels it"} {
		if !strings.Contains(usage, want) {
			t.Errorf("the --auto-delete usage lacks %q: %s", want, usage)
		}
	}
	for _, want := range []string{"deletes the secret", "needs --expires-at or --max-reads"} {
		if !strings.Contains(usage, want) {
			t.Errorf("the --auto-delete flag usage lacks %q: %s", want, usage)
		}
	}
	lc, _, _ := NewRootCmd(f).Find([]string{"secret", "client", "list"})
	if lc == nil || !strings.Contains(strings.Join(strings.Fields(lc.Long), " "), "auto-delete") {
		t.Errorf("the list help must name the auto-delete column")
	}
}

// Joran's P2-1 (a server that lacks destroyOnEnd), both rounds: the list query is a document
// of its own and revoke's lookup is the mark-free one. Against a server that rejects the field
// at validation, `client revoke` still finds its client and revokes it, and `client list` does
// not fail: it reads the mark-free document and shows the mark as UNKNOWN ("?" in the table,
// null in --json, a stderr note), never "no", so the recovery advice that points at the list
// still works on such a server.
func TestSecretClientOnAServerWithoutTheMark2720(t *testing.T) {
	validation := `{"errors":[{"message":"Cannot query field \"destroyOnEnd\" on type \"SecretReadClient\".","extensions":{"code":"GRAPHQL_VALIDATION_FAILED"}}]}`
	srv, calls := attnServer(t, map[string]string{
		"ListSecretReadClients":       validation,
		"MySecretReadClients":         readClients2680(clientRow2680("c1", "gr-c1")),
		"RevokeSecretReadClientGrant": revoked2680("REVOKED"),
	})
	if _, err := runRevoke2680(t, srv.URL, "c1", "--yes"); err != nil || revokes2680(calls) != 1 {
		t.Fatalf("revoke must work against a server without the mark: err=%v ops=%v", err, opsOf2631(calls))
	}
	for _, c := range *calls {
		if c.Op == "ListSecretReadClients" {
			t.Errorf("revoke's lookup must not select the auto-delete mark: %v", opsOf2631(calls))
		}
	}
	out, errOut, err := runSecretClient2631(t, srv.URL, "list", "--json")
	if err != nil {
		t.Fatalf("list against a server without the mark must still list: %v", err)
	}
	var rows []map[string]any
	if json.Unmarshal([]byte(out), &rows) != nil || len(rows) != 1 || rows[0]["destroyOnEnd"] != nil {
		t.Fatalf("--json must carry destroyOnEnd: null (unknown), never false: %s", out)
	}
	if _, has := rows[0]["destroyOnEnd"]; !has || !strings.Contains(errOut, "does not report the auto-delete mark") {
		t.Errorf("the key must be present and a stderr note must say the mark is unknown: has=%v %q", has, errOut)
	}
	text, _, err := runSecretClient2631(t, srv.URL, "list")
	lines := strings.Split(strings.TrimSpace(text), "\n")
	if err != nil || len(lines) != 2 || !strings.Contains(lines[1], "?") || strings.Contains(lines[1], " no ") {
		t.Errorf("the table must show ? for an unknown mark, not no:\n%s", text)
	}
	// `list --all` keeps the fallback across pages: the document the server rejected is asked
	// ONCE, then every page (and the empty terminator) is read mark-free, and the rows are
	// concatenated. A fallback that reset per page would ask the rejected document again.
	resp2 := map[string]string{
		"ListSecretReadClients": validation,
		"MySecretReadClients":   readClients2680(clientRow2680("c1", "gr-c1")),
	}
	lookups := 0
	srv2, calls2 := attnServerHook(t, resp2, func(op string) {
		if op == "MySecretReadClients" {
			lookups++
			if lookups == 2 { // the second lookup is the empty page that ends --all
				setAttnResponse(resp2, "MySecretReadClients", readClients2680())
			}
		}
	})
	out2, _, err := runSecretClient2631(t, srv2.URL, "list", "--all", "--json")
	var rows2 []map[string]any
	if err != nil || json.Unmarshal([]byte(out2), &rows2) != nil || len(rows2) != 1 || rows2[0]["destroyOnEnd"] != nil {
		t.Fatalf("list --all on a server without the mark: err=%v out=%s", err, out2)
	}
	rejected, mine := 0, 0
	for _, c := range *calls2 {
		switch c.Op {
		case "ListSecretReadClients":
			rejected++
		case "MySecretReadClients":
			mine++
		}
	}
	if rejected != 1 || mine != 2 {
		t.Errorf("one rejected list request, then two mark-free pages (a row and the empty one): rejected=%d lookups=%d %v", rejected, mine, opsOf2631(calls2))
	}
	// A server that lacks only the reveal link (cli#2758) takes the same road.
	noLinkErr := `{"errors":[{"message":"Cannot query field \"revealUrl\" on type \"SecretReadClient\".","extensions":{"code":"GRAPHQL_VALIDATION_FAILED"}}]}`
	srv3, _ := attnServer(t, map[string]string{"ListSecretReadClients": noLinkErr, "MySecretReadClients": readClients2680(clientRow2680("c1", "gr-c1"))})
	out3, errOut3, err := runSecretClient2631(t, srv3.URL, "list", "--json")
	var rows3 []map[string]any
	if err != nil || json.Unmarshal([]byte(out3), &rows3) != nil || len(rows3) != 1 || rows3[0]["destroyOnEnd"] != nil || rows3[0]["revealUrl"] != nil || !strings.Contains(errOut3, "does not report the auto-delete mark") {
		t.Errorf("a server without the link still lists, mark unknown, with the note: err=%v out=%s note=%q", err, out3, errOut3)
	}
}

// The lookup document stays mark-free (an op-name mock cannot see this): if destroyOnEnd were
// added back to MySecretReadClients, revoke would again depend on the display column.
func TestRevokeLookupDocumentStaysMarkFree2720(t *testing.T) {
	if strings.Contains(gen.MySecretReadClients_Operation, "destroyOnEnd") {
		t.Fatalf("the revoke lookup must not select destroyOnEnd:\n%s", gen.MySecretReadClients_Operation)
	}
	if !strings.Contains(gen.ListSecretReadClients_Operation, "destroyOnEnd") {
		t.Fatalf("the list document must select destroyOnEnd:\n%s", gen.ListSecretReadClients_Operation)
	}
}

// Joran's P2-3 (a silent false negative for the destructive mark): a row the server answered
// WITHOUT destroyOnEnd, or with null, is a malformed answer, never "AUTO-DELETE no". Nothing is
// printed on stdout (no partial table).
func TestSecretClientListRowWithoutTheMarkIsMalformed2720(t *testing.T) {
	for name, row := range map[string]string{
		"absent": `{"clientId":"c1","clientPrefix":"c1","grantId":"g1","secretId":"s1","secretName":"tok","selfIssued":true,"endReason":null,"createdAt":"2026-11-20T00:00:00Z"}`,
		"null":   `{"clientId":"c1","clientPrefix":"c1","grantId":"g1","secretId":"s1","secretName":"tok","selfIssued":true,"endReason":null,"createdAt":"2026-11-20T00:00:00Z","destroyOnEnd":null}`,
	} {
		for _, asJSON := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/json=%v", name, asJSON), func(t *testing.T) {
				srv, _ := attnServer(t, map[string]string{"ListSecretReadClients": readClients2680(row)})
				args := []string{"list"}
				if asJSON {
					args = append(args, "--json")
				}
				out, _, err := runSecretClient2631(t, srv.URL, args...)
				if err == nil || exitCodeFor(err) != exitcode.Error || !strings.Contains(err.Error(), "auto-delete mark") {
					t.Fatalf("want a malformed-answer error (exit 1): exit=%d err=%v", exitCodeFor(err), err)
				}
				if out != "" {
					t.Errorf("a malformed row must print nothing on stdout: %q", out)
				}
			})
		}
	}
}

// Joran's P2-2 (an irreversible mark armed quietly), both rounds: --auto-delete prints a note on
// stderr naming the secret and what revoking does for the flags sent (a window is a signed
// deadline that revoking does not stop; with only --max-reads revoking before the last read
// cancels it); stdout and the --json shape of the issue are unchanged; without the flag there is
// no note.
func TestSecretClientIssueAutoDeletePrintsAStderrNote2720(t *testing.T) {
	srv, _ := attnServer(t, issueResponses2631("OPEN"))
	out, errOut, err := runSecretClient2631(t, srv.URL, "issue", "s1", "--reason", "r", "--max-reads", "1", "--auto-delete", "--json")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(errOut, "note: --auto-delete was sent") || !strings.Contains(errOut, "deploy-token") || !strings.Contains(errOut, "secret client list") ||
		!strings.Contains(errOut, "revoking the client before its last read cancels it") {
		t.Errorf("a --max-reads-only mark: the note must name the secret, say revoking cancels it and where to read the mark back: %q", errOut)
	}
	var doc map[string]any
	if json.Unmarshal([]byte(out), &doc) != nil || doc["clientId"] == nil || doc["autoDelete"] != nil || strings.Contains(out, "note:") {
		t.Errorf("the issue --json shape and stdout must be unchanged by the note: %s", out)
	}
	srv1, _ := attnServer(t, issueResponses2631("OPEN"))
	_, errOut1, err := runSecretClient2631(t, srv1.URL, "issue", "s1", "--reason", "r", "--expires-at", "2026-12-02T00:00:00Z", "--auto-delete")
	if err != nil || !strings.Contains(errOut1, "revoking the client will not stop it") || strings.Contains(errOut1, "cancels it") {
		t.Errorf("a mark with a window: the note must say revoking will not stop it: err=%v %q", err, errOut1)
	}
	srv2, _ := attnServer(t, issueResponses2631("OPEN"))
	_, errOut2, err := runSecretClient2631(t, srv2.URL, "issue", "s1", "--reason", "r", "--max-reads", "1")
	if err != nil || strings.Contains(errOut2, "--auto-delete was sent") {
		t.Errorf("no note without the flag: err=%v %q", err, errOut2)
	}
}

// The note is printed as soon as the client exists, BEFORE the signing step: if the signing
// request then fails (the client is left pending with the mark armed) the person still saw it.
func TestSecretClientIssueAutoDeleteNoteSurvivesASigningFailure2720(t *testing.T) {
	responses := issueResponses2631("OPEN")
	responses["TeamRequestGrantSignature"] = serverRefusal2720("INTERNAL_SERVER_ERROR", "boom")
	srv, calls := attnServer(t, responses)
	_, errOut, err := runSecretClient2631(t, srv.URL, "issue", "s1", "--reason", "r", "--max-reads", "1", "--auto-delete")
	if err == nil {
		t.Fatal("the signing failure must still be an error")
	}
	if !strings.Contains(errOut, "note: --auto-delete was sent") {
		t.Errorf("the note must be printed even though the signing step failed (the mark is armed on a pending client): %q", errOut)
	}
	if got := opsOf2631(calls); len(got) != 2 {
		t.Errorf("the issue and the signing request: %v", got)
	}
}

// The secret's name reaches the terminal inside the note: it is neutralised (a control or format
// character from the server cannot forge a line).
func TestSecretClientIssueAutoDeleteNoteNeutralisesTheSecretName2720(t *testing.T) {
	responses := issueResponses2631("OPEN")
	responses["IssueSecretReadClient"] = `{"data":{"issueSecretReadClient":{"clientId":"cl-2631","grantId":"gr-2631","secretName":"tok\u001b[31m\nforged: line","selfIssued":true}}}`
	srv, _ := attnServer(t, responses)
	_, errOut, err := runSecretClient2631(t, srv.URL, "issue", "s1", "--reason", "r", "--max-reads", "1", "--auto-delete")
	if err != nil {
		t.Fatal(err)
	}
	if strings.ContainsAny(errOut, "\x1b") || strings.Contains(errOut, "\nforged: line") {
		t.Errorf("the secret name must be neutralised in the note: %q", errOut)
	}
}
