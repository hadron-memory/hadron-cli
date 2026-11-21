package cmd

import (
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
// never a success line. Fake HTTP only.

// callVarsOf2815 returns the recorded variables of the n-th call to op.
func callVarsOf2815(t *testing.T, calls *[]call2738, op string, n int) map[string]any {
	t.Helper()
	i := 0
	for _, c := range *calls {
		if c.Op == op {
			if i == n {
				return c.Vars
			}
			i++
		}
	}
	t.Fatalf("no call #%d to %s (ops: %v)", n, op, ops2738(calls))
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
// --protected=false, and the mutation is sent exactly once either way.
func TestProtectedCreateWireStates_2815(t *testing.T) {
	created := `{"data":{"createSecret":` + secretJSON + `}}`
	for _, tc := range []struct {
		name    string
		flags   []string
		present bool
		value   bool
	}{
		{"omitted sends no field", nil, false, false},
		{"--protected sends true", []string{"--protected"}, true, true},
		{"--protected=false sends false", []string{"--protected=false"}, true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv, calls := fake2738(t, only2738(200, created))
			args := append([]string{"create", "--name", "github-token", "--scope", "user",
				"--kind", "generic", "--value-file", valueFile2738(t)}, tc.flags...)
			stdout, stderr, code := run2738(t, srv.URL, args...)
			if code != 0 {
				t.Fatalf("exit = %d\n%s%s", code, stdout, stderr)
			}
			if got := callsTo2815(calls, "CreateSecret"); got != 1 {
				t.Fatalf("createSecret must be sent exactly once, sent %d", got)
			}
			vars := callVarsOf2815(t, calls, "CreateSecret", 0)
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
	vars := callVarsOf2815(t, calls, "UpdateSecret", 0)
	if _, has := vars["protected"]; has {
		t.Fatalf("an update without --protected must send no protected field (manager-only by stealth): %v", vars)
	}
}

// TestSecretUpdateProtectedFalseSendsFalse_2815 (acceptance 3, wire arm):
// --protected=false sends an explicit false.
func TestSecretUpdateProtectedFalseSendsFalse_2815(t *testing.T) {
	srv, calls := fake2738(t, only2738(200, `{"data":{"updateSecret":`+secretJSON+`}}`))
	stdout, stderr, code := run2738(t, srv.URL, "update", "01sec", "--protected=false")
	if code != 0 {
		t.Fatalf("exit = %d\n%s%s", code, stdout, stderr)
	}
	vars := callVarsOf2815(t, calls, "UpdateSecret", 0)
	if got, has := vars["protected"]; !has || got != false {
		t.Fatalf("--protected=false must send protected:false, got %v (present=%v)", got, has)
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
	all := stdout + stderr
	for _, want := range []string{"staged, not applied", "request id: " + req2738, "ceremony URL: " + srv.URL + ceremonyPath2738} {
		if !strings.Contains(all, want) {
			t.Fatalf("staged output missing %q:\n%s", want, all)
		}
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
