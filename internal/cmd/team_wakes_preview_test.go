package cmd

import (
	"testing"

	"github.com/vektah/gqlparser/v2/ast"
	"github.com/vektah/gqlparser/v2/parser"
)

// Inspect the wire value, allowing a future explicit preview flag without
// freezing the operation's spelling. This pins today's default, not #1833's.
func teamDuePreview(t *testing.T, query string, vars map[string]any) bool {
	t.Helper()
	doc, err := parser.ParseQuery(&ast.Source{Input: query})
	if err != nil {
		t.Fatal(err)
	}
	op := doc.Operations.ForName("TeamDueWakes")
	if op == nil {
		t.Fatal("missing TeamDueWakes")
	}
	field := op.SelectionSet[0].(*ast.Field)
	if field.Name != "dueWakes" {
		t.Fatalf("unexpected field %s", field.Name)
	}
	arg := field.Arguments.ForName("preview")
	if arg == nil {
		return false
	}
	value := arg.Value
	if value.Kind == ast.Variable {
		if v, present := vars[value.Raw]; present {
			return v == true
		}
		def := op.VariableDefinitions.ForName(value.Raw)
		if def == nil || def.DefaultValue == nil {
			return false
		}
		value = def.DefaultValue
	}
	got, err := value.Value(vars)
	if err != nil {
		t.Fatal(err)
	}
	return got == true
}

func TestTeamWakesDueCurrentDefaultIsSpending(t *testing.T) {
	calls, _, err := runTeamTask(t, c1279Responses(c1279Page("OK", 0, "")), "team", "wakes", "due", "--json")
	if err != nil {
		t.Fatal(err)
	}
	reads := stopCalls(calls, "TeamDueWakes")
	if len(reads) != 1 {
		t.Fatalf("due requests=%d", len(reads))
	}
	if teamDuePreview(t, reads[0].Query, reads[0].Vars) {
		t.Fatal("default team wakes due sent effective preview:true")
	}
}

func TestTeamDuePreviewWireControls(t *testing.T) {
	for _, tc := range []struct {
		name, query string
		vars        map[string]any
		want        bool
	}{
		{"absent", `query TeamDueWakes { dueWakes { status } }`, nil, false},
		{"literal-false", `query TeamDueWakes { dueWakes(preview:false) { status } }`, nil, false},
		{"literal-true", `query TeamDueWakes { dueWakes(preview:true) { status } }`, nil, true},
		{"variable-omitted", `query TeamDueWakes($preview:Boolean) { dueWakes(preview:$preview) { status } }`, nil, false},
		{"variable-false", `query TeamDueWakes($preview:Boolean) { dueWakes(preview:$preview) { status } }`, map[string]any{"preview": false}, false},
		{"variable-true", `query TeamDueWakes($preview:Boolean) { dueWakes(preview:$preview) { status } }`, map[string]any{"preview": true}, true},
		{"default-true", `query TeamDueWakes($preview:Boolean=true) { dueWakes(preview:$preview) { status } }`, nil, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := teamDuePreview(t, tc.query, tc.vars); got != tc.want {
				t.Fatalf("effective preview=%v want=%v", got, tc.want)
			}
		})
	}
}
