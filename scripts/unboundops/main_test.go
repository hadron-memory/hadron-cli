package main

import (
	"os"
	"path/filepath"
	"testing"
)

const schemaFixture = `
type Query {
  memories(limit: Int): MemoryPage!
  "A doc comment must not be mistaken for a field."
  agents: [Agent!]!
}
type Mutation {
  createMemory(name: String!): Memory!
  recordTeamWork(appRef: ID!): TeamWorkItem!
  updateTeamCollections(appRef: ID!): TeamCollectionsPayload!
}
type Memory { id: ID! }
`

func TestRootFieldsReadsQueryAndMutationOnly(t *testing.T) {
	roots, err := rootFields(schemaFixture)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if got := len(roots["Query"]); got != 2 {
		t.Errorf("Query fields: %d, want 2 (%v)", got, roots["Query"])
	}
	if got := len(roots["Mutation"]); got != 3 {
		t.Errorf("Mutation fields: %d, want 3 (%v)", got, roots["Mutation"])
	}
	// Object types that are not roots contribute nothing.
	if _, ok := roots["Memory"]; ok {
		t.Errorf("non-root type leaked into the field set: %v", roots["Memory"])
	}
}

// What matters is the server FIELD an operation selects, not the operation's
// own name — the CLI routinely names them differently (RecordTeamWork wraps
// recordTeamWork, TeamMemoryApp wraps memory).
func TestSelectedRootFieldsUsesTheFieldNotTheOperationName(t *testing.T) {
	dir := t.TempDir()
	doc := `
fragment F on TeamWorkItem { nodeId }
mutation RecordTeamWork($appRef: ID!) {
  recordTeamWork(appRef: $appRef) { ...F }
}
query TeamMemoryApp($ref: ID!) {
  memory(ref: $ref) { id appId }
}
`
	path := filepath.Join(dir, "team.graphql")
	if err := os.WriteFile(path, []byte(doc), 0o600); err != nil {
		t.Fatal(err)
	}
	used, err := selectedRootFields([]string{path})
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	for _, want := range []string{"recordTeamWork", "memory"} {
		if !used[want] {
			t.Errorf("root field %q not detected (got %v)", want, used)
		}
	}
	// The operation names themselves are not server fields.
	if used["RecordTeamWork"] || used["TeamMemoryApp"] {
		t.Errorf("operation names must not count as bound fields: %v", used)
	}
	// A fragment definition is not an operation and contributes no root field.
	if used["nodeId"] {
		t.Errorf("fragment selections must not count: %v", used)
	}
}

// The `# reason` annotations are the only human-authored part of the baseline,
// so a regeneration must carry them across.
func TestExistingReasonsSurviveRegeneration(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "unbound-ops.txt")
	if err := os.WriteFile(path, []byte("# header\n\n[Mutation] 1 unbound\ngrantCredits  # platform-admin only, no CLI surface planned\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	reasons := existingReasons(path)
	if reasons["grantCredits"] != "platform-admin only, no CLI surface planned" {
		t.Errorf("reason not preserved: %q", reasons["grantCredits"])
	}
	out := render(map[string][]string{"Mutation": {"grantCredits"}}, reasons)
	if !contains(out, "grantCredits  # platform-admin only, no CLI surface planned") {
		t.Errorf("render dropped the reason:\n%s", out)
	}
}

func contains(haystack, needle string) bool {
	return len(haystack) >= len(needle) && (func() bool {
		for i := 0; i+len(needle) <= len(haystack); i++ {
			if haystack[i:i+len(needle)] == needle {
				return true
			}
		}
		return false
	})()
}
