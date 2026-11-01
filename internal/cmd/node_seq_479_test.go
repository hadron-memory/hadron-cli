package cmd

import (
	"strings"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/exitcode"
)

// cli#479: --seq on node create/update orders ordinary nodes among siblings.
// The value is sent only when the flag is given (explicit 0 is distinct from
// absent), and the server's refusal (SEQ_PROTECTED / LOC_PROTECTED /
// ROLE_GOVERNED) is the boundary: no client allowlist, shown verbatim.

func seq479Input(t *testing.T, args ...string) (map[string]any, error) {
	t.Helper()
	responses := map[string]string{
		"ResolveUrn": resolveNodeJSON,
		"GetNode":    `{"data":{"node":` + nodeDetailJSON + `}}`,
		"CreateNode": `{"data":{"createNode":` + authoredNode1341 + `}}`,
		"UpdateNode": `{"data":{"updateNode":` + authoredNode1341 + `}}`,
	}
	captured, out, err := run1341(t, responses, args...)
	if err != nil {
		return nil, err
	}
	op := "UpdateNode"
	if args[1] == "create" {
		op = "CreateNode"
	}
	in, _ := vars1341(t, captured, op)["input"].(map[string]any)
	if in == nil {
		t.Fatalf("%s sent no input\n%s", op, out)
	}
	return in, nil
}

func TestNodeUpdateSeq479(t *testing.T) {
	in, err := seq479Input(t, "node", "update", nodeURN, "--seq", "3")
	if err != nil {
		t.Fatal(err)
	}
	if got, ok := in["seq"].(float64); !ok || got != 3 {
		t.Errorf("input.seq = %v, want 3", in["seq"])
	}

	// Explicit zero is a real position, not "absent".
	in, err = seq479Input(t, "node", "update", nodeURN, "--seq", "0")
	if err != nil {
		t.Fatal(err)
	}
	if got, ok := in["seq"].(float64); !ok || got != 0 {
		t.Errorf("--seq 0: input.seq = %v (present=%v), want 0", in["seq"], ok)
	}

	// Without the flag no seq key is sent: omitted means preserve.
	in, err = seq479Input(t, "node", "update", nodeURN, "--name", "Renamed")
	if err != nil {
		t.Fatal(err)
	}
	if _, present := in["seq"]; present {
		t.Errorf("seq sent without --seq: %v", in["seq"])
	}
}

func TestNodeCreateSeq479(t *testing.T) {
	base := []string{"node", "create", "-m", "acme.com::kb", "--loc", "findings:new", "--name", "New"}
	in, err := seq479Input(t, append(base, "--seq", "0")...)
	if err != nil {
		t.Fatal(err)
	}
	if got, ok := in["seq"].(float64); !ok || got != 0 {
		t.Errorf("--seq 0: input.seq = %v (present=%v), want 0", in["seq"], ok)
	}
	in, err = seq479Input(t, append(base, "--seq", "7")...)
	if err != nil {
		t.Fatal(err)
	}
	if got, ok := in["seq"].(float64); !ok || got != 7 {
		t.Errorf("input.seq = %v, want 7", in["seq"])
	}
	in, err = seq479Input(t, base...)
	if err != nil {
		t.Fatal(err)
	}
	if _, present := in["seq"]; present {
		t.Errorf("seq sent without --seq: %v", in["seq"])
	}
}

// A server refusal is shown verbatim and nothing else is written.
func TestNodeUpdateSeqRefusal479(t *testing.T) {
	msg := "seq writes are refused on chat-message and record nodes"
	responses := map[string]string{
		"ResolveUrn": resolveNodeJSON,
		"GetNode":    `{"data":{"node":` + nodeDetailJSON + `}}`,
		"UpdateNode": `{"errors":[{"message":"` + msg + `","extensions":{"code":"SEQ_PROTECTED"}}]}`,
	}
	captured, _, err := run1341(t, responses, "node", "update", nodeURN, "--seq", "2")
	if err == nil {
		t.Fatal("want the server's refusal to fail the command")
	}
	if !strings.Contains(err.Error(), msg) {
		t.Errorf("refusal not shown verbatim: %v", err)
	}
	// The existing mapping, deliberately unchanged (gate fence 2): SEQ_PROTECTED
	// has no case in codeForExtension, so it exits 1, unlike its siblings
	// LOC_PROTECTED and ROLE_GOVERNED (2). Mapping it is a follow-up for the
	// gate to decide, not part of this cut.
	if got := exitCodeFor(err); got != exitcode.Error {
		t.Errorf("exit = %d, want the unmapped default %d", got, exitcode.Error)
	}
	for op := range captured {
		if strings.HasPrefix(op, "Update") && op != "UpdateNode" {
			t.Errorf("unexpected write %s after refusal", op)
		}
	}
}

func TestNodeSeqHelp479(t *testing.T) {
	for _, sub := range []string{"create", "update"} {
		f, out := testFactory(t)
		root := NewRootCmd(f)
		root.SetOut(out)
		root.SetArgs([]string{"node", sub, "--help"})
		if err := root.Execute(); err != nil {
			t.Fatal(err)
		}
		h := out.String()
		for _, want := range []string{"--seq", "among siblings", "refuse"} {
			if !strings.Contains(h, want) {
				t.Errorf("node %s --help lacks %q", sub, want)
			}
		}
	}
}
