package node

import (
	"strings"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/api"
	"github.com/vektah/gqlparser/v2/gqlerror"
)

// ceremonyRefusal is the wire shape an older server answers for
// SigningRequest.ceremonyUrl (hadron-server#3120): the framework's own
// rejection of the selected field, code GRAPHQL_VALIDATION_FAILED.
func ceremonyRefusal() error {
	return gqlerror.List{&gqlerror.Error{
		Message:    `Cannot query field "ceremonyUrl" on type "SigningRequest".`,
		Extensions: map[string]any{"code": "GRAPHQL_VALIDATION_FAILED"},
	}}
}

// With no capability probe at hand the mapping must not guess between the
// two signing answers. It is exactly the generic mapping —
// never the signing-off answer the signingFields loop would give, and never
// the ceremony-pages upgrade text.
func TestMapSigningErrorNilProbeGetsGenericMapping(t *testing.T) {
	err := ceremonyRefusal()
	got := mapSigningError(err, nil)
	if want := api.MapError(err); got.Error() != want.Error() {
		t.Errorf("nil probe must get the generic mapping:\ngot:  %s\nwant: %s", got, want)
	}
	for _, guess := range []string{SigningOffRefusal, CeremonyPagesRefusal} {
		if strings.Contains(got.Error(), guess) {
			t.Errorf("nil probe must not guess an answer (%q): %s", guess, got)
		}
	}
}

// The two answers a probe at hand can give stay where they were: advertised
// is the bounded old-server text, unadvertised is signing-off. (Discovery
// failure reads as unadvertised; those two share the e2e pins in the cmd
// package's 878 suite.)
func TestMapSigningErrorProbeAnswersUnchanged(t *testing.T) {
	err := ceremonyRefusal()
	if got := mapSigningError(err, func() (bool, error) { return true, nil }); !strings.Contains(got.Error(), CeremonyPagesRefusal) {
		t.Errorf("an advertised switch must get the old-server text: %s", got)
	}
	if got := mapSigningError(err, func() (bool, error) { return false, nil }); !strings.Contains(got.Error(), SigningOffRefusal) {
		t.Errorf("an unadvertised switch must get the signing-off text: %s", got)
	}
}
