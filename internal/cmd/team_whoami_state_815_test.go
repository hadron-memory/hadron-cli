package cmd

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/exitcode"
)

// cli#815 (server #2307, cor:agt:020:13): `whoami --check` shows the server's
// OWN session state — live / lapsed / dormant / ended — and never works one out.
// The fakes answer the state and a deadline that deliberately DISAGREE with a
// client-side reading, so a CLI that recomputed from `lapsesAt` or a clock
// would fail here.

const (
	// A deadline long past and one far future: neither may decide the display.
	past815   = "2001-01-01T00:00:00Z"
	future815 = "2999-01-01T00:00:00Z"
)

// stateFake answers GetBoundSessionState per session id through `state`, the
// ordinary session read through `session`, and records every operation.
type stateFake struct {
	mu    sync.Mutex
	ops   []string
	state func(id string) string
	srv   *httptest.Server
}

func newStateFake(t *testing.T, session string, authAndList map[string]string, state func(id string) string) *stateFake {
	t.Helper()
	fk := &stateFake{state: state}
	fk.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && r.URL.Path == "/capabilities" {
			_, _ = w.Write([]byte(`{"capabilities":[]}`))
			return
		}
		var body struct {
			Op   string         `json:"operationName"`
			Vars map[string]any `json:"variables"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		fk.mu.Lock()
		fk.ops = append(fk.ops, body.Op)
		fk.mu.Unlock()
		switch body.Op {
		case "GetBoundSessionState":
			id, _ := body.Vars["id"].(string)
			if fk.state == nil {
				_, _ = w.Write([]byte(`{"errors":[{"message":"not stubbed"}]}`))
				return
			}
			resp := fk.state(id)
			if resp == "502" {
				w.WriteHeader(http.StatusBadGateway)
				_, _ = w.Write([]byte(`bad gateway`))
				return
			}
			_, _ = w.Write([]byte(resp))
		case "GetTeamSession":
			_, _ = w.Write([]byte(session))
		case "WorkerReporting":
			_, _ = w.Write([]byte(`{"data":{"worker":null}}`))
		default:
			if resp, ok := authAndList[body.Op]; ok {
				_, _ = w.Write([]byte(resp))
				return
			}
			_, _ = w.Write([]byte(`{"errors":[{"message":"not stubbed"}]}`))
		}
	}))
	t.Cleanup(fk.srv.Close)
	return fk
}

func (fk *stateFake) count(op string) int {
	fk.mu.Lock()
	defer fk.mu.Unlock()
	n := 0
	for _, o := range fk.ops {
		if o == op {
			n++
		}
	}
	return n
}

func bindState815(t *testing.T) {
	t.Helper()
	dir := teamGitDir(t)
	if err := os.WriteFile(filepath.Join(dir, "hadron-team-session.json"), []byte(bindingWithTeamFixture), 0o600); err != nil {
		t.Fatal(err)
	}
}

func runState815(t *testing.T, fk *stateFake, args ...string) (string, error) {
	t.Helper()
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs(append(args, "--server", fk.srv.URL))
	err := root.Execute()
	return out.String(), err
}

func rawJSON815(t *testing.T, s string) map[string]json.RawMessage {
	t.Helper()
	var raw map[string]json.RawMessage
	if err := json.Unmarshal([]byte(s), &raw); err != nil {
		t.Fatalf("decode: %v — %s", err, s)
	}
	return raw
}

// The four server states plus expiry-before-reap, bound worktree, text and JSON.
func TestWhoamiCheckShowsTheServersSessionState(t *testing.T) {
	cases := []struct {
		name      string
		session   string // GetTeamSession (the open-row answer)
		state     string
		wantLine  string
		notInLine []string
		wantState string
		wantLive  string
		wantLapse string
		wantAct   string
	}{
		{
			// The deadline is long past, yet the server says LIVE: displayed as
			// the server says, never recomputed from lapsesAt.
			name: "live", session: whoamiCheckSession("", ""),
			state:    whoamiStateJSON("LIVE", true, past815),
			wantLine: "live, lapses at " + past815, notInLine: []string{"LAPSED", "DORMANT", "ENDED", "still open"},
			wantState: `"live"`, wantLive: "true", wantLapse: `"` + past815 + `"`, wantAct: "true",
		},
		{
			// The deadline is far future, yet the server says LAPSED.
			name: "lapsed", session: whoamiCheckSession("", ""),
			state:    whoamiStateJSON("LAPSED", false, future815),
			wantLine: "LAPSED at " + future815, notInLine: []string{"live,", "DORMANT", "still open"},
			wantState: `"lapsed"`, wantLive: "false", wantLapse: `"` + future815 + `"`, wantAct: "true",
		},
		{
			name: "dormant", session: whoamiCheckSession("", ""),
			state:    whoamiStateJSON("DORMANT", false, past815),
			wantLine: "DORMANT — not resumable by any agent act", notInLine: []string{"live,", "LAPSED", "still open"},
			wantState: `"dormant"`, wantLive: "false", wantLapse: `"` + past815 + `"`, wantAct: "true",
		},
		{
			name: "ended by a person", session: whoamiCheckSession("2026-09-19T11:00:00Z", ""),
			state:    whoamiStateJSON("ENDED", false, ""),
			wantLine: "ENDED 2026-09-19T11:00:00Z", notInLine: []string{"auto-expired", "live,", "DORMANT"},
			wantState: `"ended"`, wantLive: "false", wantLapse: "null", wantAct: "false",
		},
		{
			name: "ended by the reaper", session: whoamiCheckSession("2026-09-19T11:00:00Z", "2026-09-19T11:00:00Z"),
			state:    whoamiStateJSON("ENDED", false, ""),
			wantLine: "ENDED 2026-09-19T11:00:00Z — auto-expired by the server", notInLine: []string{"live,", "DORMANT"},
			wantState: `"ended"`, wantLive: "false", wantLapse: "null", wantAct: "false",
		},
		{
			// Hard expiry passed, row not yet reaped: endedAt is null so
			// `active` stays true (its stable meaning), but the state is ENDED
			// and the line must not say the session is open.
			name: "ended by expiry before the reap", session: whoamiCheckSession("", ""),
			state:    whoamiStateJSON("ENDED", false, ""),
			wantLine: "ENDED — expired and not yet reaped", notInLine: []string{"still open", "live,"},
			wantState: `"ended"`, wantLive: "false", wantLapse: "null", wantAct: "true",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			bindState815(t)
			fk := newStateFake(t, c.session, nil, func(string) string { return c.state })
			out, err := runState815(t, fk, "team", "session", "whoami", "--check")
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(out, "server: "+c.wantLine) {
				t.Errorf("want server line %q in:\n%s", c.wantLine, out)
			}
			for _, bad := range c.notInLine {
				if strings.Contains(out, "server: ") && strings.Contains(out[strings.Index(out, "server: "):], bad) {
					t.Errorf("server line must not contain %q:\n%s", bad, out)
				}
			}
			jout, err := runState815(t, fk, "team", "session", "whoami", "--check", "--json")
			if err != nil {
				t.Fatal(err)
			}
			raw := rawJSON815(t, jout)
			for _, f := range []struct{ key, want string }{
				{"state", c.wantState}, {"live", c.wantLive}, {"lapsesAt", c.wantLapse},
				{"active", c.wantAct}, {"checked", "true"}, {"candidateStates", "[]"},
			} {
				if got := string(raw[f.key]); got != f.want {
					t.Errorf("%s = %s, want %s", f.key, got, f.want)
				}
			}
		})
	}
}

// An older server rejects the new field at validation: the state degrades to
// "unknown" (never guessed) and the open-row answer is unchanged.
func TestWhoamiCheckOldServerDegradesToUnknown(t *testing.T) {
	bindState815(t)
	old := `{"errors":[{"message":"Cannot query field \"state\" on type \"Session\".","extensions":{"code":"GRAPHQL_VALIDATION_FAILED"}}]}`
	fk := newStateFake(t, whoamiCheckSession("", ""), nil, func(string) string { return old })
	out, err := runState815(t, fk, "team", "session", "whoami", "--check")
	if err != nil {
		t.Fatalf("an old server must not fail the read: %v", err)
	}
	if !strings.Contains(out, "server: open (live state unknown") || strings.Contains(out, "live,") || strings.Contains(out, "DORMANT") {
		t.Errorf("want an honest unknown, nothing guessed:\n%s", out)
	}
	jout, _ := runState815(t, fk, "team", "session", "whoami", "--check", "--json")
	raw := rawJSON815(t, jout)
	if string(raw["state"]) != `"unknown"` || string(raw["live"]) != "null" || string(raw["lapsesAt"]) != "null" || string(raw["active"]) != "true" {
		t.Errorf("old-server JSON: %s", jout)
	}
}

// A transport failure on the state read is NOT "unknown": it keeps the
// command's existing error behaviour.
func TestWhoamiCheckStateTransportFailureIsAnError(t *testing.T) {
	bindState815(t)
	fk := newStateFake(t, whoamiCheckSession("", ""), nil, func(string) string { return "502" })
	_, err := runState815(t, fk, "team", "session", "whoami", "--check")
	if err == nil {
		t.Fatal("a failed read must not render as unknown")
	}
	if code := exitCodeFor(err); code == exitcode.OK {
		t.Errorf("exit code = %d", code)
	}
}

// A null session answer on the state read (unreadable) is unknown, not ended.
func TestWhoamiCheckNullStateSessionIsUnknown(t *testing.T) {
	bindState815(t)
	fk := newStateFake(t, whoamiCheckSession("", ""), nil, func(string) string { return `{"data":{"session":null}}` })
	jout, err := runState815(t, fk, "team", "session", "whoami", "--check", "--json")
	if err != nil {
		t.Fatal(err)
	}
	if got := string(rawJSON815(t, jout)["state"]); got != `"unknown"` {
		t.Errorf("state = %s", got)
	}
}

// Plain whoami makes no session-state read (the existing reporting read stays),
// and the new keys are null / [].
func TestWhoamiPlainMakesNoCallAndStateIsNull(t *testing.T) {
	bindState815(t)
	fk := newStateFake(t, whoamiCheckSession("", ""), nil, func(string) string { return whoamiStateJSON("LIVE", true, "") })
	jout, err := runState815(t, fk, "team", "session", "whoami", "--json")
	if err != nil {
		t.Fatal(err)
	}
	if fk.count("GetBoundSessionState") != 0 || fk.count("GetTeamSession") != 0 {
		t.Errorf("plain whoami must not check liveness: %v", fk.ops)
	}
	raw := rawJSON815(t, jout)
	for _, k := range []string{"live", "lapsesAt", "state"} {
		if string(raw[k]) != "null" {
			t.Errorf("%s = %s, want null when --check did not run", k, raw[k])
		}
	}
	if string(raw["candidateStates"]) != "[]" {
		t.Errorf("candidateStates = %s", raw["candidateStates"])
	}
}

// No binding: --check answers per candidate. One candidate = a single subject
// (top-level keys); several = one row each, never a collapsed answer, and a
// DORMANT row is not shown as open.
func TestWhoamiCheckServerFallbackStatesPerCandidate(t *testing.T) {
	list := func(rows ...string) map[string]string {
		return map[string]string{
			"AuthContext":  whoamiAuthJSON,
			"TeamSessions": `{"data":{"sessions":[` + strings.Join(rows, ",") + `]}}`,
		}
	}
	t.Run("one candidate", func(t *testing.T) {
		teamGitDir(t)
		fk := newStateFake(t, "", list(whoamiSession("s-a", "wkr1", "u-me", "Jonas", false)),
			func(string) string { return whoamiStateJSON("LAPSED", false, future815) })
		out, err := runState815(t, fk, "team", "session", "whoami", "--check")
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(out, "server: LAPSED at "+future815) {
			t.Errorf("single candidate lacks its state line:\n%s", out)
		}
		jout, _ := runState815(t, fk, "team", "session", "whoami", "--check", "--json")
		raw := rawJSON815(t, jout)
		if string(raw["state"]) != `"lapsed"` || string(raw["active"]) != "true" {
			t.Errorf("single-candidate JSON: %s", jout)
		}
	})
	t.Run("several candidates", func(t *testing.T) {
		teamGitDir(t)
		answers := map[string]string{
			"s-a": whoamiStateJSON("LIVE", true, future815),
			"s-b": whoamiStateJSON("DORMANT", false, past815),
			"s-c": whoamiStateJSON("LAPSED", false, past815),
		}
		fk := newStateFake(t, "", list(
			whoamiSession("s-a", "wkr1", "u-me", "Jonas", false),
			whoamiSession("s-b", "wkr2", "u-me", "Vera", false),
			whoamiSession("s-c", "wkr3", "u-me", "Iris", false)),
			func(id string) string { return answers[id] })
		out, err := runState815(t, fk, "team", "session", "whoami", "--check")
		if err != nil {
			t.Fatal(err)
		}
		for _, want := range []string{"STATE", "live (lapses " + future815 + ")", "DORMANT", "LAPSED " + past815} {
			if !strings.Contains(out, want) {
				t.Errorf("table lacks %q:\n%s", want, out)
			}
		}
		if fk.count("GetBoundSessionState") != 3 {
			t.Errorf("one state read per candidate, got %d", fk.count("GetBoundSessionState"))
		}
		jout, _ := runState815(t, fk, "team", "session", "whoami", "--check", "--json")
		raw := rawJSON815(t, jout)
		if string(raw["state"]) != "null" || string(raw["active"]) != "null" {
			t.Errorf("several candidates have no single answer: %s", jout)
		}
		var rows []struct {
			SessionID string `json:"sessionId"`
			State     string `json:"state"`
		}
		if err := json.Unmarshal(raw["candidateStates"], &rows); err != nil || len(rows) != 3 ||
			rows[0].State != "live" || rows[1].State != "dormant" || rows[2].State != "lapsed" ||
			rows[1].SessionID != "s-b" {
			t.Errorf("candidateStates = %s (%v)", raw["candidateStates"], err)
		}
	})
	t.Run("no --check makes no state read", func(t *testing.T) {
		teamGitDir(t)
		fk := newStateFake(t, "", list(whoamiSession("s-a", "wkr1", "u-me", "Jonas", false)),
			func(string) string { return whoamiStateJSON("LIVE", true, "") })
		if _, err := runState815(t, fk, "team", "session", "whoami", "--json"); err != nil {
			t.Fatal(err)
		}
		if fk.count("GetBoundSessionState") != 0 {
			t.Errorf("the state read belongs to --check only: %v", fk.ops)
		}
	})
}
