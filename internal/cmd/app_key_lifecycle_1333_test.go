package cmd

import (
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// cli#1333: app key revoke | rotate | list --scopes. Each command sends exactly
// one operation; revoke and rotate are irreversible and refuse without --yes
// when non-interactive, with nothing sent.

const (
	rotatedRaw1333 = "hdr_app_ROTATEDSECRET0123456789ab"
	rotated1333    = `{"data":{"regenerateAppKey":{"rawKey":"` + rotatedRaw1333 + `","app":{"id":"app_1","urn":"hrn:app:acme.com:ops","name":"Ops"}}}}`
)

func runKeyArgs(t *testing.T, srv *httptest.Server, args ...string) keyRun {
	t.Helper()
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs(append(append([]string{"app", "key"}, args...), "--server", srv.URL))
	err := root.Execute()
	return keyRun{out: out.String(), errOut: f.IOStreams.ErrOut.(*strings.Builder).String(), err: err}
}

func opOf(req map[string]any) string { s, _ := req["operationName"].(string); return s }

func onlyOp(t *testing.T, reqs []map[string]any, want string) map[string]any {
	t.Helper()
	real := reqs
	if len(real) != 1 || opOf(real[0]) != want {
		names := []string{}
		for _, r := range real {
			names = append(names, opOf(r))
		}
		t.Fatalf("operations = %v, want exactly [%s]", names, want)
	}
	vars, _ := real[0]["variables"].(map[string]any)
	return vars
}

func TestAppKeyRevokeSendsOneOperationWithTheKeyID(t *testing.T) {
	srv, _, reqs := keyServerCapture(t, 200, `{"data":{"revokeAppKey":true}}`)
	r := runKeyArgs(t, srv, "revoke", "key_9", "--yes", "--json")
	if r.err != nil {
		t.Fatalf("execute: %v", r.err)
	}
	vars := onlyOp(t, reqs(), "RevokeAppKey")
	if len(vars) != 1 || vars["id"] != "key_9" {
		t.Errorf("variables = %v", vars)
	}
	var m map[string]any
	if err := json.Unmarshal([]byte(r.out), &m); err != nil || m["id"] != "key_9" || m["revoked"] != true {
		t.Errorf("json = %v %q", err, r.out)
	}
}

func TestAppKeyRevokeAndRotateRefuseNonInteractiveWithoutYes(t *testing.T) {
	srv, n := keyServer(t, 200, rotated1333)
	dir := t.TempDir()
	for name, args := range map[string][]string{
		"revoke": {"revoke", "key_9"},
		"rotate": {"rotate", app1223, "--out", filepath.Join(dir, "k")},
	} {
		t.Run(name, func(t *testing.T) {
			r := runKeyArgs(t, srv, args...)
			if r.err == nil || exitCodeFor(r.err) != 2 {
				t.Fatalf("want exit 2, got %v (%d)", r.err, exitCodeFor(r.err))
			}
		})
	}
	if n.Load() != 0 {
		t.Errorf("%d request(s) sent, want none", n.Load())
	}
	if ents, _ := os.ReadDir(dir); len(ents) != 0 {
		t.Errorf("refusal left files behind: %v", ents)
	}
}

func TestAppKeyRevokeFalseIsNotReportedAsSuccess(t *testing.T) {
	srv, _ := keyServer(t, 200, `{"data":{"revokeAppKey":false}}`)
	r := runKeyArgs(t, srv, "revoke", "key_9", "--yes")
	if r.err == nil || strings.Contains(r.out, "✓") {
		t.Fatalf("a false answer must be an error, got %v %q", r.err, r.out)
	}
}

func TestAppKeyRotateWritesKeyAt0600AndNeverEchoesIt(t *testing.T) {
	srv, _, reqs := keyServerCapture(t, 200, rotated1333)
	out := filepath.Join(t.TempDir(), "rot.key")
	r := runKeyArgs(t, srv, "rotate", app1223, "--out", out, "--yes", "--json")
	if r.err != nil {
		t.Fatalf("execute: %v", r.err)
	}
	vars := onlyOp(t, reqs(), "RegenerateAppKey")
	if vars["appRef"] != app1223 {
		t.Errorf("variables = %v", vars)
	}
	got, err := os.ReadFile(out)
	if err != nil || strings.TrimSpace(string(got)) != rotatedRaw1333 {
		t.Fatalf("key file = %q, %v", got, err)
	}
	if m := readMode(t, out); !m.IsRegular() || m.Perm() != 0o600 {
		t.Errorf("mode = %v, want regular 0600", m)
	}
	if strings.Contains(r.all(), rotatedRaw1333) {
		t.Fatalf("raw key leaked: %q %q", r.out, r.errOut)
	}
	var m map[string]any
	if err := json.Unmarshal([]byte(r.out), &m); err != nil || m["out"] != out || m["appId"] != "app_1" || m["allOtherKeysRevoked"] != true {
		t.Errorf("shape = %v %q", err, r.out)
	}
}

func TestAppKeyRotateTextSaysEveryKeyIsRevokedAndNewKeyHoldsNoScopes(t *testing.T) {
	srv, _ := keyServer(t, 200, rotated1333)
	r := runKeyArgs(t, srv, "rotate", app1223, "--out", filepath.Join(t.TempDir(), "k"), "--yes")
	if r.err != nil {
		t.Fatal(r.err)
	}
	low := strings.ToLower(r.out)
	if !strings.Contains(low, "every") || !strings.Contains(low, "no scope") {
		t.Errorf("result must say every key was revoked and the new key holds no scopes: %q", r.out)
	}
}

func TestAppKeyRotatePrintKeyOptInAndJSONRefusal(t *testing.T) {
	srv, n := keyServer(t, 200, rotated1333)
	dir := t.TempDir()
	r := runKeyArgs(t, srv, "rotate", app1223, "--out", filepath.Join(dir, "k"), "--yes", "--print-key")
	if r.err != nil || !strings.Contains(r.out, rotatedRaw1333) {
		t.Fatalf("--print-key must print: %v %q", r.err, r.out)
	}
	before := n.Load()
	r = runKeyArgs(t, srv, "rotate", app1223, "--out", filepath.Join(dir, "k2"), "--yes", "--print-key", "--json")
	if r.err == nil || exitCodeFor(r.err) != 2 || n.Load() != before {
		t.Errorf("--print-key with --json must be refused before sending: %v", r.err)
	}
}

func TestAppKeyRotateExistingOutRefusedBeforeAnyMutation(t *testing.T) {
	srv, n := keyServer(t, 200, rotated1333)
	existing := filepath.Join(t.TempDir(), "k")
	if err := os.WriteFile(existing, []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}
	r := runKeyArgs(t, srv, "rotate", app1223, "--out", existing, "--yes")
	if r.err == nil || exitCodeFor(r.err) != 2 || n.Load() != 0 {
		t.Fatalf("want exit 2 and nothing sent: %v, %d", r.err, n.Load())
	}
	if got, _ := os.ReadFile(existing); string(got) != "old" {
		t.Errorf("existing file changed: %q", got)
	}
}

func TestAppKeyRotateNeverRetriesALostAnswerAndKeepsNoFile(t *testing.T) {
	srv, n := keyServer(t, 502, `bad gateway`)
	out := filepath.Join(t.TempDir(), "k")
	r := runKeyArgs(t, srv, "rotate", app1223, "--out", out, "--yes")
	if r.err == nil {
		t.Fatal("want an error")
	}
	if n.Load() != 1 {
		t.Errorf("requests = %d, want 1 (no retry)", n.Load())
	}
	if _, err := os.Lstat(out); err == nil {
		t.Errorf("a file was kept after a lost answer")
	}
	msg := strings.ToLower(r.err.Error())
	if !strings.Contains(msg, "app key list") || !strings.Contains(msg, "every") {
		t.Errorf("message must say every key may be revoked and how to recover: %v", r.err)
	}
}

func TestAppKeyRotateRefusalKeepsServerCodeAndWritesNothing(t *testing.T) {
	srv, _ := keyServer(t, 200, `{"errors":[{"message":"nope","extensions":{"code":"FORBIDDEN"}}],"data":null}`)
	out := filepath.Join(t.TempDir(), "k")
	r := runKeyArgs(t, srv, "rotate", app1223, "--out", out, "--yes", "--json")
	if r.err == nil || exitCodeFor(r.err) != 8 {
		t.Fatalf("want exit 8, got %v (%d)", r.err, exitCodeFor(r.err))
	}
	if _, err := os.Lstat(out); err == nil {
		t.Errorf("file left behind after a refusal")
	}
}

func TestAppKeyRevokeRefusalMapsToTypedExit(t *testing.T) {
	srv, _ := keyServer(t, 200, `{"errors":[{"message":"nope","extensions":{"code":"UNAUTHENTICATED"}}],"data":null}`)
	r := runKeyArgs(t, srv, "revoke", "key_9", "--yes")
	if r.err == nil || exitCodeFor(r.err) == 0 || exitCodeFor(r.err) == 1 {
		t.Fatalf("want a typed exit, got %v (%d)", r.err, exitCodeFor(r.err))
	}
}

const keysScopes1333 = `{"data":{"appKeys":[{"id":"key_1","keyPreview":"hdr_app_AAAA…1111","label":"poller","createdAt":"2026-10-01T00:00:00Z","lastUsedAt":null,"revokedAt":null,
 "scopes":[{"scope":"host:operate","hostId":"orca1","grantedAt":"2026-10-02T00:00:00Z","grantedBy":"u1"}],
 "hostUses":[{"hostId":"orca1","action":"collect","count":3,"operationId":null,"refusedCode":"APP_KEY_SCOPE_REQUIRED","usedAt":"2026-10-03T00:00:00Z"}]},
 {"id":"key_2","keyPreview":"hdr_app_BBBB…2222","label":null,"createdAt":"2026-10-01T00:00:00Z","lastUsedAt":null,"revokedAt":null,"scopes":[],"hostUses":[]}]}}`

func TestAppKeyListDefaultStaysMetadataOnly(t *testing.T) {
	srv, _, reqs := keyServerCapture(t, 200, `{"data":{"appKeys":[]}}`)
	r := runKeyArgs(t, srv, "list", app1223, "--json")
	if r.err != nil {
		t.Fatal(r.err)
	}
	onlyOp(t, reqs(), "AppKeysMetadata")
}

func TestAppKeyListScopesIsOneAppKeysCallWithScopesAndNoHostUses(t *testing.T) {
	srv, _, reqs := keyServerCapture(t, 200, keysScopes1333)
	r := runKeyArgs(t, srv, "list", app1223, "--scopes", "--json")
	if r.err != nil {
		t.Fatal(r.err)
	}
	vars := onlyOp(t, reqs(), "AppKeysScopes")
	if vars["appRef"] != app1223 {
		t.Errorf("variables = %v", vars)
	}
	var rows []map[string]any
	if err := json.Unmarshal([]byte(r.out), &rows); err != nil || len(rows) != 2 {
		t.Fatalf("json = %v %q", err, r.out)
	}
	sc, _ := rows[0]["scopes"].([]any)
	if len(sc) != 1 || sc[0].(map[string]any)["hostId"] != "orca1" {
		t.Errorf("scopes = %v", rows[0]["scopes"])
	}
	if s, ok := rows[1]["scopes"].([]any); !ok || len(s) != 0 {
		t.Errorf("an empty scope list must render [], got %v", rows[1]["scopes"])
	}
	if _, has := rows[0]["hostUses"]; has {
		t.Errorf("hostUses must be absent without --host-uses")
	}
}

func TestAppKeyListHostUsesSendsFirstAndRendersUses(t *testing.T) {
	srv, _, reqs := keyServerCapture(t, 200, keysScopes1333)
	r := runKeyArgs(t, srv, "list", app1223, "--scopes", "--host-uses", "5", "--json")
	if r.err != nil {
		t.Fatal(r.err)
	}
	vars := onlyOp(t, reqs(), "AppKeysScopesHostUses")
	if vars["first"] != float64(5) {
		t.Errorf("variables = %v", vars)
	}
	var rows []map[string]any
	_ = json.Unmarshal([]byte(r.out), &rows)
	if u, ok := rows[1]["hostUses"].([]any); !ok || len(u) != 0 {
		t.Errorf("empty hostUses must be [], got %v", rows[1]["hostUses"])
	}
	if u, _ := rows[0]["hostUses"].([]any); len(u) != 1 || u[0].(map[string]any)["refusedCode"] != "APP_KEY_SCOPE_REQUIRED" {
		t.Errorf("hostUses = %v", rows[0]["hostUses"])
	}
}

func TestAppKeyListHostUsesNeedsScopesAndAPositiveCount(t *testing.T) {
	srv, n := keyServer(t, 200, keysScopes1333)
	for name, args := range map[string][]string{
		"without --scopes": {"list", app1223, "--host-uses", "5"},
		"zero":             {"list", app1223, "--scopes", "--host-uses", "0"},
		"negative":         {"list", app1223, "--scopes", "--host-uses", "-1"},
	} {
		t.Run(name, func(t *testing.T) {
			r := runKeyArgs(t, srv, args...)
			if r.err == nil || exitCodeFor(r.err) != 2 {
				t.Fatalf("want exit 2, got %v (%d)", r.err, exitCodeFor(r.err))
			}
		})
	}
	if n.Load() != 0 {
		t.Errorf("%d request(s) sent, want none", n.Load())
	}
}

func TestAppKeyCreateHelpNoLongerPrintsTheRawAPIRevokeRoute(t *testing.T) {
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"app", "key", "create", "--help"})
	_ = root.Execute()
	if strings.Contains(out.String(), "hadron api") {
		t.Errorf("create help still routes revoke through `hadron api`: %q", out.String())
	}
}

// Acceptance 4: a refusal keeps its server code in the --json envelope beside
// the typed exit, on every new command, for both access refusals.
func TestAppKeyLifecycleRefusalJSONKeepsServerCode(t *testing.T) {
	dir := t.TempDir()
	for _, tc := range []struct {
		code string
		exit int
	}{{"FORBIDDEN", 8}, {"UNAUTHENTICATED", 3}} {
		for name, args := range map[string][]string{
			"revoke":      {"revoke", "key_9", "--yes", "--json"},
			"rotate":      {"rotate", app1223, "--out", filepath.Join(dir, tc.code+"-rot"), "--yes", "--json"},
			"list-scopes": {"list", app1223, "--scopes", "--json"},
			"list-uses":   {"list", app1223, "--scopes", "--host-uses", "3", "--json"},
		} {
			t.Run(tc.code+"/"+name, func(t *testing.T) {
				srv, _ := keyServer(t, 200, `{"errors":[{"message":"nope","extensions":{"code":"`+tc.code+`"}}],"data":null}`)
				f, out := testFactory(t)
				root := NewRootCmd(f)
				root.SetArgs(append(append([]string{"app", "key"}, args...), "--server", srv.URL))
				err := root.Execute()
				if err == nil {
					t.Fatal("refusal succeeded")
				}
				if code := renderError(f, err); code != tc.exit {
					t.Errorf("exit %d want %d", code, tc.exit)
				}
				var env struct {
					Error struct {
						Code       int    `json:"code"`
						ServerCode string `json:"serverCode"`
					} `json:"error"`
				}
				if e := json.Unmarshal([]byte(out.String()), &env); e != nil {
					t.Fatal(e, out.String())
				}
				if env.Error.ServerCode != tc.code || env.Error.Code != tc.exit {
					t.Errorf("envelope %s", out.String())
				}
			})
		}
	}
}

// A rotation whose answer has a key but no app (the SDL makes it non-null)
// still lands: the old keys are already revoked, so the only new key must be
// kept, and the command must not crash after committing it.
func TestAppKeyRotateKeepsTheKeyWhenTheAnswerHasNoApp(t *testing.T) {
	srv, _ := keyServer(t, 200, `{"data":{"regenerateAppKey":{"rawKey":"`+rotatedRaw1333+`","app":null}}}`)
	out := filepath.Join(t.TempDir(), "k")
	r := runKeyArgs(t, srv, "rotate", app1223, "--out", out, "--yes", "--json")
	if r.err != nil {
		t.Fatalf("must not fail after the key was minted: %v", r.err)
	}
	if got, _ := os.ReadFile(out); strings.TrimSpace(string(got)) != rotatedRaw1333 {
		t.Errorf("the new key must be on disk: %q", got)
	}
	var m map[string]any
	if err := json.Unmarshal([]byte(r.out), &m); err != nil || m["appUrn"] != app1223 {
		t.Errorf("shape = %v %q", err, r.out)
	}
	if strings.Contains(r.all(), rotatedRaw1333) {
		t.Errorf("raw key leaked")
	}
}
