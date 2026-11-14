package cmd

import (
	"strings"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/api/gen"
)

// server#4140 (entity refs S4) hard-renamed six arguments on four mutations with
// no alias: a document naming a dropped arg fails "Unknown argument". Pin the new
// spelling in the documents the CLI sends (cli#2011).
func TestEntityRefsS4OperationsUseRenamedArgs2011(t *testing.T) {
	for _, tc := range []struct {
		name, op string
		want     []string
		gone     []string
	}{
		{"UpdateMemory", gen.UpdateMemory_Operation, []string{"ref: $ref"}, []string{"id: $id", "organizationId: $"}},
		{"UpdateOrganization", gen.UpdateOrganization_Operation, []string{"updateOrganization(ref: $ref"}, []string{"id: $id"}},
		{"CreateAgent", gen.CreateAgent_Operation, []string{"orgRef: $orgRef", "systemMemoryRef: $systemMemoryRef"}, []string{"orgId:", "systemMemoryId:"}},
		{"UpdateAgent", gen.UpdateAgent_Operation, []string{"systemMemoryRef: $systemMemoryRef"}, []string{"systemMemoryId:"}},
	} {
		for _, w := range tc.want {
			if !strings.Contains(tc.op, w) {
				t.Errorf("%s: document lacks %q:\n%s", tc.name, w, tc.op)
			}
		}
		for _, g := range tc.gone {
			if strings.Contains(tc.op, g) {
				t.Errorf("%s: document still names the dropped %q", tc.name, g)
			}
		}
	}
}
