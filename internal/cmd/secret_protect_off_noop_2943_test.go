package cmd

// hadron-cli#2943: `secret protect --off` on a secret that is not protected.
// The server stages nothing and answers the secret with protected: false (the
// no-op); the CLI took every returned secret for an unconfirmed outcome and
// exited 7. The one returned secret that IS the answer is --off with protected
// false: the state asked for is the state reported. Everything else stays as it
// was: a state that differs from the request, --on (the server never returns a
// secret there) and the server's own refusals.

import (
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/exitcode"
)

func protectAnswer2943(protected bool) string {
	return `{"data":{"setSecretProtected":` + secretProtected2815(protected) + `}}`
}

// --off on an unprotected secret is a success: exit 0 and a plain line, not a
// "not confirmed" error. One request is sent, with protected:false.
func TestProtectOffOnAnUnprotectedSecretIsConfirmed2943(t *testing.T) {
	srv, calls := fake2738(t, only2738(200, protectAnswer2943(false)))
	stdout, stderr, code := run2738(t, srv.URL, "protect", "sec_1", "--off")
	if code != 0 {
		t.Fatalf("exit = %d, want 0 (protection is already off)\n%s%s", code, stdout, stderr)
	}
	all := stdout + stderr
	if !strings.Contains(all, "already off") || !strings.Contains(all, "github-token") {
		t.Errorf("say protection is already off, for which secret:\n%s", all)
	}
	for _, bad := range []string{"not confirmed", "staged", "ceremony"} {
		if strings.Contains(all, bad) {
			t.Errorf("nothing was staged and nothing is unconfirmed (%q):\n%s", bad, all)
		}
	}
	if got := callsTo2815(calls, "SetSecretProtected"); got != 1 {
		t.Fatalf("setSecretProtected must be sent exactly once, sent %d", got)
	}
	if v := callVarsOf2815(t, calls, "SetSecretProtected"); v["protected"] != false || v["ref"] != "sec_1" {
		t.Errorf("--off sends protected:false for the ref: %v", v)
	}
	noCeremonyRequest2738(t, calls)
}

// --json carries the secret as the server answered it (the stable secret DTO):
// protected false, and no error envelope.
func TestProtectOffNoOpJSONIsTheSecret2943(t *testing.T) {
	srv, _ := fake2738(t, only2738(200, protectAnswer2943(false)))
	stdout, stderr, code := run2738(t, srv.URL, "protect", "sec_1", "--off", "--json")
	if code != 0 {
		t.Fatalf("exit = %d, want 0\n%s%s", code, stdout, stderr)
	}
	var d map[string]any
	if err := json.Unmarshal([]byte(stdout), &d); err != nil {
		t.Fatalf("stdout is not JSON: %v\n%s", err, stdout)
	}
	if d["id"] != "sec_1" || d["protected"] != false {
		t.Errorf("the secret as answered, protected false: %v", d)
	}
	if strings.Contains(stderr, "not confirmed") {
		t.Errorf("no error on stderr: %q", stderr)
	}
}

// The success is only for a returned secret whose protected equals the request.
// --off answered with a secret that is still protected is the server neither
// staging nor clearing: exit 7 as before.
func TestProtectOffStillProtectedStaysUnconfirmed2943(t *testing.T) {
	srv, _ := fake2738(t, only2738(200, protectAnswer2943(true)))
	stdout, stderr, code := run2738(t, srv.URL, "protect", "sec_1", "--off")
	if code != exitcode.Unavailable {
		t.Fatalf("exit = %d, want 7\n%s%s", code, stdout, stderr)
	}
	if !strings.Contains(stderr, "not confirmed") || !strings.Contains(stderr, "protected: true") || !strings.Contains(stderr, "hadron secret get sec_1") {
		t.Errorf("must say it is unconfirmed, what the server reported and how to check: %q", stderr)
	}
	if strings.Contains(stdout+stderr, "already off") {
		t.Errorf("a protected secret is not already off:\n%s%s", stdout, stderr)
	}
}

// --on never accepts a returned secret: the server stages (or refuses) there, so
// a secret, whatever it reports, is unconfirmed.
func TestProtectOnNeverAcceptsAReturnedSecret2943(t *testing.T) {
	for _, reported := range []bool{true, false} {
		srv, _ := fake2738(t, only2738(200, protectAnswer2943(reported)))
		stdout, stderr, code := run2738(t, srv.URL, "protect", "sec_1", "--on")
		if code != exitcode.Unavailable {
			t.Fatalf("reported %v: exit = %d, want 7\n%s%s", reported, code, stdout, stderr)
		}
		if !strings.Contains(stderr, "not confirmed") || strings.Contains(stdout+stderr, "already") {
			t.Errorf("reported %v: --on is unconfirmed, never 'already': %q %q", reported, stdout, stderr)
		}
	}
}

// The server's own gate comes first: a caller who may not turn protection off
// is refused, never told it is already off; and --on on a protected secret keeps
// its refusal. The CLI infers success from nothing but the returned secret.
func TestProtectRefusalsAreNeverAlreadyOff2943(t *testing.T) {
	for name, tc := range map[string]struct {
		flag string
		code string
		want int
	}{
		"not a manager, --off":    {"--off", "FORBIDDEN", exitcode.Forbidden},
		"already protected, --on": {"--on", "BAD_USER_INPUT", exitcode.Usage},
		"no such secret, --off":   {"--off", "NOT_FOUND", exitcode.NotFound},
	} {
		t.Run(name, func(t *testing.T) {
			srv, _ := fake2738(t, only2738(200, gqlErr2738(tc.code, nil)))
			stdout, stderr, code := run2738(t, srv.URL, "protect", "sec_1", tc.flag)
			if code != tc.want {
				t.Fatalf("exit = %d, want %d\n%s%s", code, tc.want, stdout, stderr)
			}
			if strings.Contains(stdout+stderr, "already off") || strings.Contains(stdout+stderr, "✓") {
				t.Errorf("a refusal is never a success line:\n%s%s", stdout, stderr)
			}
		})
	}
}

// A staged request is still exit 5, for --off too: the no-op changes nothing there.
func TestProtectOffOnAProtectedSecretStillStages2943(t *testing.T) {
	srv, _ := fake2738(t, only2738(200, staged2738()))
	stdout, stderr, code := run2738(t, srv.URL, "protect", "sec_1", "--off")
	if code != exitcode.Conflict {
		t.Fatalf("exit = %d, want 5\n%s%s", code, stdout, stderr)
	}
	if strings.Contains(stdout+stderr, "already off") {
		t.Errorf("a staged change is not already off:\n%s%s", stdout, stderr)
	}
}

// The help says it: --off on an unprotected secret is a no-op, exit 0.
func TestProtectHelpStatesTheNoOp2943(t *testing.T) {
	help := strings.Join(strings.Fields(cmdHelpOut(t, "secret", "protect")), " ")
	for _, want := range []string{
		"asks for no change, and servers answer it differently",
		"already off",
		"0 --off on a secret that is not protected",
	} {
		if !strings.Contains(help, want) {
			t.Errorf("help lacks %q:\n%s", want, help)
		}
	}
	for _, banned := range []string{"cor:sec", "hadron-server#", "hadron-cli#"} {
		if strings.Contains(help, banned) {
			t.Errorf("help carries no %q:\n%s", banned, help)
		}
	}
}

// hadron-cli#2984: the contract word for --off on an unprotected secret is as
// conditional as the help (some servers stage it, exit 5; others recognize the
// no change, exit 0), and both say what "already off" rests on.
func TestProtectOffWordingIsConditionalAndNamesItsBasis2984(t *testing.T) {
	help := strings.Join(strings.Fields(cmdHelpOut(t, "secret", "protect")), " ")
	b, err := os.ReadFile("agentic/agentic-usage.md")
	if err != nil {
		t.Fatal(err)
	}
	doc := strings.Join(strings.Fields(string(b)), " ")
	for name, tc := range map[string]struct {
		text  string
		wants []string
		bans  []string
	}{
		"help": {help, []string{
			"cannot tell a secret that was already unprotected from one a server cleared without a signed request",
		}, nil},
		"agentic-usage": {doc, []string{
			"Turning protection off on a secret that is NOT protected is a no-change ask and servers answer it differently: some stage it like any protection change (exit 5 above); others recognize the no change, stage nothing and answer the secret with `protected: false`",
			"cannot tell a secret that was already unprotected from one a server cleared without a signed request",
		}, []string{"The one exception is `--off` on a secret that is NOT protected: the server stages nothing"}},
	} {
		for _, w := range tc.wants {
			if !strings.Contains(tc.text, w) {
				t.Errorf("%s lacks %q", name, w)
			}
		}
		for _, bad := range tc.bans {
			if strings.Contains(tc.text, bad) {
				t.Errorf("%s still says %q", name, bad)
			}
		}
	}
}
