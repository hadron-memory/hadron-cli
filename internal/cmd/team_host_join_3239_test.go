package cmd

// hadron-cli#3239 (slice C1 of hadron-server#5209): `team host join|leave|
// set|hosts|show` and the host credential store, against a fake server that
// records each request's operation, credential, worker session and
// variables. Every refusal the fake sends quotes the key, so a test that
// finds the key in any output proves an echo.

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/exitcode"
)

const (
	key3239       = "hk_live_S3CR3T3239keyvalue"
	otherKey3239  = "hk_live_OTHER3239keyvalue"
	personTok3239 = "hdr_user_test"
	app3239       = "capp100000000000000000000"
	hostRow3239   = `{"id":"ath1","hostId":"box1","label":"Box","pollIntervalSeconds":null,"deliveryEnabled":null,"createdAt":"2026-11-27T00:00:00Z","updatedAt":"2026-11-27T00:00:00Z"}`
)

type call3239 struct {
	op      string
	auth    string
	session string
	vars    map[string]any
}

type srv3239 struct {
	t     *testing.T
	url   string
	mu    sync.Mutex
	calls []call3239
	// respond answers one operation; the default answers below are used when
	// it returns "".
	respond func(op string, vars map[string]any) string
	// down makes every request but TeamAppIdentity fail with a 503.
	down bool
}

func refusal3239(code string) string {
	return `{"errors":[{"message":"refused for ` + key3239 + `","extensions":{"code":"` + code + `"}}],"data":null}`
}

func newSrv3239(t *testing.T, respond func(op string, vars map[string]any) string) *srv3239 {
	t.Helper()
	s := &srv3239{t: t, respond: respond}
	h := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var req struct {
			OperationName string         `json:"operationName"`
			Variables     map[string]any `json:"variables"`
		}
		_ = json.Unmarshal(body, &req)
		s.mu.Lock()
		s.calls = append(s.calls, call3239{op: req.OperationName, auth: r.Header.Get("Authorization"), session: r.Header.Get("X-Hadron-Session"), vars: req.Variables})
		s.mu.Unlock()
		if s.down && req.OperationName != "TeamAppIdentity" {
			http.Error(w, "unavailable "+key3239, http.StatusServiceUnavailable)
			return
		}
		out := ""
		if s.respond != nil {
			out = s.respond(req.OperationName, req.Variables)
		}
		if out == "" {
			out = default3239(req.OperationName)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, out)
	}))
	t.Cleanup(h.Close)
	s.url = h.URL
	return s
}

func default3239(op string) string {
	switch op {
	case "TeamAppIdentity":
		return `{"data":{"app":{"id":"` + app3239 + `","urn":"hrn:app:acme.com:eng","name":"Eng"}}}`
	case "JoinAgentTeamHost":
		return `{"data":{"joinAgentTeamHost":{"rawKey":"` + key3239 + `","host":` + hostRow3239 + `}}}`
	case "RemoveAgentTeamHost":
		return `{"data":{"removeAgentTeamHost":true}}`
	case "UpdateAgentTeamHost":
		return `{"data":{"updateAgentTeamHost":` + hostRow3239 + `}}`
	case "AgentTeamHosts":
		return `{"data":{"agentTeamHosts":[` + hostRow3239 + `]}}`
	case "AgentTeamHost":
		return `{"data":{"agentTeamHost":` + hostRow3239 + `}}`
	case "HostCredentialStatus":
		return `{"data":{"authenticationStatus":"AUTHENTICATED","myApp":{"id":"` + app3239 + `"}}}`
	}
	return `{"errors":[{"message":"unexpected operation"}],"data":null}`
}

func (s *srv3239) ops() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := []string{}
	for _, c := range s.calls {
		out = append(out, c.op)
	}
	return out
}

func (s *srv3239) count(op string) int {
	n := 0
	for _, o := range s.ops() {
		if o == op {
			n++
		}
	}
	return n
}

func (s *srv3239) last(op string) call3239 {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := len(s.calls) - 1; i >= 0; i-- {
		if s.calls[i].op == op {
			return s.calls[i]
		}
	}
	s.t.Fatalf("no %s call; calls: %v", op, s.calls)
	return call3239{}
}

// assertPersonOnly fails unless every request carried the person's token and
// no worker session, except the named dead-key check.
func (s *srv3239) assertPersonOnly(t *testing.T, except ...string) {
	t.Helper()
	s.mu.Lock()
	defer s.mu.Unlock()
	skip := map[string]bool{}
	for _, e := range except {
		skip[e] = true
	}
	for _, c := range s.calls {
		if c.session != "" {
			t.Errorf("%s carried a worker session %q", c.op, c.session)
		}
		if skip[c.op] {
			continue
		}
		if c.auth != "Bearer "+personTok3239 {
			t.Errorf("%s sent credential %q, want the person's", c.op, c.auth)
		}
	}
}

type run3239 struct {
	out, errOut string
	err         error
	code        int
}

// exec3239 runs the command against s with a fresh config and state dir. The
// store is whatever setup wrote into the config dir it is handed.
func exec3239(t *testing.T, s *srv3239, setup func(configDir string), args ...string) run3239 {
	t.Helper()
	teamGitDir(t)
	f, out := testFactory(t)
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	if setup != nil {
		setup(filepath.Join(os.Getenv("XDG_CONFIG_HOME"), "hadron"))
	}
	root := NewRootCmd(f)
	root.SetArgs(append([]string{"--server", s.url}, args...))
	err := root.Execute()
	r := run3239{err: err}
	if err != nil {
		r.code = renderFailure(f, args, err)
	}
	r.out = out.String()
	r.errOut = f.IOStreams.ErrOut.(*strings.Builder).String()
	return r
}

func (r run3239) assertNoKey(t *testing.T) {
	t.Helper()
	for _, k := range []string{key3239, otherKey3239} {
		for name, text := range map[string]string{"stdout": r.out, "stderr": r.errOut} {
			if strings.Contains(text, k) {
				t.Errorf("%s carries a key:\n%s", name, text)
			}
		}
		if r.err != nil && strings.Contains(r.err.Error(), k) {
			t.Errorf("the error carries a key: %v", r.err)
		}
	}
}

func storePath3239() string {
	return filepath.Join(os.Getenv("XDG_CONFIG_HOME"), "hadron", "host-credentials.json")
}

// writeStore3239 writes a store holding one entry per (server, app, host, key).
func writeStore3239(t *testing.T, dir string, mode os.FileMode, entries ...[4]string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	var es []map[string]string
	for _, e := range entries {
		es = append(es, map[string]string{"server": e[0], "appId": e[1], "hostId": e[2], "key": e[3]})
	}
	body, _ := json.Marshal(map[string]any{"version": 1, "entries": es})
	p := filepath.Join(dir, "host-credentials.json")
	if err := os.WriteFile(p, body, mode); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(p, mode); err != nil {
		t.Fatal(err)
	}
}

type storedEntry3239 struct {
	Server, AppID, HostID, Key string
}

func readStore3239(t *testing.T) []storedEntry3239 {
	t.Helper()
	b, err := os.ReadFile(storePath3239())
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Entries []storedEntry3239 `json:"entries"`
	}
	if err := json.Unmarshal(b, &doc); err != nil {
		t.Fatalf("store is not JSON: %v", err)
	}
	return doc.Entries
}

func jsonKeys3239(t *testing.T, s string) []string {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal([]byte(s), &m); err != nil {
		t.Fatalf("not a JSON object: %v\n%s", err, s)
	}
	keys := []string{}
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func TestHostJoin3239StoresTheKeyAndNeverPrintsIt(t *testing.T) {
	for _, asJSON := range []bool{true, false} {
		s := newSrv3239(t, nil)
		args := []string{"team", "host", "join", "--app", "acme.com:eng", "--label", "Box"}
		if asJSON {
			args = append(args, "--json")
		}
		r := exec3239(t, s, nil, args...)
		if r.err != nil {
			t.Fatalf("join: %v\n%s", r.err, r.errOut)
		}
		r.assertNoKey(t)
		s.assertPersonOnly(t)
		got := readStore3239(t)
		if len(got) != 1 || got[0].Key != key3239 || got[0].HostID != "box1" || got[0].AppID != app3239 || got[0].Server != s.url {
			t.Fatalf("stored %+v", got)
		}
		if runtime.GOOS != "windows" {
			fi, _ := os.Stat(storePath3239())
			if fi.Mode().Perm() != 0o600 {
				t.Errorf("store mode %o, want 600", fi.Mode().Perm())
			}
			di, _ := os.Stat(filepath.Dir(storePath3239()))
			if di.Mode().Perm() != 0o700 {
				t.Errorf("store dir mode %o, want 700", di.Mode().Perm())
			}
		}
		join := s.last("JoinAgentTeamHost")
		if join.vars["label"] != "Box" || join.vars["appRef"] != "hrn:app:acme.com:eng" {
			t.Errorf("join variables %v", join.vars)
		}
		for _, k := range []string{"hostId", "replace"} {
			if _, ok := join.vars[k]; ok {
				t.Errorf("join sent %s although no flag set it: %v", k, join.vars)
			}
		}
		if asJSON {
			want := []string{"appId", "hostId", "label", "next", "replaced", "server"}
			if got := jsonKeys3239(t, r.out); strings.Join(got, ",") != strings.Join(want, ",") {
				t.Errorf("--json keys %v, want %v", got, want)
			}
		} else if !strings.Contains(r.out, "box1") || !strings.Contains(r.out, "hadron team host show box1") {
			t.Errorf("human output lacks the host id or the next commands:\n%s", r.out)
		}
	}
}

func TestHostJoin3239SecondJoinExits5BeforeAnyJoinCall(t *testing.T) {
	s := newSrv3239(t, nil)
	var before []byte
	r := exec3239(t, s, func(dir string) {
		writeStore3239(t, dir, 0o600, [4]string{s.url, app3239, "box1", key3239})
		before, _ = os.ReadFile(filepath.Join(dir, "host-credentials.json"))
	}, "team", "host", "join", "--app", "acme.com:eng", "--json")
	if r.code != exitcode.Conflict {
		t.Fatalf("exit %d (%v), want 5", r.code, r.err)
	}
	if n := s.count("JoinAgentTeamHost"); n != 0 {
		t.Errorf("join was sent %d times", n)
	}
	after, _ := os.ReadFile(storePath3239())
	if !bytes.Equal(before, after) {
		t.Errorf("the stored bytes changed")
	}
	r.assertNoKey(t)
}

func TestHostJoin3239ReplaceRekeysTheHeldHostOnly(t *testing.T) {
	s := newSrv3239(t, nil)
	r := exec3239(t, s, func(dir string) {
		writeStore3239(t, dir, 0o600, [4]string{s.url, app3239, "box1", otherKey3239})
	}, "team", "host", "join", "--app", "acme.com:eng", "--replace", "--host-id", "box2")
	if r.code != exitcode.Conflict || s.count("JoinAgentTeamHost") != 0 {
		t.Fatalf("--replace with another host: exit %d, %d join calls; want 5 and none", r.code, s.count("JoinAgentTeamHost"))
	}

	s = newSrv3239(t, nil)
	r = exec3239(t, s, func(dir string) {
		writeStore3239(t, dir, 0o600, [4]string{s.url, app3239, "box1", otherKey3239})
	}, "team", "host", "join", "--app", "acme.com:eng", "--replace", "--json")
	if r.err != nil {
		t.Fatalf("replace: %v", r.err)
	}
	join := s.last("JoinAgentTeamHost")
	if join.vars["replace"] != true || join.vars["hostId"] != "box1" {
		t.Errorf("replace variables %v, want replace true for the held host", join.vars)
	}
	if got := readStore3239(t); len(got) != 1 || got[0].Key != key3239 {
		t.Errorf("stored %+v, want the new key only", got)
	}
	r.assertNoKey(t)
}

func TestHostJoin3239RefusalsMapAndStoreNothing(t *testing.T) {
	cases := map[string]int{
		"FORBIDDEN":                     exitcode.Forbidden,
		"AGENT_TEAM_HOST_NOT_PERMITTED": exitcode.Forbidden,
		"AGENT_TEAM_HOST_EXISTS":        exitcode.Conflict,
		"BAD_AGENT_TEAM_HOST_ARGS":      exitcode.Usage,
		"UNAUTHENTICATED":               exitcode.AuthRequired,
	}
	for code, want := range cases {
		t.Run(code, func(t *testing.T) {
			s := newSrv3239(t, func(op string, _ map[string]any) string {
				if op == "JoinAgentTeamHost" {
					return refusal3239(code)
				}
				return ""
			})
			r := exec3239(t, s, nil, "team", "host", "join", "--app", "acme.com:eng", "--json")
			if r.code != want {
				t.Fatalf("exit %d (%v), want %d", r.code, r.err, want)
			}
			if got := readStore3239(t); len(got) != 0 {
				t.Errorf("stored %+v after a refusal", got)
			}
			if s.count("JoinAgentTeamHost") != 1 {
				t.Errorf("join sent %d times", s.count("JoinAgentTeamHost"))
			}
			r.assertNoKey(t)
		})
	}
}

func TestHostJoin3239LostAnswerExits7AndIsNotRetried(t *testing.T) {
	s := newSrv3239(t, nil)
	s.down = true
	r := exec3239(t, s, nil, "team", "host", "join", "--app", "acme.com:eng")
	if r.code != exitcode.Unavailable {
		t.Fatalf("exit %d (%v), want 7", r.code, r.err)
	}
	if n := s.count("JoinAgentTeamHost"); n != 1 {
		t.Errorf("join sent %d times, want once", n)
	}
	if !strings.Contains(r.err.Error(), "join --replace") {
		t.Errorf("the lost answer names no recovery: %v", r.err)
	}
	r.assertNoKey(t)
}

func TestHostJoin3239RefusesBadFlagsAndNoSignInBeforeAnyCall(t *testing.T) {
	for name, args := range map[string][]string{
		"empty label":  {"--label", "  "},
		"long label":   {"--label", strings.Repeat("x", 129)},
		"bad host id":  {"--host-id", "../etc"},
		"empty hostid": {"--host-id", ""},
	} {
		t.Run(name, func(t *testing.T) {
			s := newSrv3239(t, nil)
			r := exec3239(t, s, nil, append([]string{"team", "host", "join", "--app", "acme.com:eng"}, args...)...)
			if r.code != exitcode.Usage || len(s.ops()) != 0 {
				t.Fatalf("exit %d, calls %v; want 2 and none", r.code, s.ops())
			}
		})
	}
	s := newSrv3239(t, nil)
	r := exec3239(t, s, func(string) { t.Setenv("HADRON_TOKEN", "") }, "team", "host", "join", "--app", "acme.com:eng")
	if r.code != exitcode.AuthRequired || len(s.ops()) != 0 {
		t.Fatalf("signed out: exit %d, calls %v; want 3 and none", r.code, s.ops())
	}
}

func TestHostJoin3239FromABoundWorktreeSendsNoSession(t *testing.T) {
	s := newSrv3239(t, nil)
	writeTeamBinding(t)
	setTeamBindingServer(t, s.url)
	f, out := testFactory(t)
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	root := NewRootCmd(f)
	root.SetArgs([]string{"--server", s.url, "team", "host", "join", "--json"})
	if err := root.Execute(); err != nil {
		t.Fatalf("join from a bound worktree: %v", err)
	}
	if s.count("JoinAgentTeamHost") != 1 {
		t.Fatalf("calls %v", s.ops())
	}
	s.assertPersonOnly(t)
	if strings.Contains(out.String(), key3239) {
		t.Errorf("stdout carries the key")
	}
}

func TestHostJoin3239RefusesAnOpenStore(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the mode checks are Unix-only")
	}
	for name, setup := range map[string]func(t *testing.T, dir, url string){
		"file 0644": func(t *testing.T, dir, url string) {
			writeStore3239(t, dir, 0o644, [4]string{url, "other-app", "box9", otherKey3239})
		},
		"dir 0755": func(t *testing.T, dir, url string) {
			writeStore3239(t, dir, 0o600, [4]string{url, "other-app", "box9", otherKey3239})
			if err := os.Chmod(dir, 0o755); err != nil {
				t.Fatal(err)
			}
		},
		"symlink": func(t *testing.T, dir, url string) {
			real := filepath.Join(t.TempDir(), "real.json")
			if err := os.WriteFile(real, []byte(`{"version":1,"entries":[]}`), 0o600); err != nil {
				t.Fatal(err)
			}
			if err := os.MkdirAll(dir, 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(real, filepath.Join(dir, "host-credentials.json")); err != nil {
				t.Fatal(err)
			}
		},
	} {
		t.Run(name, func(t *testing.T) {
			s := newSrv3239(t, nil)
			r := exec3239(t, s, func(dir string) { setup(t, dir, s.url) }, "team", "host", "join", "--app", "acme.com:eng")
			if r.err == nil || s.count("JoinAgentTeamHost") != 0 {
				t.Fatalf("an open store was used: err %v, calls %v", r.err, s.ops())
			}
			r.assertNoKey(t)
		})
	}
}

func TestHostLeave3239RemovesOnlyItsOwnEntryAsThePerson(t *testing.T) {
	s := newSrv3239(t, nil)
	r := exec3239(t, s, func(dir string) {
		writeStore3239(t, dir, 0o600, [4]string{s.url, app3239, "box1", key3239}, [4]string{s.url, "capp-other", "box1", otherKey3239})
	}, "team", "host", "leave", "--app", "acme.com:eng", "--yes", "--json")
	if r.err != nil {
		t.Fatalf("leave: %v\n%s", r.err, r.errOut)
	}
	if got := readStore3239(t); len(got) != 1 || got[0].AppID != "capp-other" || got[0].Key != otherKey3239 {
		t.Fatalf("stored %+v, want only the other App's entry", got)
	}
	if rm := s.last("RemoveAgentTeamHost"); rm.vars["hostId"] != "box1" {
		t.Errorf("remove variables %v", rm.vars)
	}
	if _, ok := s.last("RemoveAgentTeamHost").vars["force"]; ok {
		t.Errorf("leave sent force")
	}
	s.assertPersonOnly(t)
	want := []string{"appId", "hostId", "removedLocally", "removedOnServer", "server"}
	if got := jsonKeys3239(t, r.out); strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("--json keys %v, want %v", got, want)
	}
	r.assertNoKey(t)
}

func TestHostLeave3239KeepsTheEntryWhenRefusedOrUnreachable(t *testing.T) {
	cases := []struct {
		name string
		code string // "" = the server is down
		want int
	}{
		{"forbidden", "FORBIDDEN", exitcode.Forbidden},
		{"open sessions", "AGENT_TEAM_HOST_HAS_OPEN_SESSIONS", exitcode.Conflict},
		{"app not found", "APP_NOT_FOUND", exitcode.NotFound},
		{"unreachable", "", exitcode.Unavailable},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			s := newSrv3239(t, func(op string, _ map[string]any) string {
				switch op {
				case "RemoveAgentTeamHost":
					return refusal3239(c.code)
				case "HostDesiredState":
					return `{"data":{"hostDesiredState":{"hostId":"box1","sweepMode":"ON","workers":[` +
						`{"workerId":"w1","name":"Iris","lifecycle":"ACTIVE","desired":"RUNNING","reason":"ASSIGNED","wouldBe":null,"wouldBeReason":null,"openSessionId":"s1"},` +
						`{"workerId":"w2","name":"Zed","lifecycle":"ACTIVE","desired":"RUNNING","reason":"ASSIGNED","wouldBe":null,"wouldBeReason":null,"openSessionId":null}]}}}`
				}
				return ""
			})
			s.down = c.code == ""
			var before []byte
			r := exec3239(t, s, func(dir string) {
				writeStore3239(t, dir, 0o600, [4]string{s.url, app3239, "box1", key3239})
				before, _ = os.ReadFile(filepath.Join(dir, "host-credentials.json"))
			}, "team", "host", "leave", "--app", "acme.com:eng", "--yes")
			if r.code != c.want {
				t.Fatalf("exit %d (%v), want %d", r.code, r.err, c.want)
			}
			after, _ := os.ReadFile(storePath3239())
			if !bytes.Equal(before, after) {
				t.Errorf("the stored entry changed after a refusal")
			}
			if c.code == "AGENT_TEAM_HOST_HAS_OPEN_SESSIONS" && (!strings.Contains(r.err.Error(), "Iris") || strings.Contains(r.err.Error(), "Zed")) {
				t.Errorf("the open-session refusal does not name exactly the open session's worker: %v", r.err)
			}
			if s.count("HostCredentialStatus") != 0 {
				t.Errorf("the stored key was presented on a refusal other than HOST_NOT_FOUND")
			}
			s.assertPersonOnly(t)
			r.assertNoKey(t)
		})
	}
}

func TestHostLeave3239DeletesAGoneHostOnlyWhenItsKeyIsRefused(t *testing.T) {
	for _, status := range []string{"REFUSED", "AUTHENTICATED"} {
		t.Run(status, func(t *testing.T) {
			s := newSrv3239(t, func(op string, _ map[string]any) string {
				switch op {
				case "RemoveAgentTeamHost":
					return refusal3239("HOST_NOT_FOUND")
				case "HostCredentialStatus":
					if status == "REFUSED" {
						return `{"data":{"authenticationStatus":"REFUSED","myApp":null}}`
					}
				}
				return ""
			})
			r := exec3239(t, s, func(dir string) {
				writeStore3239(t, dir, 0o600, [4]string{s.url, app3239, "box1", key3239})
			}, "team", "host", "leave", "--app", "acme.com:eng", "--yes", "--json")
			got := readStore3239(t)
			if status == "REFUSED" {
				if r.err != nil || len(got) != 0 {
					t.Fatalf("a revoked key's entry: err %v, stored %+v; want exit 0 and deleted", r.err, got)
				}
			} else if r.code != exitcode.NotFound || len(got) != 1 {
				t.Fatalf("a live key's entry: exit %d, stored %+v; want 4 and kept", r.code, got)
			}
			if st := s.last("HostCredentialStatus"); st.auth != "Bearer "+key3239 {
				t.Errorf("the dead-key check sent %q, want the stored key", st.auth)
			}
			s.assertPersonOnly(t, "HostCredentialStatus")
			r.assertNoKey(t)
		})
	}
}

func TestHostLeave3239NeedsYesAndAHeldHost(t *testing.T) {
	s := newSrv3239(t, nil)
	r := exec3239(t, s, func(dir string) {
		writeStore3239(t, dir, 0o600, [4]string{s.url, app3239, "box1", key3239})
	}, "team", "host", "leave", "--app", "acme.com:eng")
	if r.code != exitcode.Usage || s.count("RemoveAgentTeamHost") != 0 || len(readStore3239(t)) != 1 {
		t.Fatalf("no --yes off a terminal: exit %d, calls %v", r.code, s.ops())
	}
	s = newSrv3239(t, nil)
	r = exec3239(t, s, nil, "team", "host", "leave", "--app", "acme.com:eng", "--yes")
	if r.code != exitcode.NotFound || s.count("RemoveAgentTeamHost") != 0 {
		t.Fatalf("nothing held: exit %d, calls %v; want 4 and no remove", r.code, s.ops())
	}
}

func TestHostSet3239SendsOnlyTheFlagsGiven(t *testing.T) {
	cases := []struct {
		args []string
		want map[string]any // the input's keys beside appRef and hostId
	}{
		{[]string{"--poll-interval", "30"}, map[string]any{"pollIntervalSeconds": float64(30)}},
		{[]string{"--default-poll-interval"}, map[string]any{"pollIntervalSeconds": nil}},
		{[]string{"--no-delivery"}, map[string]any{"deliveryEnabled": false}},
		{[]string{"--delivery", "--label", "B2"}, map[string]any{"deliveryEnabled": true, "label": "B2"}},
		{[]string{"--default-delivery"}, map[string]any{"deliveryEnabled": nil}},
	}
	for _, c := range cases {
		t.Run(strings.Join(c.args, " "), func(t *testing.T) {
			s := newSrv3239(t, nil)
			r := exec3239(t, s, func(dir string) {
				writeStore3239(t, dir, 0o600, [4]string{s.url, app3239, "box1", key3239})
			}, append([]string{"team", "host", "set", "box1", "--app", "acme.com:eng", "--json"}, c.args...)...)
			if r.err != nil {
				t.Fatalf("set: %v", r.err)
			}
			input, _ := s.last("UpdateAgentTeamHost").vars["input"].(map[string]any)
			want := map[string]any{"appRef": "hrn:app:acme.com:eng", "hostId": "box1"}
			for k, v := range c.want {
				want[k] = v
			}
			gotJSON, _ := json.Marshal(input)
			wantJSON, _ := json.Marshal(want)
			if string(gotJSON) != string(wantJSON) {
				t.Errorf("input %s, want %s", gotJSON, wantJSON)
			}
			s.assertPersonOnly(t)
			r.assertNoKey(t)
		})
	}
}

func TestHostSet3239RefusesContradictionsBeforeAnyCall(t *testing.T) {
	for _, args := range [][]string{
		{"--poll-interval", "30", "--default-poll-interval"},
		{"--delivery", "--no-delivery"},
		{"--no-delivery", "--default-delivery"},
		{"--poll-interval", "0"},
		{"--poll-interval", "86401"},
		{"--label", ""},
		{},
	} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			s := newSrv3239(t, nil)
			r := exec3239(t, s, nil, append([]string{"team", "host", "set", "box1", "--app", "acme.com:eng"}, args...)...)
			if r.code != exitcode.Usage || len(s.ops()) != 0 {
				t.Fatalf("exit %d, calls %v; want 2 and none", r.code, s.ops())
			}
		})
	}
}

func TestHostHostsAndShow3239(t *testing.T) {
	s := newSrv3239(t, func(op string, _ map[string]any) string {
		if op == "AgentTeamHosts" {
			return `{"data":{"agentTeamHosts":[]}}`
		}
		return ""
	})
	r := exec3239(t, s, nil, "team", "host", "hosts", "--app", "acme.com:eng", "--json")
	if r.err != nil || strings.TrimSpace(r.out) != "[]" {
		t.Fatalf("empty hosts: %v %q", r.err, r.out)
	}
	s.assertPersonOnly(t)

	s = newSrv3239(t, nil)
	r = exec3239(t, s, nil, "team", "host", "show", "box1", "--app", "acme.com:eng", "--json")
	if r.err != nil {
		t.Fatalf("show: %v", r.err)
	}
	want := []string{"createdAt", "deliveryEnabled", "hostId", "id", "label", "pollIntervalSeconds", "updatedAt"}
	if got := jsonKeys3239(t, r.out); strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("show --json keys %v, want %v", got, want)
	}
	s.assertPersonOnly(t)

	s = newSrv3239(t, func(op string, _ map[string]any) string {
		if op == "AgentTeamHost" {
			return refusal3239("HOST_NOT_FOUND")
		}
		return ""
	})
	r = exec3239(t, s, nil, "team", "host", "show", "box9", "--app", "acme.com:eng")
	if r.code != exitcode.NotFound {
		t.Fatalf("show of an unknown host: exit %d, want 4", r.code)
	}
	r.assertNoKey(t)
}
