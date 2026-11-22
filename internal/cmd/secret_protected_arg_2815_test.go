package cmd

import (
	"strconv"
	"strings"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/exitcode"
)

// cli#2815: secret create/update reach createSecret/updateSecret's `protected`
// argument through a tri-state --protected flag (Petra's decision (a) on the
// issue): omitted sends NO field (omitempty — a plain member update is not
// turned manager-only), --protected sends true, --protected=false sends false.
// A protected create or a protection change on update is staged like every
// #2738 protected change: request id + ceremony URL byte for byte, exit 5,
// never a success line. One clear is no change at all: --protected=false on a
// secret that is not protected asks for nothing (hadron-server#4749) and the
// returned secret is the confirmed answer. Fake HTTP only.

// secretProtected2815 is the shared secret fixture carrying the protected
// field an updateSecret answer reports, for pins that assert on the
// reported protection state.
func secretProtected2815(protected bool) string {
	return strings.Replace(secretJSON, `"kind":"generic",`,
		`"kind":"generic","protected":`+strconv.FormatBool(protected)+`,`, 1)
}

// callVarsOf2815 returns the recorded variables of the first call to op.
func callVarsOf2815(t *testing.T, calls *[]call2738, op string) map[string]any {
	t.Helper()
	for _, c := range *calls {
		if c.Op == op {
			return c.Vars
		}
	}
	t.Fatalf("no call to %s (ops: %v)", op, ops2738(calls))
	return nil
}

// callsTo2815 counts the calls recorded for op.
func callsTo2815(calls *[]call2738, op string) int {
	n := 0
	for _, c := range *calls {
		if c.Op == op {
			n++
		}
	}
	return n
}

// TestProtectedCreateWireStates_2815 pins the tri-state on the wire: the key is
// absent when the flag is omitted, true for --protected, false for
// --protected=false, and the mutation is sent exactly once either way. The
// contract answer differs per row (BLIND P2 hardening): a protected create
// stages (the fake answers staged and the exit is 5), omission and explicit
// false create unprotected (a secret comes back, exit 0).
func TestProtectedCreateWireStates_2815(t *testing.T) {
	created := `{"data":{"createSecret":` + secretJSON + `}}`
	for _, tc := range []struct {
		name    string
		flags   []string
		present bool
		value   bool
		staged  bool
	}{
		{"omitted sends no field", nil, false, false, false},
		{"--protected sends true", []string{"--protected"}, true, true, true},
		{"--protected=false sends false", []string{"--protected=false"}, true, false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			body := created
			wantCode := 0
			if tc.staged {
				body = staged2738()
				wantCode = exitcode.Conflict
			}
			srv, calls := fake2738(t, only2738(200, body))
			args := append([]string{"create", "--name", "github-token", "--scope", "user",
				"--kind", "generic", "--value-file", valueFile2738(t)}, tc.flags...)
			stdout, stderr, code := run2738(t, srv.URL, args...)
			if code != wantCode {
				t.Fatalf("exit = %d, want %d\n%s%s", code, wantCode, stdout, stderr)
			}
			if got := callsTo2815(calls, "CreateSecret"); got != 1 {
				t.Fatalf("createSecret must be sent exactly once, sent %d", got)
			}
			vars := callVarsOf2815(t, calls, "CreateSecret")
			got, has := vars["protected"]
			if has != tc.present {
				t.Fatalf("protected key present=%v, want %v (vars %v)", has, tc.present, vars)
			}
			if has && got != tc.value {
				t.Fatalf("protected = %v, want %v", got, tc.value)
			}
			if strings.Contains(stdout+stderr, value2738) {
				t.Fatalf("output leaked the value:\n%s%s", stdout, stderr)
			}
		})
	}
}

// TestProtectedCreateIsStagedAndNeverSaysCreated_2815 (acceptance 1, staged
// arm): a protected create the server stages prints the request id and the
// ceremony URL byte for byte, says staged, never says "created" and never
// prints a secret URN; --json carries the #2738 envelope unchanged.
func TestProtectedCreateIsStagedAndNeverSaysCreated_2815(t *testing.T) {
	srv, calls := fake2738(t, func(op string, n int) (int, string) {
		if op == "CreateSecret" {
			return 200, staged2738()
		}
		return 200, `{"data":null}`
	})
	stdout, stderr, code := run2738(t, srv.URL, "create", "--name", "app-secret", "--scope", "app",
		"--owner", "01app", "--kind", "generic", "--protected", "--value-file", valueFile2738(t), "--json")
	if code != exitcode.Conflict {
		t.Fatalf("exit = %d, want 5 (staged)\n%s%s", code, stdout, stderr)
	}
	if got := callsTo2815(calls, "CreateSecret"); got != 1 {
		t.Fatalf("createSecret must be sent exactly once, sent %d", got)
	}
	noCeremonyRequest2738(t, calls)
	e := envelope2738(t, stdout, stderr)
	if e["requestId"] != req2738 || e["ceremonyUrl"] != srv.URL+ceremonyPath2738 ||
		e["serverCode"] != "SECRET_PROTECTED_NEEDS_PASSKEY" || e["code"] != float64(5) {
		t.Fatalf("staged envelope mismatch: %v", e)
	}
	for _, leaked := range []string{"created secret", "hrn:secret"} {
		if strings.Contains(stdout, leaked) || strings.Contains(stderr, leaked) {
			t.Fatalf("a staged create must never print %q:\nstdout=%s\nstderr=%s", leaked, stdout, stderr)
		}
	}
}

// TestProtectedCreateReturningSecretIsUnconfirmed_2815 (BLIND P1, create arm):
// the contract is that createSecret(protected: true) STAGES the whole creation
// and answers SECRET_PROTECTED_NEEDS_PASSKEY (hadron-server#3999 S4; the plan
// doc). A server that instead returns the created secret did not stage what
// this CLI asked for, so the requested protection is unconfirmed: exit 7, no
// "created" success line, and the way to check — never a silent success for a
// secret that may exist unprotected.
func TestProtectedCreateReturningSecretIsUnconfirmed_2815(t *testing.T) {
	srv, calls := fake2738(t, only2738(200, `{"data":{"createSecret":`+secretJSON+`}}`))
	stdout, stderr, code := run2738(t, srv.URL, "create", "--name", "app-secret", "--scope", "app",
		"--owner", "01app", "--kind", "generic", "--protected", "--value-file", valueFile2738(t))
	if code != exitcode.Unavailable {
		t.Fatalf("exit = %d, want 7 (unconfirmed)\n%s%s", code, stdout, stderr)
	}
	if got := callsTo2815(calls, "CreateSecret"); got != 1 {
		t.Fatalf("createSecret must be sent exactly once, sent %d", got)
	}
	all := stdout + stderr
	for _, want := range []string{"not confirmed", "hadron secret get"} {
		if !strings.Contains(all, want) {
			t.Fatalf("unconfirmed output must say the outcome is unconfirmed and how to check, missing %q:\n%s", want, all)
		}
	}
	if strings.Contains(all, "✓ created secret") {
		t.Fatalf("an unconfirmed create must never print a success line:\n%s", all)
	}
}

// TestSecretUpdateProtectedReturningSecretIsUnconfirmed_2815 (BLIND P1, QA P2
// fold; update arm): a protection change on an App secret stages
// (hadron-server#3999 S4), so a returned secret is not the promised staging
// for --protected: exit 7, no "updated" success line, same shape as secret
// protect's answer to a returned secret. The one returned secret that IS the
// confirmed answer is the #4749 no-op clear (pinned below); everything else
// that reports protected: true while this CLI asked for false did neither —
// no staging, no clear — so it stays unconfirmed.
func TestSecretUpdateProtectedReturningSecretIsUnconfirmed_2815(t *testing.T) {
	for _, tc := range []struct {
		name   string
		flags  string
		answer string
	}{
		{"--protected with a returned secret", "--protected", secretJSON},
		{"--protected=false the answer still reports protected", "--protected=false", secretProtected2815(true)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv, calls := fake2738(t, only2738(200, `{"data":{"updateSecret":`+tc.answer+`}}`))
			stdout, stderr, code := run2738(t, srv.URL, "update", "01sec", tc.flags)
			if code != exitcode.Unavailable {
				t.Fatalf("exit = %d, want 7 (unconfirmed)\n%s%s", code, stdout, stderr)
			}
			if got := callsTo2815(calls, "UpdateSecret"); got != 1 {
				t.Fatalf("updateSecret must be sent exactly once, sent %d", got)
			}
			all := stdout + stderr
			for _, want := range []string{"not confirmed", "hadron secret get"} {
				if !strings.Contains(all, want) {
					t.Fatalf("unconfirmed output must say the outcome is unconfirmed and how to check, missing %q:\n%s", want, all)
				}
			}
			if strings.Contains(all, "✓ updated secret") {
				t.Fatalf("an unconfirmed update must never print a success line:\n%s", all)
			}
		})
	}
}

// TestSecretUpdateProtectedFalseNoOpIsConfirmed_2815 (QA P2 fold): clearing
// protection that is not on asks for no change (hadron-server#4749, the
// #4717 item-8 class reached through updateSecret; fix hadron-server#4805):
// the server stages nothing and returns the secret — unchanged when the flag
// is alone, or with the rest of the update applied when a value or metadata
// rode along. The CLI asked for unprotected and the answer reports
// unprotected, so the outcome IS confirmed: exit 0 and the normal updated
// line, never exit 7 for a state that is already true. Servers that still
// stage the clear (main before #4805) answer SECRET_PROTECTED_NEEDS_PASSKEY
// and take the exit-5 path pinned above.
func TestSecretUpdateProtectedFalseNoOpIsConfirmed_2815(t *testing.T) {
	for _, tc := range []struct {
		name  string
		flags []string
	}{
		{"the flag alone", []string{"--protected=false"}},
		{"with metadata", []string{"--protected=false", "--meta", "env=prod"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv, calls := fake2738(t, only2738(200, `{"data":{"updateSecret":`+secretProtected2815(false)+`}}`))
			stdout, stderr, code := run2738(t, srv.URL, append([]string{"update", "01sec"}, tc.flags...)...)
			if code != 0 {
				t.Fatalf("exit = %d, want 0 (the no-op clear is the confirmed answer)\n%s%s", code, stdout, stderr)
			}
			if got := callsTo2815(calls, "UpdateSecret"); got != 1 {
				t.Fatalf("updateSecret must be sent exactly once, sent %d", got)
			}
			vars := callVarsOf2815(t, calls, "UpdateSecret")
			if got, has := vars["protected"]; !has || got != false {
				t.Fatalf("--protected=false must send protected:false, got %v (present=%v)", got, has)
			}
			all := stdout + stderr
			if !strings.Contains(all, "✓ updated secret") {
				t.Fatalf("a confirmed no-op clear prints the normal updated line:\n%s", all)
			}
			if strings.Contains(all, "not confirmed") {
				t.Fatalf("the server did exactly what was asked; nothing is unconfirmed:\n%s", all)
			}
		})
	}
}

// TestSecretUpdateWithoutProtectedSendsNoField_2815 (acceptance 2): a plain
// update sends NO protected key, so a member's value/metadata change is not
// silently turned manager-only.
func TestSecretUpdateWithoutProtectedSendsNoField_2815(t *testing.T) {
	srv, calls := fake2738(t, only2738(200, `{"data":{"updateSecret":`+secretJSON+`}}`))
	stdout, stderr, code := run2738(t, srv.URL, "update", "01sec", "--meta", "env=prod")
	if code != 0 {
		t.Fatalf("exit = %d\n%s%s", code, stdout, stderr)
	}
	if got := callsTo2815(calls, "UpdateSecret"); got != 1 {
		t.Fatalf("updateSecret must be sent exactly once, sent %d", got)
	}
	vars := callVarsOf2815(t, calls, "UpdateSecret")
	if _, has := vars["protected"]; has {
		t.Fatalf("an update without --protected must send no protected field (manager-only by stealth): %v", vars)
	}
}

// TestSecretUpdateProtectedFalseSendsFalse_2815 (acceptance 3, wire arm):
// --protected=false sends an explicit false. The fake answers staged — the
// contract answer for ANY explicit value on an App secret (BLIND P2: a secret
// payload here would ride the unconfirmed branch, not the wire) — so the pin
// is the recorded variable, and no "updated" success line may print.
func TestSecretUpdateProtectedFalseSendsFalse_2815(t *testing.T) {
	srv, calls := fake2738(t, only2738(200, staged2738()))
	stdout, stderr, code := run2738(t, srv.URL, "update", "01sec", "--protected=false")
	if code != exitcode.Conflict {
		t.Fatalf("exit = %d, want 5 (staged)\n%s%s", code, stdout, stderr)
	}
	vars := callVarsOf2815(t, calls, "UpdateSecret")
	if got, has := vars["protected"]; !has || got != false {
		t.Fatalf("--protected=false must send protected:false, got %v (present=%v)", got, has)
	}
	if strings.Contains(stdout+stderr, "✓ updated secret") {
		t.Fatalf("a staged update must never print a success line:\n%s%s", stdout, stderr)
	}
}

// TestSecretUpdateProtectedFalseRefusalRendersAsSent_2815 (acceptance 3,
// refusal arm): a non-manager's FORBIDDEN is rendered as the server returned
// it (exit 8); the CLI does not pre-check and does not rewrite the sentence.
func TestSecretUpdateProtectedFalseRefusalRendersAsSent_2815(t *testing.T) {
	srv, _ := fake2738(t, only2738(200, gqlErr2738("FORBIDDEN", nil)))
	stdout, stderr, code := run2738(t, srv.URL, "update", "01sec", "--protected=false", "--json")
	if code != exitcode.Forbidden {
		t.Fatalf("exit = %d, want 8\n%s%s", code, stdout, stderr)
	}
	e := envelope2738(t, stdout, stderr)
	if msg, _ := e["message"].(string); !strings.Contains(msg, "refused "+value2738) {
		t.Fatalf("the server's own text must be rendered as sent, got %q (envelope %v)", msg, e)
	}
}

// TestSecretUpdateProtectedAloneIsAChange_2815 (acceptance 4): --protected
// with no other flag is a valid change — no "nothing to update" usage refusal,
// the mutation is sent once, and a staged answer takes the #2738 path.
func TestSecretUpdateProtectedAloneIsAChange_2815(t *testing.T) {
	srv, calls := fake2738(t, func(op string, n int) (int, string) {
		if op == "UpdateSecret" {
			return 200, staged2738()
		}
		return 200, `{"data":null}`
	})
	stdout, stderr, code := run2738(t, srv.URL, "update", "01sec", "--protected")
	if code != exitcode.Conflict {
		t.Fatalf("exit = %d, want 5 (staged; --protected alone must be accepted as a change)\n%s%s", code, stdout, stderr)
	}
	if got := callsTo2815(calls, "UpdateSecret"); got != 1 {
		t.Fatalf("updateSecret must be sent exactly once, sent %d", got)
	}
	// BLIND P2: the staged answer must be earned by the request, not just the
	// operation name — pin the variable, so clearing the usage guard while
	// omitting the field cannot pass as a staged protection change.
	vars := callVarsOf2815(t, calls, "UpdateSecret")
	if got, has := vars["protected"]; !has || got != true {
		t.Fatalf("--protected must send protected:true, got %v (present=%v); a staged answer without the field is the guard cleared but the change not sent", got, has)
	}
	all := stdout + stderr
	for _, want := range []string{"staged, not applied", "request id: " + req2738, "ceremony URL: " + srv.URL + ceremonyPath2738} {
		if !strings.Contains(all, want) {
			t.Fatalf("staged output missing %q:\n%s", want, all)
		}
	}
}

// TestSecretSetSendsNoProtectedKey_2815 (SEC10 P3, folded): secret set is the
// other updateSecret caller and is the value-only door — protection is never
// sent. Without this pin a set that started sending protected would pass every
// test (the fake answers by operation name), silently turning a value rotation
// into a staged, manager-gated protection change.
func TestSecretSetSendsNoProtectedKey_2815(t *testing.T) {
	srv, calls := fake2738(t, only2738(200, `{"data":{"updateSecret":`+secretJSON+`}}`))
	stdout, stderr, code := run2738(t, srv.URL, "set", "01sec", "--value-file", valueFile2738(t))
	if code != 0 {
		t.Fatalf("exit = %d\n%s%s", code, stdout, stderr)
	}
	if got := callsTo2815(calls, "UpdateSecret"); got != 1 {
		t.Fatalf("updateSecret must be sent exactly once, sent %d", got)
	}
	vars := callVarsOf2815(t, calls, "UpdateSecret")
	if _, has := vars["protected"]; has {
		t.Fatalf("secret set must send no protected field (value-only door): %v", vars)
	}
}

// TestProtectedFlagHelpNamesTheThreeStates_2815 (acceptance 5): both commands
// name the flag, its three states, that it applies to App secrets, and that
// protection changes through a signed request.
func TestProtectedFlagHelpNamesTheThreeStates_2815(t *testing.T) {
	for _, verb := range []string{"create", "update"} {
		out := secretHelp2815(t, "secret", verb)
		for _, want := range []string{"--protected", "--protected=false", "App secrets", "signed"} {
			if !strings.Contains(out, want) {
				t.Errorf("secret %s --help must mention %q:\n%s", verb, want, out)
			}
		}
	}
}

// secretHelp2815 renders a command's --help with wrapped lines joined.
func secretHelp2815(t *testing.T, args ...string) string {
	t.Helper()
	f, out := testFactory(t)
	var errOut strings.Builder
	f.IOStreams.ErrOut = &errOut
	root := NewRootCmd(f)
	root.SetArgs(append(args, "--help"))
	if err := root.Execute(); err != nil {
		t.Fatalf("%v --help: %v", args, err)
	}
	return strings.Join(strings.Fields(out.String()+" "+errOut.String()), " ")
}
