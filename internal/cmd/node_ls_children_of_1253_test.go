package cmd

import (
	"encoding/json"
	"strings"
	"testing"
)

const nodeLsChildrenEmpty = `{"data":{"findNodes":{"total":0,"degraded":null,"reason":null,"hits":[]}}}`

func nodeLsChildrenRun(t *testing.T, args ...string) (map[string]any, int) {
	t.Helper()
	srv, calls := captureGraphQL(t, map[string]string{"FindNodes": nodeLsChildrenEmpty})
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs(append([]string{"node", "list", "--json"}, append(args, "--server", srv.URL)...))
	code := 0
	if err := root.Execute(); err != nil {
		code = renderError(f, err)
	}
	var vars map[string]any
	if raw, ok := calls["FindNodes"]; ok {
		if err := json.Unmarshal(raw, &vars); err != nil {
			t.Fatal(err)
		}
	}
	return vars, code
}

// cli#1253: --children-of reaches NodeFilter.childrenOf verbatim, the empty
// string (the top level) is a value that MUST be sent, and an absent flag
// sends nothing: not null, not a filter object.
func TestNodeListChildrenOfWire(t *testing.T) {
	cases := []struct {
		name    string
		args    []string
		sent    bool
		value   string
		hasFilt bool
	}{
		{"absent sends nothing", nil, false, "", false},
		{"absent beside another filter", []string{"-m", "hrn:mem:acme.com:kb"}, false, "", true},
		{"root: empty string is sent", []string{"-m", "hrn:mem:acme.com:kb", "--children-of", ""}, true, "", true},
		{"a loc", []string{"-m", "hrn:mem:acme.com:kb", "--children-of", "findings"}, true, "findings", true},
		{"a deep loc", []string{"-m", "hrn:mem:acme.com:kb", "--children-of", "a:b:c"}, true, "a:b:c", true},
		{"composes with --prefix", []string{"-m", "hrn:mem:acme.com:kb", "--children-of", "a", "--prefix", "a:b"}, true, "a", true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			vars, code := nodeLsChildrenRun(t, c.args...)
			if code != 0 {
				t.Fatalf("exit %d", code)
			}
			filter, present := vars["filter"].(map[string]any)
			if present != c.hasFilt {
				t.Fatalf("filter present = %v, want %v (%v)", present, c.hasFilt, vars["filter"])
			}
			got, sent := filter["childrenOf"]
			if sent != c.sent {
				t.Fatalf("childrenOf sent = %v (%v), want %v", sent, got, c.sent)
			}
			if c.sent && got != c.value {
				t.Errorf("childrenOf = %q, want %q", got, c.value)
			}
		})
	}
}

// cli#1253: it composes with --limit/--offset (one page) and with the drain
// and seq modes (every page carries it).
func TestNodeListChildrenOfComposesWithPaging(t *testing.T) {
	for _, args := range [][]string{
		{"--children-of", "a", "--limit", "5", "--offset", "10"},
		{"--children-of", "a"},
		{"--children-of", "a", "--sort-seq", "asc"},
		{"--children-of", "", "--seq-gt", "3"},
	} {
		vars, code := nodeLsChildrenRun(t, args...)
		if code != 0 {
			t.Fatalf("%v: exit %d", args, code)
		}
		filter, _ := vars["filter"].(map[string]any)
		want, _ := json.Marshal(args[1])
		got, _ := json.Marshal(filter["childrenOf"])
		if string(got) != string(want) {
			t.Errorf("%v: childrenOf = %s, want %s", args, got, want)
		}
	}
}

// cli#1253: the help says direct children only.
func TestNodeListChildrenOfHelp(t *testing.T) {
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"node", "list", "--help"})
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	text := out.String()
	for _, want := range []string{"--children-of", "direct children only", "grandchildren"} {
		if !strings.Contains(text, want) {
			t.Errorf("help is missing %q", want)
		}
	}
}
