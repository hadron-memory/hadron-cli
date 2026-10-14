package cmd

import (
	"encoding/json"
	"sort"
	"strings"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/exitcode"
)

// cli#818 — passkey-signed node revisions over #1864's contract.

const signingRequestJSON = `{"id":"sr1","nodeId":"n1","nodeUrn":"hrn:node:acme.com:kb:findings:flaky-ci","revision":4,` +
	`"approvalHash":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","payloadVersion":"hadron-signature-v1",` +
	`"state":"OPEN","createdAt":"2026-10-14T06:00:00Z","expiresAt":"2026-10-15T06:00:00Z","endedAt":null,` +
	`"namedSigner":null,"requestedBy":{"userId":"u1","appId":null,"workerId":"w1","sessionId":"s1"},"signature":null}`

const signatureJSON = `{"id":"sig1","state":"CURRENT","revision":4,"verifiedAt":"2026-10-14T06:05:00Z","passkeyLabel":"YubiKey 5C",` +
	`"passkeyRevokedAt":null,"payloadVersion":"hadron-signature-v1","signingRequestId":"sr1","signer":{"handle":"ada","urn":"hrn:user:ada"}}`

func signatureStatusJSON(state string, integrity int, sigs string) string {
	if state != "null" {
		state = `"` + state + `"`
	}
	return `{"data":{"node":{"id":"n1","urn":"hrn:node:acme.com:kb:findings:flaky-ci","loc":"findings:flaky-ci","revision":4,` +
		`"signatureStatus":{"state":` + state + `,"integrityProblems":` + string(rune('0'+integrity)) + `,"signatures":[` + sigs + `]}}}}`
}

func refusalJSON(code string) string {
	return `{"errors":[{"message":"refused: ` + code + `","extensions":{"code":"` + code + `"}}],"data":null}`
}

func TestNodeSignSendsOnlyTheFlagsGivenAndKeepsTheRequest(t *testing.T) {
	cases := []struct {
		name string
		args []string
		want map[string]any
	}{
		{"bare", nil, map[string]any{"nodeRef": "n1"}},
		{"pinned", []string{"--revision", "4", "--signer", "u2"}, map[string]any{"nodeRef": "n1", "expectedRevision": float64(4), "signerRef": "u2"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			gql, captured := captureGraphQL(t, map[string]string{
				"ResolveUrn":           resolveNodeJSON,
				"RequestNodeSignature": `{"data":{"requestNodeSignature":` + signingRequestJSON + `}}`,
			})
			f, out := testFactory(t)
			root := NewRootCmd(f)
			root.SetArgs(append([]string{"node", "sign", nodeURN, "--json", "--server", gql.URL}, tc.args...))
			if err := root.Execute(); err != nil {
				t.Fatal(err)
			}
			// Omitted, not null: the variables carry exactly the flags given.
			var vars map[string]any
			if err := json.Unmarshal(captured["RequestNodeSignature"], &vars); err != nil {
				t.Fatal(err)
			}
			if len(vars) != len(tc.want) {
				t.Errorf("variables = %v, want %v", vars, tc.want)
			}
			for k, v := range tc.want {
				if vars[k] != v {
					t.Errorf("variable %s = %v, want %v", k, vars[k], v)
				}
			}
			var dto struct {
				ID          string  `json:"id"`
				State       string  `json:"state"`
				Revision    int     `json:"revision"`
				NamedSigner *string `json:"namedSigner"`
				RequestedBy struct {
					WorkerID string `json:"workerId"`
				} `json:"requestedBy"`
				Signature *struct{} `json:"signature"`
			}
			if err := json.Unmarshal([]byte(out.String()), &dto); err != nil {
				t.Fatal(err)
			}
			if dto.ID != "sr1" || dto.State != "OPEN" || dto.Revision != 4 || dto.RequestedBy.WorkerID != "w1" || dto.Signature != nil || dto.NamedSigner != nil {
				t.Errorf("request = %+v", dto)
			}
		})
	}
}

func TestNodeSignRefusesBadFlagsBeforeAnyCall(t *testing.T) {
	for _, args := range [][]string{{"--revision", "0"}, {"--signer", " "}} {
		gql, captured := captureGraphQL(t, map[string]string{})
		f, _ := testFactory(t)
		root := NewRootCmd(f)
		root.SetArgs(append([]string{"node", "sign", nodeURN, "--server", gql.URL}, args...))
		if code := exitCodeFor(root.Execute()); code != exitcode.Usage {
			t.Errorf("%v: exit = %d, want 2", args, code)
		}
		if len(captured) != 0 {
			t.Errorf("%v: calls made: %v", args, captured)
		}
	}
}

func TestSigningRefusalsMapToTheirExitCodes(t *testing.T) {
	cases := []struct {
		op, code string
		args     []string
		want     int
	}{
		{"RequestNodeSignature", "NODE_NOT_FOUND", []string{"node", "sign", nodeURN}, exitcode.NotFound},
		{"RequestNodeSignature", "ENCRYPTED_MEMORY_NOT_APPLICABLE", []string{"node", "sign", nodeURN}, exitcode.Conflict},
		{"RequestNodeSignature", "COMMENT_NOT_SIGNABLE", []string{"node", "sign", nodeURN}, exitcode.Usage},
		{"RequestNodeSignature", "NODE_IS_PLACEHOLDER", []string{"node", "sign", nodeURN}, exitcode.Conflict},
		{"RequestNodeSignature", "REVISION_MISMATCH", []string{"node", "sign", nodeURN, "--revision", "3"}, exitcode.Conflict},
		{"RequestNodeSignature", "SIGNER_NOT_FOUND", []string{"node", "sign", nodeURN, "--signer", "nobody"}, exitcode.NotFound},
		{"WithdrawSigningRequest", "SIGNING_REQUEST_NOT_FOUND", []string{"node", "signing-request", "withdraw", "sr1"}, exitcode.NotFound},
		{"WithdrawSigningRequest", "SIGNING_REQUEST_NOT_REQUESTER", []string{"node", "signing-request", "withdraw", "sr1"}, exitcode.Forbidden},
		{"WithdrawSigningRequest", "SIGNING_REQUEST_ENDED", []string{"node", "signing-request", "withdraw", "sr1"}, exitcode.Conflict},
	}
	for _, tc := range cases {
		t.Run(tc.code, func(t *testing.T) {
			gql, _ := captureGraphQL(t, map[string]string{"ResolveUrn": resolveNodeJSON, tc.op: refusalJSON(tc.code)})
			f, _ := testFactory(t)
			root := NewRootCmd(f)
			root.SetArgs(append(tc.args, "--server", gql.URL))
			if code := exitCodeFor(root.Execute()); code != tc.want {
				t.Errorf("exit = %d, want %d", code, tc.want)
			}
		})
	}
}

// A server with its signing switch off (hadron-server#1895) has no signing
// schema at all. Its refusal must not read as CLI/server version skew.
func TestSigningOnAServerWithoutSigningSaysSo(t *testing.T) {
	cases := []struct {
		op, field string
		args      []string
	}{
		{"RequestNodeSignature", "requestNodeSignature", []string{"node", "sign", nodeURN}},
		{"NodeSignatureStatus", "signatureStatus", []string{"node", "signatures", nodeURN}},
		{"NodeSigningRequests", "signingRequests", []string{"node", "signing-request", "list", nodeURN}},
		{"GetSigningRequest", "signingRequest", []string{"node", "signing-request", "get", "sr1"}},
		{"WithdrawSigningRequest", "withdrawSigningRequest", []string{"node", "signing-request", "withdraw", "sr1"}},
	}
	for _, tc := range cases {
		t.Run(tc.op, func(t *testing.T) {
			gql, _ := captureGraphQL(t, map[string]string{"ResolveUrn": resolveNodeJSON, tc.op: missingFieldJSON(tc.field)})
			f, _ := testFactory(t)
			root := NewRootCmd(f)
			root.SetArgs(append(tc.args, "--server", gql.URL))
			err := root.Execute()
			if code := exitCodeFor(err); code != exitcode.Usage {
				t.Fatalf("exit = %d, want 2", code)
			}
			if !strings.Contains(err.Error(), "does not offer node signing") || strings.Contains(err.Error(), "upgrade") {
				t.Errorf("message = %q", err.Error())
			}
		})
	}
}

func TestNodeSignaturesCheckExitsOnlyOnACleanCurrentState(t *testing.T) {
	earlier := strings.Replace(signatureJSON, `"CURRENT"`, `"EARLIER"`, 1)
	cases := []struct {
		name string
		resp string
		want int
	}{
		{"current", signatureStatusJSON("CURRENT", 0, signatureJSON), exitcode.OK},
		{"earlier", signatureStatusJSON("EARLIER", 0, earlier), exitcode.Conflict},
		{"current-with-integrity-problem", signatureStatusJSON("CURRENT", 1, signatureJSON), exitcode.Conflict},
		{"none", signatureStatusJSON("null", 0, ""), exitcode.Conflict},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			gql, _ := captureGraphQL(t, map[string]string{"ResolveUrn": resolveNodeJSON, "NodeSignatureStatus": tc.resp})
			f, out := testFactory(t)
			root := NewRootCmd(f)
			root.SetArgs([]string{"node", "signatures", nodeURN, "--check", "--json", "--server", gql.URL})
			if code := exitCodeFor(root.Execute()); code != tc.want {
				t.Errorf("exit = %d, want %d", code, tc.want)
			}
			// The status prints before the exit, whatever it is.
			if !strings.Contains(out.String(), `"integrityProblems"`) {
				t.Errorf("status not printed: %s", out.String())
			}
		})
	}
	// Without --check every state exits 0.
	gql, _ := captureGraphQL(t, map[string]string{"ResolveUrn": resolveNodeJSON, "NodeSignatureStatus": signatureStatusJSON("EARLIER", 0, earlier)})
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"node", "signatures", nodeURN, "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Errorf("without --check: %v", err)
	}
}

func TestNodeSignaturesJSONShape(t *testing.T) {
	gql, _ := captureGraphQL(t, map[string]string{"ResolveUrn": resolveNodeJSON, "NodeSignatureStatus": signatureStatusJSON("null", 0, "")})
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"node", "signatures", nodeURN, "--json", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	// No signatures: state null, signatures [] (not null).
	if !strings.Contains(out.String(), `"state": null`) || !strings.Contains(out.String(), `"signatures": []`) {
		t.Errorf("empty status = %s", out.String())
	}

	gql, _ = captureGraphQL(t, map[string]string{"ResolveUrn": resolveNodeJSON, "NodeSignatureStatus": signatureStatusJSON("CURRENT", 0, signatureJSON)})
	f, out = testFactory(t)
	root = NewRootCmd(f)
	root.SetArgs([]string{"node", "signatures", nodeURN, "--json", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	var dto struct {
		Signatures []map[string]json.RawMessage `json:"signatures"`
	}
	if err := json.Unmarshal([]byte(out.String()), &dto); err != nil {
		t.Fatal(err)
	}
	if len(dto.Signatures) != 1 {
		t.Fatalf("signatures = %s", out.String())
	}
	// The signature's public shape, pinned whole: a passkey is shown by its
	// label only (cor:dmo:130:06), so a field added here is a contract change.
	var keys []string
	for k := range dto.Signatures[0] {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	want := "id,passkeyLabel,passkeyRevokedAt,payloadVersion,revision,signer,signingRequestId,state,verifiedAt"
	if got := strings.Join(keys, ","); got != want {
		t.Errorf("signature keys = %s, want %s", got, want)
	}
}

func TestSigningRequestListFilters(t *testing.T) {
	gql, captured := captureGraphQL(t, map[string]string{
		"ResolveUrn":          resolveNodeJSON,
		"NodeSigningRequests": `{"data":{"node":{"id":"n1","urn":"hrn:node:acme.com:kb:findings:flaky-ci","loc":"findings:flaky-ci","signingRequests":[` + signingRequestJSON + `]}}}`,
	})
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"node", "signing-request", "list", nodeURN, "--state", "open", "--limit", "5", "--json", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	var vars struct {
		Ref   string `json:"ref"`
		State string `json:"state"`
		First int    `json:"first"`
	}
	if err := json.Unmarshal(captured["NodeSigningRequests"], &vars); err != nil {
		t.Fatal(err)
	}
	if vars.Ref != "n1" || vars.State != "OPEN" || vars.First != 5 {
		t.Errorf("variables = %+v", vars)
	}
	var dto struct {
		Requests []struct {
			ID string `json:"id"`
		} `json:"requests"`
	}
	if err := json.Unmarshal([]byte(out.String()), &dto); err != nil {
		t.Fatal(err)
	}
	if len(dto.Requests) != 1 || dto.Requests[0].ID != "sr1" {
		t.Errorf("requests = %s", out.String())
	}

	// Unfiltered: neither variable is sent.
	gql, captured = captureGraphQL(t, map[string]string{
		"ResolveUrn":          resolveNodeJSON,
		"NodeSigningRequests": `{"data":{"node":{"id":"n1","urn":"hrn:node:acme.com:kb:findings:flaky-ci","loc":"findings:flaky-ci","signingRequests":[]}}}`,
	})
	f, out = testFactory(t)
	root = NewRootCmd(f)
	root.SetArgs([]string{"node", "signing-request", "list", nodeURN, "--json", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	var raw map[string]any
	_ = json.Unmarshal(captured["NodeSigningRequests"], &raw)
	if _, ok := raw["state"]; ok {
		t.Errorf("state sent unfiltered: %v", raw)
	}
	if _, ok := raw["first"]; ok {
		t.Errorf("first sent without --limit: %v", raw)
	}
	if !strings.Contains(out.String(), `"requests": []`) {
		t.Errorf("empty list = %s", out.String())
	}
}

func TestSigningRequestListRefusesBadFiltersBeforeAnyCall(t *testing.T) {
	for _, args := range [][]string{{"--state", "invalid"}, {"--state", "pending"}, {"--limit", "0"}, {"--limit", "101"}} {
		gql, captured := captureGraphQL(t, map[string]string{})
		f, _ := testFactory(t)
		root := NewRootCmd(f)
		root.SetArgs(append([]string{"node", "signing-request", "list", nodeURN, "--server", gql.URL}, args...))
		if code := exitCodeFor(root.Execute()); code != exitcode.Usage {
			t.Errorf("%v: exit = %d, want 2", args, code)
		}
		if len(captured) != 0 {
			t.Errorf("%v: calls made: %v", args, captured)
		}
	}
}

func TestSigningRequestGetUnknownIsNotFound(t *testing.T) {
	gql, _ := captureGraphQL(t, map[string]string{"GetSigningRequest": `{"data":{"signingRequest":null}}`})
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"node", "signing-request", "get", "sr-missing", "--server", gql.URL})
	if code := exitCodeFor(root.Execute()); code != exitcode.NotFound {
		t.Errorf("exit = %d, want 4", code)
	}
}

func TestSigningRequestGetShowsTheCompletedSignature(t *testing.T) {
	completed := strings.Replace(signingRequestJSON, `"state":"OPEN"`, `"state":"COMPLETED"`, 1)
	completed = strings.Replace(completed, `"endedAt":null`, `"endedAt":"2026-10-14T06:05:00Z"`, 1)
	completed = strings.Replace(completed, `"signature":null`, `"signature":`+signatureJSON, 1)
	gql, _ := captureGraphQL(t, map[string]string{"GetSigningRequest": `{"data":{"signingRequest":` + completed + `}}`})
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"node", "signing-request", "get", "sr1", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"sr1  COMPLETED", "ended: 2026-10-14T06:05:00Z", `CURRENT  r4  @ada  passkey "YubiKey 5C"`} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("missing %q in:\n%s", want, out.String())
		}
	}
}
