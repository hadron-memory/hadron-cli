package api

import (
	"os"
	"strings"
	"testing"

	"github.com/vektah/gqlparser/v2"
	"github.com/vektah/gqlparser/v2/ast"
	"github.com/vektah/gqlparser/v2/gqlerror"
	"github.com/vektah/gqlparser/v2/parser"
	"github.com/vektah/gqlparser/v2/validator"

	"github.com/hadron-memory/hadron-cli/internal/api/gen"
)

// validateAgainstSnapshot3107 validates one operation the way a server does, against the
// committed SDL.
func validateAgainstSnapshot3107(t *testing.T, operation string) gqlerror.List {
	t.Helper()
	sdl, err := os.ReadFile("../../schema/schema.graphql")
	if err != nil {
		t.Fatal(err)
	}
	schema, perr := gqlparser.LoadSchema(&ast.Source{Name: "schema.graphql", Input: string(sdl)})
	if perr != nil {
		t.Fatalf("schema snapshot does not load: %v", perr)
	}
	doc, qerr := parser.ParseQuery(&ast.Source{Name: "AppRuns", Input: operation})
	if qerr != nil {
		t.Fatalf("AppRuns does not parse: %v", qerr)
	}
	return validator.Validate(schema, doc)
}

// TestAppRunsSendsRefsAsID3107: appRuns takes its ref arguments as ID
// (hadron-server#5050, cor:api:140:01). A String variable in an ID position refuses
// the whole document, so `run ls` would fail with or without a filter. The operation
// tested is the generated one, the bytes actually sent, so documents fixed without a
// regenerate are red too.
func TestAppRunsSendsRefsAsID3107(t *testing.T) {
	for _, e := range validateAgainstSnapshot3107(t, gen.AppRuns_Operation) {
		t.Errorf("AppRuns does not validate against the snapshot: %v", e)
	}
	for _, name := range []string{"appRef", "orgRef", "parentRunId"} {
		if !strings.Contains(gen.AppRuns_Operation, "$"+name+": ID") {
			t.Errorf("AppRuns does not declare $%s as ID", name)
		}
	}
}

// TestTheSnapshotRefusesStringAppRunsRefs3107 is the control for the test above: it
// passes only while the snapshot carries the cutover, and shows per variable that the
// retired scalar is refused.
func TestTheSnapshotRefusesStringAppRunsRefs3107(t *testing.T) {
	for _, name := range []string{"appRef", "orgRef", "parentRunId"} {
		t.Run(name, func(t *testing.T) {
			// Normalise to the retired scalar whichever one the client declares today.
			old := strings.Replace(gen.AppRuns_Operation, "$"+name+": ID", "$"+name+": String", 1)
			if !strings.Contains(old, "$"+name+": String") {
				t.Fatalf("AppRuns declares no $%s: the control is blind", name)
			}
			for _, e := range validateAgainstSnapshot3107(t, old) {
				if strings.Contains(e.Message, "$"+name) && strings.Contains(e.Message, `"ID"`) {
					return
				}
			}
			t.Errorf("a String $%s validates: the snapshot predates hadron-server#5050", name)
		})
	}
}
