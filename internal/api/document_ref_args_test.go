package api

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vektah/gqlparser/v2"
	"github.com/vektah/gqlparser/v2/ast"
	"github.com/vektah/gqlparser/v2/parser"
)

// The S4 entity-ref renames (server#4219) renamed the identifying ARGUMENT of a
// family of operations — orgId/memoryId/appId/agentId/userId became
// orgRef/memoryRef/appRef/agentRef/userRef, and the former optional *Id/*Ref
// pairs collapsed into one required arg. genqlient refuses a document that
// names a dropped argument, but only at `make generate`, against a schema the
// committed generated code may predate — and it says nothing if the server
// re-adds an old spelling as a deprecated alias (several *MemoryMember
// operations carry exactly such aliases today). This test parses every
// committed operation document and the committed schema snapshot (the same SDL
// genqlient checks against) and fails, in plain `go test`, when a document
// passes an entity-ref ARGUMENT that the snapshot does not declare on that
// field — the signature of a rename the document missed — or declares only as
// a deprecated alias. That makes "every renamed operation is pinned"
// (entity_refs_s4_2011_test.go) self-checking document-wide instead of a
// hand-claimed table: the next rename that lands in the snapshot without its
// documents turns this red by name, file and line (cli#2679 review).
//
// Scope notes, on purpose:
//   - Only field-call ARGUMENTS of the five renamed stems are checked. An
//     input-object field of the same name (NodeInput.memoryId) is a different
//     surface that S4 did not touch, and the AST distinguishes the two for
//     free; so do response selections (removeMemoryMember returns memoryId
//     and userId fields), which is why this is parsed, not grepped.
//   - The check is existence-and-not-deprecated, not typing: memoryRef is
//     String (server-resolved) on addMemoryMember and ID elsewhere, so an
//     ID-only rule would false-positive. The exact typing the CLI sends is
//     pinned per operation by the table test in internal/cmd.
//   - updateOrgGithubApp's appId is GitHub's app id, not the renamed appRef;
//     it is declared and undeprecated in the snapshot, so it passes here and
//     its story lives in the table test's comment.
func TestDocumentedEntityRefArgsStillInSchema2696(t *testing.T) {
	stems := map[string]bool{
		"orgId": true, "memoryId": true, "appId": true, "agentId": true, "userId": true,
		"orgRef": true, "memoryRef": true, "appRef": true, "agentRef": true, "userRef": true,
	}

	sdl, err := os.ReadFile(filepath.Join("..", "..", "schema", "schema.graphql"))
	if err != nil {
		t.Fatalf("read schema snapshot: %v", err)
	}
	schema, err := gqlparser.LoadSchema(&ast.Source{Name: "schema.graphql", Input: string(sdl)})
	if err != nil {
		t.Fatalf("parse schema snapshot: %v", err)
	}
	// field name -> argument name -> definition, unioned across types. A false
	// pass would need a same-named field on another type that carries the same
	// argument; GraphQL field names are near-unique, and this is a tripwire.
	fields := map[string]map[string]*ast.ArgumentDefinition{}
	for _, typ := range schema.Types {
		for _, f := range typ.Fields {
			args, ok := fields[f.Name]
			if !ok {
				args = map[string]*ast.ArgumentDefinition{}
				fields[f.Name] = args
			}
			for _, a := range f.Arguments {
				args[a.Name] = a
			}
		}
	}

	entries, err := os.ReadDir("queries")
	if err != nil {
		t.Fatalf("read queries dir: %v", err)
	}
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".graphql" {
			continue
		}
		src, err := os.ReadFile(filepath.Join("queries", entry.Name()))
		if err != nil {
			t.Fatalf("read %s: %v", entry.Name(), err)
		}
		doc, err := parser.ParseQuery(&ast.Source{Name: entry.Name(), Input: string(src)})
		if err != nil {
			t.Fatalf("parse %s: %v", entry.Name(), err)
		}
		var visit func(set ast.SelectionSet)
		visit = func(set ast.SelectionSet) {
			for _, sel := range set {
				switch s := sel.(type) {
				case *ast.Field:
					for _, arg := range s.Arguments {
						if !stems[arg.Name] {
							continue
						}
						def := fields[s.Name][arg.Name]
						where := fmt.Sprintf("%s: %s(%s: …)", entry.Name(), s.Name, arg.Name)
						if def == nil {
							t.Errorf("%s: the schema snapshot does not declare this argument on this field — a rename the document missed?", where)
							continue
						}
						for _, d := range def.Directives {
							if d.Name != "deprecated" {
								continue
							}
							reason := ""
							if len(d.Arguments) > 0 && d.Arguments[0].Value != nil {
								reason = strings.Trim(d.Arguments[0].Value.String(), `"`)
							}
							t.Errorf("%s: declared only as a deprecated alias (%s)", where, reason)
						}
					}
					visit(s.SelectionSet)
				case *ast.InlineFragment:
					visit(s.SelectionSet)
				}
			}
		}
		for _, op := range doc.Operations {
			visit(op.SelectionSet)
		}
		for _, frag := range doc.Fragments {
			visit(frag.SelectionSet)
		}
	}
}
