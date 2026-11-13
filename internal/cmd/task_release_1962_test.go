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
	_, out, err := runTeamTask(t, releaseResponses1962(), "--json", "team", "task", "release", "task1")
	if err != nil {
		t.Fatal(err)
	}
	var dto struct {
		ReleasedExternalID string   `json:"releasedExternalId"`
		Warnings           []string `json:"warnings"`
	}
	if err := json.Unmarshal([]byte(out), &dto); err != nil {
		t.Fatalf("%v: %q", err, out)
	}
	if dto.ReleasedExternalID != releaseItem1962 || dto.Warnings == nil {
		t.Fatalf("got %+v", dto)
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
