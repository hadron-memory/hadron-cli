package cmd

// hadron-cli#3288: the host credential store follow-ups deferred from #3283 (C1). Same fake server as #3239:
// every refusal it sends quotes the key, so a key in any output proves an echo.

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/exitcode"
)

// F3: `replaced` means this machine held an entry for the host whose key the new one replaced. With nothing held,
// --replace still re-keys on the server, but nothing here was replaced.
func TestHostJoin3288ReplacedNamesTheHeldEntry(t *testing.T) {
	s := newSrv3239(t, nil)
	r := exec3239(t, s, nil, "team", "host", "join", "--app", "acme.com:eng", "--host-id", "box1", "--replace", "--json")
	if r.err != nil {
		t.Fatalf("join --replace with nothing held: %v", r.err)
	}
	var d struct {
		Replaced bool `json:"replaced"`
	}
	if err := json.Unmarshal([]byte(r.out), &d); err != nil {
		t.Fatalf("not JSON: %v\n%s", err, r.out)
	}
	if d.Replaced {
		t.Errorf("nothing was held here, so nothing was replaced: replaced=true\n%s", r.out)
	}

	s = newSrv3239(t, nil)
	r = exec3239(t, s, nil, "team", "host", "join", "--app", "acme.com:eng", "--host-id", "box1", "--replace")
	if r.err != nil {
		t.Fatalf("join --replace (text): %v", r.err)
	}
	if !strings.Contains(r.out, "re-keyed") || strings.Contains(r.out, "replaced the key stored") {
		t.Errorf("the text says the server re-keyed, and not that a stored key was replaced:\n%s", r.out)
	}

	s = newSrv3239(t, nil)
	r = exec3239(t, s, func(dir string) {
		writeStore3239(t, dir, 0o600, [4]string{s.url, app3239, "box1", otherKey3239})
	}, "team", "host", "join", "--app", "acme.com:eng", "--replace", "--json")
	if r.err != nil {
		t.Fatalf("join --replace with a held entry: %v", r.err)
	}
	if err := json.Unmarshal([]byte(r.out), &d); err != nil || !d.Replaced {
		t.Errorf("the held entry's key was replaced: replaced=%v (%v)\n%s", d.Replaced, err, r.out)
	}
	r.assertNoKey(t)
}

// F9: the App lookup never carries a host key, and a failing TeamAppIdentity echoes nothing of the store's key,
// even when the server's refusal quotes it.
func TestHostJoinLeave3288AppLookupFailureEchoesNoKey(t *testing.T) {
	for _, code := range []string{"INTERNAL_SERVER_ERROR", "FORBIDDEN", "NOT_FOUND"} {
		for _, cmd := range [][]string{
			{"team", "host", "join", "--app", "acme.com:eng"},
			{"team", "host", "leave", "--app", "acme.com:eng", "--yes"},
		} {
			t.Run(code+" "+cmd[2], func(t *testing.T) {
				s := newSrv3239(t, func(op string, _ map[string]any) string {
					if op == "TeamAppIdentity" {
						return refusal3239(code)
					}
					return ""
				})
				r := exec3239(t, s, func(dir string) {
					writeStore3239(t, dir, 0o600, [4]string{s.url, app3239, "box1", key3239})
				}, cmd...)
				if r.err == nil {
					t.Fatalf("a failed App lookup fails the command")
				}
				r.assertNoKey(t)
				if s.count("JoinAgentTeamHost")+s.count("RemoveAgentTeamHost") != 0 {
					t.Errorf("nothing is sent after a failed App lookup: %v", s.ops())
				}
				s.assertPersonOnly(t)
			})
		}
	}
}

// F10: leave's dead-key probe presents the stored key only over the scheme the session uses, and never over
// plaintext to a host that is not this machine's own.
func TestHostLeave3288RefusesTheDeadKeyProbeOnASchemeChange(t *testing.T) {
	s := newSrv3239(t, func(op string, _ map[string]any) string {
		if op == "RemoveAgentTeamHost" {
			return refusal3239("HOST_NOT_FOUND")
		}
		return ""
	})
	// The session is http://127.0.0.1:<port>; the entry was stored on https to the same host.
	stored := strings.Replace(s.url, "http://", "https://", 1)
	r := exec3239(t, s, func(dir string) {
		writeStore3239(t, dir, 0o600, [4]string{stored, app3239, "box1", key3239})
	}, "team", "host", "leave", "--app", "acme.com:eng", "--yes")
	if r.code != exitcode.NotFound || len(readStore3239(t)) != 1 {
		t.Fatalf("the entry is kept: exit %d, %d entries", r.code, len(readStore3239(t)))
	}
	if !strings.Contains(r.err.Error(), "scheme") {
		t.Errorf("the refusal names the scheme change: %v", r.err)
	}
	if s.count("HostCredentialStatus") != 0 {
		t.Errorf("the stored key is not presented: %v", s.ops())
	}
	r.assertNoKey(t)
}

// F14: --instruction is the only path that carries the worker session; without it, set is the person's alone.
func TestHostSet3288InstructionCarriesTheSessionOnThatPathOnly(t *testing.T) {
	s := newSrv3239(t, func(op string, _ map[string]any) string {
		if op == "UpdateAgentTeamHostInstructed" {
			return default3239("UpdateAgentTeamHost")
		}
		return ""
	})
	writeTeamBinding(t)
	setTeamBindingServer(t, s.url)
	f, _ := testFactory(t)
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	root := NewRootCmd(f)
	root.SetArgs([]string{"--server", s.url, "team", "host", "set", "box1", "--app", "acme.com:eng", "--label", "B2",
		"--instruction", "task:hrn:task:acme.com:eng:t1", "--json"})
	if err := root.Execute(); err != nil {
		t.Fatalf("set --instruction: %v (calls %v)", err, s.ops())
	}
	if s.count("UpdateAgentTeamHostInstructed") != 1 || s.count("UpdateAgentTeamHost") != 0 {
		t.Fatalf("the instructed document is sent, once: %v", s.ops())
	}
	s.mu.Lock()
	for _, c := range s.calls {
		if c.op == "UpdateAgentTeamHostInstructed" && c.session == "" {
			t.Errorf("the instructed update carries the worker session")
		}
		if c.op != "UpdateAgentTeamHostInstructed" && c.session != "" {
			t.Errorf("%s carries the worker session %q; only the instructed update may", c.op, c.session)
		}
	}
	s.mu.Unlock()

	s = newSrv3239(t, nil)
	setTeamBindingServer(t, s.url)
	f, _ = testFactory(t)
	root = NewRootCmd(f)
	root.SetArgs([]string{"--server", s.url, "team", "host", "set", "box1", "--app", "acme.com:eng", "--label", "B2", "--json"})
	if err := root.Execute(); err != nil {
		t.Fatalf("set without --instruction: %v", err)
	}
	s.assertPersonOnly(t)
}

// F14: `team host hosts` with rows, in both renderings, and set's human output.
func TestHostHostsAndSet3288Rows(t *testing.T) {
	two := `{"data":{"agentTeamHosts":[` + hostRow3239 + `,{"id":"ath2","hostId":"box2","label":"Box two","pollIntervalSeconds":60,"deliveryEnabled":false,"createdAt":"2026-11-27T00:00:00Z","updatedAt":"2026-11-27T01:00:00Z"}]}}`
	s := newSrv3239(t, func(op string, _ map[string]any) string {
		if op == "AgentTeamHosts" {
			return two
		}
		return ""
	})
	r := exec3239(t, s, nil, "team", "host", "hosts", "--app", "acme.com:eng", "--json")
	var rows []map[string]any
	if r.err != nil || json.Unmarshal([]byte(r.out), &rows) != nil || len(rows) != 2 {
		t.Fatalf("hosts --json: %v\n%s", r.err, r.out)
	}
	if rows[1]["hostId"] != "box2" || rows[1]["pollIntervalSeconds"] != float64(60) || rows[1]["deliveryEnabled"] != false {
		t.Errorf("the second row is the platform's: %v", rows[1])
	}
	if rows[0]["pollIntervalSeconds"] != nil || rows[0]["deliveryEnabled"] != nil {
		t.Errorf("a default stays null: %v", rows[0])
	}
	r = exec3239(t, s, nil, "team", "host", "hosts", "--app", "acme.com:eng")
	for _, want := range []string{"box1", "box2", "Box two"} {
		if r.err != nil || !strings.Contains(r.out, want) {
			t.Errorf("hosts (text) names %q: %v\n%s", want, r.err, r.out)
		}
	}

	s = newSrv3239(t, nil)
	r = exec3239(t, s, nil, "team", "host", "set", "box1", "--app", "acme.com:eng", "--label", "Box")
	if r.err != nil || !strings.Contains(r.out, "box1") || !strings.Contains(r.out, "Box") {
		t.Errorf("set (text) shows the host it changed: %v\n%s", r.err, r.out)
	}
}

// F15: a store larger than 1 MiB is refused by its path and a fixed reason, before any request that needs it.
func TestHostJoin3288RefusesAnOversizedStore(t *testing.T) {
	s := newSrv3239(t, nil)
	r := exec3239(t, s, func(dir string) {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
		body := `{"version":1,"entries":[],"pad":"` + strings.Repeat("x", 1<<20) + `"}`
		if err := os.WriteFile(filepath.Join(dir, "host-credentials.json"), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}, "team", "host", "join", "--app", "acme.com:eng")
	if r.err == nil || !strings.Contains(r.err.Error(), "too large") || s.count("JoinAgentTeamHost") != 0 {
		t.Fatalf("an oversized store is refused before the join: %v, calls %v", r.err, s.ops())
	}
}

// F15: a Put that fails after the mint for a reason other than ErrBadEntry (an unwritable directory) names the
// rejoin; an unstorable key (as opposed to an unstorable host id) is ErrBadEntry's text.
func TestHostJoin3288StoreFailuresAfterTheMint(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("directory modes do not make a directory unwritable on Windows")
	}
	if os.Geteuid() == 0 {
		t.Skip("root writes an unwritable directory")
	}
	s := newSrv3239(t, nil)
	r := exec3239(t, s, func(d string) {
		if err := os.MkdirAll(d, 0o700); err != nil {
			t.Fatal(err)
		}
		// The lock file exists, so the lock is taken; only the store's own write fails.
		if err := os.WriteFile(filepath.Join(d, "host-credentials.lock"), nil, 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(d, 0o500); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = os.Chmod(d, 0o700) })
	}, "team", "host", "join", "--app", "acme.com:eng", "--host-id", "box1")
	if r.code != exitcode.Error || !strings.Contains(r.err.Error(), "could not be stored") || !strings.Contains(r.err.Error(), "--replace") {
		t.Fatalf("an unwritable store after the mint: exit %d, %v", r.code, r.err)
	}
	r.assertNoKey(t)

	s = newSrv3239(t, func(op string, _ map[string]any) string {
		if op == "JoinAgentTeamHost" {
			return `{"data":{"joinAgentTeamHost":{"rawKey":"hk has a space","host":` + hostRow3239 + `}}}`
		}
		return ""
	})
	r = exec3239(t, s, nil, "team", "host", "join", "--app", "acme.com:eng", "--host-id", "box1")
	if r.code != exitcode.Error || !strings.Contains(r.err.Error(), "cannot store") || strings.Contains(r.err.Error(), "hk has a space") {
		t.Fatalf("an unstorable key: exit %d, %v", r.code, r.err)
	}
	if len(readStore3239(t)) != 0 {
		t.Errorf("an unstorable key stores nothing")
	}
}

// F15: an old server without joinAgentTeamHost is named as such, exit 1, and nothing is stored.
func TestHostJoin3288OldServerRefusal(t *testing.T) {
	s := newSrv3239(t, func(op string, _ map[string]any) string {
		if op == "JoinAgentTeamHost" {
			return `{"errors":[{"message":"Cannot query field \"joinAgentTeamHost\" on type \"Mutation\".","extensions":{"code":"GRAPHQL_VALIDATION_FAILED"}}],"data":null}`
		}
		return ""
	})
	r := exec3239(t, s, nil, "team", "host", "join", "--app", "acme.com:eng")
	if r.code != exitcode.Error || !strings.Contains(r.err.Error(), "0.47.0") {
		t.Fatalf("old server: exit %d, %v", r.code, r.err)
	}
	if len(readStore3239(t)) != 0 {
		t.Errorf("nothing is stored")
	}
}
