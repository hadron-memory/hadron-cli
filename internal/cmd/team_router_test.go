package cmd

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/api"
	"github.com/hadron-memory/hadron-cli/internal/cmd/team"
	"github.com/hadron-memory/hadron-cli/internal/exitcode"
)

// cli#829 — `team router poll|loop|status`: config lookup, the --json
// contract, and one end-to-end poll through the real GraphQL client and a
// real exec of a fake `orca`. The engine's behaviour is pinned by the ported
// suite in internal/teamrouter; these pin the command's own surface.

func writeRouterConfig(t *testing.T, dir string, roster string) (path, stateDir string) {
	t.Helper()
	stateDir = filepath.Join(dir, "state")
	path = filepath.Join(dir, "router.json")
	cfg := `{"app": "hrn:app:acme.com:eng-team", "channel": "chan-team", "stateDir": ` + jsonString(stateDir) +
		`, "orcaCli": ` + jsonString(filepath.Join(dir, "orca")) + `, "busyWaitMs": 10, "submitWaitS": 1, "roster": ` + roster + `}`
	if err := os.WriteFile(path, []byte(cfg), 0o600); err != nil {
		t.Fatal(err)
	}
	return path, stateDir
}

func jsonString(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}

func TestTeamRouterStatusJSONShape(t *testing.T) {
	f, out := testFactory(t)
	dir := t.TempDir()
	path, stateDir := writeRouterConfig(t, dir, `[
		{"worker": "w-eli", "name": "Eli", "worktree": `+jsonString(filepath.Join(dir, "eli"))+`},
		{"worker": "w-ada", "name": "Ada", "worktree": `+jsonString(filepath.Join(dir, "ada"))+`, "coordinator": true}]`)
	root := NewRootCmd(f)
	root.SetArgs([]string{"team", "router", "status", "--config", path, "--json"})
	if err := root.Execute(); err != nil {
		t.Fatalf("status: %v", err)
	}
	var got map[string]json.RawMessage
	if err := json.Unmarshal([]byte(out.String()), &got); err != nil {
		t.Fatalf("status --json is not one JSON object: %v\n%s", err, out.String())
	}
	for key, want := range map[string]string{
		"stateDir": jsonString(stateDir), "tokenAdopted": "false", "locked": "false",
		"nudgedUnderCurrentToken": "[]", "held": "{}", "roster": `["Eli","Ada"]`, "liveness": `{"Ada":null}`, "pendingAcks": "[]",
	} {
		var compact bytes.Buffer
		if err := json.Compact(&compact, got[key]); err != nil || compact.String() != want {
			t.Errorf("%s = %s, want %s", key, got[key], want)
		}
	}
}

func TestTeamRouterConfigLookupOrder(t *testing.T) {
	f, out := testFactory(t)
	dir := t.TempDir()
	home := filepath.Join(dir, "home")
	t.Setenv("HOME", home)
	t.Setenv(team.RouterConfigEnv, "")
	t.Setenv("ORCA_ROUTER_CONFIG", "")
	legacyDir := filepath.Join(home, ".config", "hadron-orca-attention-router")
	if err := os.MkdirAll(legacyDir, 0o700); err != nil {
		t.Fatal(err)
	}
	legacy, legacyState := writeRouterConfig(t, legacyDir, `[]`)
	if err := os.Rename(legacy, filepath.Join(legacyDir, "config.json")); err != nil {
		t.Fatal(err)
	}
	status := func() string {
		t.Helper()
		out.Reset()
		root := NewRootCmd(f)
		root.SetArgs([]string{"team", "router", "status", "--json"})
		if err := root.Execute(); err != nil {
			t.Fatalf("status: %v", err)
		}
		var got struct{ StateDir string }
		if err := json.Unmarshal([]byte(out.String()), &got); err != nil {
			t.Fatal(err)
		}
		return got.StateDir
	}
	// Only the TS router's config exists: it is used as-is.
	if got := status(); got != legacyState {
		t.Fatalf("stateDir = %s, want the legacy config's %s", got, legacyState)
	}
	// The CLI's own config wins over the legacy one.
	ownDir := filepath.Join(os.Getenv("XDG_CONFIG_HOME"), "hadron", "team-router")
	if err := os.MkdirAll(ownDir, 0o700); err != nil {
		t.Fatal(err)
	}
	own, ownState := writeRouterConfig(t, ownDir, `[]`)
	if err := os.Rename(own, filepath.Join(ownDir, "config.json")); err != nil {
		t.Fatal(err)
	}
	if got := status(); got != ownState {
		t.Fatalf("stateDir = %s, want the CLI config's %s", got, ownState)
	}
	// The env var wins over both.
	envPath, envState := writeRouterConfig(t, filepath.Join(dir), `[]`)
	t.Setenv(team.RouterConfigEnv, envPath)
	if got := status(); got != envState {
		t.Fatalf("stateDir = %s, want $%s's %s", got, team.RouterConfigEnv, envState)
	}
}

func TestTeamRouterMissingOrInvalidConfigIsUsage(t *testing.T) {
	f, _ := testFactory(t)
	dir := t.TempDir()
	bad := filepath.Join(dir, "bad.json")
	if err := os.WriteFile(bad, []byte(`{"app": "a", "channel": "c", "stateDir": "s", "roster": [{"worker": "w", "name": "E", "worktree": "/x", "coordinator": "yes"}]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{filepath.Join(dir, "absent.json"), bad} {
		root := NewRootCmd(f)
		root.SetArgs([]string{"team", "router", "status", "--config", path})
		err := root.Execute()
		if exitCodeFor(err) != exitcode.Usage {
			t.Errorf("%s: exit %d (%v), want %d", filepath.Base(path), exitCodeFor(err), err, exitcode.Usage)
		}
	}
}

// fakeOrcaScript answers `orca terminal list|wait|read|send --json` with an
// idle, empty Claude terminal in worktree, and appends each send's argv to
// sent.log — a real exec, so the argv the router builds is what is checked.
func fakeOrcaScript(t *testing.T, dir, worktree string) {
	t.Helper()
	terminals := `{"ok":true,"result":{"terminals":[{"handle":"term_eli","worktreePath":` + jsonString(worktree) +
		`,"agentIdentity":"claude","connected":true,"writable":true,"lastOutputAt":1700000000000}]}}`
	screen := `{"ok":true,"result":{"terminal":{"handle":"term_eli","source":"screen","tail":["⏺ Done.","────────────","❯ ","────────────","  ⏵⏵ auto mode on · ← for agents"]}}}`
	script := `#!/bin/sh
case "$1 $2" in
"terminal list") echo '` + terminals + `' ;;
"terminal wait") echo '{"ok":true,"result":{"wait":{"satisfied":true}}}' ;;
"terminal read") echo '` + screen + `' ;;
"terminal send") printf '%s\n' "$*" >> ` + jsonString(filepath.Join(dir, "sent.log")) + `; echo '{"ok":true,"result":{"send":{"accepted":true,"prompt":{"stages":["input_accepted"]}}}}' ;;
*) echo '{"ok":false,"error":{"code":"unknown"}}'; exit 1 ;;
esac
`
	if err := os.WriteFile(filepath.Join(dir, "orca"), []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
}

func TestTeamRouterPollEndToEnd(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the fake orca is a POSIX shell script")
	}
	f, out := testFactory(t)
	dir := t.TempDir()
	worktree := filepath.Join(dir, "eli")
	if err := os.MkdirAll(worktree, 0o700); err != nil {
		t.Fatal(err)
	}
	fakeOrcaScript(t, dir, worktree)
	path, stateDir := writeRouterConfig(t, dir, `[{"worker": "w-eli", "name": "Eli", "worktree": `+jsonString(worktree)+`}]`)
	const page = `{"data":{"teamAttentionPage":{"scanComplete":true,"nextPage":null,"adoptableSince":"tok-SECRET-1",
		"items":[{"worker":"w-eli","workerName":"Eli","channel":"chan-team","unread":4,"unreadMentions":1,"firstUnreadSeq":100,"lastRelevantSeq":110,"lastSeq":120}]}}}`
	srv, calls := attnServer(t, map[string]string{
		"RouterAttentionPage": page,
		"RouterRegister":      `{"data":{"attendeeRegister":[{"namedBy":"WORKER","channel":{"id":"chan-team"},"entry":{"mentionOnly":true}}]}}`,
	})
	root := NewRootCmd(f)
	root.SetArgs([]string{"team", "router", "poll", "--config", path, "--json", "--server", srv.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("poll: %v\n%s", err, out.String())
	}
	var got struct {
		OK      bool
		Nudged  []string
		Held    map[string]string
		Adopted bool
	}
	if err := json.Unmarshal([]byte(out.String()), &got); err != nil {
		t.Fatalf("poll --json: %v\n%s", err, out.String())
	}
	if !got.OK || !got.Adopted || len(got.Nudged) != 1 || got.Nudged[0] != "Eli" {
		t.Fatalf("poll = %+v, want Eli nudged and the token adopted", got)
	}
	// Main scan (tokenless: nothing adopted yet), register, the fresh re-read.
	var ops []string
	for _, c := range *calls {
		ops = append(ops, c.Op)
		if _, sent := c.Vars["since"]; sent {
			t.Errorf("%s sent a since token before any was adopted: %v", c.Op, c.Vars)
		}
	}
	if strings.Join(ops, ",") != "RouterAttentionPage,RouterRegister,RouterAttentionPage" {
		t.Errorf("operations = %v", ops)
	}
	sent, err := os.ReadFile(filepath.Join(dir, "sent.log"))
	if err != nil {
		t.Fatalf("no send reached orca: %v", err)
	}
	if !strings.Contains(string(sent), "--terminal term_eli") || !strings.Contains(string(sent), "START AT #100") {
		t.Errorf("send argv = %s", sent)
	}
	if strings.Contains(string(sent), "SECRET") || strings.Contains(out.String(), "SECRET") {
		t.Error("the attention token leaked into a send or the output")
	}
	tok, err := os.ReadFile(filepath.Join(stateDir, "adopted.token"))
	if err != nil || string(tok) != "tok-SECRET-1" {
		t.Fatalf("adopted token = %q (%v)", tok, err)
	}

	// The next poll passes the adopted token back, and nobody is due.
	*calls = nil
	out.Reset()
	root = NewRootCmd(f)
	root.SetArgs([]string{"team", "router", "poll", "--config", path, "--json", "--server", srv.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("second poll: %v", err)
	}
	if len(*calls) == 0 || (*calls)[0].Vars["since"] != "tok-SECRET-1" {
		t.Fatalf("second poll did not pass the adopted token: %+v", *calls)
	}
}

func TestTeamRouterPollFailureIsExitOneTokenUnchanged(t *testing.T) {
	f, out := testFactory(t)
	dir := t.TempDir()
	path, stateDir := writeRouterConfig(t, dir, `[]`)
	srv, _ := attnServer(t, map[string]string{
		"RouterAttentionPage": `{"errors":[{"message":"boom"}],"data":null}`,
	})
	root := NewRootCmd(f)
	root.SetArgs([]string{"team", "router", "poll", "--config", path, "--json", "--server", srv.URL})
	err := root.Execute()
	if exitCodeFor(err) != exitcode.Error {
		t.Fatalf("exit %d (%v), want %d", exitCodeFor(err), err, exitcode.Error)
	}
	var got struct {
		OK    bool
		Error *string
	}
	if jerr := json.Unmarshal([]byte(out.String()), &got); jerr != nil || got.OK || got.Error == nil ||
		!strings.Contains(*got.Error, "token unchanged") {
		t.Fatalf("poll --json = %s (%v), want ok:false with the token-unchanged error", out.String(), jerr)
	}
	if _, err := os.Stat(filepath.Join(stateDir, "adopted.token")); !os.IsNotExist(err) {
		t.Fatalf("a failed poll must adopt nothing (stat: %v)", err)
	}
}

// ── v2: --source wakes (cli#829 PR 2) ───────────────────────────────────

// wakesServer answers the v2 operations and records each request's bearer
// and session header — the custody checks this source exists to keep.
type wakesCall struct {
	Op, Auth, Session string
	Vars              map[string]any
}

func wakesServer(t *testing.T, dueWakes string) (*httptest.Server, *[]wakesCall) {
	t.Helper()
	calls := &[]wakesCall{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			OperationName string         `json:"operationName"`
			Variables     map[string]any `json:"variables"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		*calls = append(*calls, wakesCall{Op: body.OperationName, Auth: r.Header.Get("Authorization"),
			Session: r.Header.Get(api.SessionHeader), Vars: body.Variables})
		w.Header().Set("Content-Type", "application/json")
		switch body.OperationName {
		case "RouterDueWakes":
			_, _ = w.Write([]byte(dueWakes))
		case "RouterAcknowledgeWakes":
			_, _ = w.Write([]byte(`{"data":{"acknowledgeWakes":{"acknowledged":1,"unchanged":0}}}`))
		default:
			t.Errorf("unexpected operation %q", body.OperationName)
			_, _ = w.Write([]byte(`{"errors":[{"message":"unexpected"}]}`))
		}
	}))
	t.Cleanup(srv.Close)
	return srv, calls
}

const oneDueWake = `{"data":{"dueWakes":{"status":"OK","withheld":0,"dormant":[],"pageInfo":{"endCursor":null,"hasNextPage":false},
	"wakes":[{"proof":"PROOF-SECRET","unread":21,"unreadMentions":1,"worker":{"id":"w-eli","name":"Eli"},
	"channels":[{"fromSeq":100,"throughSeq":120,"latestMentionSeq":110,"channel":{"id":"chan-team","name":"team"}}],
	"reasons":[{"kind":"MENTION","ticker":null,"task":null}]}]}}}`

func writeWakesConfig(t *testing.T, dir, worktree string, keyMode os.FileMode) string {
	t.Helper()
	key := filepath.Join(dir, "app.key")
	if err := os.WriteFile(key, []byte("hdr_app_ROUTERKEY\n"), keyMode); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(key, keyMode); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "wakes.json")
	cfg := `{"app": "hrn:app:acme.com:eng-team", "channel": "chan-team", "stateDir": ` + jsonString(filepath.Join(dir, "state")) +
		`, "orcaCli": ` + jsonString(filepath.Join(dir, "orca")) + `, "busyWaitMs": 10, "submitWaitS": 1, "source": "wakes", "appKeyFile": ` +
		jsonString(key) + `, "roster": [{"worker": "w-eli", "name": "Eli", "worktree": ` + jsonString(worktree) + `}]}`
	if err := os.WriteFile(path, []byte(cfg), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestTeamRouterWakesPollUsesTheAppKeyAndNoSession(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the fake orca is a POSIX shell script")
	}
	f, out := testFactory(t)
	dir := t.TempDir()
	worktree := filepath.Join(dir, "eli")
	if err := os.MkdirAll(worktree, 0o700); err != nil {
		t.Fatal(err)
	}
	fakeOrcaScript(t, dir, worktree)
	path := writeWakesConfig(t, dir, worktree, 0o600)
	srv, calls := wakesServer(t, oneDueWake)
	root := NewRootCmd(f)
	root.SetArgs([]string{"team", "router", "poll", "--config", path, "--json", "--server", srv.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("poll: %v\n%s", err, out.String())
	}
	var got struct {
		Source string
		OK     bool
		Nudged []string
		Status *string
	}
	if err := json.Unmarshal([]byte(out.String()), &got); err != nil {
		t.Fatalf("poll --json: %v\n%s", err, out.String())
	}
	if got.Source != "wakes" || !got.OK || len(got.Nudged) != 1 || got.Status == nil || *got.Status != "OK" {
		t.Fatalf("poll = %+v", got)
	}
	var ops []string
	for _, c := range *calls {
		ops = append(ops, c.Op)
		if c.Auth != "Bearer hdr_app_ROUTERKEY" {
			t.Errorf("%s carried %q, want the router's App key — never the operator's login", c.Op, c.Auth)
		}
		if c.Session != "" {
			t.Errorf("%s carried a worker session %q: an App key bound to a session is refused", c.Op, c.Session)
		}
	}
	if strings.Join(ops, ",") != "RouterDueWakes,RouterDueWakes,RouterAcknowledgeWakes" {
		t.Fatalf("operations = %v, want scan, re-read, acknowledge", ops)
	}
	if refs, _ := (*calls)[0].Vars["workerRefs"].([]any); len(refs) != 1 || refs[0] != "w-eli" {
		t.Errorf("the scan must name the roster, never read the whole App: %v", (*calls)[0].Vars)
	}
	sent, err := os.ReadFile(filepath.Join(dir, "sent.log"))
	if err != nil || !strings.Contains(string(sent), "START AT #100") {
		t.Fatalf("send argv = %s (%v)", sent, err)
	}
	if strings.Contains(string(sent)+out.String(), "SECRET") || strings.Contains(string(sent)+out.String(), "ROUTERKEY") {
		t.Fatal("the proof or the App key leaked into a send or the output")
	}
}

func TestTeamRouterWakesShadowTouchesNothing(t *testing.T) {
	f, out := testFactory(t)
	dir := t.TempDir()
	path := writeWakesConfig(t, dir, filepath.Join(dir, "eli"), 0o600) // no orca script exists: any exec would fail
	srv, calls := wakesServer(t, oneDueWake)
	root := NewRootCmd(f)
	root.SetArgs([]string{"team", "router", "poll", "--config", path, "--shadow", "--json", "--server", srv.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("shadow poll: %v\n%s", err, out.String())
	}
	if len(*calls) != 1 || (*calls)[0].Op != "RouterDueWakes" {
		t.Fatalf("shadow sent %v, want the one scan", *calls)
	}
	var got struct {
		Shadow bool
		Would  map[string]string
	}
	if err := json.Unmarshal([]byte(out.String()), &got); err != nil || !got.Shadow || got.Would["Eli"] == "" {
		t.Fatalf("shadow = %s (%v)", out.String(), err)
	}
	if _, err := os.Stat(filepath.Join(dir, "state")); !os.IsNotExist(err) {
		t.Fatalf("shadow wrote state (%v)", err)
	}
}

func TestTeamRouterWakesRefusals(t *testing.T) {
	f, _ := testFactory(t)
	dir := t.TempDir()
	loose := writeWakesConfig(t, dir, filepath.Join(dir, "eli"), 0o644)
	v1, _ := writeRouterConfig(t, t.TempDir(), `[]`)
	for name, args := range map[string][]string{
		"world-readable key file":   {"team", "router", "poll", "--config", loose},
		"--shadow on the v1 source": {"team", "router", "poll", "--config", v1, "--shadow"},
		"unknown --source":          {"team", "router", "poll", "--config", v1, "--source", "both"},
	} {
		root := NewRootCmd(f)
		root.SetArgs(args)
		err := root.Execute()
		if exitCodeFor(err) != exitcode.Usage {
			t.Errorf("%s: exit %d (%v), want %d", name, exitCodeFor(err), err, exitcode.Usage)
		}
		if err != nil && strings.Contains(err.Error(), "ROUTERKEY") {
			t.Errorf("%s: the refusal quoted the key", name)
		}
	}
}
