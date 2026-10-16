package comment

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/api/gen"
)

// Encrypted targets have no fingerprint (cor:dmo:110:01).
func TestAnchorApprovalHashPreservesNullAndPlaintext(t *testing.T) {
	var fields gen.CommentFields
	if err := json.Unmarshal([]byte(`{"id":"c1","anchorApprovalHash":null}`), &fields); err != nil {
		t.Fatal(err)
	}
	out, err := json.Marshal(fromFields(fields))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(out), `"anchorApprovalHash":null`) {
		t.Fatalf("encrypted-target comment JSON %s", out)
	}
	hash := strings.Repeat("a", 64)
	if err := json.Unmarshal([]byte(`{"id":"c1","anchorApprovalHash":"`+hash+`"}`), &fields); err != nil {
		t.Fatal(err)
	}
	if got := fromFields(fields).AnchorApprovalHash; got == nil || *got != hash {
		t.Fatalf("hash %v, want %q", got, hash)
	}
}
