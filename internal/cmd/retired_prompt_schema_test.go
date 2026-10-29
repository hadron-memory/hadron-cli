package cmd

import (
 "os"
 "path/filepath"
 "strings"
 "testing"

 "github.com/vektah/gqlparser/v2"
 "github.com/vektah/gqlparser/v2/ast"
 "github.com/vektah/gqlparser/v2/parser"
 "github.com/vektah/gqlparser/v2/validator"
)

// Validate the whole operation set against the removal contract before the
// provider drops fields. This catches fragment and mutation-argument callers.
func TestOperationsSurviveRetiredPromptRemoval(t *testing.T) {
 sdl, err := os.ReadFile("../../schema/schema.graphql")
 if err != nil { t.Fatal(err) }
 schema, err := gqlparser.LoadSchema(&ast.Source{Name:"removal-schema", Input:string(sdl)})
 if err != nil { t.Fatal(err) }
 retired := func(s string) bool { return s == "systemPrompt" || s == "promptOverride" }
 for _, typ := range schema.Types {
  fields := ast.FieldList{}
  for _, field := range typ.Fields {
   if retired(field.Name) { continue }
   args := ast.ArgumentDefinitionList{}
   for _, arg := range field.Arguments { if !retired(arg.Name) { args = append(args,arg) } }
   field.Arguments = args
   fields = append(fields,field)
  }
  typ.Fields = fields
 }
 files, err := filepath.Glob("../api/queries/*.graphql")
 if err != nil { t.Fatal(err) }
 var operations strings.Builder
 for _, path := range files {
  b, err := os.ReadFile(path); if err != nil { t.Fatal(err) }
  operations.Write(b); operations.WriteByte('\n')
 }
 doc, err := parser.ParseQuery(&ast.Source{Name:"all CLI operations",Input:operations.String()})
 if err != nil { t.Fatal(err) }
 if errs := validator.Validate(schema,doc); len(errs)>0 { t.Fatal(errs) }
}
