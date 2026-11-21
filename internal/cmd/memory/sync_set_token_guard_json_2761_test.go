package memory

import (
	"testing"

	"github.com/vektah/gqlparser/v2/gqlerror"

	"github.com/hadron-memory/hadron-cli/internal/api"
)

// cli#2761 after the #2831 merge: the output guard that judges text a later layer
// appends (renderError's stored-credential hint) must apply the --json envelope
// predicate too. Token a\"b is not in the string a"b, but the encoded envelope
// carries it (cli#2777), so under --json the guard must trip, and in text it must not.
func TestSetTokenOutputGuardJudgesEnvelopeForm2761(t *testing.T) {
	const token = `a\"b`
	const hint = `stored credential a"b hint`
	err := &gqlerror.Error{Message: "refused", Extensions: map[string]any{"code": "FORBIDDEN"}}
	if !api.OutputCarriesGuarded(setTokenError(err, token, true), hint) {
		t.Errorf("--json: a hint that re-forms the token in the envelope must be caught")
	}
	if api.OutputCarriesGuarded(setTokenError(err, token, false), hint) {
		t.Errorf("text: the hint does not carry the token, it must not be caught")
	}
}
