package cmd

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/exitcode"
)

// hadron-cli#3038: the five polish items on `agent tasks create` (follow-ups to cli#2990 / PR #3033). Each
// pin states the behaviour the item asks for; every server refusal the command already prints is unchanged
// (the cli#2990 pins keep covering those).

const roleTaskZeroEdges3038 = `{"data":{"createTaskNode":{"id":"n1","memoryId":"` + roleTaskMemory2990 + `","loc":"` + roleTaskLoc2990 + `","urn":"` + roleTaskURN2990 + `","name":"Restart the poller","nodeType":"task","isRunnable":true,"role":null,"updatedAt":"2026-11-22T00:00:00Z","outgoingEdges":[]}}}`

func roleTaskWithLabel3038(label string) string {
	enc, _ := json.Marshal(label)
	return `{"data":{"createTaskNode":{"id":"n1","memoryId":"` + roleTaskMemory2990 + `","loc":"` + roleTaskLoc2990 + `","urn":"` + roleTaskURN2990 + `","name":"Restart the poller","nodeType":"task","isRunnable":true,"role":null,"updatedAt":"2026-11-22T00:00:00Z","outgoingEdges":[{"id":"e1","name":` + string(enc) + `,"target":{"id":"idx1","loc":"tasks","urn":"` + roleTaskIndexURN2990 + `"}}]}}}`
}

// Item 1: the server writes the index edge WITH the node, so an answer that lists none is surprising. The
// command says so on stderr and still succeeds: no failure, no CLI-side check or retry, and --json stdout is
// unchanged (an empty edges array, nothing extra).
func TestAgentTasksCreateZeroEdgeSuccessSaysSoOnStderr3038(t *testing.T) {
	for _, jsonMode := range []bool{false, true} {
		resp := roleTaskResponses2990()
		resp["CreateRoleTask"] = roleTaskZeroEdges3038
		extra := []string{}
		if jsonMode {
			extra = append(extra, "--json")
		}
		run := runRoleTask2990(t, resp, roleTaskArgs2990(t, extra...)...)
		if run.err != nil {
			t.Fatalf("json=%v: a zero-edge success must not fail: %v", jsonMode, run.err)
		}
		for _, want := range []string{"index edge", "hadron agent tasks list " + roleTaskAgentRef2990} {
			if !strings.Contains(run.errOut, want) {
				t.Errorf("json=%v: the stderr note must contain %q, got %q", jsonMode, want, run.errOut)
			}
		}
		if strings.Contains(run.out, "index edge") {
			t.Errorf("json=%v: the note belongs on stderr, not stdout: %q", jsonMode, run.out)
		}
		writes := 0
		for _, op := range run.ops {
			if op == "CreateRoleTask" {
				writes++
			}
		}
		if writes != 1 || len(run.ops) != 2 {
			t.Errorf("json=%v: no CLI-side check or retry, want GetAgent + one write, got %v", jsonMode, run.ops)
		}
		if jsonMode {
			var got struct {
				Edges []json.RawMessage `json:"edges"`
			}
			if err := json.Unmarshal([]byte(run.out), &got); err != nil || got.Edges == nil || len(got.Edges) != 0 {
				t.Errorf("--json stdout stays the DTO with edges [] (err %v): %q", err, run.out)
			}
		} else if !strings.Contains(run.out, "✓ created role task") {
			t.Errorf("the success line still prints: %q", run.out)
		}
	}
	// With the edge present there is nothing to say.
	ok := runRoleTask2990(t, roleTaskResponses2990(), roleTaskArgs2990(t)...)
	if ok.err != nil || ok.errOut != "" {
		t.Errorf("an indexed success is silent on stderr: err %v stderr %q", ok.err, ok.errOut)
	}
}

// Item 2: a null mutation result is the lost-answer class the other write commands use: exit 1, a message that
// says the outcome is unknown and the task may have been recorded, and the command that shows it, before any
// retry. It is sent once and never retried.
func TestAgentTasksCreateNullResultIsTheLostAnswerClass3038(t *testing.T) {
	resp := roleTaskResponses2990()
	resp["CreateRoleTask"] = `{"data":{"createTaskNode":null}}`
	run := runRoleTask2990(t, resp, roleTaskArgs2990(t)...)
	if got := exitCodeFor(run.err); got != exitcode.Error {
		t.Fatalf("a null result exits %d, want 1 (the lost-answer class): %v", got, run.err)
	}
	for _, want := range []string{
		"the server answered createTaskNode with no node and no error",
		"so the outcome is unknown",
		"it may have been recorded",
		"`hadron agent tasks list " + roleTaskAgentRef2990 + "` before retrying",
	} {
		if run.err == nil || !strings.Contains(run.err.Error(), want) {
			t.Errorf("the lost-answer message must contain %q: %v", want, run.err)
		}
	}
	writes := 0
	for _, op := range run.ops {
		if op == "CreateRoleTask" {
			writes++
		}
	}
	if writes != 1 {
		t.Errorf("sent once, never retried: %v", run.ops)
	}
	if strings.Contains(run.out, "created") {
		t.Errorf("no success line for an unknown outcome: %q", run.out)
	}
}

// Item 3: ONE quoting style for the index label. The label is neutralised by output.SafeCell and printed as is,
// like every other field on the page, not wrapped in a second escaping layer.
func TestAgentTasksCreateQuotesTheLabelOnce3038(t *testing.T) {
	label := `say "hi" \x`
	resp := roleTaskResponses2990()
	resp["CreateRoleTask"] = roleTaskWithLabel3038(label)
	run := runRoleTask2990(t, resp, roleTaskArgs2990(t)...)
	if run.err != nil {
		t.Fatal(run.err)
	}
	want := "  indexed: " + label + " -> tasks (" + roleTaskIndexURN2990 + ")"
	if !strings.Contains(run.out, want) {
		t.Errorf("the label prints once, without a second escaping layer: want %q in %q", want, run.out)
	}
	if strings.Contains(run.out, `\"`) || strings.Contains(run.out, `\\`) {
		t.Errorf("no double escaping: %q", run.out)
	}
}

// Item 4: --template prints the template and creates nothing, so any other flag is refused with a usage error
// naming it, before any request; so is a positional argument (already refused).
func TestAgentTasksCreateTemplateRefusesOtherFlags3038(t *testing.T) {
	for _, tc := range []struct {
		flag string
		args []string
	}{
		{"--name", []string{"--name", "x"}},
		{"--abstract", []string{"--abstract", "x"}},
		{"--body-file", []string{"--body-file", "body.md"}},
		{"--trigger", []string{"--trigger", "x"}},
		{"--export-skill", []string{"--export-skill", "x"}},
		{"--skill-description", []string{"--skill-description", "x"}},
	} {
		t.Run(tc.flag, func(t *testing.T) {
			run := runRoleTask2990(t, map[string]string{}, append([]string{"--template"}, tc.args...)...)
			if got := exitCodeFor(run.err); got != exitcode.Usage {
				t.Fatalf("--template %s exits %d, want usage 2: %v", tc.flag, got, run.err)
			}
			if run.err == nil || !strings.Contains(run.err.Error(), tc.flag) || !strings.Contains(run.err.Error(), "--template") {
				t.Errorf("the refusal must name both --template and %s: %v", tc.flag, run.err)
			}
			if len(run.ops) != 0 {
				t.Errorf("refused before any request, got %v", run.ops)
			}
		})
	}
	// Several at once are all named.
	run := runRoleTask2990(t, map[string]string{}, "--template", "--name", "x", "--trigger", "y")
	if run.err == nil || !strings.Contains(run.err.Error(), "--name") || !strings.Contains(run.err.Error(), "--trigger") {
		t.Errorf("every other flag given is named: %v", run.err)
	}
}
