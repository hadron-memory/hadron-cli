package cmd

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/exitcode"
)

// cli#58 slice 2: the four Agent argument rows (createAgent usesTasks /
// resetContextAfterTask, updateAgent listedOnMarketplace / registerDefaults).
// Every pin reads the RAW request variables, because the defect class is
// presence: an omitted key preserves (or takes the server default), an
// explicit false is a value, and an explicit null on registerDefaults CLEARS.

// agentFlagsJSON is the returned Agent with the new read-back fields set.
func agentFlagsJSON(listed, mentionOnly bool) string {
	b := func(v bool) string {
		if v {
			return "true"
		}
		return "false"
	}
	return strings.Replace(agentJSON, `"usesTasks":true,`,
		`"usesTasks":true,"resetContextAfterTask":false,"listedOnMarketplace":`+b(listed)+`,"registerDefaults":{"mentionOnly":`+b(mentionOnly)+`},`, 1)
}

// runAgentCapture58 runs `hadron agent <verb> ...` against a capturing fake and
// returns the raw variables the named operation carried.
func runAgentCapture58(t *testing.T, op, resp string, argv ...string) map[string]json.RawMessage {
	t.Helper()
	gql, captured := captureGraphQL(t, map[string]string{op: `{"data":{"` + strings.ToLower(op[:1]) + op[1:] + `":` + resp + `}}`})
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs(append(argv, "--server", gql.URL))
	if err := root.Execute(); err != nil {
		t.Fatalf("%v: %v", argv, err)
	}
	var vars map[string]json.RawMessage
	if err := json.Unmarshal(captured[op], &vars); err != nil {
		t.Fatal(err)
	}
	return vars
}

func TestAgentCreateTaskSettingFlagsSendExplicitValues(t *testing.T) {
	for _, tc := range []struct {
		flag, key, want string
	}{
		{"--uses-tasks", "usesTasks", "true"},
		{"--uses-tasks=true", "usesTasks", "true"},
		{"--uses-tasks=false", "usesTasks", "false"},
		{"--reset-context-after-task", "resetContextAfterTask", "true"},
		{"--reset-context-after-task=false", "resetContextAfterTask", "false"},
	} {
		t.Run(tc.flag, func(t *testing.T) {
			vars := runAgentCapture58(t, "CreateAgent", agentFlagsJSON(false, false), "agent", "create", "--name", "A", "--owner-me", tc.flag)
			if got, ok := vars[tc.key]; !ok || string(got) != tc.want {
				t.Fatalf("%s presence=%v value=%s want %s (vars %v)", tc.key, ok, got, tc.want, vars)
			}
		})
	}
}

// An ordinary create is byte-identical to today's payload: neither key appears.
func TestAgentCreateWithoutTaskSettingFlagsSendsNeitherKey(t *testing.T) {
	vars := runAgentCapture58(t, "CreateAgent", agentFlagsJSON(false, false), "agent", "create", "--name", "A", "--owner-me")
	for _, k := range []string{"usesTasks", "resetContextAfterTask"} {
		if _, ok := vars[k]; ok {
			t.Fatalf("unset flag sent %s: %v", k, vars)
		}
	}
	// A single flag sends only itself (the other keeps the server default).
	vars = runAgentCapture58(t, "CreateAgent", agentFlagsJSON(false, false), "agent", "create", "--name", "A", "--owner-me", "--uses-tasks=false")
	if _, ok := vars["resetContextAfterTask"]; ok {
		t.Fatalf("--uses-tasks also sent resetContextAfterTask: %v", vars)
	}
}

func TestAgentUpdateListedOnMarketplaceSendsExplicitValue(t *testing.T) {
	for flag, want := range map[string]string{
		"--listed-on-marketplace":       "true",
		"--listed-on-marketplace=true":  "true",
		"--listed-on-marketplace=false": "false",
	} {
		t.Run(flag, func(t *testing.T) {
			vars := runAgentCapture58(t, "UpdateAgent", agentFlagsJSON(true, false), "agent", "update", "agt1", flag)
			if got, ok := vars["listedOnMarketplace"]; !ok || string(got) != want {
				t.Fatalf("listedOnMarketplace presence=%v value=%s want %s", ok, got, want)
			}
			if len(vars) != 2 { // ref + the flag
				t.Fatalf("flag-only update sent unrelated keys: %v", vars)
			}
		})
	}
}

// registerDefaults has THREE wire states: object sets, null clears, omitted
// preserves. Omission must never turn into a clear.
func TestAgentUpdateRegisterDefaultsThreeStates(t *testing.T) {
	for flag, want := range map[string]string{
		"--register-mention-only":       `{"mentionOnly":true}`,
		"--register-mention-only=true":  `{"mentionOnly":true}`,
		"--register-mention-only=false": `{"mentionOnly":false}`,
		"--clear-register-defaults":     `null`,
	} {
		t.Run(flag, func(t *testing.T) {
			vars := runAgentCapture58(t, "UpdateAgent", agentFlagsJSON(false, true), "agent", "update", "agt1", flag)
			got, ok := vars["registerDefaults"]
			if !ok {
				t.Fatalf("registerDefaults omitted: %v", vars)
			}
			if string(got) != want {
				t.Fatalf("registerDefaults = %s, want %s", got, want)
			}
			if len(vars) != 2 {
				t.Fatalf("unrelated keys sent: %v", vars)
			}
		})
	}
	// Omitted: no key at all (never null).
	for _, argv := range [][]string{{"--name", "R"}, {"--listed-on-marketplace"}, {"--uses-tasks=false"}} {
		vars := runAgentCapture58(t, "UpdateAgent", agentFlagsJSON(false, true), append([]string{"agent", "update", "agt1"}, argv...)...)
		if _, ok := vars["registerDefaults"]; ok {
			t.Fatalf("%v sent registerDefaults: %v", argv, vars)
		}
		if _, ok := vars["listedOnMarketplace"]; ok && !strings.Contains(strings.Join(argv, " "), "listed") {
			t.Fatalf("%v sent listedOnMarketplace: %v", argv, vars)
		}
	}
}

// The conflicting pair and --clear-register-defaults=false are usage errors
// with ZERO requests (nothing is sent, so nothing can clear).
func TestAgentUpdateRegisterDefaultsLocalRefusals(t *testing.T) {
	for _, argv := range [][]string{
		{"--register-mention-only", "--clear-register-defaults"},
		{"--register-mention-only=false", "--clear-register-defaults"},
		{"--register-mention-only=true", "--clear-register-defaults=false"},
		{"--clear-register-defaults=false"},
	} {
		calls, out, err := runTeamTask(t, map[string]string{
			"UpdateAgent": `{"data":{"updateAgent":` + agentFlagsJSON(false, false) + `}}`,
		}, append([]string{"agent", "update", "agt1"}, argv...)...)
		if err == nil || exitCodeFor(err) != exitcode.Usage {
			t.Fatalf("%v: err=%v want usage", argv, err)
		}
		if len(*calls) != 0 || out != "" {
			t.Fatalf("%v: refusal sent %d request(s), out=%q", argv, len(*calls), out)
		}
	}
}

// Server refusals pass through on one send, with no retry or follow-up.
func TestAgentUpdateNewFlagsRefusalMapsOnce(t *testing.T) {
	for code, want := range map[string]int{"FORBIDDEN": exitcode.Forbidden, "BAD_USER_INPUT": exitcode.Usage} {
		calls, out, err := runTeamTask(t, map[string]string{
			"UpdateAgent": `{"errors":[{"message":"refused","extensions":{"code":"` + code + `"}}]}`,
		}, "agent", "update", "agt1", "--clear-register-defaults")
		if err == nil || exitCodeFor(err) != want {
			t.Fatalf("%s: err=%v", code, err)
		}
		if len(callsFor(calls, "UpdateAgent")) != 1 || len(*calls) != 1 || out != "" {
			t.Fatalf("%s: retried or printed: calls=%d out=%q", code, len(*calls), out)
		}
	}
}

// Read-back: the RETURNED agent's values show in --json and text, truthfully
// (not the flag the caller passed).
func TestAgentReadbackShowsListedAndRegisterDefaults(t *testing.T) {
	for _, tc := range []struct{ listed, mention bool }{{true, true}, {false, false}} {
		resp := map[string]string{"GetAgent": `{"data":{"agent":` + agentFlagsJSON(tc.listed, tc.mention) + `}}`}
		_, out, err := runTeamTask(t, resp, "agent", "get", "agt1", "--json")
		if err != nil {
			t.Fatal(err)
		}
		var dto struct {
			Listed *bool `json:"listedOnMarketplace"`
			Reg    *struct {
				MentionOnly bool `json:"mentionOnly"`
			} `json:"registerDefaults"`
		}
		if err := json.Unmarshal([]byte(out), &dto); err != nil {
			t.Fatal(err)
		}
		if dto.Listed == nil || *dto.Listed != tc.listed || dto.Reg == nil || dto.Reg.MentionOnly != tc.mention {
			t.Fatalf("json readback wrong for %+v: %s", tc, out)
		}
		_, human, err := runTeamTask(t, resp, "agent", "get", "agt1")
		if err != nil {
			t.Fatal(err)
		}
		wantL := "listed on marketplace: " + map[bool]string{true: "true", false: "false"}[tc.listed]
		wantM := "register mention only: " + map[bool]string{true: "true", false: "false"}[tc.mention]
		if !strings.Contains(human, wantL) || !strings.Contains(human, wantM) {
			t.Fatalf("text missing %q / %q:\n%s", wantL, wantM, human)
		}
	}
}

// Help claims: catalogue (not access), create can set the reset option, and
// the reset option is recorded only.
func TestAgentFlagsHelp58(t *testing.T) {
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	upd, _, err := root.Find([]string{"agent", "update"})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(upd.Long, "agent create can't set it") {
		t.Fatalf("update help still says create cannot set the reset option:\n%s", upd.Long)
	}
	if fl := upd.Flags().Lookup("listed-on-marketplace"); fl == nil || !strings.Contains(fl.Usage, "catalogue") || !strings.Contains(fl.Usage, "visibility") {
		t.Fatalf("listed-on-marketplace usage must say catalogue, not visibility/access: %+v", fl)
	}
	for _, n := range []string{"register-mention-only", "clear-register-defaults"} {
		if upd.Flags().Lookup(n) == nil {
			t.Fatalf("flag --%s missing", n)
		}
	}
	cr, _, err := root.Find([]string{"agent", "create"})
	if err != nil {
		t.Fatal(err)
	}
	if fl := cr.Flags().Lookup("reset-context-after-task"); fl == nil || !strings.Contains(fl.Usage, "Recorded only") {
		t.Fatalf("create --reset-context-after-task usage: %+v", fl)
	}
	if fl := cr.Flags().Lookup("uses-tasks"); fl == nil {
		t.Fatal("create --uses-tasks missing")
	}
	// Every create now sends the two arguments, so the help names the server floor.
	if !strings.Contains(cr.Long, "Server compatibility") || !strings.Contains(cr.Long, "schema skew") {
		t.Fatalf("create help lacks the server-compatibility note:\n%s", cr.Long)
	}
}
