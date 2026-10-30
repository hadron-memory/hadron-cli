package cmd

import (
	"encoding/json"
	"strings"
	"testing"
)

func session1280Page(items string, total int) string {
	b, _ := json.Marshal(total)
	return `{"data":{"session":{"servedPrompts":{"items":` + items + `,"total":` + string(b) + `}}}}`
}

func TestSession1280ServerOrderAndPaging(t *testing.T) {
	older := strings.Replace(c3bRecord, `"id":"rec1"`, `"id":"rec2"`, 1)
	calls, out, err := runTeamTask(t, map[string]string{"SessionServedPrompts": session1280Page("["+c3bRecord+","+older+"]", 7)}, "team", "session", "served-prompts", "session-123", "--limit", "2", "--offset", "3", "--json")
	if err != nil {
		t.Fatal(err)
	}
	var page struct {
		ServedPrompts struct {
			Items []struct {
				ID string `json:"id"`
			}
			Total, Limit, Offset int
		} `json:"servedPrompts"`
	}
	if err := json.Unmarshal([]byte(out), &page); err != nil {
		t.Fatal(err)
	}
	p := page.ServedPrompts
	if len(p.Items) != 2 || p.Items[0].ID != "rec1" || p.Items[1].ID != "rec2" || p.Total != 7 || p.Limit != 2 || p.Offset != 3 {
		t.Fatalf("lost order/page: %s", out)
	}
	c := c2Mutation(t, calls, "SessionServedPrompts")
	if len(*calls) != 1 || c.Vars["id"] != "session-123" || c.Vars["historyLimit"] != float64(2) || c.Vars["historyOffset"] != float64(3) {
		t.Fatalf("wrong request: %+v", *calls)
	}
	for _, fragment := range []string{"content(", "edges(", "bootPromptNode", "promptOverride"} {
		if strings.Contains(c.Query, fragment) {
			t.Fatalf("source read: %s", c.Query)
		}
	}
}

func TestSession1280ConcealmentEmptyAndErrors(t *testing.T) {
	for _, tc := range []struct {
		name, body, want string
		code             int
	}{
		{"missing or denied", `{"data":{"session":null}}`, `"servedPrompts": null`, 0},
		{"empty or concealed", session1280Page("[]", 0), `"items": []`, 0},
		{"counts only", session1280Page("[]", 4), `"total": 4`, 0},
		{"provider forbidden", gqlErrorJSON("FORBIDDEN"), "", 8},
		{"provider not found", gqlErrorJSON("NOT_FOUND"), "", 4},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls, out, err := runTeamTask(t, map[string]string{"SessionServedPrompts": tc.body}, "team", "session", "served-prompts", "session-123", "--limit", "0", "--json")
			if exitCodeFor(err) != tc.code || len(*calls) != 1 {
				t.Fatalf("code %d calls %v: %v", exitCodeFor(err), *calls, err)
			}
			if tc.code == 0 && !strings.Contains(out, tc.want) {
				t.Fatalf("missing %s: %s", tc.want, out)
			}
			if tc.code != 0 && out != "" {
				t.Fatalf("partial output %s", out)
			}
		})
	}
}

func TestSession1280Help(t *testing.T) {
	_, out, err := runTeamTask(t, nil, "team", "session", "served-prompts", "--help")
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range []string{"session-id", "--limit", "--offset", "newest", "unavailable", "provenance", "--json"} {
		if !strings.Contains(out, s) {
			t.Fatalf("missing %s: %s", s, out)
		}
	}
	_, out, err = runTeamTask(t, nil, "team", "worker", "served-prompt", "--help")
	if err != nil || !strings.Contains(out, "session served-prompts") {
		t.Fatalf("missing history pointer: %v %s", err, out)
	}
}
