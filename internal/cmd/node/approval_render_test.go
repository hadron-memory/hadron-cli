package node

import (
	"bytes"
	"strings"
	"testing"
)

// hadron-server#1611: an approval stays valid across metadata-only revisions,
// so the revision it was recorded at can be older than the node's current one.
// The text must say where the approval was recorded, never imply it is the
// node's current revision (hadron-cli#895).
func TestRenderApprovalStatusSaysWhereTheApprovalWasRecorded(t *testing.T) {
	info := "app:abc"
	rec := &approvalDTO{Revision: 2, ApprovedAt: "2026-10-01T23:00:00Z", ApprovedByInfo: &info}
	for _, tc := range []struct {
		state string
		want  []string
		deny  []string
	}{
		{"APPROVED", []string{"approval: APPROVED", "recorded at revision 2"}, []string{"APPROVED revision 2"}},
		{"SUPERSEDED", []string{"approval: SUPERSEDED", "title, abstract or content changed", "recorded at revision 2"}, []string{"newer revision", "last approved revision"}},
	} {
		var b bytes.Buffer
		renderApprovalStatus(&b, "", &approvalStatusDTO{State: tc.state, Approval: rec})
		got := b.String()
		for _, w := range tc.want {
			if !strings.Contains(got, w) {
				t.Errorf("%s: %q does not contain %q", tc.state, got, w)
			}
		}
		for _, d := range tc.deny {
			if strings.Contains(got, d) {
				t.Errorf("%s: %q must not contain %q", tc.state, got, d)
			}
		}
	}
}

func TestRenderApprovalStatusLeavesOtherStatesAlone(t *testing.T) {
	var b bytes.Buffer
	renderApprovalStatus(&b, "  ", &approvalStatusDTO{State: "NOT_APPROVED"})
	if got := b.String(); got != "  approval: NOT_APPROVED\n" {
		t.Errorf("got %q", got)
	}
	b.Reset()
	renderApprovalStatus(&b, "", nil)
	if !strings.Contains(b.String(), "predates node approvals") {
		t.Errorf("got %q", b.String())
	}
}
