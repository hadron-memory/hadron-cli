package cmd

import (
	"encoding/json"
	"strings"
	"testing"
)

func chat1280Page(items string, total int) string {
	b, _ := json.Marshal(total)
	return `{"data":{"chatServedPrompts":{"items":` + items + `,"total":` + string(b) + `}}}`
}

func TestChat1280ConcealedOrEmptyIsExplicit(t *testing.T) {
	// Missing, denied, serving off and no record are indistinguishable on the provider.
	for _, jsonMode := range []bool{false, true} {
		args := []string{"chat", "served-prompts", "chat-denied"}
		if jsonMode {
			args = append(args, "--json")
		}
		calls, out, err := runTeamTask(t, map[string]string{"ChatServedPrompts": chat1280Page("[]", 0)}, args...)
		if err != nil || len(*calls) != 1 {
			t.Fatalf("concealed read: %v calls %v", err, *calls)
		}
		if jsonMode {
			var dto struct {
				ServedPrompts *struct {
					Items                []any
					Total, Limit, Offset int
				}
			}
			if err := json.Unmarshal([]byte(out), &dto); err != nil || dto.ServedPrompts == nil || dto.ServedPrompts.Items == nil || len(dto.ServedPrompts.Items) != 0 || dto.ServedPrompts.Total != 0 {
				t.Fatalf("empty page: %v %s", err, out)
			}
		} else if !strings.Contains(out, "No records on this page") || !strings.Contains(out, "unreadable") {
			t.Fatalf("missing concealment text: %s", out)
		}
	}
}

func TestChat1280OrderAndIndependentPaging(t *testing.T) {
	newer := strings.Replace(c3bRecord, `"kind":"BIND"`, `"kind":"CHAT"`, 1)
	older := strings.Replace(newer, `"id":"rec1"`, `"id":"rec2"`, 1)
	calls, out, err := runTeamTask(t, map[string]string{"ChatServedPrompts": chat1280Page("["+newer+","+older+"]", 9)}, "chat", "served-prompts", "chat-123", "--limit", "2", "--offset", "3", "--provenance-limit", "1", "--provenance-offset", "1", "--json")
	if err != nil {
		t.Fatal(err)
	}
	var dto struct {
		ServedPrompts struct {
			Items []struct {
				ID, Kind   string
				Provenance struct{ Total, Limit, Offset int }
			}
			Total, Limit, Offset int
		}
	}
	if err := json.Unmarshal([]byte(out), &dto); err != nil {
		t.Fatal(err)
	}
	p := dto.ServedPrompts
	if len(p.Items) != 2 || p.Items[0].ID != "rec1" || p.Items[1].ID != "rec2" || p.Items[0].Kind != "CHAT" || p.Total != 9 || p.Limit != 2 || p.Offset != 3 || p.Items[0].Provenance.Limit != 1 || p.Items[0].Provenance.Offset != 1 {
		t.Fatalf("order/page lost: %s", out)
	}
	c := c2Mutation(t, calls, "ChatServedPrompts")
	if len(*calls) != 1 || c.Session != "" || c.Vars["chatId"] != "chat-123" || c.Vars["historyLimit"] != float64(2) || c.Vars["historyOffset"] != float64(3) || c.Vars["limit"] != float64(1) || c.Vars["offset"] != float64(1) {
		t.Fatalf("request: %+v", *calls)
	}
}

func TestChat1280TextAndCountsOnly(t *testing.T) {
	_, out, err := runTeamTask(t, map[string]string{"ChatServedPrompts": chat1280Page("["+c3bRecord+"]", 1)}, "chat", "served-prompts", "chat-123", "--provenance-limit", "1", "--provenance-offset", "1")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"served prompts: 1 of 1", "record: rec1", "served revision: 7", "recorded signature", "provenance: 1 of 2 (offset 1)"} {
		if !strings.Contains(out, want) {
			t.Fatalf("missing %q: %s", want, out)
		}
	}
	_, out, err = runTeamTask(t, map[string]string{"ChatServedPrompts": chat1280Page("[]", 4)}, "chat", "served-prompts", "chat-123", "--limit", "0", "--json")
	if err != nil || !strings.Contains(out, `"total": 4`) || !strings.Contains(out, `"items": []`) {
		t.Fatalf("counts: %v %s", err, out)
	}
}

func TestChat1280ProviderErrorsAndBadPages(t *testing.T) {
	cases := []struct {
		body string
		code int
	}{
		{gqlErrorJSON("FORBIDDEN"), 8}, {gqlErrorJSON("NOT_FOUND"), 4},
		{`{"data":{"chatServedPrompts":null}}`, 1}, {chat1280Page("null", 0), 1},
		{chat1280Page("[null]", 1), 1}, {chat1280Page("[]", -1), 1},
		{chat1280Page("["+c3bRecord+"]", 0), 1},
		{chat1280Page("["+strings.Replace(c3bRecord, `"servedRevSeq":7`, `"servedRevSeq":0`, 1)+"]", 1), 1},
	}
	for _, tc := range cases {
		_, out, err := runTeamTask(t, map[string]string{"ChatServedPrompts": tc.body}, "chat", "served-prompts", "chat-123", "--json")
		if exitCodeFor(err) != tc.code || out != "" {
			t.Fatalf("body %s: %v %s", tc.body, err, out)
		}
	}
}

func TestChat1280InvalidArgsNeverRead(t *testing.T) {
	cases := [][]string{{}, {" "}, {"chat-123", "--limit", "-1"}, {"chat-123", "--limit", "201"}, {"chat-123", "--offset", "-1"}, {"chat-123", "--offset", "2147483648"}, {"chat-123", "--provenance-limit", "201"}, {"chat-123", "--provenance-offset", "-1"}}
	for _, suffix := range cases {
		calls, _, err := runTeamTask(t, nil, append([]string{"chat", "served-prompts"}, suffix...)...)
		if exitCodeFor(err) != 2 || len(*calls) != 0 {
			t.Fatalf("args %v: %v calls %v", suffix, err, *calls)
		}
	}
}

func TestChat1280HelpDistinguishesChatbotFromChannel(t *testing.T) {
	_, out, err := runTeamTask(t, nil, "chat", "served-prompts", "--help")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"chat-id", "chatbot", "Channel", "newest", "--provenance-limit", "empty", "App", "sensitive"} {
		if !strings.Contains(out, want) {
			t.Fatalf("help lacks %q: %s", want, out)
		}
	}
}
