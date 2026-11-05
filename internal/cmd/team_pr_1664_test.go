package cmd

import (
	"encoding/json"
	"fmt"
	"github.com/hadron-memory/hadron-cli/internal/exitcode"
	"reflect"
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
	text := out.String()
	if err != nil && text == "" {
		text = f.IOStreams.ErrOut.(*strings.Builder).String()
	}
	return text, count(op), vars(op), err
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
			if tt.cmd != "schedule" && vars["head"] != prHead1664 {
				t.Fatalf("head not passed: %s", raw)
			}
			if tt.cmd == "schedule" {
				v, ok := vars["slots"].([]any)
				if !ok || len(v) != 2 || v[0] != "qa:portal" || v[1] != "blind" {
					t.Fatal(string(raw))
				}
			}
			if tt.cmd == "seat" && vars["slot"] != "qa:portal" {
				t.Fatal(string(raw))
			}
			if tt.cmd == "verdict" && vars["verdict"] != "APPROVE" {
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
				want := map[string]any{"ready": false, "head": prHead1664, "slots": []any{
					map[string]any{"slot": "local", "currentTaskId": "s1", "state": "FINISHED", "verdict": "APPROVE", "head": prHead1664, "counts": true},
					map[string]any{"slot": "blind", "currentTaskId": "s2", "state": "FINISHED", "verdict": "PASS", "head": prHead1664, "counts": true},
					map[string]any{"slot": "qa:portal", "currentTaskId": nil, "state": "MISSING", "verdict": nil, "head": nil, "counts": false},
				}}
				if !reflect.DeepEqual(dto, want) {
					t.Fatal(out)
				}
			} else {
				action := dto["action"].(map[string]any)
				if len(action) != 4 {
					t.Fatal(out)
				}
				for _, key := range []string{"taskLoc", "taskUrn"} {
					value, present := action[key]
					if !present || value != nil {
						t.Fatal(out)
					}
				}
				task := dto["task"].(map[string]any)
				if len(task) != 3 || task["id"] != "t1" || task["handle"] != "AT0.7" || task["status"] != "STARTED" {
					t.Fatal(out)
				}
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

func TestPR1664OptionalAndActions(t *testing.T) {
	for _, state := range []string{"MAPPED", "UNMAPPED", "UNRESOLVED", "WITHHELD"} {
		reply := `{"data":{"recordReviewVerdict":{"task":{"id":"t1","handle":"AT0.7","status":"STARTED"},"action":{"code":"review.verdict","state":"` + state + `","taskLoc":"tasks:\u001breview","taskUrn":"hrn:node:acme.com:role:tasks:review"}}}}`
		for _, asJSON := range []bool{false, true} {
			args := []string{"verdict", "AT0.7", "--head", prHead1664, "--verdict", "PASS", "--node", "hrn:node:acme.com:reviews:x", "--receipt", "https://example.invalid/receipt"}
			if asJSON {
				args = append(args, "--json")
			}
			out, n, v, err := runPR1664(t, "PRVerdict", reply, args...)
			if err != nil || n != 1 {
				t.Fatalf("%v %d", err, n)
			}
			var vars map[string]any
			if json.Unmarshal(v, &vars) != nil || vars["node"] != "hrn:node:acme.com:reviews:x" || vars["receipt"] != "https://example.invalid/receipt" {
				t.Fatal(string(v))
			}
			if asJSON {
				var dto struct {
					Action struct {
						Code, State      string
						TaskLoc, TaskURN *string
					}
				}
				if json.Unmarshal([]byte(out), &dto) != nil || dto.Action.State != state || dto.Action.TaskLoc == nil || *dto.Action.TaskLoc != "tasks:\x1breview" || dto.Action.TaskURN == nil {
					t.Fatal(out)
				}
			} else if !strings.Contains(out, state) || !strings.Contains(out, "tasks: review") || strings.Contains(out, "\x1b") {
				t.Fatal(out)
			}
		}
	}
	reply := `{"data":{"assignReviewSeat":{"task":{"id":"t1","handle":"AT0.7","status":"PENDING"},"action":{"code":"review.seat.assigned","state":"WITHHELD","taskLoc":null,"taskUrn":null}}}}`
	_, n, v, err := runPR1664(t, "PRSeat", reply, "seat", "AT0.7", "--slot", "qa:portal", "--head", prHead1664, "--supersedes", "AT0.6")
	if err != nil || n != 1 || !strings.Contains(string(v), `"supersedes":"AT0.6"`) {
		t.Fatalf("%s %v", v, err)
	}
}
func TestPR1664ReadinessViews(t *testing.T) {
	for _, slots := range []string{`[]`, `[{"slot":"qa:portal","currentTaskId":"s1","state":"STARTED","verdict":null,"head":"` + prHead1664 + `","counts":false}]`} {
		reply := `{"data":{"pullRequestReadiness":{"ready":false,"head":"` + prHead1664 + `","slots":` + slots + `}}}`
		for _, asJSON := range []bool{false, true} {
			args := []string{"readiness", "AT0.7", "--head", prHead1664}
			if asJSON {
				args = append(args, "--json")
			}
			out, n, _, err := runPR1664(t, "PRReadiness", reply, args...)
			if err != nil || n != 1 {
				t.Fatal(err)
			}
			if asJSON {
				var dto struct{ Slots []any }
				if json.Unmarshal([]byte(out), &dto) != nil || dto.Slots == nil {
					t.Fatal(out)
				}
			} else if !strings.Contains(out, "NOT READY") || (slots != "[]" && (!strings.Contains(out, "qa:portal") || !strings.Contains(out, "STARTED") || !strings.Contains(out, "does not count"))) {
				t.Fatal(out)
			}
		}
	}
	_, n, _, err := runPR1664(t, "PRReadiness", `{"data":{"pullRequestReadiness":null}}`, "readiness", "AT0.7", "--head", prHead1664)
	if n != 1 || exitCodeFor(err) != exitcode.NotFound {
		t.Fatalf("%d %v", n, err)
	}
}
func TestPR1664HumanRefusal(t *testing.T) {
	reply := `{"errors":[{"message":"provider refusal","extensions":{"code":"PR_NOT_READY","slots":[{"slot":"qa:\u001bportal","state":"STARTED","counts":false}]}}]}`
	out, n, _, err := runPR1664(t, "PRReady", reply, "ready", "AT0.7", "--head", prHead1664)
	if n != 1 || exitCodeFor(err) != exitcode.Conflict || !strings.Contains(out, "PR_NOT_READY") || !strings.Contains(out, "qa: portal") || strings.Contains(out, "\x1b") {
		t.Fatalf("%q %d %v", out, n, err)
	}
}

func TestPR1664ReadyAndSkew(t *testing.T) {
	reply := `{"data":{"pullRequestReadiness":{"ready":true,"head":"` + prHead1664 + `","slots":[{"slot":"local","currentTaskId":"s1","state":"FINISHED","verdict":"APPROVE","head":"` + prHead1664 + `","counts":true}]}}}`
	out, n, _, err := runPR1664(t, "PRReadiness", reply, "readiness", "AT0.7", "--head", prHead1664)
	if err != nil || n != 1 || !strings.HasPrefix(out, "READY  head ") || !strings.Contains(out, "counts") {
		t.Fatalf("%q %v", out, err)
	}
	out, n, _, err = runPR1664(t, "PRReadiness", `{"errors":[{"message":"not the frozen head","extensions":{"code":"PR_HEAD_NOT_FROZEN"}}]}`, "readiness", "AT0.7", "--head", prHead1664, "--json")
	if n != 1 || exitCodeFor(err) != exitcode.Conflict || !strings.Contains(out, `"serverCode": "PR_HEAD_NOT_FROZEN"`) {
		t.Fatalf("%s %v", out, err)
	}
}

func TestPR1664MalformedReceipt(t *testing.T) {
	for _, result := range []string{`null`, `{}`, `{"task":{"id":"t1"},"action":null}`, `{"task":{"id":""},"action":{"code":"pr.ready","state":"WITHHELD"}}`} {
		out, n, _, err := runPR1664(t, "PRReady", `{"data":{"recordReadyCall":`+result+`}}`, "ready", "AT0.7", "--head", prHead1664, "--json")
		if n != 1 || exitCodeFor(err) != exitcode.Unavailable || !strings.Contains(out, "inspect task/events") {
			t.Fatalf("%s %d %v", out, n, err)
		}
	}
}

// Exercise the actual middleware, not just its operation classifier. Identity
// lookups are headerless; only admitted writes carry the synthetic binding.
func TestPR1664Binding(t *testing.T) {
	for _, verb := range []string{"schedule", "freeze", "seat", "verdict", "ready", "readiness"} {
		for _, bound := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/bound=%t", verb, bound), func(t *testing.T) {
				if bound {
					writeTeamBinding(t)
				} else {
					teamGitDir(t)
				}
				op := map[string]string{"schedule": "PRSchedule", "freeze": "PRFreeze", "seat": "PRSeat", "verdict": "PRVerdict", "ready": "PRReady", "readiness": "PRReadiness"}[verb]
				field := map[string]string{"schedule": "setReviewSchedule", "freeze": "freezePullRequestHead", "seat": "assignReviewSeat", "verdict": "recordReviewVerdict", "ready": "recordReadyCall", "readiness": "pullRequestReadiness"}[verb]
				result := `{"task":{"id":"t1","handle":"AT0.7","status":"STARTED"},"action":{"code":"pr.ready","state":"WITHHELD","taskLoc":null,"taskUrn":null}}`
				if verb == "readiness" {
					result = `{"ready":false,"head":"` + prHead1664 + `","slots":[]}`
				}
				srv, calls := attnServer(t, map[string]string{op: `{"data":{"` + field + `":` + result + `}}`, "TeamAppIdentity": teamAppIdentityJSON})
				if bound {
					setTeamBindingServer(t, srv.URL)
				}
				f, _ := testFactory(t)
				root := NewRootCmd(f)
				args := []string{"team", "pr", verb, "task1", "--json", "--server", srv.URL, "--app", "capp100000000000000000000"}
				switch verb {
				case "schedule":
					args = append(args, "--slot", "local")
				case "freeze":
					args = append(args, prHead1664)
				default:
					args = append(args, "--head", prHead1664)
				}
				if verb == "seat" {
					args = append(args, "--slot", "local")
				}
				if verb == "verdict" {
					args = append(args, "--verdict", "APPROVE")
				}
				root.SetArgs(args)
				if err := root.Execute(); err != nil {
					t.Fatal(err)
				}
				n := 0
				for _, call := range *calls {
					want := ""
					if call.Op == op {
						n++
						if bound && verb != "readiness" {
							want = "s-new"
						}
					}
					if call.Session != want {
						t.Errorf("%s session=%q want %q", call.Op, call.Session, want)
					}
				}
				if n != 1 {
					t.Fatalf("calls=%+v", *calls)
				}
			})
		}
	}
}

func TestPR1664TaskRefusalSafety(t *testing.T) {
	for _, door := range []string{"freeze", "readiness"} {
		for _, code := range []string{"TASK_NOT_ASSIGNEE", "TASK_NOT_OWNER", "TASK_ENDED", "TASK_INVALID_TRANSITION", "TASK_NOT_FOUND", "TASK_HANDLE_QUALIFY", "BAD_USER_INPUT", "PROPERTIES_SCHEMA_VIOLATION"} {
			op := "PRFreeze"
			args := []string{door, "task1", prHead1664}
			if door == "readiness" {
				op = "PRReadiness"
				args = []string{door, "task1", "--head", prHead1664}
			}
			reply := `{"errors":[{"message":"refused \u001b[31mred\u001b[0m text","extensions":{"code":"` + code + `"}}]}`
			text, n, _, err := runPR1664(t, op, reply, args...)
			if err == nil || n != 1 || strings.Contains(text, "\x1b") || !strings.Contains(text, code) {
				t.Fatalf("%s %s: calls=%d err=%v text=%q", door, code, n, err, text)
			}
			out, _, _, _ := runPR1664(t, op, reply, append(args, "--json")...)
			var dto map[string]any
			if json.Unmarshal([]byte(out), &dto) != nil || dto["error"].(map[string]any)["serverCode"] != code {
				t.Fatal(out)
			}
		}
	}
}
func TestPR1664FailingSlotContract(t *testing.T) {
	slots := []any{map[string]any{"slot": "qa:cli", "currentTaskId": nil, "state": "MISSING", "verdict": nil, "head": nil, "counts": false}, map[string]any{"slot": "local", "currentTaskId": "s1", "state": "STARTED", "verdict": "FAIL", "head": prHead1664, "counts": false}}
	raw, _ := json.Marshal(slots)
	reply := `{"errors":[{"message":"refused","extensions":{"code":"PR_NOT_READY","slots":` + string(raw) + `}}]}`
	out, _, _, _ := runPR1664(t, "PRReady", reply, "ready", "task1", "--head", prHead1664, "--json")
	var dto map[string]any
	if json.Unmarshal([]byte(out), &dto) != nil || !reflect.DeepEqual(dto["error"].(map[string]any)["slots"], slots) {
		t.Fatal(out)
	}
}
