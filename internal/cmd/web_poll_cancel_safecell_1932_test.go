package cmd

import (
	"encoding/json"
	"strings"
	"testing"
)

// cli#1932: web-poll cancel prints the SERVER-returned jobId, which is
// untrusted text. Each text outcome must run it through output.SafeCell (as
// web-poll list does), while --json keeps the value the server sent.
const hostileJobID1932 = "job\x1b[31m‮\rX\nforged"

func hostileCancel1932(cancelled, already bool) string {
	id, _ := json.Marshal(hostileJobID1932)
	c, a := "false", "false"
	if cancelled {
		c = "true"
	}
	if already {
		a = "true"
	}
	return `{"data":{"cancelWebPoll":{"jobId":` + string(id) + `,"cancelled":` + c + `,"alreadyCancelled":` + a + `}}}`
}

func TestWebPollCancelTextOutcomesSanitizeServerJobID1932(t *testing.T) {
	for name, tc := range map[string]struct {
		cancelled, already bool
		want               string
	}{
		"stopped":         {true, false, "stopped watch"},
		"already stopped": {true, true, "was already stopped"},
		"not stopped":     {false, false, "was not stopped"},
	} {
		t.Run(name, func(t *testing.T) {
			_, out, _ := runWebPoll(t, map[string]string{"CancelWebPoll": hostileCancel1932(tc.cancelled, tc.already)},
				"cancel", "job_1", "--org", "acme.com", "--yes")
			if !strings.Contains(out, tc.want) {
				t.Fatalf("want %q in output: %q", tc.want, out)
			}
			for _, bad := range []string{"\x1b", "‮", "\r"} {
				if strings.Contains(out, bad) {
					t.Fatalf("hidden control %q reached the terminal: %q", bad, out)
				}
			}
			if strings.Count(out, "\n") != 1 {
				t.Fatalf("a newline in the id forged an extra line: %q", out)
			}
		})
	}
}

func TestWebPollCancelJSONKeepsRawServerJobID1932(t *testing.T) {
	_, out, err := runWebPoll(t, map[string]string{"CancelWebPoll": hostileCancel1932(true, false)},
		"cancel", "job_1", "--org", "acme.com", "--yes", "--json")
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.Unmarshal([]byte(out), &got); err != nil || got["jobId"] != hostileJobID1932 {
		t.Fatalf("--json must carry the raw server value: %v err %v\n%q", got["jobId"], err, out)
	}
}
