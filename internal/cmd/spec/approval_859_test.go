package spec

import (
	"bytes"
	"strings"
	"testing"
)

func TestApprovalLabel859(t *testing.T) {
	for _, state := range []string{"NOT_APPLICABLE", "FUTURE_STATE", "APPROVED", "SUPERSEDED", "NOT_APPROVED"} {
		for _, retained := range []bool{false, true} {
			s := &specApprovalDTO{State: state}
			if retained {
				s.Approval = &specApprovalRecordDTO{Revision: 3, ApprovedAt: "now"}
			}
			var b bytes.Buffer
			renderSpecApproval(&b, s)
			got := b.String()
			if state == "NOT_APPLICABLE" {
				if got != "Approval: not applicable (encrypted memory)\n" {
					t.Errorf("%s retained=%v: %q", state, retained, got)
				}
			} else if !strings.Contains(got, "Approval: "+state) {
				t.Errorf("other state changed: %q", got)
			}
			if (state == "NOT_APPLICABLE" || state == "FUTURE_STATE") && strings.Contains(got, "APPROVED") {
				t.Errorf("false approval: %q", got)
			}
		}
	}
	var b bytes.Buffer
	var s *specApprovalDTO
	renderSpecApproval(&b, s)
	if !strings.Contains(b.String(), "unknown") {
		t.Fatal(b.String())
	}
}
