package cmd

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/api/gen"
)

func TestS3LegacyOperationCompatibility2067(t *testing.T) {
	for name, doc := range map[string]string{
		"GetAgent": gen.GetAgent_Operation, "Agents": gen.Agents_Operation, "PublicAgents": gen.PublicAgents_Operation,
		"CreateAgent": gen.CreateAgent_Operation, "UpdateAgent": gen.UpdateAgent_Operation,
		"GetWorker": gen.GetWorker_Operation, "GetWorkerInApp": gen.GetWorkerInApp_Operation, "WorkersRoster": gen.WorkersRoster_Operation,
	} {
		t.Run(name, func(t *testing.T) {
			if strings.Contains(doc, "$lifecycle") {
				t.Fatal("legacy document declares an S3 lifecycle variable even when the flag is omitted")
			}
			for _, line := range strings.Split(doc, "\n") {
				switch strings.TrimSpace(line) {
				case "lifecycle", "leaveAlone", "leaveAloneReason", "leaveAloneBy", "leaveAloneAt", "sweepMode":
					t.Fatalf("legacy document selects S3 field %s", strings.TrimSpace(line))
				}
			}
		})
	}
}

func TestS3AgentGetOldSchemaUnknown2067(t *testing.T) {
	calls, out, err := runTeamTask(t, map[string]string{
		"GetAgent":          `{"data":{"agent":` + agentJSON + `}}`,
		"GetAgentLifecycle": `{"errors": [{"message": "Cannot query field \"lifecycle\" on type \"Agent\".", "extensions": {"code": "GRAPHQL_VALIDATION_FAILED"}}]}`,
	}, "agent", "get", "agt1")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "lifecycle: unknown (server before #4242)") {
		t.Fatal(out)
	}
	if len(callsFor(calls, "GetAgent")) != 1 || len(callsFor(calls, "GetAgentLifecycle")) != 1 {
		t.Fatalf("calls %+v", *calls)
	}
	for _, c := range *calls {
		if c.Session != "" {
			t.Fatal("read attributed to a worker")
		}
	}
}

func TestS3LeaveAloneRead2067(t *testing.T) {
	calls, out, err := runTeamTask(t, map[string]string{"GetWorkerLeaveAlone": `{"data":{"worker":{"id":"wkr1","leaveAlone":true,"leaveAloneReason":"maintenance","leaveAloneBy":"u1","leaveAloneAt":"today"}}}`}, "team", "worker", "leave-alone", "Iris", "--json")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, `"leaveAlone": true`) || !strings.Contains(out, `"workerId": "wkr1"`) {
		t.Fatal(out)
	}
	reads := callsFor(calls, "GetWorkerLeaveAlone")
	if len(reads) != 1 || reads[0].Session != "" {
		t.Fatalf("read %+v", *calls)
	}
	for _, c := range *calls {
		if c.Op == "SetWorkerLeaveAlone" {
			t.Fatal("marker read wrote state")
		}
	}
}

// A read failure is not evidence of an old schema. Only the framework's
// lifecycle-field rejection may turn a successful base read into unknown.
func TestS3LifecycleFallbackIsSpecific2067(t *testing.T) {
	for _, failure := range []string{
		`{"errors":[{"message":"denied","extensions":{"code":"FORBIDDEN"}}]}`,
		`{"errors":[{"message":"Cannot query field \"other\" on type \"Agent\".","extensions":{"code":"GRAPHQL_VALIDATION_FAILED"}}]}`,
		`{"errors":[{"message":"Cannot query field \"lifecycle\" on type \"Agent\".","extensions":{"code":"GRAPHQL_VALIDATION_FAILED"}},{"message":"denied","extensions":{"code":"FORBIDDEN"}}]}`,
		`{"data":{"agent":null}}`,
		`gateway failed`,
	} {
		calls, out, err := runTeamTask(t, map[string]string{"GetAgent": `{"data":{"agent":` + agentJSON + `}}`, "GetAgentLifecycle": failure}, "agent", "get", "agt1", "--json")
		if err == nil || out != "" {
			t.Fatalf("failure %s became unknown: %v %s", failure, err, out)
		}
		if len(callsFor(calls, "GetAgentLifecycle")) != 1 {
			t.Fatalf("retried: %+v", *calls)
		}
	}
}

// Exporting newer SDL must not send unused input fields to older servers.
func TestS3SchemaRefreshOmitsUnusedHealthFields2067(t *testing.T) {
	b, err := json.Marshal(gen.WorkerTerminalReportInput{})
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]any
	if err := json.Unmarshal(b, &fields); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"contextTokens", "contextMeasuredAt"} {
		if _, ok := fields[key]; ok {
			t.Fatalf("unused field %s sent by legacy health input: %s", key, b)
		}
	}
}
