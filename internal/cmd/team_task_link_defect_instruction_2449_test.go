package cmd

import (
	"github.com/hadron-memory/hadron-cli/internal/exitcode"
	"reflect"
	"strings"
	"testing"
)

func TestLinkDefectInstruction2449Wire(t *testing.T) {
	for _, instructed := range []bool{false, true} {
		t.Run(map[bool]string{false: "plain", true: "instructed"}[instructed], func(t *testing.T) {
			r := taskResponses()
			op := "TaskLinkDefect"
			if instructed {
				op += "Instructed"
			}
			r[op] = linkResponse2189(true, head2189)
			args := []string{"team", "task", "link-defect", "AT1.2", "--defect", defect2189, "--introduced-at-head", head2189}
			if instructed {
				args = append(args, "--instruction", "live-chat:opaque:ref")
			}
			calls, _, err := runTeamTask(t, r, args...)
			if err != nil {
				t.Fatal(err)
			}
			c := oneCall2422(t, calls, op)
			want := map[string]any{"prTaskRef": "AT1.2", "defectRef": defect2189, "introducedAtHead": head2189}
			if instructed {
				want["instruction"] = map[string]any{"kind": "LIVE_CHAT", "ref": "opaque:ref"}
			}
			if !reflect.DeepEqual(c.Vars, want) || c.Session != "s-new" {
				t.Fatalf("request vars=%v session=%q", c.Vars, c.Session)
			}
			if strings.Contains(c.Query, "instruction: $instruction") != instructed {
				t.Fatalf("wrong document: %s", c.Query)
			}
			mutations := 0
			for _, call := range *calls {
				if strings.HasPrefix(call.Op, "TaskLinkDefect") {
					mutations++
				} else if call.Session != "" {
					t.Fatalf("lookup carries session: %+v", call)
				}
			}
			if mutations != 1 {
				t.Fatalf("mutations=%d calls=%v", mutations, *calls)
			}
		})
	}
}

func TestLinkDefectInstruction2449BadClaim(t *testing.T) {
	for _, claim := range []string{"", "bad:ref", "task:   "} {
		t.Run(claim, func(t *testing.T) {
			calls, _, err := runTeamTask(t, taskResponses(), "team", "task", "link-defect", "AT1.2", "--defect", defect2189, "--instruction", claim)
			if exitCodeFor(err) != exitcode.Usage || len(*calls) != 0 {
				t.Fatalf("err=%v requests=%v", err, *calls)
			}
		})
	}
}

func TestLinkDefectInstruction2449Refusal(t *testing.T) {
	r := taskResponses()
	r["TaskLinkDefectInstructed"] = `{"errors":[{"message":"denied","extensions":{"code":"DEFECT_LINK_FORBIDDEN"}}]}`
	calls, _, err := runTeamTask(t, r, "team", "task", "link-defect", "AT1.2", "--defect", defect2189, "--instruction", "task:ref")
	if exitCodeFor(err) != exitcode.Forbidden {
		t.Fatalf("err=%v", err)
	}
	oneCall2422(t, calls, "TaskLinkDefectInstructed")
	for _, c := range *calls {
		if c.Op == "TaskLinkDefect" {
			t.Fatal("retried plain")
		}
	}
}
