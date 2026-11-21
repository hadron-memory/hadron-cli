package lintwarn

import (
	"context"
	"go/ast"
	"go/parser"
	"go/token"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

func TestReportUnavailableFirstLabels2227(t *testing.T) {
	var stderr strings.Builder
	cmd := &cobra.Command{}
	cmd.SetContext(context.Background())
	cmd.SetErr(&stderr)
	ReportUnavailable(cmd, "signature lint")
	ReportUnavailable(cmd, "prompt lint")
	if got, want := stderr.String(), UnavailableClass+" [signature lint]\n"; got != want {
		t.Fatalf("got=%q want=%q", got, want)
	}
}
func TestReportUnavailableAggregationComment2227(t *testing.T) {
	f, err := parser.ParseFile(token.NewFileSet(), "lintwarn.go", nil, parser.ParseComments)
	if err != nil {
		t.Fatal(err)
	}
	for _, d := range f.Decls {
		fn, ok := d.(*ast.FuncDecl)
		if ok && fn.Name.Name == "ReportUnavailable" {
			doc := fn.Doc.Text()
			if !strings.Contains(doc, "first call's labels win") || !strings.Contains(doc, "aggregate") {
				t.Fatalf("missing caller aggregation constraint: %s", doc)
			}
			return
		}
	}
	t.Fatal("ReportUnavailable declaration missing")
}
