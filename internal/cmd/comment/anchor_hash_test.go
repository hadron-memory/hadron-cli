package comment

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/api/gen"
)

// anchorApprovalHash is nullable since server #1866: an encrypted target has
// no fingerprint (cor:dmo:110:01). The CLI has always emitted "" for it — a
// null decoded into the old non-null string left it empty — and the SDL
// refresh that made the field a pointer must not move that --json shape.
func TestAnchorApprovalHashNullKeepsEmptyString(t *testing.T) {
	var fields gen.CommentFields
	if err := json.Unmarshal([]byte(`{"id":"c1","anchorApprovalHash":null}`), &fields); err != nil {
		t.Fatal(err)
	}
	out, err := json.Marshal(fromFields(fields))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(out), `"anchorApprovalHash":""`) {
		t.Fatalf("encrypted-target comment JSON %s", out)
	}
	hash := strings.Repeat("a", 64)
	if err := json.Unmarshal([]byte(`{"id":"c1","anchorApprovalHash":"`+hash+`"}`), &fields); err != nil {
		t.Fatal(err)
	}
	if got := fromFields(fields).AnchorApprovalHash; got != hash {
		t.Fatalf("hash %q, want %q", got, hash)
	}
}
