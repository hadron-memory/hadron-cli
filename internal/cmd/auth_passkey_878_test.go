package cmd

// cli#878 part 1 — `hadron auth passkey list` against Query.myPasskeys
// (hadron-server#1631 PR3). The gate is the point: a server without the
// `passkey-registration` capability (switch off, older server, failed
// discovery) answers exit 2 with ZERO GraphQL requests, credential or not.
// Fixtures are legal responses of the merged SDL (hadron-server 2755d34ad):
// the transports-null guard is a DTO unit test in internal/cmd/auth, not here.

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/cmd/auth"
)

// passkeyServer serves /capabilities with capsStatus/capsBody and answers
// every GraphQL POST with gqlStatus/gqlBody, counting the POSTs.
func passkeyServer(t *testing.T, capsStatus int, capsBody string, gqlStatus int, gqlBody string) (*httptest.Server, *atomic.Int32, *atomic.Value) {
	t.Helper()
	var posts atomic.Int32
	var lastAuth atomic.Value
	lastAuth.Store("")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && r.URL.Path == "/capabilities" {
			if r.Header.Get("Authorization") != "" {
				t.Errorf("/capabilities carried a credential")
			}
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(capsStatus)
			_, _ = io.WriteString(w, capsBody)
			return
		}
		posts.Add(1)
		lastAuth.Store(r.Header.Get("Authorization"))
		var body struct {
			OperationName string `json:"operationName"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		if body.OperationName != "MyPasskeys" {
			t.Errorf("unexpected operation %q", body.OperationName)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(gqlStatus)
		_, _ = io.WriteString(w, gqlBody)
	}))
	t.Cleanup(srv.Close)
	return srv, &posts, &lastAuth
}

const passkeysOn = `{"capabilities":["node-signing","passkey-registration"]}`

// One row per state, dates far from any test clock so the derivation is
// unambiguous; newest first, as the server orders them.
const passkeyListJSON = `{"data":{"myPasskeys":[
 {"id":"pk3","label":"New laptop","transports":["internal","hybrid"],"backupEligible":true,"backedUp":true,
  "registeredAt":"2026-11-10T09:00:00Z","lastUsedAt":null,"revokedAt":null,
  "window":{"endsAt":"2999-01-01T00:00:00Z","endedAt":null,"outcome":null,"registrationMethod":"github"},"cancel":null},
 {"id":"pk2","label":"Lost key","transports":["usb"],"backupEligible":false,"backedUp":false,
  "registeredAt":"2026-10-01T09:00:00Z","lastUsedAt":"2026-10-02T09:00:00Z","revokedAt":"2026-10-03T09:00:00Z",
  "window":{"endsAt":"2026-10-04T09:00:00Z","endedAt":"2026-10-03T09:00:00Z","outcome":"cancelled","registrationMethod":"google"},
  "cancel":{"at":"2026-10-03T09:00:00Z"}},
 {"id":"pk1","label":"Yubikey 5C","transports":[],"backupEligible":false,"backedUp":false,
  "registeredAt":"2026-01-01T09:00:00Z","lastUsedAt":"2026-11-01T09:00:00Z","revokedAt":null,
  "window":{"endsAt":"2026-01-04T09:00:00Z","endedAt":null,"outcome":null,"registrationMethod":"apple"},"cancel":null}
]}}`

func runPasskeyList(t *testing.T, url string, jsonOut bool, env map[string]string) (string, string, int) {
	t.Helper()
	f, out := testFactory(t)
	for k, v := range env {
		t.Setenv(k, v)
	}
	errOut := &strings.Builder{}
	f.IOStreams.ErrOut = errOut
	root := NewRootCmd(f)
	args := []string{"auth", "passkey", "list", "--server", url}
	if jsonOut {
		args = append(args, "--json")
	}
	root.SetArgs(args)
	err := root.Execute()
	code := 0
	if err != nil {
		code = renderError(f, err)
	}
	return out.String(), errOut.String(), code
}

// Pin 1: no capability means exit 2 and no GraphQL at all, for every way
// discovery can say "not offered" — and before the credential is consulted.
func TestPasskeyListOffMakesNoGraphQLRequest(t *testing.T) {
	cases := []struct {
		name   string
		status int
		body   string
		env    map[string]string
	}{
		{"switch off", 200, `{"capabilities":["node-signing"]}`, nil},
		{"all off", 200, `{"capabilities":[]}`, nil},
		{"older server 404", 404, `not found`, nil},
		{"unparsable discovery", 200, `{`, nil},
		{"off and no credential", 200, `{"capabilities":[]}`, map[string]string{"HADRON_TOKEN": ""}},
	}
	for _, tc := range cases {
		for _, js := range []bool{false, true} {
			t.Run(tc.name+map[bool]string{true: " json", false: ""}[js], func(t *testing.T) {
				srv, posts, _ := passkeyServer(t, tc.status, tc.body, 200, passkeyListJSON)
				out, errOut, code := runPasskeyList(t, srv.URL, js, tc.env)
				if code != 2 {
					t.Fatalf("exit %d, want 2 (out %q)", code, out)
				}
				if posts.Load() != 0 {
					t.Fatalf("%d GraphQL requests to a server without passkeys, want 0", posts.Load())
				}
				want := "this server does not offer passkeys"
				if tc.status != 200 || !strings.HasPrefix(tc.body, "{\"capabilities\"") {
					// Discovery failed: unknown, said as unknown.
					want = "could not confirm that this server offers passkeys"
				}
				if !strings.Contains(out+errOut, want) {
					t.Fatalf("want %q: out %q err %q", want, out, errOut)
				}
				if want != "this server does not offer passkeys" && strings.Contains(out+errOut, "does not offer passkeys") {
					t.Fatalf("a failed discovery must not claim the feature is absent: %q", out+errOut)
				}
			})
		}
	}
}

// Pin 2: the switch flipping off between discovery and the query is a
// validation error naming the dropped field — exit 2 with the passkeys
// sentence, not the generic schema-skew advice. Anything else keeps its code.
func TestPasskeyListSchemaDroppedAfterDiscovery(t *testing.T) {
	validation := `{"errors":[{"message":"Cannot query field \"myPasskeys\" on type \"Query\".","extensions":{"code":"GRAPHQL_VALIDATION_FAILED"}}]}`
	srv, posts, _ := passkeyServer(t, 200, passkeysOn, 400, validation)
	out, errOut, code := runPasskeyList(t, srv.URL, false, nil)
	if code != 2 || posts.Load() != 1 {
		t.Fatalf("exit %d posts %d, want 2 and 1", code, posts.Load())
	}
	all := out + errOut
	if !strings.Contains(all, "this server does not offer passkeys") || strings.Contains(all, "out of sync") {
		t.Fatalf("schema-skew advice for a switched-off server: %q", all)
	}

	// A validation error on some OTHER name is still skew, not "no passkeys":
	// the user gets the skew advice, after the one query.
	other := `{"errors":[{"message":"Cannot query field \"backedUp\" on type \"PasskeyCredential\".","extensions":{"code":"GRAPHQL_VALIDATION_FAILED"}}]}`
	srv2, posts2, _ := passkeyServer(t, 200, passkeysOn, 400, other)
	out2, errOut2, code2 := runPasskeyList(t, srv2.URL, false, nil)
	if code2 != 2 || posts2.Load() != 1 {
		t.Fatalf("exit %d posts %d, want 2 and 1", code2, posts2.Load())
	}
	if all := out2 + errOut2; !strings.Contains(all, "out of sync") || strings.Contains(all, "does not offer passkeys") {
		t.Fatalf("a different validation failure must keep the skew mapping, got %q", all)
	}
}

func TestPasskeyListJSON(t *testing.T) {
	srv, posts, authHdr := passkeyServer(t, 200, passkeysOn, 200, passkeyListJSON)
	out, _, code := runPasskeyList(t, srv.URL, true, nil)
	if code != 0 || posts.Load() != 1 {
		t.Fatalf("exit %d posts %d: %s", code, posts.Load(), out)
	}
	if got := authHdr.Load().(string); got != "Bearer hdr_user_test" {
		t.Fatalf("authorization %q", got)
	}
	var got []map[string]any
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("json: %v\n%s", err, out)
	}
	if len(got) != 3 {
		t.Fatalf("rows %d", len(got))
	}
	wantOrder := []string{"pk3", "pk2", "pk1"}
	wantState := []string{"cooling-off", "revoked", "active"}
	wantKeys := []string{"id", "label", "state", "registeredAt", "lastUsedAt", "revokedAt", "window", "cancel", "transports", "backupEligible", "backedUp"}
	for i, row := range got {
		if row["id"] != wantOrder[i] || row["state"] != wantState[i] {
			t.Fatalf("row %d = %v, want id %s state %s (server order kept)", i, row, wantOrder[i], wantState[i])
		}
		if len(row) != len(wantKeys) {
			t.Fatalf("row %d keys %v, want exactly %v", i, row, wantKeys)
		}
		for _, k := range wantKeys {
			if _, ok := row[k]; !ok {
				t.Fatalf("row %d lacks %q", i, k)
			}
		}
	}
	if tr, ok := got[2]["transports"].([]any); !ok || len(tr) != 0 {
		t.Fatalf("empty transports must be [], got %#v", got[2]["transports"])
	}
	w := got[1]["window"].(map[string]any)
	if w["outcome"] != "cancelled" || w["registrationMethod"] != "google" || got[1]["cancel"].(map[string]any)["at"] != "2026-10-03T09:00:00Z" {
		t.Fatalf("window/cancel not carried: %v", got[1])
	}
	// Pin 3b: no key-material name as any key of the output. The names are
	// the one canonical list the operation-selection unit test also reads
	// (cli#1950 item 3), so the two guards cannot drift apart.
	forbidden := map[string]bool{}
	for _, n := range auth.PasskeyKeyMaterialNames {
		forbidden[strings.ToLower(n)] = true
	}
	var keys []map[string]any
	_ = json.Unmarshal([]byte(out), &keys)
	var walk func(any)
	walk = func(v any) {
		switch v := v.(type) {
		case map[string]any:
			for k, inner := range v {
				if forbidden[strings.ToLower(k)] {
					t.Fatalf("--json carries %s: %s", k, out)
				}
				walk(inner)
			}
		case []any:
			for _, inner := range v {
				walk(inner)
			}
		}
	}
	for _, row := range keys {
		walk(row)
	}
}

func TestPasskeyListTable(t *testing.T) {
	srv, _, _ := passkeyServer(t, 200, passkeysOn, 200, passkeyListJSON)
	out, _, code := runPasskeyList(t, srv.URL, false, nil)
	if code != 0 {
		t.Fatalf("exit %d", code)
	}
	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	if len(lines) != 4 {
		t.Fatalf("table:\n%s", out)
	}
	for _, h := range []string{"LABEL", "STATE", "REGISTERED", "LAST USED", "WINDOW ENDS", "ID"} {
		if !strings.Contains(lines[0], h) {
			t.Fatalf("header %q lacks %s", lines[0], h)
		}
	}
	for i, want := range [][]string{
		{"New laptop", "cooling-off", "never", "2999-01-01T00:00:00Z", "pk3"},
		{"Lost key", "revoked", "2026-10-04T09:00:00Z", "pk2"},
		{"Yubikey 5C", "active", "2026-11-01T09:00:00Z", "2026-01-04T09:00:00Z", "pk1"},
	} {
		for _, cell := range want {
			if !strings.Contains(lines[i+1], cell) {
				t.Fatalf("row %d %q lacks %q", i+1, lines[i+1], cell)
			}
		}
	}
	if strings.Contains(strings.ToLower(out), "usb") {
		t.Fatalf("transports belong to --json only:\n%s", out)
	}
}

func TestPasskeyListWindowlessRowShowsDash(t *testing.T) {
	body := `{"data":{"myPasskeys":[{"id":"pk9","label":"Second key","transports":["usb"],"backupEligible":false,"backedUp":false,` +
		`"registeredAt":"2026-11-01T00:00:00Z","lastUsedAt":null,"revokedAt":null,"window":null,"cancel":null}]}}`
	srv, _, _ := passkeyServer(t, 200, passkeysOn, 200, body)
	out, _, code := runPasskeyList(t, srv.URL, false, nil)
	if code != 0 {
		t.Fatalf("exit %d", code)
	}
	// The WINDOW ENDS cell itself is "-", not a dash anywhere in the padding
	// (cli#1950 item 5). Cells are split on the table's two-space gutter.
	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	cells := tableCells(lines[1])
	header := tableCells(lines[0])
	if len(cells) != len(header) {
		t.Fatalf("row %q has %d cells, header %q has %d", lines[1], len(cells), lines[0], len(header))
	}
	for i, h := range header {
		switch h {
		case "STATE":
			if cells[i] != "active" {
				t.Fatalf("STATE %q, want active", cells[i])
			}
		case "WINDOW ENDS":
			if cells[i] != "-" {
				t.Fatalf("WINDOW ENDS %q, want - for a later passkey", cells[i])
			}
		}
	}
}

var tableGutter = regexp.MustCompile(`\s{2,}`)

func tableCells(line string) []string {
	return tableGutter.Split(strings.TrimSpace(line), -1)
}

// cli#1950 item 4 (+ item 6, the PasskeyCapabilities seam): a discovery
// error whose text spans lines (an HTML error page quoted in it) still
// reads as ONE refusal line that keeps its "no query was sent" tail.
func TestPasskeyListDiscoveryErrorIsOneLine(t *testing.T) {
	srv, posts, _ := passkeyServer(t, 200, passkeysOn, 200, passkeyListJSON)
	prev := auth.PasskeyCapabilities
	t.Cleanup(func() { auth.PasskeyCapabilities = prev })
	calls := 0
	auth.PasskeyCapabilities = func(context.Context, string) (map[string]bool, error) {
		calls++
		return nil, errors.New("GET /capabilities: 502\n<html>\n<body>boom</body>\r\n</html>")
	}
	out, errOut, code := runPasskeyList(t, srv.URL, false, nil)
	if code != 2 || posts.Load() != 0 || calls != 1 {
		t.Fatalf("exit %d posts %d discovery calls %d, want 2, 0 and 1", code, posts.Load(), calls)
	}
	all := out + errOut
	var line string
	for _, l := range strings.Split(all, "\n") {
		if strings.Contains(l, "could not confirm that this server offers passkeys") {
			line = l
		}
	}
	if line == "" || !strings.Contains(line, "boom") || !strings.HasSuffix(strings.TrimSpace(line), "no query was sent") {
		t.Fatalf("the refusal must be one line with the cause and its tail: %q", all)
	}
	if strings.Contains(all, "\r") {
		t.Fatalf("a raw CR reached the terminal: %q", all)
	}
}

// cli#1950 item 7: a transport failure at query time (capabilities on, the
// GraphQL POST answers 500 with an HTML page) keeps the generic server-error
// mapping: one request, not the passkeys sentence, no HTML dumped.
func TestPasskeyListQueryTransportFailure(t *testing.T) {
	srv, posts, _ := passkeyServer(t, 200, passkeysOn, 500, "<html><body>Internal Server Error</body></html>")
	out, errOut, code := runPasskeyList(t, srv.URL, true, nil)
	if posts.Load() != 1 {
		t.Fatalf("posts %d, want 1", posts.Load())
	}
	if code == 0 || code == 2 {
		t.Fatalf("exit %d: a 500 is neither success nor \"no passkeys\" (out %q err %q)", code, out, errOut)
	}
	all := out + errOut
	if strings.Contains(all, "does not offer passkeys") || strings.Contains(all, "could not confirm") {
		t.Fatalf("a query-time 500 must not read as a discovery answer: %q", all)
	}
	if strings.Contains(all, "<html") {
		t.Fatalf("the HTML body reached the output: %q", all)
	}
}

// cli#1950 item 8: an owner-chosen label carrying control characters can
// neither recolor the terminal nor forge a row (output.SafeCell).
func TestPasskeyListLabelIsSafeCell(t *testing.T) {
	body := `{"data":{"myPasskeys":[{"id":"pk9","label":"evil\u001b[31mred\nforged row","transports":[],"backupEligible":false,"backedUp":false,` +
		`"registeredAt":"2026-11-01T00:00:00Z","lastUsedAt":null,"revokedAt":null,"window":null,"cancel":null}]}}`
	srv, _, _ := passkeyServer(t, 200, passkeysOn, 200, body)
	out, _, code := runPasskeyList(t, srv.URL, false, nil)
	if code != 0 {
		t.Fatalf("exit %d", code)
	}
	if strings.ContainsRune(out, 0x1b) {
		t.Fatalf("an escape reached the terminal: %q", out)
	}
	if lines := strings.Split(strings.TrimRight(out, "\n"), "\n"); len(lines) != 2 || !strings.Contains(lines[1], "forged row") {
		t.Fatalf("the label forged a row: %q", out)
	}
	// --json keeps the owner's original label.
	jout, _, _ := runPasskeyList(t, srv.URL, true, nil)
	var rows []map[string]any
	if err := json.Unmarshal([]byte(jout), &rows); err != nil || rows[0]["label"] != "evil\x1b[31mred\nforged row" {
		t.Fatalf("--json must carry the original label: %v %s", err, jout)
	}
}

// Pin 5: an empty list is [] in --json and a sentence in text.
func TestPasskeyListEmpty(t *testing.T) {
	srv, _, _ := passkeyServer(t, 200, passkeysOn, 200, `{"data":{"myPasskeys":[]}}`)
	out, _, code := runPasskeyList(t, srv.URL, true, nil)
	if code != 0 || strings.TrimSpace(out) != "[]" {
		t.Fatalf("exit %d out %q, want []", code, out)
	}
	out, _, _ = runPasskeyList(t, srv.URL, false, nil)
	if strings.TrimSpace(out) != "No passkeys." {
		t.Fatalf("text %q", out)
	}
}

// Pin 6: the server's two refusals keep their exits and their server code.
func TestPasskeyListRefusals(t *testing.T) {
	cases := []struct {
		name, code, message string
		exit                int
	}{
		{"app key", "UNAUTHENTICATED", "myPasskeys requires the owner's own user credential.", 3},
		{"impersonation", "FORBIDDEN", "Forbidden", 8},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			body, _ := json.Marshal(map[string]any{"data": nil, "errors": []any{map[string]any{
				"message": tc.message, "path": []string{"myPasskeys"}, "extensions": map[string]any{"code": tc.code}}}})
			srv, posts, _ := passkeyServer(t, 200, passkeysOn, 200, string(body))
			out, _, code := runPasskeyList(t, srv.URL, true, nil)
			if code != tc.exit || posts.Load() != 1 {
				t.Fatalf("exit %d posts %d, want %d and 1: %s", code, posts.Load(), tc.exit, out)
			}
			var env struct {
				Error struct {
					Code       int    `json:"code"`
					ServerCode string `json:"serverCode"`
					Message    string `json:"message"`
				} `json:"error"`
			}
			if err := json.Unmarshal([]byte(out), &env); err != nil {
				t.Fatalf("envelope: %v\n%s", err, out)
			}
			if env.Error.Code != tc.exit || env.Error.ServerCode != tc.code {
				t.Fatalf("envelope %s", out)
			}
			if tc.code == "UNAUTHENTICATED" && !strings.Contains(env.Error.Message, "owner's own user credential") {
				t.Fatalf("the App-key refusal must name the owner's own credential: %q", env.Error.Message)
			}
		})
	}
}

// With the capability on but no credential, the existing sign-in refusal
// answers 3 with no GraphQL request.
func TestPasskeyListOnWithoutCredential(t *testing.T) {
	srv, posts, _ := passkeyServer(t, 200, passkeysOn, 200, passkeyListJSON)
	_, _, code := runPasskeyList(t, srv.URL, false, map[string]string{"HADRON_TOKEN": ""})
	if code != 3 || posts.Load() != 0 {
		t.Fatalf("exit %d posts %d, want 3 and 0", code, posts.Load())
	}
}

// Pin 7: the help says the CLI cannot remove a passkey and names no URL or
// portal page; there is no revoke verb.
func TestPasskeyHelpIsTruthfulAboutRemoval(t *testing.T) {
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"auth", "passkey", "list", "--help"})
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	help := out.String()
	if !strings.Contains(help, "This CLI cannot remove a passkey.") {
		t.Fatalf("help:\n%s", help)
	}
	// cli#1950 items 1-2: STATE leads with its meaning, and the cooling-off
	// window is explained inside cor:aut:040:07 (the FIT's four points):
	// first passkey only, it holds back signatures (not sign-in), how it
	// ends, and a server-set length of at least 72 hours, never a fixed one.
	flat := strings.Join(strings.Fields(help), " ")
	for _, want := range []string{
		"STATE is revoked for a removed passkey",
		"one registered when you had no other",
		"at least 72 hours",
		"set by the server",
		"pending and don't count",
		"can still sign you in",
		"a fresh sign-in by a different method ends it early",
		"its pending signatures never count",
		"worked out on this machine's clock and grants nothing",
	} {
		if !strings.Contains(flat, want) {
			t.Fatalf("help lacks %q:\n%s", want, help)
		}
	}
	if strings.Contains(flat, "STATE is worked out") {
		t.Fatalf("STATE still leads with the mechanism:\n%s", help)
	}
	if strings.Contains(strings.ReplaceAll(flat, "at least 72 hours", ""), "72 hours") {
		t.Fatalf("a fixed window length is named:\n%s", help)
	}
	for _, bad := range []string{"http", "/passkeys/", "portal", "security page"} {
		if strings.Contains(help, bad) {
			t.Fatalf("help names %q:\n%s", bad, help)
		}
	}
	cmd, _, err := NewRootCmd(f).Find([]string{"auth", "passkey", "revoke"})
	if err == nil && cmd.Name() == "revoke" {
		t.Fatal("a revoke command exists")
	}
}
