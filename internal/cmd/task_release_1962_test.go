package cmd

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/exitcode"
)

// cli#1962 (cor:agt:120:02, hadron-server#4084): `team task release` gives a
// task's outside item back and says which item it was.

const releaseItem1962 = "https://github.com/acme/app/issues/9"

func releaseResponses1962() map[string]string {
	r := taskResponses()
	r["IntakeTaskExternalId"] = `{"data":{"agentTask":{"externalId":"` + releaseItem1962 + `"}}}`
	r["AgentTaskRelease"] = `{"data":{"releaseAgentTaskExternalId":` + taskFieldsJSON + `}}`
	return r
}

func TestTaskReleaseGivesTheItemBack1962(t *testing.T) {
	calls, out, err := runTeamTask(t, releaseResponses1962(), "team", "task", "release", "task1")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "released: "+releaseItem1962) {
		t.Fatalf("the receipt must give the URL back: %q", out)
	}
	var sawRead, sawRelease bool
	for _, c := range *calls {
		switch c.Op {
		case "IntakeTaskExternalId":
			sawRead = true
			if sawRelease {
				t.Fatal("the item must be read BEFORE the release clears it")
			}
		case "AgentTaskRelease":
			sawRelease = true
			if c.Vars["taskId"] != "task1" || c.Session != "s-new" {
				t.Fatalf("release not sent as the bound session on task1: %+v", c)
			}
		case "AgentTaskCancel":
			t.Fatal("release must not cancel")
		}
	}
	if !sawRead || !sawRelease {
		t.Fatalf("read=%v release=%v", sawRead, sawRelease)
	}
}

func TestTaskReleaseJSONCarriesTheReleasedItem1962(t *testing.T) {
	r := releaseResponses1962()
	r["AgentTaskRelease"] = strings.TrimSuffix(r["AgentTaskRelease"], "}") + `,"extensions":{"warnings":[{"path":"releaseAgentTaskExternalId","code":"future.release","text":"released with advice","register":{"state":"unmapped","taskLoc":null,"taskUrn":null}}]}}`
	_, out, err := runTeamTask(t, r, "--json", "team", "task", "release", "task1")
	if err != nil {
		t.Fatal(err)
	}
	var dto struct {
		ReleasedExternalID string `json:"releasedExternalId"`
		Warnings           []struct {
			Code     string `json:"code"`
			Text     string `json:"text"`
			Register *struct {
				State   string  `json:"state"`
				TaskLoc *string `json:"taskLoc"`
				TaskURN *string `json:"taskUrn"`
			} `json:"register"`
		} `json:"warnings"`
	}
	if err := json.Unmarshal([]byte(out), &dto); err != nil {
		t.Fatalf("%v: %q", err, out)
	}
	if dto.ReleasedExternalID != releaseItem1962 || len(dto.Warnings) != 1 || dto.Warnings[0].Code != "future.release" || dto.Warnings[0].Text != "released with advice" || dto.Warnings[0].Register == nil || dto.Warnings[0].Register.State != "unmapped" || dto.Warnings[0].Register.TaskLoc != nil || dto.Warnings[0].Register.TaskURN != nil {
		t.Fatalf("got %+v", dto)
	}
}

// #2263 pins mapped register values on release (introduced in #1962).
// future.release is deliberately unknown: warning codes pass through verbatim.
// The input path routes the warning to this mutation; it is not a JSON DTO field.
func TestTaskReleaseJSONCarriesMappedWarning2263(t *testing.T) {
	r := releaseResponses1962()
	r["AgentTaskRelease"] = strings.TrimSuffix(r["AgentTaskRelease"], "}") + `,"extensions":{"warnings":[{"path":"releaseAgentTaskExternalId","code":"future.release","text":"released with advice","register":{"state":"mapped","taskLoc":"tasks:release-advice","taskUrn":"hrn:node:acme.com:runbooks:tasks:release-advice"}}]}}`
	_, out, err := runTeamTask(t, r, "--json", "team", "task", "release", "task1")
	if err != nil {
		t.Fatal(err)
	}
	var dto struct {
		ReleasedExternalID string `json:"releasedExternalId"`
		Warnings           []struct {
			Code     string `json:"code"`
			Text     string `json:"text"`
			Register *struct {
				State   string  `json:"state"`
				TaskLoc *string `json:"taskLoc"`
				TaskURN *string `json:"taskUrn"`
			} `json:"register"`
		} `json:"warnings"`
	}
	if err := json.Unmarshal([]byte(out), &dto); err != nil {
		t.Fatalf("%v: %q", err, out)
	}
	if dto.ReleasedExternalID != releaseItem1962 {
		t.Errorf("releasedExternalId: got %q, want %q", dto.ReleasedExternalID, releaseItem1962)
	}
	if len(dto.Warnings) != 1 {
		t.Fatalf("warnings length: got %d, want 1: %s", len(dto.Warnings), out)
	}
	warning := dto.Warnings[0]
	if warning.Code != "future.release" {
		t.Errorf("warnings[0].code: got %q, want future.release", warning.Code)
	}
	if warning.Text != "released with advice" {
		t.Errorf("warnings[0].text: got %q, want released with advice", warning.Text)
	}
	if warning.Register == nil {
		t.Fatalf("warnings[0].register: got null, want mapped register: %s", out)
	}
	if warning.Register.State != "mapped" {
		t.Errorf("warnings[0].register.state: got %q, want mapped", warning.Register.State)
	}
	if warning.Register.TaskLoc == nil {
		t.Error("warnings[0].register.taskLoc: got null, want tasks:release-advice")
	} else if *warning.Register.TaskLoc != "tasks:release-advice" {
		t.Errorf("warnings[0].register.taskLoc: got %q, want tasks:release-advice", *warning.Register.TaskLoc)
	}
	if warning.Register.TaskURN == nil {
		t.Error("warnings[0].register.taskUrn: got null, want hrn:node:acme.com:runbooks:tasks:release-advice")
	} else if *warning.Register.TaskURN != "hrn:node:acme.com:runbooks:tasks:release-advice" {
		t.Errorf("warnings[0].register.taskUrn: got %q, want hrn:node:acme.com:runbooks:tasks:release-advice", *warning.Register.TaskURN)
	}
}

// Release always emits a warnings array; no server advice is [] rather than null.
func TestTaskReleaseJSONAbsentWarningsAreEmpty2263(t *testing.T) {
	out, stderr, err := runTeamTaskStreams(t, releaseResponses1962(), "--json", "team", "task", "release", "task1")
	if err != nil {
		t.Fatal(err)
	}
	var dto map[string]json.RawMessage
	if err := json.Unmarshal([]byte(out), &dto); err != nil {
		t.Fatalf("invalid JSON: %v: %s", err, out)
	}
	if got := strings.TrimSpace(string(dto["warnings"])); got != "[]" {
		t.Errorf("warnings: got %q, want []", got)
	}
	if stderr != "" {
		t.Errorf("unexpected stderr: %q", stderr)
	}
}

// The server owns every refusal and the CLI shows it: a task holding no
// external id (a second release included), a caller who may not cancel it.
func TestTaskReleaseRefusalsAreTheServers1962(t *testing.T) {
	cases := []struct {
		name, body string
		code       int
	}{
		{"none", `{"errors":[{"message":"This task holds no external id.","extensions":{"code":"TASK_EXTERNAL_ID_NONE"}}]}`, exitcode.Conflict},
		{"not owner", `{"errors":[{"message":"Only whoever may cancel it.","extensions":{"code":"TASK_NOT_OWNER"}}]}`, exitcode.Forbidden},
		{"uninstalled", `{"errors":[{"message":"App is uninstalled.","extensions":{"code":"APP_UNINSTALLED"}}]}`, exitcode.Conflict},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := releaseResponses1962()
			r["AgentTaskRelease"] = tc.body
			_, out, err := runTeamTask(t, r, "team", "task", "release", "task1")
			if err == nil {
				t.Fatalf("a refusal must fail: %q", out)
			}
			if got := exitCodeFor(err); got != tc.code {
				t.Fatalf("exit %d, want %d: %v", got, tc.code, err)
			}
			if strings.Contains(out, "released:") {
				t.Fatalf("a refused release printed a receipt: %q", out)
			}
		})
	}
}

func TestTaskReleaseUnreadableItemStillLetsTheServerAnswer1962(t *testing.T) {
	r := releaseResponses1962()
	r["IntakeTaskExternalId"] = `{"errors":[{"message":"nope","extensions":{"code":"TASK_NOT_FOUND"}}]}`
	_, out, err := runTeamTask(t, r, "team", "task", "release", "task1")
	if err != nil {
		t.Fatalf("the mutation decides, not the pre-read: %v", err)
	}
	if strings.Contains(out, "released: ") {
		t.Fatalf("no item was read, so none can be claimed back: %q", out)
	}
}
