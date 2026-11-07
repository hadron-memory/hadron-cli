package cmd

import (
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
		{"inline content wins over a live pipe", "ignored", []string{"--content", "inline"}},
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
