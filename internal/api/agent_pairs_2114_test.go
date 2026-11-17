package api

import (
	"os"
	"sort"
	"strings"
	"testing"

	"github.com/vektah/gqlparser/v2/ast"
	"github.com/vektah/gqlparser/v2/parser"
)

// hadron-cli#2114 item 1: agent.graphql (legacy) and agent_s3.graphql
// (lifecycle) duplicate the create/update documents ON PURPOSE, so an older
// server keeps working. This pins that the copies only differ by the lifecycle
// variable, argument and selection: a change to one that is not mirrored in the
// other (a new argument, a type, the omitempty annotation) fails here instead of
// silently reaching only one path.

func parseQueries2114(t *testing.T, file string) *ast.QueryDocument {
	t.Helper()
	raw, err := os.ReadFile("queries/" + file)
	if err != nil {
		t.Fatal(err)
	}
	doc, perr := parser.ParseQuery(&ast.Source{Name: file, Input: string(raw)})
	if perr != nil {
		t.Fatalf("%s: %v", file, perr)
	}
	return doc
}

type opShape2114 struct {
	vars map[string]string // variable -> "Type|omitempty|default|directives"
	args map[string]string // root argument -> value (plus "@root", "@alias", "@directives")
	sel  []string          // the FULL normalized selection (nested, aliased, directed), sorted
}

// normArgs2114 and normDirs2114 render arguments and directives canonically
// (argument order is not significant).
func normArgs2114(args ast.ArgumentList) string {
	var out []string
	for _, a := range args {
		out = append(out, a.Name+":"+a.Value.String())
	}
	sort.Strings(out)
	return strings.Join(out, ",")
}

func normDirs2114(ds ast.DirectiveList) string {
	var out []string
	for _, d := range ds {
		out = append(out, "@"+d.Name+"("+normArgs2114(d.Arguments)+")")
	}
	sort.Strings(out)
	return strings.Join(out, "")
}

// normSel2114 renders a selection set completely and recursively: aliases,
// arguments, directives, nested selections, fragment spreads and inline
// fragments are all part of what the document returns.
func normSel2114(set ast.SelectionSet) []string {
	var out []string
	for _, sel := range set {
		switch n := sel.(type) {
		case *ast.Field:
			e := n.Name
			if n.Alias != "" && n.Alias != n.Name {
				e = n.Alias + ":" + n.Name
			}
			e += "(" + normArgs2114(n.Arguments) + ")" + normDirs2114(n.Directives)
			if len(n.SelectionSet) > 0 {
				e += "{" + strings.Join(normSel2114(n.SelectionSet), " ") + "}"
			}
			out = append(out, e)
		case *ast.FragmentSpread:
			out = append(out, "..."+n.Name+normDirs2114(n.Directives))
		case *ast.InlineFragment:
			out = append(out, "...on "+n.TypeCondition+normDirs2114(n.Directives)+"{"+strings.Join(normSel2114(n.SelectionSet), " ")+"}")
		}
	}
	sort.Strings(out)
	return out
}

func shapeOf2114(t *testing.T, op *ast.OperationDefinition) opShape2114 {
	t.Helper()
	s := opShape2114{vars: map[string]string{}, args: map[string]string{}}
	for _, v := range op.VariableDefinitions {
		omit := false
		if v.Comment != nil {
			for _, c := range v.Comment.List {
				if strings.Contains(c.Text(), "@genqlient(omitempty: true)") {
					omit = true
				}
			}
		}
		def := "-"
		if v.DefaultValue != nil {
			def = v.DefaultValue.String()
		}
		s.vars[v.Variable] = v.Type.String() + "|omitempty=" + map[bool]string{true: "yes", false: "no"}[omit] + "|default=" + def + "|" + normDirs2114(v.Directives)
	}
	if len(op.SelectionSet) != 1 {
		t.Fatalf("%s: want one root field", op.Name)
	}
	root, ok := op.SelectionSet[0].(*ast.Field)
	if !ok {
		t.Fatalf("%s: root is not a field", op.Name)
	}
	s.args["@root"] = root.Name
	s.args["@alias"] = root.Alias
	s.args["@directives"] = normDirs2114(root.Directives)
	for _, a := range root.Arguments {
		s.args[a.Name] = a.Value.String()
	}
	s.sel = normSel2114(root.SelectionSet)
	return s
}

func TestAgentLegacyAndLifecycleDocumentsDoNotDrift2114(t *testing.T) {
	legacy, s3 := parseQueries2114(t, "agent.graphql"), parseQueries2114(t, "agent_s3.graphql")
	for _, pair := range [][2]string{{"CreateAgent", "CreateAgentWithLifecycle"}, {"UpdateAgent", "UpdateAgentWithLifecycle"}} {
		lop, sop := legacy.Operations.ForName(pair[0]), s3.Operations.ForName(pair[1])
		if lop == nil || sop == nil {
			t.Fatalf("%v: a paired document is missing", pair)
		}
		l, s := shapeOf2114(t, lop), shapeOf2114(t, sop)
		// The only things the lifecycle copy may add.
		if s.vars["lifecycle"] != "AgentLifecycle|omitempty=yes|default=-|" {
			t.Errorf("%s: the lifecycle variable must be an omitempty AgentLifecycle, got %q", pair[1], s.vars["lifecycle"])
		}
		if s.args["lifecycle"] != "$lifecycle" {
			t.Errorf("%s: the lifecycle argument must pass $lifecycle, got %q", pair[1], s.args["lifecycle"])
		}
		delete(s.vars, "lifecycle")
		delete(s.args, "lifecycle")
		var selNoLife []string
		for _, x := range s.sel {
			if x != "lifecycle()" {
				selNoLife = append(selNoLife, x)
			}
		}
		if len(selNoLife) == len(s.sel) {
			t.Errorf("%s must select the lifecycle it returns", pair[1])
		}
		if !equalMaps2114(l.vars, s.vars) {
			t.Errorf("%v: variables (name, type, omission) drifted:\n legacy %v\n s3     %v", pair, l.vars, s.vars)
		}
		if !equalMaps2114(l.args, s.args) {
			t.Errorf("%v: arguments drifted:\n legacy %v\n s3     %v", pair, l.args, s.args)
		}
		if strings.Join(l.sel, ",") != strings.Join(selNoLife, ",") {
			t.Errorf("%v: selection drifted:\n legacy %v\n s3     %v", pair, l.sel, selNoLife)
		}
	}
}

func equalMaps2114(a, b map[string]string) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if b[k] != v {
			return false
		}
	}
	return true
}

// The legacy documents and the fragment they share stay free of S3 fields, so
// they remain valid against a server that predates the lifecycle.
func TestLegacyAgentDocumentsStayFreeOfLifecycle2114(t *testing.T) {
	doc := parseQueries2114(t, "agent.graphql")
	var walk func(set ast.SelectionSet, where string)
	walk = func(set ast.SelectionSet, where string) {
		for _, sel := range set {
			switch n := sel.(type) {
			case *ast.Field:
				if n.Name == "lifecycle" {
					t.Errorf("%s selects lifecycle: that belongs in agent_s3.graphql", where)
				}
				walk(n.SelectionSet, where)
			case *ast.InlineFragment:
				walk(n.SelectionSet, where)
			}
		}
	}
	for _, op := range doc.Operations {
		walk(op.SelectionSet, op.Name)
		for _, v := range op.VariableDefinitions {
			if v.Variable == "lifecycle" {
				t.Errorf("%s declares $lifecycle", op.Name)
			}
		}
	}
	for _, fr := range doc.Fragments {
		walk(fr.SelectionSet, "fragment "+fr.Name)
	}
}
