package cmd

import (
	"encoding/json"
	"github.com/hadron-memory/hadron-cli/internal/exitcode"
	"strings"
	"testing"
)

const prHead1664 = "0123456789abcdef0123456789abcdef01234567"

func runPR1664(t *testing.T, op, reply string, args ...string) (string, int, json.RawMessage, error) {
	t.Helper()
	srv, count, vars := countingGraphQL(t, map[string]string{op: reply})
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs(append([]string{"team", "pr", "--server", srv.URL}, args...))
	err := root.Execute()
	if err != nil {
		renderError(f, err)
	}
	return out.String(), count(op), vars(op), err
}
func TestPR1664SixContracts(t *testing.T) {
	tests := []struct {
		cmd, op, field string
		args           []string
	}{
		{"schedule", "PRSchedule", "setReviewSchedule", []string{"--slot", "qa:portal", "--slot", "blind"}},
		{"freeze", "PRFreeze", "freezePullRequestHead", []string{prHead1664}},
		{"seat", "PRSeat", "assignReviewSeat", []string{"--slot", "qa:portal", "--head", prHead1664}},
		{"verdict", "PRVerdict", "recordReviewVerdict", []string{"--head", prHead1664, "--verdict", "APPROVE"}},
		{"ready", "PRReady", "recordReadyCall", []string{"--head", prHead1664}},
		{"readiness", "PRReadiness", "pullRequestReadiness", []string{"--head", prHead1664}},
	}
	for _, tt := range tests {
		t.Run(tt.cmd, func(t *testing.T) {
			reply := `{"data":{"` + tt.field + `":{"task":{"id":"t1","handle":"AT0.7","status":"STARTED"},"action":{"code":"pr.ready","state":"WITHHELD","taskLoc":null,"taskUrn":null}}}}`
			if tt.cmd == "readiness" {
				reply = `{"data":{"pullRequestReadiness":{"ready":false,"head":"` + prHead1664 + `","slots":[{"slot":"local","currentTaskId":"s1","state":"FINISHED","verdict":"APPROVE","head":"` + prHead1664 + `","counts":true},{"slot":"blind","currentTaskId":"s2","state":"FINISHED","verdict":"PASS","head":"` + prHead1664 + `","counts":true},{"slot":"qa:portal","currentTaskId":null,"state":"MISSING","verdict":null,"head":null,"counts":false}]}}}`
			}
			args := append([]string{tt.cmd, "hrn:agenttask:hadronmemory.com:team:AT0.7", "--json"}, tt.args...)
			out, n, raw, err := runPR1664(t, tt.op, reply, args...)
			if err != nil || n != 1 {
				t.Fatalf("calls=%d %v %s", n, err, out)
			}
			var vars map[string]any
			if json.Unmarshal(raw, &vars) != nil || vars["taskRef"] != "hrn:agenttask:hadronmemory.com:team:AT0.7" {
				t.Fatal(string(raw))
			}
			for _, key := range []string{"supersedes", "node", "receipt"} {
				if _, ok := vars[key]; ok {
					t.Fatalf("optional %s sent: %s", key, raw)
				}
			}
			var dto map[string]any
			if json.Unmarshal([]byte(out), &dto) != nil {
				t.Fatal(out)
			}
			if tt.cmd == "readiness" {
				if dto["ready"] != false || len(dto["slots"].([]any)) != 3 {
					t.Fatal(out)
				}
			} else {
				if len(dto) != 2 || dto["task"] == nil || dto["action"].(map[string]any)["state"] != "WITHHELD" {
					t.Fatal(out)
				}
			}
		})
	}
}
func TestPR1664LocalValidation(t *testing.T) {
	for _, args := range [][]string{{"freeze", "AT0.7", "abc"}, {"readiness", "AT0.7"}, {"readiness", "AT0.7", "--head", "bad"}, {"verdict", "AT0.7", "--head", prHead1664, "--verdict", "BAD"}, {"schedule", "AT0.7", "--slot", "qa::portal"}, {"schedule", "AT0.7", "--slot", "qa:"}, {"seat", "AT0.7", "--slot", "qa:", "--head", prHead1664}} {
		_, n, _, err := runPR1664(t, "PRSchedule", `{}`, args...)
		if err == nil || exitCodeFor(err) != exitcode.Usage || n != 0 {
			t.Fatalf("%v calls=%d %v", args, n, err)
		}
	}
}
func TestPR1664Refusals(t *testing.T) {
	for _, code := range []string{"REVIEW_SEAT_NOT_SUPERSEDABLE", "REVIEW_SEAT_ALREADY_ASSIGNED", "REVIEW_SEAT_SUPERSEDES_REQUIRED", "REVIEW_HEAD_MISMATCH", "REVIEW_SEAT_UNASSIGNED", "PR_NOT_READY", "PR_HEAD_NOT_FROZEN", "REVIEW_SCHEDULE_MISSING"} {
		reply := `{"errors":[{"message":"provider refusal","extensions":{"code":"` + code + `","slots":[{"slot":"qa:portal","currentTaskId":"s1","state":"STARTED","verdict":null,"head":"` + prHead1664 + `","counts":false}]}}]}`
		out, n, _, err := runPR1664(t, "PRReady", reply, "ready", "AT0.7", "--head", prHead1664, "--json")
		if err == nil || n != 1 || exitCodeFor(err) == 0 {
			t.Fatalf("%s %d %v", code, n, err)
		}
		var dto struct {
			Error struct {
				ServerCode, Message string
				Slots               []any
			}
		}
		if json.Unmarshal([]byte(out), &dto) != nil || dto.Error.ServerCode != code || dto.Error.Message != "provider refusal" {
			t.Fatal(out)
		}
		if code == "PR_NOT_READY" && (len(dto.Error.Slots) != 1 || !strings.Contains(out, "qa:portal")) {
			t.Fatal(out)
		}
	}
}
