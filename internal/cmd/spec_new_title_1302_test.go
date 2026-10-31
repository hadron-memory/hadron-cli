package cmd

import (
	"encoding/json"
	"strings"
	"testing"
)

// #1302 item 3: the copied-prefix guard (#1148) must leave every automatic
// naming path's plain title alone. Each case writes (not --dry-run) and
// asserts every CreateSpecNode it sends carries exactly one canonical
// "<loc> — " prefix, the target named "<loc> — <title>". The negative pins and
// the explicit-role exemption stay in spec_new_title_1148_test.go.

const spec1302Create = `{"data":{"createSpecNode":{"id":"n1","memoryId":"mem1","loc":"x","name":"x","nodeType":"info","tags":["spec"],"updatedAt":"2026-10-31T00:00:00Z"}}}`

// spec1302Run runs one spec new and returns the loc -> name of every node it
// created, and the primary result's name.
func spec1302Run(t *testing.T, scan string, args ...string) (map[string]string, string) {
	t.Helper()
	server, captured := queueGraphQL(t, map[string][]string{
		// The scan pages to exhaustion: one page, then an empty one.
		"FindNodes":      {`{"data":{"nodes":[` + scan + `]}}`, `{"data":{"nodes":[]}}`},
		"CreateSpecNode": {spec1302Create},
		"ResolveUrn":     {`{"data":{"resolveUrn":{"id":"t1","kind":"node","memoryId":"mem1"}}}`},
		"CreateEdge":     {`{"data":{"createEdge":{"id":"e1","label":"x","priority":0,"source":{"id":"n1","loc":"x"},"target":{"id":"t1","loc":"y"}}}}`},
	})
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs(append(append([]string{"spec", "new"}, args...), "--json", "--server", server.URL))
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	names := map[string]string{}
	for _, raw := range captured["CreateSpecNode"] {
		var in sentSpecInput
		if err := json.Unmarshal(raw, &in); err != nil {
			t.Fatal(err)
		}
		names[in.Input.Loc] = in.Input.Name
	}
	var result struct {
		Name string `json:"name"`
	}
	if err := json.Unmarshal([]byte(out.String()), &result); err != nil {
		t.Fatalf("output not JSON: %v\n%s", err, out.String())
	}
	return names, result.Name
}

func spec1302AssertNames(t *testing.T, names map[string]string, primary, target, want string, also []string) {
	t.Helper()
	if names[target] != want || primary != want {
		t.Errorf("target %s: payload %q, output %q, want %q", target, names[target], primary, want)
	}
	for _, loc := range append([]string{target}, also...) {
		name, ok := names[loc]
		if !ok {
			t.Errorf("no CreateSpecNode for %s; created %v", loc, names)
			continue
		}
		prefix := loc + " — "
		if !strings.HasPrefix(name, prefix) || strings.HasPrefix(strings.TrimPrefix(name, prefix), prefix) {
			t.Errorf("%s: name %q does not carry exactly one %q prefix", loc, name, prefix)
		}
	}
	if len(names) != 1+len(also) {
		t.Errorf("created %d nodes, want %d: %v", len(names), 1+len(also), names)
	}
}

func TestSpecNewTitlePositiveNewPath(t *testing.T) {
	names, primary := spec1302Run(t, "", "msg:010:01", "--new-path", "--title", "Burst allowance", "-m", specMem)
	spec1302AssertNames(t, names, primary, "msg:010:01", "msg:010:01 — Burst allowance",
		[]string{"msg", "msg:000", "msg:010", "msg:010:00"})
}

func TestSpecNewTitlePositiveNumberedRoots(t *testing.T) {
	msg := specNodeList("msg", `["spec"]`)
	feature := specNodeList("msg:010", `["spec"]`)
	rule := specNodeList("msg:010:01", `["spec"]`)
	for _, tc := range []struct {
		name, scan, target, title string
		args, also                []string
	}{
		{"new product", "", "cli", "Hadron CLI",
			[]string{"-m", specProductMem, "--new-product", "--product", "cli"}, []string{"cli:gen"}},
		{"new module", specNodeList("cli", `["spec"]`), "cli:cha", "chat command group",
			[]string{"-m", specProductMem, "--product", "cli", "--new-module", "--module", "cha"}, []string{"cli:cha:000"}},
		{"new feature", msg, "msg:010", "Color palette",
			[]string{"-m", specMem, "--module", "msg", "--new-feature"}, []string{"msg:010:00"}},
		{"allocated rule", msg + "," + feature, "msg:010:01", "Burst allowance",
			[]string{"-m", specMem, "--module", "msg", "--feature", "010"}, nil},
		{"numbered flow", msg + "," + feature + "," + rule, "msg:010:01:01", "Retry once",
			[]string{"-m", specMem, "--module", "msg", "--feature", "010", "--rule", "01", "--flow", "01"}, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			names, primary := spec1302Run(t, tc.scan, append(tc.args, "--title", tc.title)...)
			spec1302AssertNames(t, names, primary, tc.target, tc.target+" — "+tc.title, tc.also)
		})
	}
}

// #1302 item 4: the guidance is pinned by meaning, not by line wrapping — the
// help text is compared with its whitespace collapsed.
func TestSpecNewTitleHelpGuidance(t *testing.T) {
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"spec", "new", "--help"})
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	help := strings.Join(strings.Fields(out.String()), " ")
	for _, s := range []string{
		`--title must be the human text after "<loc> — "`,
		"A title already starting with that exact prefix is refused (exit 2) before any write, including --dry-run",
		"remove the prefix and run again",
		"An explicit --role keeps its literal-title behavior",
	} {
		if !strings.Contains(help, s) {
			t.Errorf("spec new --help is missing %q:\n%s", s, out.String())
		}
	}
}
