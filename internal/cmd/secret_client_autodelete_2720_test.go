package cmd

import (
	"encoding/json"
	"strings"
	"testing"

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

// Joran's P2-1 (a server that lacks destroyOnEnd): the list query is a document of its own, so
// `client revoke`, a safety action, does not depend on the display column. Against a server that
// rejects the field at validation, list fails with the schema-skew refusal (exit 2) and revoke
// still finds its client and revokes it.
func TestSecretClientRevokeDoesNotDependOnTheAutoDeleteColumn2720(t *testing.T) {
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
	_, _, err := runSecretClient2631(t, srv.URL, "list")
	if err == nil || exitCodeFor(err) != exitcode.Usage {
		t.Fatalf("list against a server that rejects the field is the schema-skew refusal (exit 2): exit=%d %v", exitCodeFor(err), err)
	}
}

// Joran's P2-3 (a silent false negative for the destructive mark): a row the server answered
// WITHOUT destroyOnEnd, or with null, is a malformed answer, never "AUTO-DELETE no".
func TestSecretClientListRowWithoutTheMarkIsMalformed2720(t *testing.T) {
	for name, row := range map[string]string{
		"absent": `{"clientId":"c1","clientPrefix":"c1","grantId":"g1","secretId":"s1","secretName":"tok","selfIssued":true,"endReason":null,"createdAt":"2026-11-20T00:00:00Z"}`,
		"null":   `{"clientId":"c1","clientPrefix":"c1","grantId":"g1","secretId":"s1","secretName":"tok","selfIssued":true,"endReason":null,"createdAt":"2026-11-20T00:00:00Z","destroyOnEnd":null}`,
	} {
		for _, asJSON := range []bool{false, true} {
			srv, _ := attnServer(t, map[string]string{"ListSecretReadClients": readClients2680(row)})
			args := []string{"list"}
			if asJSON {
				args = append(args, "--json")
			}
			out, _, err := runSecretClient2631(t, srv.URL, args...)
			if err == nil || exitCodeFor(err) != exitcode.Error || !strings.Contains(err.Error(), "auto-delete mark") || strings.Contains(out, "no") && !asJSON {
				t.Errorf("%s json=%v: want a malformed-answer error (exit 1), not a row: exit=%d err=%v out=%q", name, asJSON, exitCodeFor(err), err, out)
			}
		}
	}
}

// Joran's P2-2 (an irreversible mark armed quietly): --auto-delete prints a note on stderr naming
// the secret and what was asked; stdout and the --json shape of the issue are unchanged, and
// without the flag there is no note.
func TestSecretClientIssueAutoDeletePrintsAStderrNote2720(t *testing.T) {
	srv, _ := attnServer(t, issueResponses2631("OPEN"))
	out, errOut, err := runSecretClient2631(t, srv.URL, "issue", "s1", "--reason", "r", "--max-reads", "1", "--auto-delete", "--json")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(errOut, "note: --auto-delete was sent") || !strings.Contains(errOut, "deploy-token") || !strings.Contains(errOut, "secret client list") {
		t.Errorf("the stderr note must name the secret and where to read the mark back: %q", errOut)
	}
	var doc map[string]any
	if json.Unmarshal([]byte(out), &doc) != nil || doc["clientId"] == nil || doc["autoDelete"] != nil || strings.Contains(out, "note:") {
		t.Errorf("the issue --json shape and stdout must be unchanged by the note: %s", out)
	}
	srv2, _ := attnServer(t, issueResponses2631("OPEN"))
	_, errOut2, err := runSecretClient2631(t, srv2.URL, "issue", "s1", "--reason", "r", "--max-reads", "1")
	if err != nil || strings.Contains(errOut2, "--auto-delete was sent") {
		t.Errorf("no note without the flag: err=%v %q", err, errOut2)
	}
}
