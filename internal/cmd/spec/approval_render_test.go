package spec

import (
	"bytes"
	"strings"
	"testing"
)

// hadron-server#1611 / hadron-cli#895: see the node package's twin test.
func TestRenderSpecApprovalSaysWhereTheApprovalWasRecorded(t *testing.T) {
	info := "app:abc"
	rec := &specApprovalRecordDTO{Revision: 2, ApprovedAt: "2026-10-01T23:00:00Z", ApprovedByInfo: &info}
	for _, tc := range []struct {
		state string
		want  []string
		deny  []string
	}{
		{"APPROVED", []string{"Approval: APPROVED", "recorded at revision 2"}, []string{"APPROVED revision 2"}},
		{"SUPERSEDED", []string{"Approval: SUPERSEDED", "title, abstract or content changed", "recorded at revision 2"}, []string{"last approved revision"}},
	} {
		var b bytes.Buffer
		renderSpecApproval(&b, &specApprovalDTO{State: tc.state, Approval: rec})
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

// cli#3220 item 1: the line beside `Approval: SUPERSEDED` says what the value is, because
// it reads like "replaced" and is not (cli#1906). It is there for SUPERSEDED only.
func TestSupersededApprovalLineSaysItIsNotRetirement3220(t *testing.T) {
	info := "app:abc"
	rec := &specApprovalRecordDTO{Revision: 2, ApprovedAt: "2026-10-01T23:00:00Z", ApprovedByInfo: &info}
	const line = "  SUPERSEDED here is the approval state of the current text. It does not mean the spec was replaced or retired.\n"
	var sup bytes.Buffer
	renderSpecApproval(&sup, &specApprovalDTO{State: "SUPERSEDED", Approval: rec})
	if !strings.Contains(sup.String(), "\n"+line) {
		t.Errorf("SUPERSEDED output lacks the clarifying line, on its own line after the Approval line:\n%s", sup.String())
	}
	for _, state := range []string{"APPROVED", "NOT_APPROVED"} {
		var b bytes.Buffer
		renderSpecApproval(&b, &specApprovalDTO{State: state, Approval: rec})
		if strings.Contains(b.String(), "SUPERSEDED here") || strings.Contains(b.String(), "does not mean the spec was replaced") {
			t.Errorf("%s output must not carry the SUPERSEDED line:\n%s", state, b.String())
		}
	}
}
