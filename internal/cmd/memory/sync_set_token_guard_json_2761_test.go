package memory

import (
	"testing"

	"github.com/vektah/gqlparser/v2/gqlerror"

	"github.com/hadron-memory/hadron-cli/internal/api"
	"github.com/hadron-memory/hadron-cli/internal/exitcode"
)

// cli#2761: the output guard setTokenError attaches judges text a later layer
// appends (renderError's stored-credential hint). The table pins the predicate
// itself, one case per way it can be wrong: a predicate reduced to
// `return jsonOut`, one that drops the plain-spelling test, and one that drops
// the --json envelope test each fail a different row, and one weakened from
// the covered-spelling test to a plain substring fails the escaped-spelling rows. The render path is pinned
// in package cmd (TestSetTokenStoredHintEnvelopeSeam2761).
func TestSetTokenOutputGuardPredicate2761(t *testing.T) {
	const token = `a\"b`
	refusal := &gqlerror.Error{Message: "refused", Extensions: map[string]any{"code": "FORBIDDEN"}}
	for _, c := range []struct {
		name      string
		token     string
		candidate string
		jsonOut   bool
		want      bool
	}{
		{"benign hint, text", token, `refused — a stored credential hint`, false, false},
		{"benign hint, --json", token, `refused — a stored credential hint`, true, false},
		{"literal token, text", token, `refused — ` + token, false, true},
		{"literal token, --json", token, `refused — ` + token, true, true},
		{"escaped spelling, text", token, `refused — a\\\"b`, false, true},
		{"escaped spelling, --json", token, `refused — a\\\"b`, true, true},
		{"envelope form only, text", token, `stored credential a"b hint`, false, false},
		{"envelope form only, --json", token, `stored credential a"b hint`, true, true},
		{"empty token has no guard, text", ``, `anything`, false, false},
		{"empty token has no guard, --json", ``, `anything`, true, false},
	} {
		if got := api.OutputCarriesGuarded(setTokenError(refusal, c.token, c.jsonOut), c.candidate); got != c.want {
			t.Errorf("%s: guard(%q) = %v, want %v", c.name, c.candidate, got, c.want)
		}
	}
}

// Jill's assumptions about the guard wrapper, pinned: it keeps the wrapped
// error's text, type chain and exit code, and never turns a nil into an error.
func TestSetTokenOutputGuardKeepsTheError2761(t *testing.T) {
	const token = `zz-guard-2761`
	refusal := &gqlerror.Error{Message: "refused", Extensions: map[string]any{"code": "FORBIDDEN"}}
	guarded := setTokenError(refusal, token, true)
	unguarded := setTokenErrorUnguarded(refusal, token, true)
	if guarded.Error() != unguarded.Error() {
		t.Errorf("the guard changed the text: %q vs %q", guarded.Error(), unguarded.Error())
	}
	if carriedCode(guarded) != carriedCode(unguarded) || carriedCode(guarded) != "FORBIDDEN" {
		t.Errorf("the guard lost the server code: %q vs %q", carriedCode(guarded), carriedCode(unguarded))
	}
	if g, u := exitcode.FromError(guarded), exitcode.FromError(unguarded); g != u {
		t.Errorf("the guard changed the exit code: %d vs %d", g, u)
	}
	if got := api.WithOutputGuard(nil, func(string) bool { return true }); got != nil {
		t.Errorf("a nil error must stay nil, got %v", got)
	}
}
