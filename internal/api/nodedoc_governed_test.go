package api

import "testing"

// cli#714: `memory export` renders through this mapping, and NodeBatch already
// selects role and isRunnable — the mapping dropped them, so an exported task,
// spec or review re-imported as an ordinary node.
func TestDocumentFromBatchNodeCarriesTheGovernedSignals(t *testing.T) {
	role, runnable := "spec", false
	doc := DocumentFromBatchNode(&batchNode{Id: "n1", Loc: "x", Name: "X", Role: &role, IsRunnable: &runnable})
	if doc.Role == nil || *doc.Role != "spec" {
		t.Errorf("role = %v, want spec", doc.Role)
	}
	if doc.IsRunnable == nil || *doc.IsRunnable {
		t.Errorf("isRunnable = %v, want an explicit false", doc.IsRunnable)
	}
	bare := DocumentFromBatchNode(&batchNode{Id: "n1", Loc: "x", Name: "X"})
	if bare.Role != nil || bare.IsRunnable != nil {
		t.Errorf("unset signals must stay nil, got role %v runnable %v", bare.Role, bare.IsRunnable)
	}
}
