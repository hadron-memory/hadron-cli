package cmd

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/exitcode"
)

// Per-trigger run limits (#1194, server#2479). The wire contract: an unset flag
// is OMITTED (preserve / platform default), a value is sent as a number, and
// only schedule update's --clear-budget-* sends an explicit null.

func budgetInput(t *testing.T, captured map[string]json.RawMessage, op string) map[string]any {
	t.Helper()
	var vars struct {
		Input map[string]any `json:"input"`
	}
	if err := json.Unmarshal(captured[op], &vars); err != nil {
		t.Fatalf("%s variables: %v", op, err)
	}
	return vars.Input
}

func runBudgetCmd(t *testing.T, responses map[string]string, args ...string) (map[string]json.RawMessage, error) {
	t.Helper()
	gql, captured := captureGraphQL(t, responses)
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs(append(args, "--server", gql.URL))
	return captured, root.Execute()
}

func TestTriggerBudgetFlagsSendNumbers(t *testing.T) {
	const app = "capp100000000000000000000"
	for _, tc := range []struct {
		name, op string
		resp     string
		args     []string
	}{
		{"schedule create", "CreateAgentSchedule", `{"data":{"createAgentSchedule":` + scheduleJSON + `}}`,
			[]string{"schedule", "create", "--app", app, "--name", "n", "--cron", "* * * * *", "--entry", "hrn:node:acme.com::ops::x"}},
		{"schedule update", "UpdateAgentSchedule", `{"data":{"updateAgentSchedule":` + scheduleJSON + `}}`,
			[]string{"schedule", "update", "sch1"}},
		{"webhook create", "CreateAgentWebhook", `{"data":{"createAgentWebhook":` + webhookCredsJSON + `}}`,
			[]string{"webhook", "create", "--app", app, "--name", "n", "--entry", "hrn:node:acme.com::ops::x"}},
		{"run trigger", "TriggerAppRun", `{"data":{"triggerAppRun":` + appRunJSON + `}}`,
			[]string{"run", "trigger", "--app", app, "--entry", "hrn:node:acme.com::ops::x"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			captured, err := runBudgetCmd(t, map[string]string{tc.op: tc.resp}, append(tc.args, "--budget-tokens", "5000", "--budget-actions", "40")...)
			if err != nil {
				t.Fatalf("execute: %v", err)
			}
			in := budgetInput(t, captured, tc.op)
			if in["budgetTokens"] != float64(5000) || in["budgetActions"] != float64(40) {
				t.Errorf("limits not sent as numbers: %v / %v", in["budgetTokens"], in["budgetActions"])
			}
		})
	}
}

func TestTriggerBudgetUnsetIsOmitted(t *testing.T) {
	const app = "capp100000000000000000000"
	for _, tc := range []struct {
		name, op string
		resp     string
		args     []string
	}{
		{"schedule create", "CreateAgentSchedule", `{"data":{"createAgentSchedule":` + scheduleJSON + `}}`,
			[]string{"schedule", "create", "--app", app, "--name", "n", "--cron", "* * * * *", "--entry", "hrn:node:acme.com::ops::x"}},
		// An unrelated update must PRESERVE the stored limits: null would clear them.
		{"schedule update", "UpdateAgentSchedule", `{"data":{"updateAgentSchedule":` + scheduleJSON + `}}`,
			[]string{"schedule", "update", "sch1", "--cron", "0 7 * * *"}},
		{"webhook create", "CreateAgentWebhook", `{"data":{"createAgentWebhook":` + webhookCredsJSON + `}}`,
			[]string{"webhook", "create", "--app", app, "--name", "n", "--entry", "hrn:node:acme.com::ops::x"}},
		{"run trigger", "TriggerAppRun", `{"data":{"triggerAppRun":` + appRunJSON + `}}`,
			[]string{"run", "trigger", "--app", app, "--entry", "hrn:node:acme.com::ops::x"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			captured, err := runBudgetCmd(t, map[string]string{tc.op: tc.resp}, tc.args...)
			if err != nil {
				t.Fatalf("execute: %v", err)
			}
			in := budgetInput(t, captured, tc.op)
			for _, k := range []string{"budgetTokens", "budgetActions"} {
				if v, present := in[k]; present {
					t.Errorf("unset %s must be omitted, got %v", k, v)
				}
			}
		})
	}
}

func TestScheduleUpdateClearBudgetSendsNull(t *testing.T) {
	captured, err := runBudgetCmd(t, map[string]string{
		"UpdateAgentSchedule": `{"data":{"updateAgentSchedule":` + scheduleJSON + `}}`,
	}, "schedule", "update", "sch1", "--clear-budget-tokens", "--budget-actions", "7")
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	in := budgetInput(t, captured, "UpdateAgentSchedule")
	if v, present := in["budgetTokens"]; !present || v != nil {
		t.Errorf("--clear-budget-tokens must send budgetTokens:null, got %v (present=%v)", v, present)
	}
	if in["budgetActions"] != float64(7) {
		t.Errorf("budgetActions = %v, want 7", in["budgetActions"])
	}
}

func TestTriggerBudgetUsageErrors(t *testing.T) {
	for _, tc := range []struct {
		name string
		args []string
		want string
	}{
		{"value with clear", []string{"schedule", "update", "sch1", "--budget-tokens", "5", "--clear-budget-tokens"}, "mutually exclusive"},
		{"zero", []string{"run", "trigger", "--entry", "hrn:node:acme.com::ops::x", "--budget-actions", "0"}, "positive whole number"},
		{"negative", []string{"schedule", "update", "sch1", "--budget-tokens", "-3"}, "positive whole number"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f, _ := testFactory(t)
			root := NewRootCmd(f)
			root.SetArgs(append(tc.args, "--server", "http://127.0.0.1:1", "--app", "capp100000000000000000000"))
			err := root.Execute()
			if err == nil || !strings.Contains(err.Error(), tc.want) || exitCodeFor(err) != exitcode.Usage {
				t.Fatalf("got %v (code %v), want usage error containing %q", err, exitCodeFor(err), tc.want)
			}
		})
	}
}

// Above-default values are not checked client-side: the server's refusal passes
// through with the CLI's usual BAD_USER_INPUT exit code.
func TestTriggerBudgetServerRefusalPassesThrough(t *testing.T) {
	const msg = "Token budget must be a positive whole number no greater than the platform per-run default (100000)."
	_, err := runBudgetCmd(t, map[string]string{
		"TriggerAppRun": memberGQLError(msg, "BAD_USER_INPUT"),
	}, "run", "trigger", "--app", "capp100000000000000000000", "--entry", "hrn:node:acme.com::ops::x", "--budget-tokens", "999999999")
	if err == nil || !strings.Contains(err.Error(), "platform per-run default") {
		t.Fatalf("server refusal text not passed through: %v", err)
	}
	if exitCodeFor(err) != exitcode.Usage {
		t.Errorf("exit code = %v, want Usage", exitCodeFor(err))
	}
}

func TestTriggerBudgetReadSurface(t *testing.T) {
	sched := strings.Replace(scheduleJSON, `"policy":null,`, `"policy":null,"budgetTokens":5000,"budgetActions":null,`, 1)
	gql, _ := captureGraphQL(t, map[string]string{
		"AgentSchedules": `{"data":{"agentSchedules":{"items":[` + sched + `],"total":1}}}`,
	})
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"schedule", "ls", "--app", "capp100000000000000000000", "--json", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	var rows []map[string]any
	if err := json.Unmarshal([]byte(out.String()), &rows); err != nil || len(rows) != 1 {
		t.Fatalf("bad json %q: %v", out.String(), err)
	}
	if rows[0]["budgetTokens"] != float64(5000) {
		t.Errorf("budgetTokens = %v", rows[0]["budgetTokens"])
	}
	if v, present := rows[0]["budgetActions"]; !present || v != nil {
		t.Errorf("budgetActions must be a present null, got %v (present=%v)", v, present)
	}
}
