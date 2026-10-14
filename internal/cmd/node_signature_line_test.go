package cmd

// cli#878 part 3 — the signature-status summary in `node get`, gated on the
// server's `node-signing` capability (GET /capabilities, hadron-server#1925,
// mounted by #1930). The gate is the whole point: a switched-off or older
// server must cost no extra refused round trip, and an unreadable status is
// null (text: unavailable), never a guessed zero — the commentSummary
// precedent.

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/cmd/node"
)

// signatureLineServer answers the node reads and, if asked, the signature
// status; it counts NodeSignatureStatus calls so the tests can pin "zero
// extra round trips when unadvertised".
func signatureLineServer(t *testing.T, statusJSON string, statusErr bool) (string, *int32) {
	t.Helper()
	var mu sync.Mutex
	var statusCalls int32
	gql, _ := captureGraphQLFunc(t, func(op string) string {
		mu.Lock()
		defer mu.Unlock()
		switch op {
		case "ResolveUrn":
			return `{"data":{"resolveUrn":{"id":"n1","kind":"node","memoryId":"mem1"}}}`
		case "GetNode":
			return nodeGetJSON(testNodeURL)
		case "NodeBatch":
			return `{"data":{"nodeBatch":{"truncated":false,"omitted":[],"unavailable":[],"nodes":[` +
				`{"id":"n1","urn":"` + testNodeURN + `","loc":"start-here"},` +
				`{"id":"n2","urn":"` + testNodeURN + `","loc":"start-here"}]}}}`
		case "NodeSignatureStatus":
			statusCalls++
			if statusErr {
				return `{"errors":[{"message":"signing is switched off"}]}`
			}
			return statusJSON
		}
		return ""
	})
	return gql.URL, &statusCalls
}

const signedStatusJSON = `{"data":{"node":{"id":"n1","urn":"` + testNodeURN + `","loc":"start-here","revision":7,` +
	`"signatureStatus":{"state":"SIGNED","integrityProblems":0,"signatures":[` +
	`{"id":"sig1","state":"CURRENT","revision":7,"verifiedAt":"2026-10-14T10:00:00Z","signer":{"handle":"holger","urn":null},"passkeyLabel":"Yubikey 5C","passkeyRevokedAt":null,"payloadVersion":"1","signingRequestId":"sr1"},` +
	`{"id":"sig2","state":"PENDING","revision":7,"verifiedAt":"","signer":{"handle":"ada","urn":null},"passkeyLabel":"Backup key","passkeyRevokedAt":null,"payloadVersion":"1","signingRequestId":"sr2"}]}}}}`

func withSigningProbe(t *testing.T, advertised bool, probeErr error) {
	t.Helper()
	prev := node.SigningCapabilityProbe
	node.SigningCapabilityProbe = func(ctx context.Context, baseURL string) (bool, error) {
		return advertised, probeErr
	}
	t.Cleanup(func() { node.SigningCapabilityProbe = prev })
}

func nodeGetRun(t *testing.T, url string, jsonOut bool, refs ...string) string {
	t.Helper()
	f, out := testFactory(t)
	root := NewRootCmd(f)
	args := []string{"node", "get", "--server", url}
	args = append(args, refs...)
	if jsonOut {
		args = append(args, "--json")
	}
	root.SetArgs(args)
	if err := root.Execute(); err != nil {
		t.Fatalf("node get: %v", err)
	}
	return out.String()
}

type signatureSummaryJSON struct {
	SignatureSummary *struct {
		State             string `json:"state"`
		IntegrityProblems int    `json:"integrityProblems"`
		Signatures        int    `json:"signatures"`
		Verified          int    `json:"verified"`
	} `json:"signatureSummary"`
}

func TestNodeGetSignatureLineWhenSigningAdvertised(t *testing.T) {
	url, calls := signatureLineServer(t, signedStatusJSON, false)
	withSigningProbe(t, true, nil)

	if out := nodeGetRun(t, url, false, testNodeURN); !strings.Contains(out, "signature: SIGNED (2 signatures; 1 verified)") {
		t.Fatalf("human output missing the signature line:\n%s", out)
	}
	if *calls != 1 {
		t.Fatalf("NodeSignatureStatus calls = %d, want 1", *calls)
	}

	var d signatureSummaryJSON
	raw := nodeGetRun(t, url, true, testNodeURN)
	if err := json.Unmarshal([]byte(raw), &d); err != nil {
		t.Fatalf("--json: %v (%q)", err, raw)
	}
	if d.SignatureSummary == nil || d.SignatureSummary.State != "SIGNED" || d.SignatureSummary.Signatures != 2 || d.SignatureSummary.Verified != 1 {
		t.Fatalf("signatureSummary = %+v, want state SIGNED, 2 signatures, 1 verified", d.SignatureSummary)
	}
}

func TestNodeGetNoSignatureRoundTripWhenUnadvertised(t *testing.T) {
	url, calls := signatureLineServer(t, signedStatusJSON, false)
	withSigningProbe(t, false, nil)

	out := nodeGetRun(t, url, false, testNodeURN)
	if strings.Contains(out, "signature:") {
		t.Fatalf("unadvertised signing must not render a signature line:\n%s", out)
	}
	if *calls != 0 {
		t.Fatalf("NodeSignatureStatus calls = %d, want 0 (the gate exists so an off server never pays the refused round trip)", *calls)
	}
	var d signatureSummaryJSON
	if err := json.Unmarshal([]byte(nodeGetRun(t, url, true, testNodeURN)), &d); err != nil {
		t.Fatalf("--json: %v", err)
	}
	if d.SignatureSummary != nil {
		t.Fatalf("signatureSummary = %+v, want null when unadvertised", d.SignatureSummary)
	}
}

func TestNodeGetCapabilityProbeFailureReadsAsUnadvertised(t *testing.T) {
	url, calls := signatureLineServer(t, signedStatusJSON, false)
	withSigningProbe(t, false, errProbeDown) // the probe itself fails

	out := nodeGetRun(t, url, false, testNodeURN)
	if strings.Contains(out, "signature:") {
		t.Fatalf("a failed probe must read as unadvertised:\n%s", out)
	}
	if *calls != 0 {
		t.Fatalf("NodeSignatureStatus calls = %d, want 0", *calls)
	}
}

var errProbeDown = &probeError{}

type probeError struct{}

func (*probeError) Error() string { return "capabilities endpoint down" }

func TestNodeGetSignatureStatusFailureIsUnavailable(t *testing.T) {
	url, calls := signatureLineServer(t, "", true)
	withSigningProbe(t, true, nil)

	out := nodeGetRun(t, url, false, testNodeURN)
	if !strings.Contains(out, "signature: unavailable") {
		t.Fatalf("a failed status read must render unavailable, not a guessed zero:\n%s", out)
	}
	if *calls != 1 {
		t.Fatalf("NodeSignatureStatus calls = %d, want 1", *calls)
	}
	var d signatureSummaryJSON
	if err := json.Unmarshal([]byte(nodeGetRun(t, url, true, testNodeURN)), &d); err != nil {
		t.Fatalf("--json: %v", err)
	}
	if d.SignatureSummary != nil {
		t.Fatalf("signatureSummary = %+v, want null on a failed read", d.SignatureSummary)
	}
}

func TestNodeGetMultiRefSkipsSignatureSummary(t *testing.T) {
	// v1 folds the summary into the single-ref object form only: a 200-node
	// batch must not pay one status query per node.
	url, calls := signatureLineServer(t, signedStatusJSON, false)
	withSigningProbe(t, true, nil)

	nodeGetRun(t, url, false, testNodeURN, testNodeURN)
	if *calls != 0 {
		t.Fatalf("NodeSignatureStatus calls = %d, want 0 for the multi-ref envelope", *calls)
	}
}
