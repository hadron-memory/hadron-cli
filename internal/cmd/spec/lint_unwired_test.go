package spec

import (
	"errors"
	"strings"
	"testing"
)

func wired(loc, name string, edges ...specEdge) specNode {
	return specNode{Loc: loc, Name: name, NodeType: "info", Tags: []string{"spec"}, OutEdges: edges}
}

func existsIn(locs ...string) func(string) (bool, error) {
	set := map[string]bool{}
	for _, l := range locs {
		set[l] = true
	}
	return func(l string) (bool, error) { return set[l], nil }
}

// findingTargets lists "<citation>→<target>" for each unwired finding.
func findingTargets(t *testing.T, fs []lintFindingDTO) []string {
	t.Helper()
	var out []string
	for _, f := range fs {
		if f.Rule != "unwired" || f.Severity != sevInfo {
			t.Errorf("unexpected finding %+v", f)
		}
		i := strings.Index(f.Message, "`hadron spec link ")
		parts := strings.Fields(f.Message[i+len("`hadron spec link "):])
		out = append(out, parts[0]+"→"+parts[1])
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
	if len(fs) != 1 || !strings.Contains(fs[0].Message, `-m `+lintMem+` --label "Win back"`+"`") {
		t.Errorf("the parent edge is labelled with the spec's title, as spec new labels it: %+v", fs)
	}
}

func TestUnwiredLookupErrorStopsTheReport(t *testing.T) {
	boom := errors.New("server down")
	_, err := unwiredFindings([]specNode{wired("msg:010:02", "x — W")}, lintMem,
		func(string) (bool, error) { return false, boom })
	if !errors.Is(err, boom) {
		t.Errorf("a failed lookup must not read as a missing target: %v", err)
	}
}
