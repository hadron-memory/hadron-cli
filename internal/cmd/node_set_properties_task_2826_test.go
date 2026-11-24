package cmd

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/api/gen"
	"github.com/hadron-memory/hadron-cli/internal/exitcode"
)

// cli#2826: `node set-properties` on a TASK node. The generic merge mutations refuse a governed
// task node, so the merge goes through the task door with mergeExports:true (one call, the patch
// as `properties`); every other node keeps updateNodeProperties and never sends mergeExports.

const (
	governedRefusal2826 = `{"errors":[{"message":"this node is governed: write it through its own door","extensions":{"code":"FORBIDDEN"}}]}`
	mergedTask2826      = `{"data":{"updateTaskNode":{"id":"n1","memoryId":"mem1","loc":"tasks:build","name":"Build","properties":{"exports":{"codexSkill":{"enable":false},"claudeSkill":{"enable":true}}},"updatedAt":"2026-11-21T00:00:00Z"}}}`
)

// taskNode2826 is the NodeKind answer for a task node. The kind read selects the runnable flag
// ONLY (cli#2826): a node's properties and role are never read, so role is not part of the answer
// (a task that is also a review is told apart by the server's refusal, not by the CLI).
func taskNode2826() string { return `{"data":{"node":{"id":"n1","isRunnable":true}}}` }

func setProps2826(t *testing.T, responses map[string]string, args ...string) (map[string]json.RawMessage, string, error) {
	t.Helper()
	own := map[string]string{"ResolveUrn": resolveNodeJSON}
	for op, resp := range responses {
		own[op] = resp
	}
	return run1341(t, own, append([]string{"node", "set-properties", nodeURN}, args...)...)
}

// Acceptance 1 and 2: on a task node the generic merge is refused by the server's governed gate
// (what main sent), and the command now makes ONE updateTaskNode call with mergeExports:true and
// the patch as the properties, never a read-modify-write.
func TestSetPropertiesOnATaskNodeUsesTheTaskDoorWithMergeExports2826(t *testing.T) {
	captured, out, err := setProps2826(t, map[string]string{
		"NodeKind": taskNode2826(),
		// main's route: a real server refuses it for a governed node.
		"UpdateNodeProperties":    governedRefusal2826,
		"MergeTaskNodeProperties": mergedTask2826,
	}, "--patch", `{"exports":{"codexSkill":{"enable":false}}}`, "--reason", "disable on Codex", "--json")
	if err != nil {
		t.Fatalf("a task node must be writable: %v\n%s", err, out)
	}
	for _, op := range []string{"UpdateNodeProperties", "UpdateTaskNode", "UpdateNode"} {
		if _, sent := captured[op]; sent {
			t.Errorf("a task node must not be written through %s", op)
		}
	}
	v := vars1341(t, captured, "MergeTaskNodeProperties")
	if v["mergeExports"] != true {
		t.Errorf("mergeExports must be true: %v", v)
	}
	in, _ := v["input"].(map[string]any)
	if in["ref"] != "n1" || asJSON(in["properties"]) != `{"exports":{"codexSkill":{"enable":false}}}` || in["reason"] != "disable on Codex" {
		t.Errorf("the input is the node id, the patch as properties and the reason: %v", in)
	}
	for k := range in {
		if k != "ref" && k != "properties" && k != "reason" {
			t.Errorf("only ref, properties and reason are sent, got %q", k)
		}
	}
	var dto map[string]any
	if json.Unmarshal([]byte(out), &dto) != nil || len(dto) != 6 || dto["loc"] != "tasks:build" ||
		asJSON(dto["properties"]) != `{"exports":{"claudeSkill":{"enable":true},"codexSkill":{"enable":false}}}` {
		t.Errorf("--json has the generic route's six fields with the merged properties: %s", out)
	}
}

// Acceptance 2, the other half: a non-task node keeps one updateNodeProperties and never sends
// mergeExports (the argument exists on the task door only).
func TestSetPropertiesOnAnOrdinaryNodeKeepsTheGenericMerge2826(t *testing.T) {
	captured, _, err := setProps2826(t, map[string]string{
		"NodeKind":             `{"data":{"node":{"id":"n1","isRunnable":false}}}`,
		"UpdateNodeProperties": `{"data":{"updateNodeProperties":{"id":"n1","memoryId":"mem1","loc":"findings:flaky-ci","name":"Flaky CI","properties":{"b":2},"updatedAt":"2026-10-31T00:00:00Z"}}}`,
	}, "--patch", `{"b":2}`)
	if err != nil {
		t.Fatal(err)
	}
	for _, op := range []string{"MergeTaskNodeProperties", "UpdateTaskNode"} {
		if _, sent := captured[op]; sent {
			t.Errorf("an ordinary node must not go through %s", op)
		}
	}
	if raw := string(captured["UpdateNodeProperties"]); strings.Contains(raw, "mergeExports") {
		t.Errorf("mergeExports does not exist on the generic mutation: %s", raw)
	}
}

// cor:agt:030:06 S9: two patches to different hosts each send only their own host's keys, and
// the CLI never reads `exports` first (the node read is for its kind alone, and what it returned
// is not part of what is sent).
func TestSetPropertiesTwoHostsSendOnlyTheirOwnPatch2826(t *testing.T) {
	// The kind read selects no properties at all, so the stored declaration of the OTHER host can
	// never reach the write; the document is asserted below.
	node := taskNode2826()
	for host, patch := range map[string]string{
		"codexSkill":  `{"exports":{"codexSkill":{"enable":false}}}`,
		"claudeSkill": `{"exports":{"claudeSkill":{"enable":false}}}`,
	} {
		captured, _, err := setProps2826(t, map[string]string{"NodeKind": node, "MergeTaskNodeProperties": mergedTask2826}, "--patch", patch)
		if err != nil {
			t.Fatal(err)
		}
		in, _ := vars1341(t, captured, "MergeTaskNodeProperties")["input"].(map[string]any)
		if asJSON(in["properties"]) != patch {
			t.Errorf("%s: exactly that host's patch is sent: %v", host, in["properties"])
		}
		// Never a read-modify-write: the only read of the node is its kind; nothing that returns
		// its properties (GetNode, GetNodeRaw, FindNodes, NodeBatch) is ever sent.
		for _, op := range []string{"GetNode", "GetNodeRaw", "FindNodes", "NodeBatch", "NodeBatchContent", "UpdateNodeProperties"} {
			if _, sent := captured[op]; sent {
				t.Errorf("%s: %s must not be sent (a read of the stored exports is a read-modify-write)", host, op)
			}
		}
	}
}

// A node that is a task AND a review or spec: the task door alone is refused by the server, and
// the CLI prints that refusal as sent: no fallback to another route.
func TestSetPropertiesOnATaskThatIsAlsoGovernedPrintsTheRefusalAsSent2826(t *testing.T) {
	captured, _, err := setProps2826(t, map[string]string{
		"NodeKind":                taskNode2826(),
		"MergeTaskNodeProperties": governedRefusal2826,
	}, "--patch", `{"a":1}`)
	if err == nil || exitCodeFor(err) != exitcode.Forbidden || !strings.Contains(err.Error(), "this node is governed") {
		t.Fatalf("the server's refusal is printed as sent (exit 8): %v", err)
	}
	for _, op := range []string{"UpdateNodeProperties", "UpdateNode", "UpdateReviewNode", "UpdateSpecNode"} {
		if _, sent := captured[op]; sent {
			t.Errorf("no fallback to another route: %s was sent", op)
		}
	}
}

// A non-writer's refusal is the server's, with its exit code; a null answer is an unknown
// outcome, not a success.
func TestSetPropertiesOnATaskNodeRefusalsAndNullAnswer2826(t *testing.T) {
	_, out, err := setProps2826(t, map[string]string{"NodeKind": taskNode2826(), "MergeTaskNodeProperties": gqlErrorJSON("FORBIDDEN")}, "--patch", `{"a":1}`)
	if exitCodeFor(err) != exitcode.Forbidden || out != "" {
		t.Errorf("a non-writer is refused as the server says, exit 8: %v %q", err, out)
	}
	_, out, err = setProps2826(t, map[string]string{"NodeKind": taskNode2826(), "MergeTaskNodeProperties": `{"data":{"updateTaskNode":null}}`}, "--patch", `{"a":1}`)
	if exitCodeFor(err) != exitcode.Unavailable || out != "" || err == nil || !strings.Contains(err.Error(), "not confirmed") {
		t.Errorf("a null answer is an unknown outcome (exit 7): %v %q", err, out)
	}
}

// Text output is the generic route's: the check line and the merged properties.
func TestSetPropertiesOnATaskNodeTextOutputIsUnchanged2826(t *testing.T) {
	_, out, err := setProps2826(t, map[string]string{"NodeKind": taskNode2826(), "MergeTaskNodeProperties": mergedTask2826}, "--patch", `{"a":1}`)
	if err != nil || !strings.HasPrefix(out, "✓ updated properties of tasks:build\n{") {
		t.Errorf("text output: %v %q", err, out)
	}
}

// A patch that is not an object is a usage error before ANY request, the kind read included.
func TestSetPropertiesNonObjectPatchSendsNothing2826(t *testing.T) {
	captured, _, err := setProps2826(t, map[string]string{"NodeKind": taskNode2826(), "MergeTaskNodeProperties": mergedTask2826}, "--patch", `[1]`)
	if exitCodeFor(err) != exitcode.Usage || len(captured) != 0 {
		t.Errorf("exit 2 and no request at all: %v sent=%v", err, keys1340(captured))
	}
}

// A kind read that fails does not decide anything: the generic route runs and the server's own
// answer is final.
func TestSetPropertiesFailedKindReadKeepsTheGenericRoute2826(t *testing.T) {
	captured, _, err := setProps2826(t, map[string]string{
		"NodeKind":             gqlErrorJSON("INTERNAL_SERVER_ERROR"),
		"UpdateNodeProperties": governedRefusal2826,
	}, "--patch", `{"a":1}`)
	if err == nil {
		t.Fatal("the generic route's refusal is the answer")
	}
	if _, sent := captured["MergeTaskNodeProperties"]; sent {
		t.Error("an unread kind must not be guessed to be a task")
	}
	if _, sent := captured["UpdateNodeProperties"]; !sent {
		t.Error("the generic merge is still tried")
	}
}

// The documents (a fake keyed by operation name cannot see these): the kind read selects the
// runnable flag and no properties, the generic merge document never gained mergeExports (an older
// server must still validate it), and the task door's ordinary update document is untouched.
func TestSetPropertiesDocuments2826(t *testing.T) {
	if strings.Contains(gen.NodeKind_Operation, "properties") || !strings.Contains(gen.NodeKind_Operation, "isRunnable") {
		t.Errorf("NodeKind must select the runnable flag and no properties:\n%s", gen.NodeKind_Operation)
	}
	for name, doc := range map[string]string{"UpdateNodeProperties": gen.UpdateNodeProperties_Operation, "UpdateNodePropertiesAuthored": gen.UpdateNodePropertiesAuthored_Operation, "UpdateTaskNode": gen.UpdateTaskNode_Operation} {
		if strings.Contains(doc, "mergeExports") {
			t.Errorf("%s must not carry mergeExports (an older server would reject the document):\n%s", name, doc)
		}
	}
	if !strings.Contains(gen.MergeTaskNodeProperties_Operation, "mergeExports: $mergeExports") {
		t.Errorf("the task merge document must pass mergeExports:\n%s", gen.MergeTaskNodeProperties_Operation)
	}
}

// Jack's P2 (a claim on a task node): the task branch carries the SAME attestation the generic
// route does, both halves (task and revision), in input.authoredWith; without the flags no
// authoredWith key is sent; one flag alone is exit 2 before any request (the pair rule).
func TestSetPropertiesOnATaskNodeCarriesTheAuthoredClaim2826(t *testing.T) {
	responses := map[string]string{"NodeKind": taskNode2826(), "MergeTaskNodeProperties": mergedTask2826}
	captured, _, err := setProps2826(t, responses, "--patch", `{"a":1}`, "--authored-with", "task1", "--authored-revision", "4")
	if err != nil {
		t.Fatal(err)
	}
	in, _ := vars1341(t, captured, "MergeTaskNodeProperties")["input"].(map[string]any)
	if asJSON(in["authoredWith"]) != `{"revision":4,"task":"task1"}` {
		t.Errorf("both halves of the claim ride the task door's input: %v", in["authoredWith"])
	}
	captured, _, err = setProps2826(t, responses, "--patch", `{"a":1}`)
	if err != nil {
		t.Fatal(err)
	}
	in, _ = vars1341(t, captured, "MergeTaskNodeProperties")["input"].(map[string]any)
	if _, has := in["authoredWith"]; has {
		t.Errorf("no claim flags, no authoredWith key: %v", in)
	}
	for _, flags := range [][]string{{"--authored-with", "task1"}, {"--authored-revision", "4"}} {
		captured, _, err = setProps2826(t, responses, append([]string{"--patch", `{"a":1}`}, flags...)...)
		if exitCodeFor(err) != exitcode.Usage || len(captured) != 0 {
			t.Errorf("%v: one half of the pair is exit 2 before any request: %v sent=%v", flags, err, keys1340(captured))
		}
	}
}
