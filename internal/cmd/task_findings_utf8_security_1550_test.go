package cmd

import (
	"strings"
	"testing"
)

func TestTaskFindingsInStringUTF8Security1550(t *testing.T) {
	body := "[{\"severity\":\"P1\",\"text\":\"SENTINEL-a\xffb\"}]"
	calls, out, err := runTeamTask(t, map[string]string{"AgentTaskFinishWithFindings": finishReply1550(t, `{"items":[],"total":0,"hasMore":false}`)}, "team", "task", "finish", "AT0.7", "--outcome", "succeeded", "--findings-file", findingsFile1550(t, body))
	if err == nil || exitCodeFor(err) != 2 || len(*calls) != 0 || out != "" || strings.Contains(err.Error(), "SENTINEL") {
		t.Fatalf("in-string UTF-8 refusal err%v calls%d out%q", err, len(*calls), out)
	}
}
