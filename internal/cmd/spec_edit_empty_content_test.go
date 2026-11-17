package cmd

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/exitcode"
)

// cli#1092: sibling of cli#1035 on the specs corpus. A zero-byte --content-file
// (or empty piped `--content -`) would blank a spec body; it is refused before
// ANY request unless --allow-empty.

func specEditEmptyCases(t *testing.T) []struct {
	name, stdin, source string
	args                []string
} {
	file := writeTemp(t, "empty.md", "")
	return []struct {
		name, stdin, source string
		args                []string
	}{
		{"zero-byte file", "", file, []string{"--content-file", file}},
		{"empty stdin", "", "stdin", []string{"--content", "-"}},
	}
}

func TestSpecEditEmptyContentIsRefusedBeforeAnyRequest(t *testing.T) {
	for _, c := range specEditEmptyCases(t) {
		t.Run(c.name, func(t *testing.T) {
			_, captured, err := runSpecEdit(t, guardMocks(guardWriteOK), c.stdin, c.args...)
			if err == nil || exitCodeFor(err) != exitcode.Usage {
				t.Fatalf("want a Usage refusal, got %v", err)
			}
			for _, want := range []string{c.source, "--allow-empty"} {
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

func TestSpecEditEmptyContentWithAllowEmptySendsOneEmptyBodyWrite(t *testing.T) {
	for _, c := range specEditEmptyCases(t) {
		t.Run(c.name, func(t *testing.T) {
			_, captured, err := runSpecEdit(t, guardMocks(guardWriteOK), c.stdin, append(c.args, "--allow-empty")...)
			if err != nil {
				t.Fatal(err)
			}
			if n := len(captured["UpdateSpecNode"]); n != 1 {
				t.Fatalf("want exactly one write, got %d", n)
			}
			var v struct {
				Input struct {
					Content *string `json:"content"`
				} `json:"input"`
			}
			if err := json.Unmarshal(captured["UpdateSpecNode"][0], &v); err != nil {
				t.Fatal(err)
			}
			if v.Input.Content == nil || *v.Input.Content != "" {
				t.Errorf("the write must carry an explicit empty content, got %v", v.Input.Content)
			}
		})
	}
}

func TestSpecEditEmptyContentGuardLeavesOtherInputsAlone(t *testing.T) {
	ws := writeTemp(t, "ws.md", "  \n")
	nonEmpty := writeTemp(t, "b.md", "# body\n")
	for name, args := range map[string][]string{
		"whitespace-only file":                {"--content-file", ws},
		"inline empty --content":              {"--content", ""},
		"--allow-empty with a non-empty file": {"--content-file", nonEmpty, "--allow-empty"},
	} {
		t.Run(name, func(t *testing.T) {
			_, captured, err := runSpecEdit(t, guardMocks(guardWriteOK), "", args...)
			if err != nil {
				t.Fatal(err)
			}
			if n := len(captured["UpdateSpecNode"]); n != 1 {
				t.Errorf("want exactly one write, got %d", n)
			}
		})
	}
}
