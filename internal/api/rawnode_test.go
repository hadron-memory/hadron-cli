package api

import (
	"strings"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/api/gen"
)

// #736: GetNodeRaw must actually ask the server for the stored body. The
// command fakes answer by operation NAME, so they cannot see this; the
// document itself is the evidence.
func TestGetNodeRawAsksForTheStoredBody(t *testing.T) {
	if !strings.Contains(gen.GetNodeRaw_Operation, "node(ref: $ref, raw: true)") {
		t.Errorf("GetNodeRaw must request raw: true, document:\n%s", gen.GetNodeRaw_Operation)
	}
	if strings.Contains(gen.GetNode_Operation, "raw:") {
		t.Errorf("GetNode is the rendered read and must not request raw, document:\n%s", gen.GetNode_Operation)
	}
}
