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
	// BLIND P3 fold (Eden-directed, with the 7eb4499b bounce): the message
	// must also say the secret EXISTS — the creation was applied, not staged —
	// and may be unprotected, so the reader knows what state to check.
	for _, want := range []string{"The secret exists", "may be unprotected", "not confirmed", "hadron secret get"} {
		if !strings.Contains(all, want) {
			t.Fatalf("unconfirmed output must say the secret exists and may be unprotected, that the outcome is unconfirmed, and how to check; missing %q:\n%s", want, all)
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

// TestSecretUpdateHelpStatesTheNoOpConditionally_2815 (BLIND P2 fold): the
// already-off clear is server-dependent (hadron-server#4749; fix #4805 still
// open when 7eb4499b shipped): servers that stage the clear answer
// SECRET_PROTECTED_NEEDS_PASSKEY — the exit-5 ceremony flow, not the updated
// line — and servers with the no-op return the secret. The help must name
// both answers, never state the no-op outcome as unconditional fact.
func TestSecretUpdateHelpStatesTheNoOpConditionally_2815(t *testing.T) {
	out := secretHelp2815(t, "secret", "update")
	for _, want := range []string{
		"answer differently",
		"some stage the clear",
		"others recognize the no change",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("secret update --help must state the already-off clear conditionally, missing %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "asks for no change: nothing is staged") {
		t.Errorf("secret update --help still states the no-op outcome unconditionally:\n%s", out)
	}
}

// TestAgenticUsageStatesProtectedOutcomes_2815 (BLIND P2 fold at e107e08b,
// round 3): the agent-facing contract said every explicit --protected value
// "stages like any protected change" — but three answers are not staging (the
// no-op clear exit 0; the not-confirmed returns exit 7 on update and create),
// and an agent branching on that sentence misreads them. The contract states
// the outcomes; this pin keeps it from drifting back to the blanket claim.
func TestAgenticUsageStatesProtectedOutcomes_2815(t *testing.T) {
	contract := string(readContract(t))
	for _, want := range []string{
		"stages nothing and returns the secret",
		"the creation was applied, not staged",
		"the secret exists and may be unprotected",
		"asks for a protection change in the same request",
		"refuses the request even flag-omitted",
	} {
		if !strings.Contains(contract, want) {
			t.Errorf("agentic-usage must state the protected-flag outcomes, missing %q", want)
		}
	}
	if strings.Contains(contract, "every explicit value stages like any protected change") {
		t.Error("agentic-usage still claims every explicit --protected value stages")
	}
	// BLIND P3 fold, round 4: the contract states current behaviour, not issue
	// history — "was display only until cli#2815" narrates the past.
	if strings.Contains(contract, "was display only") {
		t.Error("agentic-usage still narrates issue history (display-only past)")
	}
}

// TestSecretCreateProtectedFalseAnsweredTrueIsUnconfirmed_2815 (BLIND P3 fold,
// round 4): the create guard covered only --protected, so a --protected=false
// create answered with protected: true returned a success line — the same
// "server did not do what was asked" class the update guard catches (the
// update pin covers its two arms above). An explicit false that the answer
// reports protected did not get the protection asked for: exit 7, no success
// line, and the reader is told the secret may be protected.
func TestSecretCreateProtectedFalseAnsweredTrueIsUnconfirmed_2815(t *testing.T) {
	srv, calls := fake2738(t, only2738(200, `{"data":{"createSecret":`+secretProtected2815(true)+`}}`))
	stdout, stderr, code := run2738(t, srv.URL, "create", "--name", "app-secret", "--scope", "app",
		"--owner", "01app", "--kind", "generic", "--protected=false", "--value-file", valueFile2738(t))
	if code != exitcode.Unavailable {
		t.Fatalf("exit = %d, want 7 (unconfirmed)\n%s%s", code, stdout, stderr)
	}
	if got := callsTo2815(calls, "CreateSecret"); got != 1 {
		t.Fatalf("createSecret must be sent exactly once, sent %d", got)
	}
	all := stdout + stderr
	for _, want := range []string{"not confirmed", "may be protected", "hadron secret get"} {
		if !strings.Contains(all, want) {
			t.Fatalf("unconfirmed output must say the outcome is unconfirmed, that it may be protected, and how to check; missing %q:\n%s", want, all)
		}
	}
	if strings.Contains(all, "✓ created secret") {
		t.Fatalf("an unconfirmed create must never print a success line:\n%s", all)
	}
}

// TestProtectedSpaceSeparatedValueRefused_2815 (BLIND P3 fold, round 4):
// "--protected false" with a space parses as --protected plus a stray
// positional — on create it died as an "unknown command", on update as an
// arg-count error, both naming everything but the actual mistake: the flag is
// attached-form only. The refusal names the two spellings and NOTHING is sent.
func TestProtectedSpaceSeparatedValueRefused_2815(t *testing.T) {
	for _, tc := range []struct {
		name string
		args []string
		op   string
	}{
		{"create --protected false", []string{"create", "--name", "s", "--scope", "user",
			"--kind", "generic", "--value-file", valueFile2738(t), "--protected", "false"}, "CreateSecret"},
		{"update <id> --protected false", []string{"update", "01sec", "--protected", "false"}, "UpdateSecret"},
		{"update --protected false <id>", []string{"update", "--protected", "false", "01sec"}, "UpdateSecret"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv, calls := fake2738(t, only2738(200, `{"data":{"updateSecret":`+secretJSON+`}}`))
			stdout, stderr, code := run2738(t, srv.URL, tc.args...)
			if code != exitcode.Usage {
				t.Fatalf("exit = %d, want 2 (usage)\n%s%s", code, stdout, stderr)
			}
			if got := callsTo2815(calls, tc.op); got != 0 {
				t.Fatalf("nothing must be sent for a misparsed flag value, sent %d", got)
			}
			all := stdout + stderr
			for _, want := range []string{"--protected", "--protected=false"} {
				if !strings.Contains(all, want) {
					t.Fatalf("the refusal must name the attached spellings, missing %q:\n%s", want, all)
				}
			}
		})
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

// --- round 6 (Eden ruling, chat #137754): flag-conditional documents ---
//
// The BLIND round-5 P2: $protected sat in the static CreateSecret/UpdateSecret
// documents, so a server from before the argument reached server main
// (2026-11-21, hadron-server#4177 merge e42bb1897) rejected plain
// create/update/set at validation even flag-omitted — commands that worked
// before the flag existed. The fix follows the secret client list precedent
// (cli#2758/2720: one document per server generation, stepping only as far as
// the server forces): the shared documents keep their pre-#2815 shape and the
// flag rides its own CreateSecretProtected/UpdateSecretProtected document,
// chosen only when --protected is given. Wire behavior on servers that have the
// argument is unchanged (Petra's tri-state conditions: omitted sends no field,
// --protected sends true, --protected=false sends false).

// callQueryOf2815 returns the recorded raw query of the first call to op.
func callQueryOf2815(t *testing.T, calls *[]call2738, op string) string {
	t.Helper()
	for _, c := range *calls {
		if c.Op == op {
			return c.Query
		}
	}
	t.Fatalf("no call to %s (ops: %v)", op, ops2738(calls))
	return ""
}

// oneFlagChosenCall2815 asserts the request set is exactly one call to want
// (any call to the other document fails: the flag must choose its document,
// never both, never a retry).
func oneFlagChosenCall2815(t *testing.T, calls *[]call2738, want, other string) {
	t.Helper()
	if got := callsTo2815(calls, want); got != 1 {
		t.Fatalf("%s must be sent exactly once, sent %d", want, got)
	}
	if got := callsTo2815(calls, other); got != 0 {
		t.Fatalf("%s must not be sent when the flag chose %s, sent %d", other, want, got)
	}
}

// TestFlagOmittedRequestsStayUnprotected_2815 (BLIND round-5 P2, the
// compatibility core): a flag-omitted create, a plain update and secret set
// send the pre-#2815 documents — operation CreateSecret/UpdateSecret whose
// query text carries no `protected` at all — so servers older than the
// argument keep accepting the plain commands exactly as before the flag
// existed. The old documents must stay byte-equivalent to their pre-#2815
// shape; pinning the absence of the word in the query pins that.
func TestFlagOmittedRequestsStayUnprotected_2815(t *testing.T) {
	t.Run("create without the flag", func(t *testing.T) {
		srv, calls := fake2738(t, only2738(200, `{"data":{"createSecret":`+secretJSON+`}}`))
		_, _, code := run2738(t, srv.URL, "create", "--name", "github-token", "--scope", "user",
			"--kind", "generic", "--value-file", valueFile2738(t))
		if code != 0 {
			t.Fatalf("exit = %d\n", code)
		}
		oneFlagChosenCall2815(t, calls, "CreateSecret", "CreateSecretProtected")
		if q := callQueryOf2815(t, calls, "CreateSecret"); strings.Contains(q, "protected") {
			t.Fatalf("the flag-omitted create document must carry no `protected` (the round-5 P2 regression):\n%s", q)
		}
	})
	t.Run("update without the flag", func(t *testing.T) {
		srv, calls := fake2738(t, only2738(200, `{"data":{"updateSecret":`+secretJSON+`}}`))
		_, _, code := run2738(t, srv.URL, "update", "01sec", "--meta", "env=prod")
		if code != 0 {
			t.Fatalf("exit = %d\n", code)
		}
		oneFlagChosenCall2815(t, calls, "UpdateSecret", "UpdateSecretProtected")
		if q := callQueryOf2815(t, calls, "UpdateSecret"); strings.Contains(q, "protected") {
			t.Fatalf("the flag-omitted update document must carry no `protected` (the round-5 P2 regression):\n%s", q)
		}
	})
	t.Run("set never names the argument", func(t *testing.T) {
		srv, calls := fake2738(t, only2738(200, `{"data":{"updateSecret":`+secretJSON+`}}`))
		_, _, code := run2738(t, srv.URL, "set", "01sec", "--value-file", valueFile2738(t))
		if code != 0 {
			t.Fatalf("exit = %d\n", code)
		}
		oneFlagChosenCall2815(t, calls, "UpdateSecret", "UpdateSecretProtected")
		if q := callQueryOf2815(t, calls, "UpdateSecret"); strings.Contains(q, "protected") {
			t.Fatalf("secret set rides the flag-omitted update document; it must carry no `protected`:\n%s", q)
		}
	})
}

// TestProtectedFlagUsesItsOwnDocuments_2815: every attached spelling of the
// flag — --protected, --protected=true, --protected=false — sends the
// Protected document with the explicit value, once, and never the shared one.
func TestProtectedFlagUsesItsOwnDocuments_2815(t *testing.T) {
	for _, tc := range []struct {
		name  string
		flags []string
		value bool
	}{
		{"create --protected", []string{"--protected"}, true},
		{"create --protected=true", []string{"--protected=true"}, true},
		{"create --protected=false", []string{"--protected=false"}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv, calls := fake2738(t, only2738(200, staged2738()))
			args := append([]string{"create", "--name", "app-secret", "--scope", "app", "--owner", "01app",
				"--kind", "generic", "--value-file", valueFile2738(t)}, tc.flags...)
			_, _, code := run2738(t, srv.URL, args...)
			if code != exitcode.Conflict {
				t.Fatalf("exit = %d, want 5 (staged)\n", code)
			}
			oneFlagChosenCall2815(t, calls, "CreateSecretProtected", "CreateSecret")
			vars := callVarsOf2815(t, calls, "CreateSecretProtected")
			if got, has := vars["protected"]; !has || got != tc.value {
				t.Fatalf("%s must send protected:%v on its own document, got %v (present=%v)", tc.name, tc.value, got, has)
			}
		})
	}
	t.Run("update --protected", func(t *testing.T) {
		srv, calls := fake2738(t, only2738(200, staged2738()))
		_, _, code := run2738(t, srv.URL, "update", "01sec", "--protected")
		if code != exitcode.Conflict {
			t.Fatalf("exit = %d, want 5 (staged)\n", code)
		}
		oneFlagChosenCall2815(t, calls, "UpdateSecretProtected", "UpdateSecret")
		vars := callVarsOf2815(t, calls, "UpdateSecretProtected")
		if got, has := vars["protected"]; !has || got != true {
			t.Fatalf("--protected must send protected:true on its own document, got %v (present=%v)", got, has)
		}
	})
}

// TestProtectedUpdateWithValueFileSendsBoth_2815 (BLIND round-5 P3 pin): the
// flag and a new value ride ONE UpdateSecretProtected with both variables — a
// protection change that also rotates the value is one request, not two.
func TestProtectedUpdateWithValueFileSendsBoth_2815(t *testing.T) {
	srv, calls := fake2738(t, only2738(200, staged2738()))
	_, _, code := run2738(t, srv.URL, "update", "01sec", "--protected", "--value-file", valueFile2738(t))
	if code != exitcode.Conflict {
		t.Fatalf("exit = %d, want 5 (staged)\n", code)
	}
	oneFlagChosenCall2815(t, calls, "UpdateSecretProtected", "UpdateSecret")
	vars := callVarsOf2815(t, calls, "UpdateSecretProtected")
	if got, has := vars["protected"]; !has || got != true {
		t.Fatalf("--protected must send protected:true, got %v (present=%v)", got, has)
	}
	if v, ok := vars["value"]; !ok || v == nil {
		t.Fatalf("--value-file alongside --protected must send the value in the same request: %v", vars)
	}
}

// TestProtectedUpdateUnconfirmedNamesTheRest_2815 (BLIND round-5 P3): when the
// exit-7 unconfirmed answer arrives for an update that carried a value or
// metadata alongside the flag, the message must say THAT part may have been
// applied — "not confirmed" alone hides the half-applied state the reader must
// check. Text and --json arms.
func TestProtectedUpdateUnconfirmedNamesTheRest_2815(t *testing.T) {
	for _, tc := range []struct {
		name string
		json bool
	}{
		{"text", false},
		{"json", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			body := `{"data":{"updateSecret":` + secretProtected2815(true) +
				`,"updateSecretProtected":` + secretProtected2815(true) + `}}`
			srv, calls := fake2738(t, only2738(200, body))
			args := []string{"update", "01sec", "--protected=false", "--value-file", valueFile2738(t)}
			if tc.json {
				args = append(args, "--json")
			}
			stdout, stderr, code := run2738(t, srv.URL, args...)
			if code != exitcode.Unavailable {
				t.Fatalf("exit = %d, want 7 (unconfirmed)\n%s%s", code, stdout, stderr)
			}
			oneFlagChosenCall2815(t, calls, "UpdateSecretProtected", "UpdateSecret")
			all := stdout + stderr
			for _, want := range []string{"not confirmed", "may have been applied", "hadron secret get"} {
				if !strings.Contains(all, want) {
					t.Fatalf("the unconfirmed update must say the value/metadata part may have been applied, missing %q:\n%s", want, all)
				}
			}
			if strings.Contains(all, "✓ updated secret") {
				t.Fatalf("an unconfirmed update must never print a success line:\n%s", all)
			}
		})
	}
}

// TestProtectedParseBoolSpellingsRefused_2815 (BLIND round-5 P3): the detached
// misparse guard caught only the words true/false; every other strconv.ParseBool
// spelling — 1, 0, t, f, and the mixed-case words — died as an arg-count error
// naming everything but the mistake. All spellings refuse exit 2 with the
// attached-form message, nothing sent.
func TestProtectedParseBoolSpellingsRefused_2815(t *testing.T) {
	for _, v := range []string{"1", "0", "t", "f", "T", "F", "TRUE", "FALSE", "True", "False"} {
		t.Run("create --protected "+v, func(t *testing.T) {
			srv, calls := fake2738(t, only2738(200, `{"data":null}`))
			_, _, code := run2738(t, srv.URL, "create", "--name", "s", "--scope", "user",
				"--kind", "generic", "--value-file", valueFile2738(t), "--protected", v)
			if code != exitcode.Usage {
				t.Fatalf("exit = %d, want 2 (the detached value is a misparse whatever its spelling)\n", code)
			}
			for _, op := range []string{"CreateSecret", "CreateSecretProtected"} {
				if got := callsTo2815(calls, op); got != 0 {
					t.Fatalf("nothing must be sent for a misparsed flag value, sent %d to %s", got, op)
				}
			}
			// The refusal must be the guard's message naming the attached
			// spellings — the bare arg-count error a stray positional causes
			// exits 2 too, naming everything but the mistake.
			stdout, stderr, _ := run2738(t, srv.URL, "create", "--name", "s", "--scope", "user",
				"--kind", "generic", "--value-file", valueFile2738(t), "--protected", v)
			if all := stdout + stderr; !strings.Contains(all, "--protected takes no separate value") {
				t.Fatalf("the %q misparse must be refused by the guard naming the attached spellings, got:\n%s", v, all)
			}
		})
	}
}

// TestAgenticUsageFloorAndSingleOutcomeSentence_2815 (BLIND round-5 P3s): the
// outcome sentence appears exactly once (round 5 shipped it twice), and the
// floor sentence states the split truth — --protected needs a server from
// 2026-11-21; the flag-omitted request is unchanged, so older servers keep
// accepting it — never the round-4 "refuses the request even flag-omitted".
func TestAgenticUsageFloorAndSingleOutcomeSentence_2815(t *testing.T) {
	contract := string(readContract(t))
	if n := strings.Count(contract, "The answers are the protected-secret changes entry above"); n != 1 {
		t.Fatalf("the protected-outcome sentence must appear exactly once, found %d", n)
	}
	for _, want := range []string{
		"needs a server from 2026-11-21",
		"the flag-omitted request is unchanged",
	} {
		if !strings.Contains(contract, want) {
			t.Errorf("agentic-usage must state the document-split floor truth, missing %q", want)
		}
	}
	if strings.Contains(contract, "refuses the request even flag-omitted") {
		t.Error("agentic-usage still claims the floor applies to flag-omitted requests")
	}
}

// TestProtectedHelpStatesTheDocumentSplit_2815 (BLIND round-5 P3 + ruling):
// both helps say --protected needs the newer server AND that without the flag
// the request is unchanged — the old claim that a pre-argument server refuses
// the request even without the flag is gone.
func TestProtectedHelpStatesTheDocumentSplit_2815(t *testing.T) {
	for _, verb := range []string{"create", "update"} {
		out := secretHelp2815(t, "secret", verb)
		for _, want := range []string{
			"needs a server from 2026-11-21",
			"without the flag",
			"unchanged",
		} {
			if !strings.Contains(out, want) {
				t.Errorf("secret %s --help must state the document split (missing %q):\n%s", verb, want, out)
			}
		}
		if strings.Contains(out, "refuses the request even without the flag") {
			t.Errorf("secret %s --help still claims the floor applies to flag-omitted calls:\n%s", verb, out)
		}
	}
}
