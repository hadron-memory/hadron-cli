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
