package cmd

// cli#878 part 2 — the node-sign ceremony URL handoff. The server hosts the
// ceremony page and returns its link on SigningRequest.ceremonyUrl
// (hadron-server#3120, String!); the CLI copies it verbatim, prints it in text
// only while the request is OPEN (an ended request's link only explains that it
// ended), and carries it in --json for every state (Wolfgang's author deltas,
// gate comment 6982347689). Red first: on main none of this prints, which is
// exactly Cloe's AT0.7136 reproduction.

import (
	"context"
	"strings"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/cmd/node"
	"github.com/hadron-memory/hadron-cli/internal/exitcode"
)

// ceremonyHost deliberately differs from the fake server's host: an output URL
// containing the --server host would be a composed one, and composing is
// forbidden (the server's own tool text: "copied verbatim, never composed").
const (
	ceremonyURLSr1 = "https://ceremonies.acme.example/signing/sr1"
	ceremonyURLSr2 = "https://ceremonies.acme.example/signing/sr2"
)

// ceremonyRequestJSON is signingRequestJSON with a ceremonyUrl and the given
// state (and endedAt set for ended states).
func ceremonyRequestJSON(state string) string {
	out := strings.Replace(signingRequestJSON, `"state":"OPEN",`,
		`"state":"`+state+`","ceremonyUrl":"`+ceremonyURLSr1+`",`, 1)
	switch state {
	case "OPEN":
		return out
	default:
		return strings.Replace(out, `"endedAt":null`, `"endedAt":"2026-10-14T07:00:00Z"`, 1)
	}
}

// missingSigningFieldJSON is missingFieldJSON with the honest type name, the
// shape an older server answers for a field SigningRequest lacks.
func missingSigningFieldJSON(field, typ string) string {
	return `{"errors":[{"message":"Cannot query field \"` + field + `\" on type \"` + typ + `\".","extensions":{"code":"GRAPHQL_VALIDATION_FAILED"}}],"data":null}`
}

func TestSigningCeremonyURL878GetPrintsTheLinkWhileOpen(t *testing.T) {
	gql, _ := captureGraphQL(t, map[string]string{
		"GetSigningRequest": `{"data":{"signingRequest":` + ceremonyRequestJSON("OPEN") + `}}`,
	})
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"node", "signing-request", "get", "sr1", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	if want := "ceremony: " + ceremonyURLSr1; !strings.Contains(out.String(), want) {
		t.Errorf("an OPEN request must print the ceremony link:\n%s", out.String())
	}
	if strings.Contains(out.String(), gql.URL) {
		t.Errorf("the link must be the server's own URL, not one composed from --server %s:\n%s", gql.URL, out.String())
	}
}

func TestSigningCeremonyURL878GetJSONCarriesIt(t *testing.T) {
	gql, _ := captureGraphQL(t, map[string]string{
		"GetSigningRequest": `{"data":{"signingRequest":` + ceremonyRequestJSON("OPEN") + `}}`,
	})
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"node", "signing-request", "get", "sr1", "--json", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	if want := `"ceremonyUrl":"` + ceremonyURLSr1 + `"`; !strings.Contains(out.String(), want) {
		t.Errorf("--json must carry the server's ceremonyUrl verbatim:\n%s", out.String())
	}
}

// An ended request's link only explains that it ended (the schema's own words):
// no ceremony line in text, while --json still carries what the server sent.
func TestSigningCeremonyURL878EndedRequestHasNoTextLineButJSONCarriesIt(t *testing.T) {
	for _, state := range []string{"COMPLETED", "DECLINED", "EXPIRED", "WITHDRAWN", "STALE"} {
		t.Run(state, func(t *testing.T) {
			gql, _ := captureGraphQL(t, map[string]string{
				"GetSigningRequest": `{"data":{"signingRequest":` + ceremonyRequestJSON(state) + `}}`,
			})
			f, out := testFactory(t)
			root := NewRootCmd(f)
			root.SetArgs([]string{"node", "signing-request", "get", "sr1", "--server", gql.URL})
			if err := root.Execute(); err != nil {
				t.Fatal(err)
			}
			if strings.Contains(out.String(), "ceremony:") {
				t.Errorf("a %s request must not print a ceremony line:\n%s", state, out.String())
			}
			f2, out2 := testFactory(t)
			root2 := NewRootCmd(f2)
			root2.SetArgs([]string{"node", "signing-request", "get", "sr1", "--json", "--server", gql.URL})
			if err := root2.Execute(); err != nil {
				t.Fatal(err)
			}
			if want := `"ceremonyUrl":"` + ceremonyURLSr1 + `"`; !strings.Contains(out2.String(), want) {
				t.Errorf("--json must carry ceremonyUrl for a %s request too:\n%s", state, out2.String())
			}
		})
	}
}

func TestNodeSign878PrintsTheCeremonyLink(t *testing.T) {
	gql, _ := captureGraphQL(t, map[string]string{
		"ResolveUrn":           resolveNodeJSON,
		"RequestNodeSignature": `{"data":{"requestNodeSignature":` + ceremonyRequestJSON("OPEN") + `}}`,
	})
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"node", "sign", nodeURN, "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	if want := "ceremony: " + ceremonyURLSr1; !strings.Contains(out.String(), want) {
		t.Errorf("node sign must hand over the ceremony link:\n%s", out.String())
	}
}

func TestSigningWithdraw878EndedRequestHasNoTextLineButJSONCarriesIt(t *testing.T) {
	gql, _ := captureGraphQL(t, map[string]string{
		"WithdrawSigningRequest": `{"data":{"withdrawSigningRequest":` + ceremonyRequestJSON("WITHDRAWN") + `}}`,
	})
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"node", "signing-request", "withdraw", "sr1", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out.String(), "ceremony:") {
		t.Errorf("a withdrawn request must not print a ceremony line:\n%s", out.String())
	}
	f2, out2 := testFactory(t)
	root2 := NewRootCmd(f2)
	root2.SetArgs([]string{"node", "signing-request", "withdraw", "sr1", "--json", "--server", gql.URL})
	if err := root2.Execute(); err != nil {
		t.Fatal(err)
	}
	if want := `"ceremonyUrl":"` + ceremonyURLSr1 + `"`; !strings.Contains(out2.String(), want) {
		t.Errorf("--json must carry the withdrawn request's ceremonyUrl:\n%s", out2.String())
	}
}

// The list table cannot hold URLs; its OPEN rows get an id-qualified ceremony
// line under the table, ended rows none, and --json carries every row's URL.
func TestSigningRequestList878OpenRowsGetCeremonyLines(t *testing.T) {
	open := ceremonyRequestJSON("OPEN")
	ended := strings.Replace(ceremonyRequestJSON("COMPLETED"), ceremonyURLSr1, ceremonyURLSr2, 1)
	gql, _ := captureGraphQL(t, map[string]string{
		"ResolveUrn":          resolveNodeJSON,
		"NodeSigningRequests": `{"data":{"node":{"id":"n1","urn":"hrn:node:acme.com:kb:findings:flaky-ci","loc":"findings:flaky-ci","signingRequestsPage":{"items":[` + open + `,` + ended + `],"hasMore":false,"nextCursor":null}}}}`,
	})
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"node", "signing-request", "list", nodeURN, "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	if want := "sr1 ceremony: " + ceremonyURLSr1; !strings.Contains(out.String(), want) {
		t.Errorf("the OPEN row must carry its ceremony line:\n%s", out.String())
	}
	if strings.Contains(out.String(), ceremonyURLSr2) {
		t.Errorf("an ended row must not print its link:\n%s", out.String())
	}

	f2, out2 := testFactory(t)
	root2 := NewRootCmd(f2)
	root2.SetArgs([]string{"node", "signing-request", "list", nodeURN, "--json", "--server", gql.URL})
	if err := root2.Execute(); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`"ceremonyUrl":"` + ceremonyURLSr1 + `"`, `"ceremonyUrl":"` + ceremonyURLSr2 + `"`} {
		if !strings.Contains(out2.String(), want) {
			t.Errorf("--json must carry every row's ceremonyUrl (%s missing):\n%s", want, out2.String())
		}
	}
}

// Delta 5, token first (cor:api:020:01): a ceremonyUrl validation refusal
// alone cannot tell an older server from a switched-off one, so the
// node-signing capability token decides. Token present: the bounded
// old-server answer, never a raw GraphQL error, never version sniffing.
func TestSigningCeremonyURL878OldServerSaysUpgrade(t *testing.T) {
	withSigningProbe(t, true, nil)
	gql, _ := captureGraphQL(t, map[string]string{
		"GetSigningRequest": missingSigningFieldJSON("ceremonyUrl", "SigningRequest"),
	})
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"node", "signing-request", "get", "sr1", "--server", gql.URL})
	err := root.Execute()
	if code := exitCodeFor(err); code != exitcode.Usage {
		t.Fatalf("exit = %d, want 2 (%v)", code, err)
	}
	if !strings.Contains(err.Error(), "upgrade the server") || !strings.Contains(err.Error(), "hadron-server#3120") {
		t.Errorf("the old-server message must name the server change: %q", err.Error())
	}
	for _, wrong := range []string{"does not offer node signing", "Cannot query field"} {
		if strings.Contains(err.Error(), wrong) {
			t.Errorf("message must not say %q: %q", wrong, err.Error())
		}
	}
}

// …and with no token (signing off, or discovery failed — which reads the same),
// the shipped signing-off answer stands and the upgrade text never appears.
func TestSigningCeremonyURL878SigningOffNeverSaysUpgrade(t *testing.T) {
	cases := []struct {
		name      string
		advertise bool
		probeErr  error
	}{
		{"switched-off", false, nil},
		{"discovery-failed", false, errProbeDown},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			withSigningProbe(t, tc.advertise, tc.probeErr)
			gql, _ := captureGraphQL(t, map[string]string{
				"GetSigningRequest": missingSigningFieldJSON("ceremonyUrl", "SigningRequest"),
			})
			f, _ := testFactory(t)
			root := NewRootCmd(f)
			root.SetArgs([]string{"node", "signing-request", "get", "sr1", "--server", gql.URL})
			err := root.Execute()
			if code := exitCodeFor(err); code != exitcode.Usage {
				t.Fatalf("exit = %d, want 2 (%v)", code, err)
			}
			if !strings.Contains(err.Error(), "does not offer node signing") {
				t.Errorf("message = %q", err.Error())
			}
			if strings.Contains(err.Error(), "upgrade") {
				t.Errorf("a server that may simply be off must never get upgrade text: %q", err.Error())
			}
		})
	}
}

// A surface-name refusal (the switch is off, the whole schema is gone) keeps
// today's answer WITHOUT reading capabilities: the token probe runs only when
// it disambiguates a ceremonyUrl refusal.
func TestSigningCeremonyURL878SurfaceRefusalNeverProbes(t *testing.T) {
	calls := 0
	prev := node.SigningCapabilityProbe
	node.SigningCapabilityProbe = func(ctx context.Context, baseURL string) (bool, error) {
		calls++
		return false, nil
	}
	t.Cleanup(func() { node.SigningCapabilityProbe = prev })

	gql, _ := captureGraphQL(t, map[string]string{
		"GetSigningRequest": missingSigningFieldJSON("signingRequest", "Query"),
	})
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"node", "signing-request", "get", "sr1", "--server", gql.URL})
	err := root.Execute()
	if code := exitCodeFor(err); code != exitcode.Usage {
		t.Fatalf("exit = %d, want 2 (%v)", code, err)
	}
	if !strings.Contains(err.Error(), "does not offer node signing") {
		t.Errorf("message = %q", err.Error())
	}
	if calls != 0 {
		t.Errorf("capability reads = %d, want 0 for a surface-name refusal", calls)
	}
}
