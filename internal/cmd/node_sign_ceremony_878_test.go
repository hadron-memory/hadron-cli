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
	"encoding/json"
	"strings"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/cmd/node"
	"github.com/hadron-memory/hadron-cli/internal/exitcode"
)

// ceremonyURLField asserts the server's ceremonyUrl came through --json
// verbatim. --json is indented by contract, so the field is read by
// unmarshalling, never by a compact substring.
func ceremonyURLField(t *testing.T, out string) any {
	t.Helper()
	var dto map[string]any
	if err := json.Unmarshal([]byte(out), &dto); err != nil {
		t.Fatal(err, out)
	}
	return dto["ceremonyUrl"]
}

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
	if got := ceremonyURLField(t, out.String()); got != ceremonyURLSr1 {
		t.Errorf("--json must carry the server's ceremonyUrl verbatim, got %v:\n%s", got, out.String())
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
			if got := ceremonyURLField(t, out2.String()); got != ceremonyURLSr1 {
				t.Errorf("--json must carry ceremonyUrl for a %s request too, got %v:\n%s", state, got, out2.String())
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
	if got := ceremonyURLField(t, out2.String()); got != ceremonyURLSr1 {
		t.Errorf("--json must carry the withdrawn request's ceremonyUrl, got %v:\n%s", got, out2.String())
	}
}

// The list table cannot hold URLs; its OPEN rows get an id-qualified ceremony
// line under the table, ended rows none, and --json carries every row's URL.
func TestSigningRequestList878OpenRowsGetCeremonyLines(t *testing.T) {
	open := ceremonyRequestJSON("OPEN")
	// The ended row carries its OWN id, so the id-qualified line can be
	// checked against the row that printed it (BLIND F7).
	ended := strings.Replace(ceremonyRequestJSON("COMPLETED"), ceremonyURLSr1, ceremonyURLSr2, 1)
	ended = strings.Replace(ended, `"id":"sr1"`, `"id":"sr2"`, 1)
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
	if strings.Contains(out.String(), "sr2 ceremony:") || strings.Contains(out.String(), ceremonyURLSr2) {
		t.Errorf("an ended row must not print its link:\n%s", out.String())
	}

	f2, out2 := testFactory(t)
	root2 := NewRootCmd(f2)
	root2.SetArgs([]string{"node", "signing-request", "list", nodeURN, "--json", "--server", gql.URL})
	if err := root2.Execute(); err != nil {
		t.Fatal(err)
	}
	// --json is indented by contract, so read the rows by unmarshalling; the
	// two fixture rows share an id, so the check is positional.
	var listDTO struct {
		Requests []struct {
			CeremonyURL string `json:"ceremonyUrl"`
		} `json:"requests"`
	}
	if err := json.Unmarshal([]byte(out2.String()), &listDTO); err != nil {
		t.Fatal(err, out2.String())
	}
	if len(listDTO.Requests) != 2 ||
		listDTO.Requests[0].CeremonyURL != ceremonyURLSr1 ||
		listDTO.Requests[1].CeremonyURL != ceremonyURLSr2 {
		t.Errorf("--json must carry every row's own ceremonyUrl, got %+v:\n%s", listDTO.Requests, out2.String())
	}
}

// The list row's ceremony line goes through the same neutralisation as the
// get path's: a hostile value in an OPEN row must not reach the terminal raw,
// while --json keeps it. Without this pin, replacing the list row's
// ceremonyText with the raw URL leaves the suite green (the get-path pins
// cover signing-request get only).
func TestSigningRequestList878HostileValueIsNeutralised(t *testing.T) {
	hostile := "https://ceremonies.acme.example/\x1b]8;;https://evil.example\x07link\x1b]8;;\x07\nspoofed"
	hostileJSON, err := json.Marshal(hostile)
	if err != nil {
		t.Fatal(err)
	}
	open := strings.Replace(ceremonyRequestJSON("OPEN"), ceremonyURLSr1, strings.Trim(string(hostileJSON), `"`), 1)
	gql, _ := captureGraphQL(t, map[string]string{
		"ResolveUrn":          resolveNodeJSON,
		"NodeSigningRequests": `{"data":{"node":{"id":"n1","urn":"hrn:node:acme.com:kb:findings:flaky-ci","loc":"findings:flaky-ci","signingRequestsPage":{"items":[` + open + `],"hasMore":false,"nextCursor":null}}}}`,
	})
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"node", "signing-request", "list", nodeURN, "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	got := out.String()
	if strings.Contains(got, "\x1b") {
		t.Errorf("the list ceremony line must not emit escape sequences raw:\n%q", got)
	}
	if n := strings.Count(got, "\nspoofed"); n != 0 {
		t.Errorf("an embedded newline must not forge an output line:\n%q", got)
	}
	if n := strings.Count(got, "ceremony:"); n != 1 {
		t.Errorf("exactly one ceremony line, got %d:\n%q", n, got)
	}
	f2, out2 := testFactory(t)
	root2 := NewRootCmd(f2)
	root2.SetArgs([]string{"node", "signing-request", "list", nodeURN, "--json", "--server", gql.URL})
	if err := root2.Execute(); err != nil {
		t.Fatal(err)
	}
	var listDTO struct {
		Requests []struct {
			CeremonyURL string `json:"ceremonyUrl"`
		} `json:"requests"`
	}
	if err := json.Unmarshal([]byte(out2.String()), &listDTO); err != nil {
		t.Fatal(err, out2.String())
	}
	if len(listDTO.Requests) != 1 || listDTO.Requests[0].CeremonyURL != hostile {
		t.Errorf("--json must keep the hostile row's raw ceremonyUrl, got %+v", listDTO.Requests)
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

// BLIND F1: the ceremony line exists to be opened, so a hostile value must
// not reach the terminal raw. An OSC 8 hyperlink can hide the real target,
// OSC 52 writes the clipboard, an embedded newline forges lines: none may
// survive. A clean absolute web address still prints byte for byte (the
// pins above), and --json keeps the raw value.
func TestSigningCeremonyURL878HostileValueIsNeutralised(t *testing.T) {
	hostile := "https://ceremonies.acme.example/\x1b]8;;https://evil.example\x07link\x1b]8;;\x07\nspoofed"
	// The wire form is JSON-escaped control characters (raw ESC bytes are not
	// valid JSON), so splice in the marshalled spelling: the decoded value the
	// CLI receives is still the raw hostile string above.
	hostileJSON, err := json.Marshal(hostile)
	if err != nil {
		t.Fatal(err)
	}
	gql, _ := captureGraphQL(t, map[string]string{
		"GetSigningRequest": `{"data":{"signingRequest":` + strings.Replace(ceremonyRequestJSON("OPEN"), ceremonyURLSr1, strings.Trim(string(hostileJSON), `"`), 1) + `}}`,
	})
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"node", "signing-request", "get", "sr1", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	got := out.String()
	if strings.Contains(got, "\x1b") {
		t.Errorf("the ceremony line must not emit escape sequences raw:\n%q", got)
	}
	if n := strings.Count(got, "\nspoofed"); n != 0 {
		t.Errorf("an embedded newline must not forge an output line:\n%q", got)
	}
	if strings.Count(got, "ceremony:") != 1 {
		t.Errorf("exactly one ceremony line, got %d:\n%q", strings.Count(got, "ceremony:"), got)
	}
}

// The neutralisation guard must catch what url.Parse accepts: format
// characters (a bidi override reorders the line, a zero-width rune forges
// look-alike URLs) parse fine, so the parse check alone would print them
// verbatim. Each becomes a single space, like any table cell.
func TestSigningCeremonyURL878FormatCharactersAreNeutralised(t *testing.T) {
	for name, hostile := range map[string]string{
		"bidi-override": "https://ceremonies.acme.example/signing/\u202Eev1l",
		"zero-width":    "https://ceremonies.acme.example/signing/sr\u200B1",
	} {
		t.Run(name, func(t *testing.T) {
			hostileJSON, err := json.Marshal(hostile)
			if err != nil {
				t.Fatal(err)
			}
			gql, _ := captureGraphQL(t, map[string]string{
				"GetSigningRequest": `{"data":{"signingRequest":` + strings.Replace(ceremonyRequestJSON("OPEN"), ceremonyURLSr1, strings.Trim(string(hostileJSON), `"`), 1) + `}}`,
			})
			f, out := testFactory(t)
			root := NewRootCmd(f)
			root.SetArgs([]string{"node", "signing-request", "get", "sr1", "--server", gql.URL})
			if err := root.Execute(); err != nil {
				t.Fatal(err)
			}
			got := out.String()
			for _, bad := range []rune{'\u202E', '\u200B'} {
				if strings.ContainsRune(got, bad) {
					t.Errorf("the ceremony line must not carry %q raw:\n%q", bad, got)
				}
			}
			want := "ceremony: " + strings.Map(func(r rune) rune {
				if r == '\u202E' || r == '\u200B' {
					return ' '
				}
				return r
			}, hostile)
			if !strings.Contains(got, want) {
				t.Errorf("the ceremony line must neutralise the format character in place:\nwant: %s\ngot:  %s", want, got)
			}
		})
	}
}

// BLIND F6: an OPEN request whose server sent an empty URL prints no
// ceremony line (the != "" guard is load-bearing).
func TestSigningCeremonyURL878EmptyURLOpenRequestPrintsNoLine(t *testing.T) {
	noURL := strings.Replace(ceremonyRequestJSON("OPEN"), `"ceremonyUrl":"`+ceremonyURLSr1+`"`, `"ceremonyUrl":""`, 1)
	gql, _ := captureGraphQL(t, map[string]string{
		"GetSigningRequest": `{"data":{"signingRequest":` + noURL + `}}`,
	})
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"node", "signing-request", "get", "sr1", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out.String(), "ceremony:") {
		t.Errorf("an empty ceremonyUrl must print no line:\n%s", out.String())
	}
}

// BLIND F4: the old-server refusal is every signing command's refusal — the
// shared fragment carries the field — so each gets the bounded message, not
// a raw GraphQL error.
func TestSigningCeremonyURL878OldServerRefusalAcrossCommands(t *testing.T) {
	withSigningProbe(t, true, nil)
	refusal := missingSigningFieldJSON("ceremonyUrl", "SigningRequest")
	cases := []struct {
		name string
		ops  map[string]string
		args []string
	}{
		{"sign", map[string]string{
			"ResolveUrn":           resolveNodeJSON,
			"RequestNodeSignature": refusal,
		}, []string{"node", "sign", nodeURN}},
		{"withdraw", map[string]string{
			"WithdrawSigningRequest": refusal,
		}, []string{"node", "signing-request", "withdraw", "sr1"}},
		{"list", map[string]string{
			"ResolveUrn":          resolveNodeJSON,
			"NodeSigningRequests": refusal,
		}, []string{"node", "signing-request", "list", nodeURN}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			gql, _ := captureGraphQL(t, tc.ops)
			f, _ := testFactory(t)
			root := NewRootCmd(f)
			root.SetArgs(append(append([]string{}, tc.args...), "--server", gql.URL))
			err := root.Execute()
			if code := exitCodeFor(err); code != exitcode.Usage {
				t.Fatalf("exit = %d, want 2 (%v)", code, err)
			}
			if !strings.Contains(err.Error(), "hadron-server#3120") {
				t.Errorf("the old-server message must name the server change: %q", err.Error())
			}
		})
	}
}

// BLIND F4 (ordering): a signingRequestsPage refusal is decided before the
// ceremony branch — the paged-list message stands and the capability token
// is never read.
func TestSigningCeremonyURL878PagedListRefusalWinsAndNeverProbes(t *testing.T) {
	calls := 0
	prev := node.SigningCapabilityProbe
	node.SigningCapabilityProbe = func(ctx context.Context, baseURL string) (bool, error) {
		calls++
		return true, nil
	}
	t.Cleanup(func() { node.SigningCapabilityProbe = prev })

	gql, _ := captureGraphQL(t, map[string]string{
		"ResolveUrn":          resolveNodeJSON,
		"NodeSigningRequests": missingSigningFieldJSON("signingRequestsPage", "Node"),
	})
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"node", "signing-request", "list", nodeURN, "--server", gql.URL})
	err := root.Execute()
	if code := exitCodeFor(err); code != exitcode.Usage {
		t.Fatalf("exit = %d, want 2 (%v)", code, err)
	}
	if !strings.Contains(err.Error(), "paged signing-request list") || !strings.Contains(err.Error(), "hadron-server#4123") {
		t.Errorf("the paged-list refusal must keep its own message: %q", err.Error())
	}
	if calls != 0 {
		t.Errorf("capability reads = %d, want 0", calls)
	}
}

// BLIND F5: the probe reads the command's own --server, not a default.
func TestSigningCeremonyURL878ProbeReadsTheCommandServer(t *testing.T) {
	var probed []string
	prev := node.SigningCapabilityProbe
	node.SigningCapabilityProbe = func(ctx context.Context, baseURL string) (bool, error) {
		probed = append(probed, baseURL)
		return true, nil
	}
	t.Cleanup(func() { node.SigningCapabilityProbe = prev })

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
	if len(probed) != 1 || probed[0] != gql.URL {
		t.Errorf("probe read %v, want exactly [%s]", probed, gql.URL)
	}
}

// Rodolfo's P3-1 / BLIND F4 remainder: the check path's probe wiring.
// `node signatures --check` reaches mapSigningError through
// localChecker.request, and only the owning command's wiring hands the
// checker a probe — without it a nil probe would give the generic advice on
// an old server with signing. The advertised token must decide: exit 2, the
// bounded old-server message (kills the wiring mutant).
func TestSigningCeremonyURL878CheckPathCarriesTheProbe(t *testing.T) {
	probed := withSigningProbe(t, true, nil)
	responses := l879Responses("CURRENT", 0, l879Sig("hadron-signature-v3", 4), l879Request1(l879GoodHash()))
	responses["GetSigningRequest"] = missingSigningFieldJSON("ceremonyUrl", "SigningRequest")
	gql, _ := captureGraphQL(t, responses)
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"node", "signatures", nodeURN, "--check", "--server", gql.URL})
	err := root.Execute()
	if code := exitCodeFor(err); code != exitcode.Usage {
		t.Fatalf("exit = %d, want 2 (%v)", code, err)
	}
	if !strings.Contains(err.Error(), "hadron-server#3120") {
		t.Errorf("the check path must give the bounded old-server answer: %q", err.Error())
	}
	// The probe must read the command's own --server, exactly as the get path
	// does: a check run against one server must not consult another's switch.
	if len(*probed) != 1 || (*probed)[0] != gql.URL {
		t.Errorf("probe read %v, want exactly [%s]", *probed, gql.URL)
	}
}

// The check path's unadvertised leg: a server whose switch is off (or whose
// discovery failed, which reads the same) gets the signing-off answer, never
// upgrade text — the same rule the get path already pins.
func TestSigningCeremonyURL878CheckPathSigningOffNeverSaysUpgrade(t *testing.T) {
	withSigningProbe(t, false, nil)
	responses := l879Responses("CURRENT", 0, l879Sig("hadron-signature-v3", 4), l879Request1(l879GoodHash()))
	responses["GetSigningRequest"] = missingSigningFieldJSON("ceremonyUrl", "SigningRequest")
	gql, _ := captureGraphQL(t, responses)
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"node", "signatures", nodeURN, "--check", "--server", gql.URL})
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
}
