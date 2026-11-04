package spec

import (
	"encoding/json"
	"github.com/hadron-memory/hadron-cli/internal/api/gen"
	"testing"
)

func TestLabelBatchProtocolErrors(t *testing.T) {
	for _, raw := range []string{
		`{"nodes":[],"unavailable":[],"omitted":[],"truncated":false}`,
		`{"nodes":[{"id":"foreign"}],"truncated":false}`,
		`{"nodes":[{"id":"p"}],"omitted":["p"],"truncated":true}`,
		`{"nodes":[],"omitted":["p"],"truncated":false}`,
	} {
		var r gen.NodeBatchNodeBatchNodeBatchResult
		if e := json.Unmarshal([]byte(raw), &r); e != nil {
			t.Fatal(e)
		}
		if e := validateLabelBatch([]string{"p"}, &r); e == nil {
			t.Fatal("accepted protocol failure", raw)
		}
	}
	var r gen.NodeBatchNodeBatchNodeBatchResult
	_ = json.Unmarshal([]byte(`{"nodes":[],"unavailable":["p"],"omitted":[],"truncated":false}`), &r)
	if e := validateLabelBatch([]string{"p"}, &r); e != nil {
		t.Fatal(e)
	}
}
