package node

import (
	"strings"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/api"
	"github.com/hadron-memory/hadron-cli/internal/output"
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
	// Short fragments independent of the constants: rewording a constant must
	// not reword the negative check with it (cli#2540).
	for _, guess := range []string{"does not offer node signing", "ceremony pages", "upgrade the server"} {
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

// cli#2540: the constants select the branch in the tests above; these pin the
// part of each message a reader acts on, so dropping the issue reference or
// the upgrade advice from a constant cannot stay green.
func TestRefusalConstantsKeepTheirAdvice(t *testing.T) {
	for _, tc := range []struct {
		name, msg string
		frags     []string
	}{
		{"PagedListRefusal", PagedListRefusal, []string{"upgrade the server", "hadron-server#4123"}},
		{"CeremonyPagesRefusal", CeremonyPagesRefusal, []string{"upgrade the server", "hadron-server#3120"}},
		{"SigningOffRefusal", SigningOffRefusal, []string{"does not offer node signing"}},
	} {
		for _, frag := range tc.frags {
			if !strings.Contains(tc.msg, frag) {
				t.Errorf("%s lost %q: %s", tc.name, frag, tc.msg)
			}
		}
	}
	// The constants reach exitcode.Newf as arguments, not format strings: a
	// "%" in the wording must come out verbatim.
	if got := mapSigningError(ceremonyRefusal(), func() (bool, error) { return false, nil }).Error(); got != SigningOffRefusal {
		t.Errorf("refusal must carry the constant verbatim: %q", got)
	}
}

// cli#2540 / cli#2541: ceremonyText's rule is "display-clean => verbatim, at
// any length"; anything neutralised routes through SafeCell.
func TestCeremonyTextDisplayCleanIsVerbatim(t *testing.T) {
	long := "https://h.example/" + strings.Repeat("a", 300)
	for name, in := range map[string]string{
		"https":        "https://ceremonies.acme.example/signing/sr1",
		"javascript":   "javascript:alert(1)",
		"ftp":          "ftp://h/x",
		"long-clean":   long,
		"long-badesc":  "https://h.example/%zz" + strings.Repeat("a", 300),
		"short-badesc": "https://h.example/%zz",
	} {
		if got := ceremonyText(in); got != in {
			t.Errorf("%s: clean value must print verbatim:\ngot  %q\nwant %q", name, got, in)
		}
		if got, want := ceremonyText(in), output.SafeCell(in); len(in) <= 256 && got != want {
			t.Errorf("%s: both arms must agree on short clean input: %q vs %q", name, got, want)
		}
	}
	for name, in := range map[string]string{
		"bidi":       "https://h/\u202Eev1l",
		"newline":    "https://h/x\nforged: line",
		"osc":        "https://h/\x1b]8;;evil\x07x",
		"zero-width": "https://h/sr\u200B1",
	} {
		if got, want := ceremonyText(in), output.SafeCell(in); got != want {
			t.Errorf("%s: neutralised value must route through SafeCell:\ngot  %q\nwant %q", name, got, want)
		}
	}
}
