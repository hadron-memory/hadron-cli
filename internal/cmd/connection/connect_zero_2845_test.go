package connection

import "testing"

// cli#2845: the zero value of detailsLookup is a failed read, so a flowResult
// built without an outcome is degraded, never passed off as filled. This pins
// that the zero value degrades: a reorder that puts lookupListed at zero fails
// here. It does not pin which of the two degraded outcomes sits at zero, so
// swapping lookupFailed and lookupUnlisted survives it (cli#2892).
func TestFlowResultZeroValueIsDegraded2845(t *testing.T) {
	r := flowResult{connection: &connectedDTO{}}
	if !r.degraded() {
		t.Fatal("the zero detailsLookup must degrade a connection-bearing result")
	}
}
