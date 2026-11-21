package connection

import "testing"

// cli#2845 (Jerry's LOCAL pin): the zero value of detailsLookup is a failed
// read, so a flowResult built without an outcome is degraded, never passed off
// as filled. Reordering the constants must fail here, not in production: this
// pin is the guard a reorder mutant has to break.
func TestFlowResultZeroValueIsDegraded2845(t *testing.T) {
	r := flowResult{connection: &connectedDTO{}}
	if !r.degraded() {
		t.Fatal("the zero detailsLookup must degrade a connection-bearing result")
	}
}
