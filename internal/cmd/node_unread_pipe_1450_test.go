package cmd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/exitcode"
)

// #1450: a non-empty pipe that no flag reads is refused (exit 2) instead of
// the command succeeding with the pipe's bytes dropped.

func stubs1450() map[string]string {
	s := bodyOnlyStubs("stored body")
	s["UpdateNodeData"] = `{"data":{"updateNodeData":` + nodeJSON + `}}`
	s["CreateNode"] = `{"data":{"createNode":` + nodeJSON + `}}`
	return s
}

func TestNodeCreateRefusesUnreadPipe1450(t *testing.T) {
	// The RED leg on main: this exited 0 with "✓ created" and an empty body.
	_, errOut, captured, err := runNode(t, stubs1450(), "piped body",
		"node", "create", "-m", "acme.com::kb", "--loc", "x:y", "--name", "N")
	if exitCodeFor(err) != exitcode.Usage {
		t.Fatalf("want exit 2, got %v", err)
	}
	if !strings.Contains(errOut, "--content -") {
		t.Errorf("refusal should name the flag that reads a pipe:\n%s", errOut)
	}
	if len(captured) != 0 {
		t.Errorf("a refused create must send nothing, sent %v", captured)
	}
}

func TestNodeCreateUnreadPipeLegsUnchanged1450(t *testing.T) {
	for _, tc := range []struct {
		name  string
		stdin string
		args  []string
	}{
		{"no pipe attached", "", nil},
		{"content - reads the pipe", "piped body", []string{"--content", "-"}},
		{"content-file - reads the pipe", "piped body", []string{"--content-file", "-"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			args := append([]string{"node", "create", "-m", "acme.com::kb", "--loc", "x:y", "--name", "N"}, tc.args...)
			_, _, captured, err := runNode(t, stubs1450(), tc.stdin, args...)
			if err != nil {
				t.Fatalf("legacy leg must still succeed: %v", err)
			}
			if _, ok := captured["CreateNode"]; !ok {
				t.Errorf("expected a CreateNode call")
			}
		})
	}
}

func TestNodeUpdateRefusesUnreadPipe1450(t *testing.T) {
	_, errOut, captured, err := runNode(t, stubs1450(), "piped body",
		"node", "update", testNodeURN, "--name", "Renamed")
	if exitCodeFor(err) != exitcode.Usage {
		t.Fatalf("want exit 2, got %v", err)
	}
	if !strings.Contains(errOut, "--content -") || !strings.Contains(errOut, "--abstract -") || !strings.Contains(errOut, "--data-merge -") {
		t.Errorf("refusal should name the stdin flags:\n%s", errOut)
	}
	if len(captured) != 0 {
		t.Errorf("a refused update must send no request, sent %v", captured)
	}
}

func TestNodeUpdateStdinReaderSuppressesGuard1450(t *testing.T) {
	for _, flag := range [][]string{
		{"--content", "-"}, {"--content-file", "-"}, {"--abstract", "-"}, {"--data-merge", "-"},
	} {
		t.Run(flag[0]+" "+flag[1], func(t *testing.T) {
			body := "piped body"
			if flag[0] == "--data-merge" {
				body = `{"a":1}`
			}
			args := append([]string{"node", "update", testNodeURN}, flag...)
			_, _, captured, err := runNode(t, stubs1450(), body, args...)
			if err != nil {
				t.Fatalf("a pipe a flag consumes must not trip the guard: %v", err)
			}
			op := "UpdateNode"
			if flag[0] == "--data-merge" {
				op = "UpdateNodeData"
			}
			if _, ok := captured[op]; !ok {
				t.Errorf("expected an %s call", op)
			}
		})
	}
	t.Run("no pipe attached", func(t *testing.T) {
		_, _, _, err := runNode(t, stubs1450(), "", "node", "update", testNodeURN, "--name", "Renamed")
		if err != nil {
			t.Fatal(err)
		}
	})
}

// The file-form flags read a PATH, never stdin, so a pipe beside them is still
// unread and refused (#1450 BLIND P2-b: pins the reader inventory per flag).
func TestNodeUpdateFileFlagsDoNotConsumeStdin1450(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "in.txt")
	if err := os.WriteFile(file, []byte(`{"a":1}`), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, flag := range []string{"--abstract-file", "--data-merge-file", "--data-file", "--properties-file"} {
		t.Run(flag, func(t *testing.T) {
			_, _, captured, err := runNode(t, stubs1450(), "piped body",
				"node", "update", testNodeURN, flag, file)
			if exitCodeFor(err) != exitcode.Usage {
				t.Fatalf("a pipe beside %s is unread and must be refused, got %v", flag, err)
			}
			if !strings.Contains(err.Error(), "stdin has input but no flag reads it") {
				t.Errorf("must be the guard's refusal, not another usage error: %v", err)
			}
			if len(captured) != 0 {
				t.Errorf("a refused update must send no request, sent %v", captured)
			}
		})
	}
}

// The dash spelling of a *-file flag is a file literally named "-", never
// stdin (os.ReadFile, not the pipe), so a pipe beside it is unread too: pinned
// for update and for create's data/properties file flags (#1450 BLIND P2-b).
func TestDashFileFlagsDoNotConsumeStdin1450(t *testing.T) {
	cases := [][]string{
		{"node", "update", testNodeURN, "--abstract-file", "-"},
		{"node", "update", testNodeURN, "--data-merge-file", "-"},
		{"node", "update", testNodeURN, "--data-file", "-"},
		{"node", "update", testNodeURN, "--properties-file", "-"},
		{"node", "create", "-m", "acme.com::kb", "--loc", "x:y", "--name", "N", "--data-file", "-"},
		{"node", "create", "-m", "acme.com::kb", "--loc", "x:y", "--name", "N", "--properties-file", "-"},
	}
	for _, args := range cases {
		t.Run(strings.Join(args[1:2], "")+" "+args[len(args)-2], func(t *testing.T) {
			_, _, captured, err := runNode(t, stubs1450(), "piped body", args...)
			if exitCodeFor(err) != exitcode.Usage || err == nil ||
				!strings.Contains(err.Error(), "stdin has input but no flag reads it") {
				t.Fatalf("want the guard's exit-2 refusal, got %v", err)
			}
			if len(captured) != 0 {
				t.Errorf("a refused command must send nothing, sent %v", captured)
			}
		})
	}
}

// create counts stdin readers like update: a body from an inline value or a
// file PATH leaves a live pipe unread, so it is refused too (#1450 LOCAL P2).
func TestNodeCreateInlineOrPathBodyWithUnreadPipe1450(t *testing.T) {
	file := filepath.Join(t.TempDir(), "body.md")
	if err := os.WriteFile(file, []byte("from file"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, flag := range [][]string{{"--content", "inline"}, {"--content-file", file}} {
		t.Run(flag[0], func(t *testing.T) {
			args := append([]string{"node", "create", "-m", "acme.com::kb", "--loc", "x:y", "--name", "N"}, flag...)
			_, _, captured, err := runNode(t, stubs1450(), "piped body", args...)
			if exitCodeFor(err) != exitcode.Usage || err == nil ||
				!strings.Contains(err.Error(), "stdin has input but no flag reads it") {
				t.Fatalf("want the guard's exit-2 refusal, got %v", err)
			}
			if len(captured) != 0 {
				t.Errorf("a refused create must send nothing, sent %v", captured)
			}
		})
	}
}
