package cmd

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// hadron-cli#198 Slice B: `email test` and `email disconnect`. Fake HTTP only.
// Constraints from the gate and the FIT: one request each and never a retry; the
// server's single Forbidden is shown as it is (never "not found" or "already
// disconnected"); no reconnect advice from provider prose; impersonation and the
// admin/owner rules are the server's, with no client pre-check; disconnect asks
// first and, without a terminal, needs --yes with zero requests otherwise.

const (
	testOK198   = `{"data":{"testExchangeConnection":true}}`
	testFail198 = `{"data":{"testExchangeConnection":false}}`
	discOK198   = `{"data":{"deleteExchangeConnection":true}}`
	discNo198   = `{"data":{"deleteExchangeConnection":false}}`
	forbid198   = `{"errors":[{"message":"Forbidden","extensions":{"code":"FORBIDDEN"}}]}`
	unknown198  = `{"errors":[{"message":"reconnect your account now","extensions":{"code":"EMAIL_TOOL_ERROR","emailErrorCode":"unknown"}}]}`
)

func TestEmailTest198(t *testing.T) {
	out, ops, err := runEmail198(t, map[string]string{"EmailTestConnection": testOK198}, "test", " conn-1 ", "--json")
	if err != nil {
		t.Fatal(err)
	}
	if requests198.Load() != 1 || vars198(t, ops, "EmailTestConnection")["id"] != "conn-1" {
		t.Fatalf("want one request with id conn-1, got %d: %v", requests198.Load(), ops)
	}
	if !strings.Contains(out, `"connectionId": "conn-1"`) || !strings.Contains(out, `"ok": true`) {
		t.Errorf("DTO = %s", out)
	}
	text, _, err := runEmail198(t, map[string]string{"EmailTestConnection": testOK198}, "test", "conn-1")
	if err != nil || !strings.Contains(text, "conn-1") {
		t.Errorf("text = %q, %v", text, err)
	}
}

// A false answer is the server saying the probe failed: reported as such with a
// nonzero exit, never as success, and with no reconnect advice of the CLI's own.
func TestEmailTestFalseIsAFailedProbe198(t *testing.T) {
	out, _, err := runEmail198(t, map[string]string{"EmailTestConnection": testFail198}, "test", "conn-1", "--json")
	if exitCodeFor(err) != 1 || requests198.Load() != 1 {
		t.Fatalf("a failed probe: exit %d (%v) with %d requests; want exit 1 and one request: %s", exitCodeFor(err), err, requests198.Load(), out)
	}
	if !strings.Contains(out, `"ok": false`) {
		t.Errorf("the failed probe is not in the output: %s", out)
	}
	if strings.Contains(strings.ToLower(out+err.Error()), "reconnect") {
		t.Errorf("the CLI invented a reconnect remedy: %s %v", out, err)
	}
}

func TestEmailManageErrors198(t *testing.T) {
	for _, tc := range []struct {
		cmd, op string
		args    []string
	}{
		{"test", "EmailTestConnection", []string{"test", "conn-1"}},
		{"disconnect", "EmailDisconnect", []string{"disconnect", "conn-1", "--yes"}},
	} {
		cases := []struct {
			name, wire string
			want       int
		}{
			{"forbidden passes through", forbid198, 8},
			{"unknown tool error keeps the fallback", unknown198, 1},
		}
		// The server types both answers Boolean!, so an absent or null one cannot be
		// told from false after decoding: for disconnect false IS "not confirmed"
		// (exit 7); for test it is a probe that did not pass (covered above).
		if tc.cmd == "disconnect" {
			cases = append(cases, struct {
				name, wire string
				want       int
			}{"absent result is unconfirmed", `{"data":{}}`, 7})
		}
		for _, e := range cases {
			t.Run(tc.cmd+" "+e.name, func(t *testing.T) {
				_, _, err := runEmail198(t, map[string]string{tc.op: e.wire}, tc.args...)
				if exitCodeFor(err) != e.want {
					t.Fatalf("exit %d (%v), want %d", exitCodeFor(err), err, e.want)
				}
				if requests198.Load() != 1 {
					t.Errorf("%d requests: a failed call is never retried", requests198.Load())
				}
				if err != nil {
					low := strings.ToLower(err.Error())
					// What the SERVER said passes through verbatim (the unknown-tool
					// fixture itself says "reconnect"); the CLI adds no wording of its own.
					for _, bad := range []string{"not found", "already disconnected", "hadron connection connect"} {
						if strings.Contains(low, bad) {
							t.Errorf("the error carries CLI-invented wording %q: %v", bad, err)
						}
					}
				}
			})
		}
	}
}

func TestEmailManageNeedsAConnectionID198(t *testing.T) {
	for _, cmd := range []string{"test", "disconnect"} {
		for _, args := range [][]string{{}, {""}, {"  "}, {"a", "b"}} {
			full := append([]string{cmd}, args...)
			if cmd == "disconnect" {
				full = append(full, "--yes")
			}
			_, _, err := runEmail198(t, map[string]string{}, full...)
			if exitCodeFor(err) != 2 || requests198.Load() != 0 {
				t.Errorf("email %v: exit %d (%v) with %d requests; want a usage error and none sent", full, exitCodeFor(err), err, requests198.Load())
			}
		}
	}
}

// No effect without confirmation: off a terminal, no --yes is a usage error with
// ZERO requests; with --yes exactly one request is made and the DTO says what the
// server confirmed.
func TestEmailDisconnectConfirmation198(t *testing.T) {
	_, _, err := runEmail198(t, map[string]string{"EmailDisconnect": discOK198}, "disconnect", "conn-1")
	if exitCodeFor(err) != 2 || requests198.Load() != 0 {
		t.Fatalf("without --yes: exit %d (%v) with %d requests; want exit 2 and none", exitCodeFor(err), err, requests198.Load())
	}
	out, ops, err := runEmail198(t, map[string]string{"EmailDisconnect": discOK198}, "disconnect", " conn-1 ", "--yes", "--json")
	if err != nil {
		t.Fatal(err)
	}
	if requests198.Load() != 1 || vars198(t, ops, "EmailDisconnect")["id"] != "conn-1" {
		t.Fatalf("want one request with id conn-1, got %d: %v", requests198.Load(), ops)
	}
	if !strings.Contains(out, `"connectionId": "conn-1"`) || !strings.Contains(out, `"disconnected": true`) {
		t.Errorf("DTO = %s", out)
	}
	text, _, err := runEmail198(t, map[string]string{"EmailDisconnect": discOK198}, "disconnect", "conn-1", "--yes")
	if err != nil || !strings.Contains(text, "conn-1") {
		t.Errorf("text = %q, %v", text, err)
	}
	// "Disconnected" means Hadron's record; the provider-side teardown is
	// best-effort, and the CLI says no more than that.
	if strings.Contains(strings.ToLower(text), "revoked") {
		t.Errorf("the CLI claims a provider-side revocation it cannot know: %s", text)
	}
}

// A false answer is NOT confirmed: the CLI does not claim success, and does not
// say "not found" or "already disconnected" either — the server did not say that.
func TestEmailDisconnectNotConfirmed198(t *testing.T) {
	out, _, err := runEmail198(t, map[string]string{"EmailDisconnect": discNo198}, "disconnect", "conn-1", "--yes")
	if exitCodeFor(err) != 7 {
		t.Fatalf("exit %d (%v), want 7 (unavailable)", exitCodeFor(err), err)
	}
	low := strings.ToLower(out + err.Error())
	for _, bad := range []string{"not found", "already disconnected", "disconnected conn"} {
		if strings.Contains(low, bad) {
			t.Errorf("an unconfirmed delete says %q: %s %v", bad, out, err)
		}
	}
}

// runEmailTTY198 runs a command on an interactive terminal that answers with the
// given lines; it returns stderr (where the prompt is drawn), the HTTP
// request count, the captured variables and the error.
func runEmailTTY198(t *testing.T, responses map[string]string, answers string, args ...string) (stderr string, n int, ops map[string]json.RawMessage, err error) {
	t.Helper()
	requests198.Store(0)
	srv, ops := captureGraphQL(t, responses)
	counter := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			requests198.Add(1)
		}
		proxyTo198(t, w, r, srv.URL)
	}))
	t.Cleanup(counter.Close)
	f, _, errOut := testFactoryTTY(t, answers)
	root := NewRootCmd(f)
	root.SetArgs(append(append([]string{"email"}, args...), "--server", counter.URL))
	err = root.Execute()
	return errOut.String(), int(requests198.Load()), ops, err
}

// The #2735 blind and local P2: a missing or null answer to the probe is NOT an
// explicit false. Only a real false is "the probe did not pass" (exit 1, ok:false);
// an absent or null result is unavailable (exit 7) and prints no definitive DTO.
func TestEmailTestMissingResultIsNotAFailedProbe198(t *testing.T) {
	for _, wire := range []string{`{"data":{}}`, `{"data":{"testExchangeConnection":null}}`} {
		t.Run(wire, func(t *testing.T) {
			out, _, err := runEmail198(t, map[string]string{"EmailTestConnection": wire}, "test", "conn-1", "--json")
			if exitCodeFor(err) != 7 {
				t.Fatalf("exit %d (%v), want 7 (unavailable)", exitCodeFor(err), err)
			}
			if strings.Contains(out, `"ok"`) {
				t.Errorf("a missing result printed a definitive probe DTO: %s", out)
			}
			if requests198.Load() != 1 {
				t.Errorf("%d requests", requests198.Load())
			}
		})
	}
	out, _, err := runEmail198(t, map[string]string{"EmailTestConnection": testFail198}, "test", "conn-1", "--json")
	if exitCodeFor(err) != 1 || requests198.Load() != 1 || !strings.Contains(out, `"ok": false`) {
		t.Errorf("an explicit false: exit %d (%v), %d requests, out %s; want exit 1, one request, ok:false", exitCodeFor(err), err, requests198.Load(), out)
	}
}

// The #2735 local P2 (§10 delete boundary): the id shown in the destructive
// prompt is made visible, never raw, while the id SENT stays exactly as given.
// And the interactive safeguard itself is pinned: y sends one request; n and a
// bare Enter send none.
func TestEmailDisconnectPromptIsSafeAndAnswered198(t *testing.T) {
	evil := "conn\x1b[2J\nforged: Delete everything? (y/N)"
	stderr, n, _, err := runEmailTTY198(t, map[string]string{"EmailDisconnect": discOK198}, "n\n", "disconnect", evil)
	if err == nil || n != 0 {
		t.Fatalf("declining must send nothing: err %v, %d requests", err, n)
	}
	if strings.ContainsRune(stderr, '\x1b') || strings.Contains(stderr, "\nforged:") {
		t.Errorf("a control-bearing id reached the terminal raw:\n%q", stderr)
	}
	_, n, ops, err := runEmailTTY198(t, map[string]string{"EmailDisconnect": discOK198}, "y\n", "disconnect", evil)
	if err != nil || n != 1 {
		t.Fatalf("accepting must send exactly one request: err %v, %d requests", err, n)
	}
	if got := vars198(t, ops, "EmailDisconnect")["id"]; got != evil {
		t.Errorf("the id sent was changed: %q", got)
	}
	for _, ans := range []string{"\n", "no\n", ""} {
		if _, n, _, err := runEmailTTY198(t, map[string]string{"EmailDisconnect": discOK198}, ans, "disconnect", "conn-1"); err == nil || n != 0 {
			t.Errorf("answer %q: err %v, %d requests; anything but y/yes sends nothing", ans, err, n)
		}
	}
}

// The group help describes what the group now does: it reads mail metadata AND
// probes (recording sync status) and disconnects (deleting a connection record).
func TestEmailGroupHelpDescribesTestAndDisconnect198(t *testing.T) {
	f, _ := testFactory(t)
	cmd, _, err := NewRootCmd(f).Find([]string{"email"})
	if err != nil {
		t.Fatal(err)
	}
	help := strings.Join(strings.Fields(cmd.Long), " ")
	if strings.Contains(help, "only read") || strings.Contains(help, "only lists") {
		t.Errorf("the group help still says it only reads: %q", help)
	}
	for _, want := range []string{"disconnect", "probe"} {
		if !strings.Contains(help, want) {
			t.Errorf("the group help does not mention %q: %q", want, help)
		}
	}
}
