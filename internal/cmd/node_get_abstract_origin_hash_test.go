package cmd

import (
	"strings"
	"testing"
)

// #306 — `node get --json` must EMIT abstractOriginHash, spec 032's staleness
// fingerprint.
//
// The issue this closes was a misdiagnosis that the missing field caused, and
// the shape is the reason these assertions are written against the raw output
// rather than a decode. Its reproduction was:
//
//	$ hadron node get <ref> --json | jq .abstractOriginHash
//	null
//
// read as "the hash is null, so the detector is disarmed". It was not: `jq`
// prints null for a key that is NOT THERE, identically to a key whose value is
// null, and this DTO simply had no such key. The command printed null for every
// node in every state, so the reproduction could not have produced any other
// answer — and the server had been stamping the field correctly all along.
//
// `encoding/json` has exactly the same blindness: an absent key and a null key
// both decode to a nil *string. A test that unmarshals and asserts
// `dto.AbstractOriginHash == nil` therefore PASSES against the bug, which is
// what review:stable-json-dto means by an assertion blind to the difference it
// is about. So these look at the bytes.

func nodeGetJSONWithHash(hash string) string {
	q := func(s string) string {
		if s == "" {
			return "null"
		}
		return `"` + s + `"`
	}
	return `{"data":{"node":{"id":"n1","urn":"` + testNodeURN + `","portalUrl":null,
		"memoryId":"mem1","loc":"findings:flaky","name":"flaky","description":null,
		"abstract":"An abstract.","abstractOriginHash":` + q(hash) + `,
		"nodeType":"info","objectType":null,"tags":[],"content":"Body.",
		"data":null,"properties":null,"seq":null,"isRunnable":false,
		"createdAt":"2026-08-24T00:00:00Z","updatedAt":"2026-08-24T00:00:00Z",
		"outgoingEdges":[],"incomingEdges":[]}}}`
}

func TestNodeGetJSONEmitsAbstractOriginHash(t *testing.T) {
	gql, _ := captureGraphQL(t, nodeGetStubs(nodeGetJSONWithHash("8eb3757c")))
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"node", "get", testNodeURN, "--server", gql.URL, "--json"})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	got := out.String()
	if !strings.Contains(got, `"abstractOriginHash": "8eb3757c"`) {
		t.Errorf("the server's fingerprint must reach --json; got:\n%s", got)
	}
}

// The load-bearing half. A null hash is a REAL answer — the node has no
// abstract, or has one that was never fingerprinted (#1128's unverified state)
// — so the key must be PRESENT and null, never omitted. Omitting it is exactly
// the state that produced #306.
func TestNodeGetJSONKeepsAbstractOriginHashPresentWhenNull(t *testing.T) {
	gql, _ := captureGraphQL(t, nodeGetStubs(nodeGetJSONWithHash("")))
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"node", "get", testNodeURN, "--server", gql.URL, "--json"})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	got := out.String()
	if !strings.Contains(got, `"abstractOriginHash": null`) {
		t.Errorf("a null hash must render as a PRESENT key, not a missing one — "+
			"an absent key is what `jq .abstractOriginHash` cannot tell from null; got:\n%s", got)
	}
	// Stated separately so a failure says WHICH way it broke: the key vanishing
	// is the regression, and "does not contain the value" would not catch a
	// rename.
	if !strings.Contains(got, "abstractOriginHash") {
		t.Errorf("the key disappeared entirely; got:\n%s", got)
	}
}

// The BATCH path carries it too (#306). The DTO is shared but the assignment is
// a second, separate line, so dropping only that one is a real edit the
// single-read tests above cannot see — and `--prefix` is the form someone would
// actually use to survey a branch for stale abstracts, which is the case that
// most needs the field.
func TestNodeGetBatchJSONEmitsAbstractOriginHash(t *testing.T) {
	node := `{"id":"n1","urn":"hrn:node:acme.com:kb:findings:alpha","portalUrl":null,
		"memoryId":"mem1","loc":"findings:alpha","name":"alpha","alias":null,
		"nodeType":"info","objectType":null,"isRunnable":false,"description":null,
		"abstract":"An abstract.","abstractOriginHash":"8eb3757c","tags":[],"seq":null,
		"data":null,"properties":null,"content":"Body.",
		"createdAt":"2026-07-27T00:00:00Z","updatedAt":"2026-07-27T00:00:00Z",
		"outgoingEdges":[],"incomingEdges":[]}`
	gql, _ := captureGraphQL(t, map[string]string{
		"NodeBatch": nodeBatchResult([]string{node, batchNodeJSON("n2", "findings:beta")}, ""),
	})
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"node", "get", "--prefix", "findings:", "-m", "acme.com::kb", "--json", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	got := out.String()
	if !strings.Contains(got, `"abstractOriginHash": "8eb3757c"`) {
		t.Errorf("the batch read must carry the fingerprint; got:\n%s", got)
	}
	// And the sibling with no hash keeps a present null, for the same reason as
	// the single read: absent and null must stay distinguishable.
	if !strings.Contains(got, `"abstractOriginHash": null`) {
		t.Errorf("a null hash must stay a PRESENT key in the batch shape too; got:\n%s", got)
	}
}
