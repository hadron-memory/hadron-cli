package spec

import (
	"errors"
	"strings"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/api/gen"
)

func wired(loc, name string, edges ...specEdge) specNode {
	return specNode{Loc: loc, Name: name, NodeType: "info", Tags: []string{"spec"}, OutEdges: edges}
}

// existsIn says each loc is a live spec; a loc prefixed "!" is a live node
// that is not a spec.
func existsIn(locs ...string) func(string) (bool, bool, error) {
	set := map[string]bool{}
	for _, l := range locs {
		if strings.HasPrefix(l, "!") {
			set[l[1:]] = false
		} else {
			set[l] = true
		}
	}
	return func(l string) (bool, bool, error) { spec, ok := set[l]; return ok, spec, nil }
}

// findingTargets lists "<citation>→<target>" for each unwired finding.
func findingTargets(t *testing.T, fs []lintFindingDTO) []string {
	t.Helper()
	var out []string
	for _, f := range fs {
		if f.Rule != "unwired" || f.Severity != sevInfo {
			t.Errorf("unexpected finding %+v", f)
		}
		if i := strings.Index(f.Message, "`hadron spec link "); i >= 0 {
			parts := strings.Fields(f.Message[i+len("`hadron spec link "):])
			out = append(out, parts[0]+"→"+parts[1])
			continue
		}
		i := strings.Index(f.Message, "`hadron edge create ")
		parts := strings.Fields(f.Message[i+len("`hadron edge create "):])
		out = append(out, parts[3]+"→"+parts[5]+" (edge create)")
	}
	return out
}

func TestUnwiredPlansWhatSpecNewPlans(t *testing.T) {
	cases := []struct {
		name   string
		node   specNode
		exists []string
		want   []string
	}{
		{"rule: parent and contract", wired("msg:010:02", "msg:010:02 — W"),
			[]string{"msg:010", "msg:010:00"}, []string{"msg:010:02→msg:010", "msg:010:02→msg:010:00"}},
		{"flow: parent only", wired("msg:010:02:01", "x — F"),
			[]string{"msg:010:02", "msg:010:00"}, []string{"msg:010:02:01→msg:010:02"}},
		{"contract: parent only", wired("msg:010:00", "x — C"),
			[]string{"msg:010", "msg:010:00"}, []string{"msg:010:00→msg:010"}},
		{"feature: module and :000", wired("msg:010", "x — F"),
			[]string{"msg", "msg:000"}, []string{"msg:010→msg", "msg:010→msg:000"}},
		{"product-rooted module: product root and :gen", wired("cli:cha", "x — M"),
			[]string{"cli", "cli:gen"}, []string{"cli:cha→cli", "cli:cha→cli:gen"}},
		{"flat module root: nothing", wired("msg", "x — M"), []string{"msg:000"}, nil},
		{"targets that do not exist: nothing", wired("msg:010:02", "x — W"), nil, nil},
		{"non-legacy loc: nothing", wired("onboarding:mentor:screens", "x — S"),
			[]string{"onboarding:mentor"}, nil},
		{"renamed parent edge still counts", wired("msg:010:02", "x — New title",
			specEdge{Name: "Old title", Loc: "msg:010"}, specEdge{Name: inheritEdgeLabel, Loc: "msg:010:00"}),
			[]string{"msg:010", "msg:010:00"}, nil},
		{"a cross-reference to the contract is not the inheritance edge", wired("msg:010:02", "x — W",
			specEdge{Name: "W", Loc: "msg:010"}, specEdge{Name: "documents W on the C entity", Loc: "msg:010:00"}),
			[]string{"msg:010", "msg:010:00"}, []string{"msg:010:02→msg:010:00"}},
		{"unavailable: nothing", specNode{Loc: "msg:010:02", Unavailable: true},
			[]string{"msg:010", "msg:010:00"}, nil},
		// spec new plans an edge to whatever node holds the loc, but spec link
		// takes only specs: a non-spec target gets a remedy that can run.
		{"non-spec target: edge create instead of spec link", wired("msg:010:02", "x — W"),
			[]string{"!msg:010", "msg:010:00"}, []string{"msg:010:02→msg:010 (edge create)", "msg:010:02→msg:010:00"}},
		// An untagged node at a citation loc is lint's missing-spec-tag case,
		// not a spec that owes wiring.
		{"non-spec source: nothing", specNode{Loc: "msg:010:02", Name: "x — W"},
			[]string{"msg:010", "msg:010:00"}, nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			fs, err := unwiredFindings([]specNode{c.node}, lintMem, existsIn(c.exists...))
			if err != nil {
				t.Fatal(err)
			}
			got := findingTargets(t, fs)
			if strings.Join(got, ",") != strings.Join(c.want, ",") {
				t.Errorf("got %v, want %v", got, c.want)
			}
		})
	}
}

func TestUnwiredParentLabelIsTheTitle(t *testing.T) {
	fs, _ := unwiredFindings([]specNode{wired("msg:010:02", "msg:010:02 — Win back")}, lintMem, existsIn("msg:010"))
	if len(fs) != 1 || !strings.Contains(fs[0].Message, `-m `+lintMem+` --label 'Win back'`+"`") {
		t.Errorf("the parent edge is labelled with the spec's title, as spec new labels it: %+v", fs)
	}
}

func TestUnwiredLookupErrorStopsTheReport(t *testing.T) {
	boom := errors.New("server down")
	_, err := unwiredFindings([]specNode{wired("msg:010:02", "x — W")}, lintMem,
		func(string) (bool, bool, error) { return false, false, boom })
	if !errors.Is(err, boom) {
		t.Errorf("a failed lookup must not read as a missing target: %v", err)
	}
}

// An edge whose target sits in ANOTHER memory is a cross-reference to that
// corpus, whatever its loc: it cannot stand in for the local edge spec new
// plans, so a same-loc target elsewhere must not hide the missing wiring
// (#1083 local review). Pinned through both read projections, beside a
// same-memory control, since each projection carries the memory ids itself.
func TestUnwiredForeignSameLocEdgeIsNotLocalWiring(t *testing.T) {
	str := func(s string) *string { return &s }
	third, inherit := "Third", inheritEdgeLabel
	type edge struct {
		name     *string
		loc, mem string
	}
	edges := func(mem string) []edge {
		return []edge{{str(third), "msg:010", mem}, {str(inherit), "msg:010:00", mem}}
	}
	single := func(es []edge) specNode {
		n := &gen.GetNodeNode{Loc: "msg:010:03", Name: "msg:010:03 — Third", MemoryId: "local", Tags: []string{"spec"}}
		for _, e := range es {
			n.OutgoingEdges = append(n.OutgoingEdges, &gen.GetNodeNodeOutgoingEdgesEdge{
				Name: e.name, Target: &gen.GetNodeNodeOutgoingEdgesEdgeTargetNode{Loc: e.loc, MemoryId: e.mem}})
		}
		return nodeFromGQL(n)
	}
	batch := func(es []edge) specNode {
		n := &gen.NodeBatchNodeBatchNodeBatchResultNodesNode{Loc: "msg:010:03", Name: "msg:010:03 — Third", MemoryId: "local", Tags: []string{"spec"}}
		for _, e := range es {
			n.OutgoingEdges = append(n.OutgoingEdges, &gen.NodeBatchNodeBatchNodeBatchResultNodesNodeOutgoingEdgesEdge{
				Name: e.name, Target: &gen.NodeBatchNodeBatchNodeBatchResultNodesNodeOutgoingEdgesEdgeTargetNode{Loc: e.loc, MemoryId: e.mem}})
		}
		return nodeFromBatch(n)
	}
	for pname, project := range map[string]func([]edge) specNode{"single": single, "batch": batch} {
		for _, c := range []struct {
			mem  string
			want []string
		}{
			{"foreign", []string{"msg:010:03→msg:010", "msg:010:03→msg:010:00"}},
			{"local", nil},
		} {
			t.Run(pname+"/"+c.mem, func(t *testing.T) {
				fs, err := unwiredFindings([]specNode{project(edges(c.mem))}, lintMem, existsIn("msg:010", "msg:010:00"))
				if err != nil {
					t.Fatal(err)
				}
				if got := findingTargets(t, fs); strings.Join(got, ",") != strings.Join(c.want, ",") {
					t.Errorf("got %v, want %v", got, c.want)
				}
			})
		}
	}
}

func TestUnwiredNonSpecTargetRemedyIsEdgeCreate(t *testing.T) {
	fs, _ := unwiredFindings([]specNode{wired("msg:010:02", "msg:010:02 — Win back")}, lintMem, existsIn("!msg:010"))
	want := "`hadron edge create -m " + lintMem + " --from msg:010:02 --to msg:010 --name 'Win back'`"
	if len(fs) != 1 || !strings.Contains(fs[0].Message, want) {
		t.Errorf("a non-spec target is wired with edge create, as spec link refuses it: want %s in %+v", want, fs)
	}
}
