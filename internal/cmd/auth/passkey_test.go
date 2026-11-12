package auth

// cli#878 part 1 — pure logic of `auth passkey list`: the state derivation at
// an injected clock, the DTO mapping, and that the operation this binary sends
// selects no key material. Command-level behaviour (capability gate, refusals,
// output) is in internal/cmd/auth_passkey_878_test.go.

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/vektah/gqlparser/v2/ast"
	"github.com/vektah/gqlparser/v2/parser"

	"github.com/hadron-memory/hadron-cli/internal/api/gen"
)

func strp(s string) *string { return &s }

var stateNow = time.Date(2026, 11, 12, 18, 0, 0, 0, time.UTC)

func TestDerivePasskeyState(t *testing.T) {
	future := stateNow.Add(time.Hour).Format(time.RFC3339Nano)
	past := stateNow.Add(-time.Hour).Format(time.RFC3339Nano)
	at := stateNow.Format(time.RFC3339Nano)
	cases := []struct {
		name string
		dto  passkeyDTO
		want string
	}{
		{"no window", passkeyDTO{}, passkeyActive},
		{"open window ahead", passkeyDTO{Window: &passkeyWindowDTO{EndsAt: future}}, passkeyCoolingOff},
		{"revoked wins over an open window", passkeyDTO{RevokedAt: strp(past), Window: &passkeyWindowDTO{EndsAt: future}}, passkeyRevoked},
		{"revoked by its cancel link", passkeyDTO{RevokedAt: strp(past), Window: &passkeyWindowDTO{EndsAt: future, EndedAt: strp(past), Outcome: strp("cancelled")}, Cancel: &passkeyCancelDTO{At: past}}, passkeyRevoked},
		{"window ended early", passkeyDTO{Window: &passkeyWindowDTO{EndsAt: future, EndedAt: strp(past), Outcome: strp("ended-early")}}, passkeyActive},
		{"window ran out, outcome still null", passkeyDTO{Window: &passkeyWindowDTO{EndsAt: past}}, passkeyActive},
		{"endsAt exactly now is over", passkeyDTO{Window: &passkeyWindowDTO{EndsAt: at}}, passkeyActive},
		{"unparsable endsAt claims nothing stronger", passkeyDTO{Window: &passkeyWindowDTO{EndsAt: "soon"}}, passkeyCoolingOff},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := derivePasskeyState(tc.dto, stateNow); got != tc.want {
				t.Fatalf("state %q, want %q", got, tc.want)
			}
		})
	}
}

// The server's SDL makes transports non-null, so no legal response carries
// null; this pins the DTO's own guard (robustness only, not a server shape).
func TestPasskeyDTONormalizesNilTransports(t *testing.T) {
	d := toPasskeyDTO(&passkeyRow{Id: "pk1", Label: "Key", RegisteredAt: "2026-11-01T00:00:00Z"}, stateNow)
	b, err := json.Marshal(d)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`"transports":[]`, `"window":null`, `"cancel":null`, `"lastUsedAt":null`, `"revokedAt":null`, `"state":"active"`} {
		if !strings.Contains(string(b), want) {
			t.Fatalf("missing %s in %s", want, b)
		}
	}
}

// The operation compiled into this binary selects no key material. The SDL
// has none of these names on PasskeyCredential today; this keeps a future
// edit of passkeys.graphql from asking for one if the server ever adds it.
func TestMyPasskeysSelectsNoKeyMaterial(t *testing.T) {
	doc, err := parser.ParseQuery(&ast.Source{Input: gen.MyPasskeys_Operation})
	if err != nil {
		t.Fatal(err)
	}
	forbidden := map[string]bool{}
	for _, n := range keyMaterialNames {
		forbidden[strings.ToLower(n)] = true
	}
	var walk func(ast.SelectionSet)
	seen := 0
	walk = func(set ast.SelectionSet) {
		for _, s := range set {
			if f, ok := s.(*ast.Field); ok {
				seen++
				if forbidden[strings.ToLower(f.Name)] {
					t.Errorf("MyPasskeys selects %q", f.Name)
				}
				walk(f.SelectionSet)
			}
		}
	}
	for _, op := range doc.Operations {
		walk(op.SelectionSet)
	}
	if seen < 10 {
		t.Fatalf("walked only %d fields: the parse did not reach the selection", seen)
	}
}

// keyMaterialNames are the fields the server withholds over a bearer
// (hadron-server resolvers.passkeys.ts, #1631 §10). Shared with the
// command-level --json grep.
var keyMaterialNames = []string{"credentialId", "publicKey", "ip", "ipAddress", "userAgent"}
