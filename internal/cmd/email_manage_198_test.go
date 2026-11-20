package cmd

import (
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
	if requests198 != 1 || vars198(t, ops, "EmailTestConnection")["id"] != "conn-1" {
		t.Fatalf("want one request with id conn-1, got %d: %v", requests198, ops)
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
	if err == nil {
		t.Fatalf("a failed probe exited 0: %s", out)
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
		for _, e := range []struct {
			name, wire string
			want       int
		}{
			{"forbidden passes through", forbid198, 8},
			{"unknown tool error keeps the fallback", unknown198, 1},
			{"null result", `{"data":{}}`, 7},
		} {
			t.Run(tc.cmd+" "+e.name, func(t *testing.T) {
				_, _, err := runEmail198(t, map[string]string{tc.op: e.wire}, tc.args...)
				if exitCodeFor(err) != e.want {
					t.Fatalf("exit %d (%v), want %d", exitCodeFor(err), err, e.want)
				}
				if requests198 != 1 {
					t.Errorf("%d requests: a failed call is never retried", requests198)
				}
				if err != nil {
					low := strings.ToLower(err.Error())
					for _, bad := range []string{"not found", "already", "reconnect"} {
						if strings.Contains(low, bad) {
							t.Errorf("the error says %q, which the server did not: %v", bad, err)
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
			if exitCodeFor(err) != 2 || requests198 != 0 {
				t.Errorf("email %v: exit %d (%v) with %d requests; want a usage error and none sent", full, exitCodeFor(err), err, requests198)
			}
		}
	}
}

// No effect without confirmation: off a terminal, no --yes is a usage error with
// ZERO requests; with --yes exactly one request is made and the DTO says what the
// server confirmed.
func TestEmailDisconnectConfirmation198(t *testing.T) {
	_, _, err := runEmail198(t, map[string]string{"EmailDisconnect": discOK198}, "disconnect", "conn-1")
	if exitCodeFor(err) != 2 || requests198 != 0 {
		t.Fatalf("without --yes: exit %d (%v) with %d requests; want exit 2 and none", exitCodeFor(err), err, requests198)
	}
	out, ops, err := runEmail198(t, map[string]string{"EmailDisconnect": discOK198}, "disconnect", " conn-1 ", "--yes", "--json")
	if err != nil {
		t.Fatal(err)
	}
	if requests198 != 1 || vars198(t, ops, "EmailDisconnect")["id"] != "conn-1" {
		t.Fatalf("want one request with id conn-1, got %d: %v", requests198, ops)
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
