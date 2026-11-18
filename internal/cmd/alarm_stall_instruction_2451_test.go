package cmd

import (
	"encoding/json"
	"github.com/hadron-memory/hadron-cli/internal/cmd/team"
	"github.com/hadron-memory/hadron-cli/internal/exitcode"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func alarmStallCases2451() []taskInstructionCase2422 {
	return []taskInstructionCase2422{
		{"ack", []string{"team", "alarm", "ack", "alarm1"}, "AlarmAcknowledge", "AlarmAcknowledgeInstructed", `{"data":{"acknowledgeAlarm":` + ackedAlarmEventFieldsJSON + `}}`},
		{"set", []string{"team", "stall-schedule", "set", "--assignment-minutes", "30"}, "TeamStallScheduleSet", "TeamStallScheduleSetInstructed", `{"data":{"setAgentTaskStallSchedule":` + stallScheduleJSON + `}}`},
	}
}
func TestAlarmStallInstruction2451(t *testing.T) {
	for _, c := range alarmStallCases2451() {
		t.Run(c.name, func(t *testing.T) {
			r := stallResponses()
			r[c.plainOp] = c.response
			r[c.instrOp] = c.response
			calls, _, err := runTeamTask(t, r, c.args...)
			if err != nil {
				t.Fatal(err)
			}
			plain := oneCall2422(t, calls, c.plainOp)
			wantPlainSession := ""
			if c.name == "ack" {
				wantPlainSession = "s-new"
			}
			if plain.Session != wantPlainSession || strings.Contains(plain.Query, "instruction") {
				t.Fatalf("plain changed: %+v", plain)
			}
			args := append(append([]string{}, c.args...), "--instruction", "team-chat-message:"+taskInstructionRef2422)
			calls, _, err = runTeamTask(t, r, args...)
			if err != nil {
				t.Fatal(err)
			}
			got := oneCall2422(t, calls, c.instrOp)
			if got.Session != "s-new" || !strings.Contains(got.Query, "instruction: $instruction") {
				t.Fatalf("instruction/session missing: %+v", got)
			}
			want := map[string]any{"kind": "TEAM_CHAT_MESSAGE", "ref": taskInstructionRef2422}
			if !reflect.DeepEqual(got.Vars["instruction"], want) {
				t.Fatalf("claim=%v", got.Vars)
			}
			delete(got.Vars, "instruction")
			if !reflect.DeepEqual(got.Vars, plain.Vars) {
				t.Fatalf("other variables changed: %v vs %v", got.Vars, plain.Vars)
			}
			for _, call := range *calls {
				if call.Op != c.instrOp {
					if call.Session != "" || call.Vars["instruction"] != nil {
						t.Fatalf("lookup attributed: %+v", call)
					}
				}
			}
		})
	}
}
func TestAlarmStallInstructionBadFlag2451(t *testing.T) {
	for _, c := range alarmStallCases2451() {
		for _, raw := range []string{"", "wrong:ref", "task: "} {
			args := append(append([]string{}, c.args...), "--instruction", raw)
			calls, _, err := runTeamTask(t, stallResponses(), args...)
			if exitCodeFor(err) != exitcode.Usage || len(*calls) != 0 {
				t.Fatalf("%s %q: %v calls=%v", c.name, raw, err, *calls)
			}
		}
	}
}
func TestAlarmStallInstructionRefusal2451(t *testing.T) {
	for _, c := range alarmStallCases2451() {
		t.Run(c.name, func(t *testing.T) {
			r := stallResponses()
			r[c.instrOp] = `{"errors":[{"message":"refused once","extensions":{"code":"FORBIDDEN"}}]}`
			calls, out, err := runTeamTask(t, r, append(append([]string{}, c.args...), "--instruction", "live-chat:opaque")...)
			if err == nil || !strings.Contains(err.Error(), "refused once") || out != "" {
				t.Fatalf("err=%v out=%q", err, out)
			}
			oneCall2422(t, calls, c.instrOp)
			for _, call := range *calls {
				if call.Op == c.plainOp {
					t.Fatal("retried plain")
				}
			}
		})
	}
}

func TestAlarmStallInstructionPersonFallback2451(t *testing.T) {
	for _, c := range alarmStallCases2451() {
		for _, mode := range []string{"unbound", "other-server", "incomplete"} {
			t.Run(c.name+"/"+mode, func(t *testing.T) {
				if mode == "unbound" {
					teamGitDir(t)
				} else {
					writeTeamBinding(t)
				}
				r := stallResponses()
				r[c.instrOp] = c.response
				srv, calls := attnServer(t, r)
				if mode == "other-server" {
					setTeamBindingServer(t, "https://other.invalid")
				}
				if mode == "incomplete" {
					p := filepath.Join(os.Getenv(team.GitDirEnv), "hadron-team-session.json")
					if err := os.WriteFile(p, []byte(`{"sessionId":"incomplete"}`), 0600); err != nil {
						t.Fatal(err)
					}
				}
				f, _ := testFactory(t)
				root := NewRootCmd(f)
				args := append([]string{"--server", srv.URL, "--app", "capp100000000000000000000"}, c.args...)
				root.SetArgs(append(args, "--instruction", "live-chat:opaque:ref"))
				if err := root.Execute(); err != nil {
					t.Fatal(err)
				}
				got := oneCall2422(t, calls, c.instrOp)
				if got.Session != "" || got.Vars["instruction"] == nil {
					t.Fatalf("person fallback: %+v", got)
				}
			})
		}
	}
}
func TestAlarmInstructionWarnings2451(t *testing.T) {
	c := alarmStallCases2451()[0]
	r := stallResponses()
	warning := `{"path":"acknowledgeAlarm","code":"alarm.ack.outside-chain","text":"Outside chain.","register":null}`
	r[c.instrOp] = strings.TrimSuffix(c.response, "}") + `,"extensions":{"warnings":[` + warning + `]}}`
	args := append(append([]string{}, c.args...), "--instruction", "live-chat:opaque")
	out, stderr, err := runTeamTaskStreams(t, r, args...)
	if err != nil || !strings.Contains(out, "acknowledged alarm") || !strings.Contains(stderr, "warning: [alarm.ack.outside-chain] Outside chain.") {
		t.Fatalf("out=%q stderr=%q err=%v", out, stderr, err)
	}
	for _, withWarning := range []bool{true, false} {
		if !withWarning {
			r[c.instrOp] = c.response
		}
		calls, out, err := runTeamTask(t, r, append(args, "--json")...)
		if err != nil {
			t.Fatal(err)
		}
		oneCall2422(t, calls, c.instrOp)
		var dto struct {
			Warnings []map[string]any `json:"warnings"`
		}
		if err := json.Unmarshal([]byte(out), &dto); err != nil {
			t.Fatal(err)
		}
		want := 0
		if withWarning {
			want = 1
		}
		if dto.Warnings == nil || len(dto.Warnings) != want {
			t.Fatalf("warnings=%s", out)
		}
	}
}
func TestAlarmStallUsagePrecedence2451(t *testing.T) {
	for _, args := range [][]string{{"team", "alarm", "ack", " "}, {"team", "stall-schedule", "set"}, {"team", "stall-schedule", "get"}} {
		calls, _, err := runTeamTask(t, stallResponses(), append(args, "--instruction", "bad")...)
		if exitCodeFor(err) != exitcode.Usage || len(*calls) != 0 {
			t.Fatalf("args=%v err=%v calls=%v", args, err, *calls)
		}
	}
}

func TestStallInstructionChangedBinding2451(t *testing.T) {
	writeTeamBinding(t)
	r := stallResponses()
	r["TeamStallScheduleSetInstructed"] = alarmStallCases2451()[1].response
	r["TeamAppIdentity"] = `{"data":{"app":{"id":"capp100000000000000000000"}}}`
	changed := false
	srv, calls := attnServerHook(t, r, func(op string) {
		if op == "TeamAppIdentity" {
			changed = true
			path := filepath.Join(os.Getenv(team.GitDirEnv), "hadron-team-session.json")
			b, err := os.ReadFile(path)
			if err != nil {
				t.Error(err)
				return
			}
			if err := os.WriteFile(path, []byte(strings.Replace(string(b), `"s-new"`, `"s-other"`, 1)), 0600); err != nil {
				t.Error(err)
			}
		}
	})
	setTeamBindingServer(t, srv.URL)
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"--server", srv.URL, "--app", "acme.com:eng-team", "team", "stall-schedule", "set", "--assignment-minutes", "30", "--instruction", "live-chat:opaque"})
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	got := oneCall2422(t, calls, "TeamStallScheduleSetInstructed")
	if !changed || got.Session != "" || got.Vars["instruction"] == nil {
		t.Fatalf("changed=%v call=%+v", changed, got)
	}
	if !strings.Contains(f.IOStreams.ErrOut.(*strings.Builder).String(), "changed") {
		t.Fatal("no binding-change diagnostic")
	}
}

// The instructed family keeps machine-readable refusal codes; plain calls retain
// their established envelope. Render the same final envelope as the executable.
func TestAlarmStallInstructionRefusalCode2451(t *testing.T) {
	for _, c := range alarmStallCases2451() {
		for _, instructed := range []bool{false, true} {
			t.Run(c.name+map[bool]string{false: "/plain", true: "/instructed"}[instructed], func(t *testing.T) {
				writeTeamBinding(t)
				r := stallResponses()
				op := c.plainOp
				if instructed {
					op = c.instrOp
				}
				r[op] = `{"errors":[{"message":"refused once","extensions":{"code":"FORBIDDEN"}}]}`
				srv, calls := attnServer(t, r)
				setTeamBindingServer(t, srv.URL)
				f, out := testFactory(t)
				root := NewRootCmd(f)
				args := append([]string{"--server", srv.URL, "--json"}, c.args...)
				if instructed {
					args = append(args, "--instruction", "live-chat:opaque")
				}
				root.SetArgs(args)
				err := root.Execute()
				if err == nil || exitCodeFor(err) != exitcode.Forbidden || out.Len() != 0 {
					t.Fatalf("err=%v out=%q", err, out.String())
				}
				oneCall2422(t, calls, op)
				for _, call := range *calls {
					if call.Op == c.plainOp && instructed {
						t.Fatal("retried plain")
					}
				}
				renderError(f, err)
				var envelope struct {
					Error struct {
						ServerCode string `json:"serverCode"`
						Message    string `json:"message"`
					} `json:"error"`
				}
				rendered := out.String() + f.IOStreams.ErrOut.(*strings.Builder).String()
				if err := json.Unmarshal([]byte(rendered), &envelope); err != nil {
					t.Fatal(err)
				}
				want := ""
				if instructed {
					want = "FORBIDDEN"
				}
				if envelope.Error.ServerCode != want || !strings.Contains(envelope.Error.Message, "refused once") {
					t.Fatalf("lost code/message: %s", rendered)
				}
			})
		}
	}
}
