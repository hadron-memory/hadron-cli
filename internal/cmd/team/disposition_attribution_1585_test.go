package team

import (
	"os"
	"testing"

	"github.com/Khan/genqlient/graphql"
	"github.com/hadron-memory/hadron-cli/internal/api/gen"
	"github.com/vektah/gqlparser/v2/ast"
	"github.com/vektah/gqlparser/v2/parser"
)

func TestTaskWritesAuthored1585(t *testing.T) {
	for name, query := range map[string]string{
		"AgentTaskDisposeFinding": gen.AgentTaskDisposeFinding_Operation,
		"AgentTaskClone":          gen.AgentTaskClone_Operation,
		"AgentTaskCreateBatch":    gen.AgentTaskCreateBatch_Operation,
		"AgentTaskUpdate":         gen.AgentTaskUpdate_Operation,
	} {
		t.Run(name, func(t *testing.T) {
			if !authoredWriteRequest(&graphql.Request{OpName: name, Query: query}) {
				t.Fatal("task write must carry verified worker attribution")
			}
		})
	}
}

func TestTaskMutationAttributionCensus1585(t *testing.T) {
	source, err := os.ReadFile("../../api/queries/agenttasks.graphql")
	if err != nil {
		t.Fatal(err)
	}
	doc, err := parser.ParseQuery(&ast.Source{Input: string(source)})
	if err != nil {
		t.Fatal(err)
	}
	// These are intentional person-only manager writes, not forgotten worker writes.
	excluded := map[string]string{
		"setAgentTaskStallSchedule":  "cli#866: actorManagesApp admits only a person",
		"setModelFamilyAvailability": "cli#868: actorManagesApp admits only a person",
	}
	seen := map[string]bool{}
	for _, op := range doc.Operations {
		if op.Operation != ast.Mutation {
			continue
		}
		for _, s := range op.SelectionSet {
			field, ok := s.(*ast.Field)
			if !ok {
				t.Fatalf("review non-field mutation selection in %s", op.Name)
			}
			seen[field.Name] = true
			reason, skip := excluded[field.Name]
			if skip {
				if reason == "" || authoredWriteField(field.Name) {
					t.Errorf("intentional exclusion changed: %s (%s)", field.Name, reason)
				}
			} else if !authoredWriteField(field.Name) {
				t.Errorf("mutation %s needs attribution or an explicit reviewed exclusion", field.Name)
			}
		}
	}
	for field := range excluded {
		if !seen[field] {
			t.Errorf("stale exclusion %s", field)
		}
	}
}
