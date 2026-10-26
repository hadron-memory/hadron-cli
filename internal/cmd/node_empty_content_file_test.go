package cmd

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/exitcode"
)

// cli#1035: a zero-byte --content-file (a failed `generate > body.md`) used to
// blank the stored body — the empty string is a real "clear" on the wire. The
// refusal is client-side and precedes every request; --allow-empty is the
// deliberate "yes, empty" opt-in.

type emptyBodyWrite struct {
	name  string
	stubs map[string]string
	op    string
	args  []string
}

func emptyBodyWrites(file string) []emptyBodyWrite {
	return []emptyBodyWrite{
		{"node update", bodyOnlyStubs("stored"), "UpdateNode",
			[]string{"node", "update", testNodeURN, "--content-file", file}},
		{"node add", map[string]string{"CreateNode": `{"data":{"createNode":` + nodeJSON + `}}`}, "CreateNode",
			[]string{"node", "add", "-m", "acme.com::kb", "--loc", "x:y", "--name", "N", "--content-file", file}},
	}
}

// contentSentOn reports the content the op carried and whether the op was sent
// at all. node add omits an empty body from CreateNode's input (the server
// creates an empty node), so absent content on a sent op reads as "".
func contentSentOn(t *testing.T, captured map[string]json.RawMessage, op string) (string, bool) {
	t.Helper()
	raw, ok := captured[op]
	if !ok {
		return "", false
	}
	var vars struct {
		Input struct {
			Content *string `json:"content"`
		} `json:"input"`
	}
	if err := json.Unmarshal(raw, &vars); err != nil {
		t.Fatal(err)
	}
	if vars.Input.Content == nil {
		return "", true
	}
	return *vars.Input.Content, true
}

func TestEmptyContentFileIsRefusedBeforeAnyRequest(t *testing.T) {
	file := writeTemp(t, "empty.md", "")
	for _, w := range emptyBodyWrites(file) {
		t.Run(w.name, func(t *testing.T) {
			_, _, captured, err := runNode(t, w.stubs, "", w.args...)
			if err == nil {
				t.Fatal("a zero-byte --content-file must be refused")
			}
			if code := exitCodeFor(err); code != exitcode.Usage {
				t.Errorf("exit = %d, want Usage", code)
			}
			for _, want := range []string{file, "--allow-empty"} {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("refusal %q must name %q", err, want)
				}
			}
			if len(captured) != 0 {
				t.Errorf("refused input must send no request, sent %v", captured)
			}
		})
	}
}

func TestEmptyContentFileWithAllowEmptySendsOneEmptyBodyMutation(t *testing.T) {
	file := writeTemp(t, "empty.md", "")
	for _, w := range emptyBodyWrites(file) {
		t.Run(w.name, func(t *testing.T) {
			_, _, captured, err := runNode(t, w.stubs, "", append(w.args, "--allow-empty")...)
			if err != nil {
				t.Fatal(err)
			}
			got, ok := contentSentOn(t, captured, w.op)
			if !ok || got != "" {
				t.Errorf("want one %s carrying content \"\", got %q (present=%v)", w.op, got, ok)
			}
		})
	}
}

func TestEmptyStdinContentFileIsRefusedIdentically(t *testing.T) {
	for _, w := range emptyBodyWrites("-") {
		t.Run(w.name, func(t *testing.T) {
			gql, captured := captureGraphQL(t, w.stubs)
			f, _ := testFactory(t)
			f.IOStreams.In = strings.NewReader("")
			root := NewRootCmd(f)
			root.SetArgs(append(w.args, "--server", gql.URL))
			err := root.Execute()
			if err == nil || exitCodeFor(err) != exitcode.Usage {
				t.Fatalf("zero-byte piped stdin must be refused as Usage, got %v", err)
			}
			for _, want := range []string{"stdin", "--allow-empty"} {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("refusal %q must name %q", err, want)
				}
			}
			if len(captured) != 0 {
				t.Errorf("refused input must send no request, sent %v", captured)
			}
		})
	}
}

func TestEmptyStdinContentFileWithAllowEmptySendsOneEmptyBodyMutation(t *testing.T) {
	for _, w := range emptyBodyWrites("-") {
		t.Run(w.name, func(t *testing.T) {
			gql, captured := captureGraphQL(t, w.stubs)
			f, _ := testFactory(t)
			f.IOStreams.In = strings.NewReader("")
			root := NewRootCmd(f)
			root.SetArgs(append(append(w.args, "--allow-empty"), "--server", gql.URL))
			if err := root.Execute(); err != nil {
				t.Fatal(err)
			}
			if got, ok := contentSentOn(t, captured, w.op); !ok || got != "" {
				t.Errorf("want empty-body %s, got %q (present=%v)", w.op, got, ok)
			}
		})
	}
}

// Only a ZERO-byte file is the failure signature. A whitespace-only file and an
// explicit --content "" keep their behaviour, and --allow-empty beside a
// non-empty file is a harmless no-op.
func TestEmptyContentGuardLeavesOtherInputsAlone(t *testing.T) {
	cases := []struct {
		name string
		args func(file string) []string
		file string
		want string
	}{
		{"whitespace-only file", func(f string) []string { return []string{"--content-file", f} }, "  \n", "  \n"},
		{"explicit empty --content", func(string) []string { return []string{"--content", ""} }, "", ""},
		{"--allow-empty with a non-empty file", func(f string) []string { return []string{"--content-file", f, "--allow-empty"} }, "body", "body"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			file := writeTemp(t, "b.md", c.file)
			_, _, captured, err := runNode(t, bodyOnlyStubs("stored"), "",
				append([]string{"node", "update", testNodeURN}, c.args(file)...)...)
			if err != nil {
				t.Fatal(err)
			}
			if got, ok := contentSentOn(t, captured, "UpdateNode"); !ok || got != c.want {
				t.Errorf("sent %q (present=%v), want %q", got, ok, c.want)
			}
		})
	}
}
