package cmd

import (
	"encoding/json"
	"os"
	"reflect"
	"sort"
	"strings"
	"testing"
)

func messagesC2Page(t *testing.T, times ...any) string {
	t.Helper()
	if times == nil {
		times = []any{"2026-11-06T01:20:59.987654321Z", "2026-11-06T03:20:15+02:00", "2026-01-01T00:05:00+01:00", nil, "not-a-time\nFORGED"}
	}
	items := make([]map[string]any, 0, len(times))
	for i, at := range times {
		items = append(items, map[string]any{"nodeId": "message", "seq": i + 1, "body": "hello", "at": at, "authorName": "Iris", "authorWorkerId": "worker", "authorUserId": nil, "mentions": []string{}, "replyToSeq": nil})
	}
	raw, err := json.Marshal(map[string]any{"data": map[string]any{"teamChatMessages": map[string]any{"total": len(items), "items": items}}})
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

func runMessagesC2Chat(t *testing.T, asJSON bool, times ...any) (string, []string) {
	t.Helper()
	r := chatReadResponses()
	r["TeamChatMessages"] = messagesC2Page(t, times...)
	args := []string{"team", "chat", "read", "--app", "acme.com:eng-team"}
	if asJSON {
		args = append(args, "--json")
	}
	calls, out, err := runTeamTask(t, r, args...)
	if err != nil {
		t.Fatal(err)
	}
	return out, opsOf(*calls)
}

func runMessagesC2Cast(t *testing.T, worker string, asJSON bool) (string, []string) {
	t.Helper()
	args := []string{"team", "worker", "cast", "--app", "acme.com:eng-team", "--agent", "agt1", "--name", "Iris"}
	if asJSON {
		args = append(args, "--json")
	}
	calls, out, err := runTeamTask(t, map[string]string{"CastWorker": `{"data":{"castWorker":` + worker + `}}`}, args...)
	if err != nil {
		t.Fatal(err)
	}
	return out, opsOf(*calls)
}

func TestMessagesC2CastURN(t *testing.T) {
	out, ops := runMessagesC2Cast(t, irisWorkerJSON, false)
	if !reflect.DeepEqual(ops, []string{"CastWorker"}) {
		t.Fatalf("new lookup: %v", ops)
	}
	wantText := "✓ cast Iris (backend-engineer) — worker wkr1\n" +
		"  urn: hrn:worker:acme.com:eng-team:iris\n" +
		"reporting choice withheld from this caller\n\nYou are Iris.\n"
	if out != wantText {
		t.Fatalf("cast line order/text: got %q want %q", out, wantText)
	}
	raw, ops := runMessagesC2Cast(t, irisWorkerJSON, true)
	want, err := os.ReadFile("testdata/messages_c2_cast_base.json")
	if err != nil {
		t.Fatal(err)
	}
	if raw != string(want) {
		t.Fatalf("JSON changed:\n%s", raw)
	}
	if !reflect.DeepEqual(ops, []string{"CastWorker"}) {
		t.Fatal(ops)
	}
	var fields map[string]any
	if err := json.Unmarshal([]byte(raw), &fields); err != nil {
		t.Fatal(err)
	}
	if fields["urn"] != "hrn:worker:acme.com:eng-team:iris" {
		t.Fatal(fields["urn"])
	}
	// Nil and empty server URNs must not be invented from the App/name.
	var fixture map[string]any
	if err := json.Unmarshal([]byte(irisWorkerJSON), &fixture); err != nil {
		t.Fatal(err)
	}
	for _, urn := range []any{nil, ""} {
		fixture["urn"] = urn
		b, err := json.Marshal(fixture)
		if err != nil {
			t.Fatal(err)
		}
		out, ops := runMessagesC2Cast(t, string(b), false)
		if strings.Contains(out, "  urn:") || !strings.Contains(out, "✓ cast Iris (backend-engineer) — worker wkr1\n") || len(ops) != 1 {
			t.Fatalf("fallback: %q %v", out, ops)
		}
	}
}

func TestMessagesC2CastHostileURN(t *testing.T) {
	var fixture map[string]any
	if err := json.Unmarshal([]byte(irisWorkerJSON), &fixture); err != nil {
		t.Fatal(err)
	}
	const hostile = "hrn:worker:server.example:team:iris\x1b]52;c;payload\a\nFORGED-LINE\r"
	fixture["urn"] = hostile
	data, err := json.Marshal(fixture)
	if err != nil {
		t.Fatal(err)
	}
	out, ops := runMessagesC2Cast(t, string(data), false)
	if len(ops) != 1 || strings.ContainsAny(out, "\x1b\a\r") || strings.Contains(out, "\nFORGED-LINE") || !strings.Contains(out, "\n  urn: hrn:worker:server.example:team:iris") {
		t.Fatalf("unsafe or absent URN: %q %v", out, ops)
	}
	raw, ops := runMessagesC2Cast(t, string(data), true)
	var fields map[string]any
	if err := json.Unmarshal([]byte(raw), &fields); err != nil {
		t.Fatal(err)
	}
	if len(ops) != 1 || fields["urn"] != hostile {
		t.Fatalf("JSON changed returned URN: %q %v", raw, ops)
	}
}

func TestMessagesC2ChatTimes(t *testing.T) {
	out, ops := runMessagesC2Chat(t, false)
	wantLines := []string{
		"[1] 2026-11-06T01:20Z Iris (worker): hello",
		"[2] 2026-11-06T01:20Z Iris (worker): hello",
		"[3] 2025-12-31T23:05Z Iris (worker): hello",
		"[4] Iris (worker): hello",
		"[5] Iris (worker): hello",
	}
	assertMessageLines1762(t, out, wantLines)
	if strings.Contains(out, "FORGED") {
		t.Fatal(out)
	}
	// Captured from fresh main before the render-only change; no extra operation.
	expected, err := os.ReadFile("testdata/messages_c2_chat_ops_base.json")
	if err != nil {
		t.Fatal(err)
	}
	var wantOps []string
	if err := json.Unmarshal(expected, &wantOps); err != nil {
		t.Fatal(err)
	}
	assertChatOpsEconomy1677(t, ops, wantOps)
	raw, _ := runMessagesC2Chat(t, true)
	want, err := os.ReadFile("testdata/messages_c2_chat_base.json")
	if err != nil {
		t.Fatal(err)
	}
	if raw != string(want) {
		t.Fatalf("JSON changed:\n%s", raw)
	}
}

func assertMessageLines1762(t *testing.T, out string, want []string) {
	t.Helper()
	var lines []string
	for _, line := range strings.Split(out, "\n") {
		if strings.HasPrefix(line, "[") {
			lines = append(lines, line)
		}
	}
	if !reflect.DeepEqual(lines, want) {
		t.Fatalf("exact message lines: got %q want %q", lines, want)
	}
}

func TestCastBlankRenderedURN1762(t *testing.T) {
	for _, raw := range []string{"   ", "\t\r\n\x1b\u202e", "\u00a0\u200b\u2028", " hrn:worker:acme.com:eng-team:iris "} {
		t.Run(raw, func(t *testing.T) {
			var fixture map[string]any
			if err := json.Unmarshal([]byte(irisWorkerJSON), &fixture); err != nil {
				t.Fatal(err)
			}
			fixture["urn"] = raw
			data, err := json.Marshal(fixture)
			if err != nil {
				t.Fatal(err)
			}
			out, ops := runMessagesC2Cast(t, string(data), false)
			want := "✓ cast Iris (backend-engineer) — worker wkr1\n"
			if strings.HasPrefix(raw, " hrn:") {
				want += "  urn: " + raw + "\n"
			}
			want += "reporting choice withheld from this caller\n\nYou are Iris.\n"
			if out != want || !reflect.DeepEqual(ops, []string{"CastWorker"}) {
				t.Fatalf("text/economics: got %q ops %v want %q", out, ops, want)
			}
			jsonOut, jsonOps := runMessagesC2Cast(t, string(data), true)
			var dto map[string]any
			if err := json.Unmarshal([]byte(jsonOut), &dto); err != nil {
				t.Fatal(err)
			}
			if dto["urn"] != raw || !reflect.DeepEqual(jsonOps, ops) {
				t.Fatalf("raw JSON/economics: %q %v", jsonOut, jsonOps)
			}
		})
	}
}

func TestChatTimestampEdges1762(t *testing.T) {
	times := []any{"2026-11-06T01:20:59Z", "2026-11-06T01:20:59+00:00", "2026-11-06T01:20:59", "2026-11-06t01:20:59z", "2026-11-06 01:20:59Z", " 2026-11-06T01:20:59Z ", "2026-11-06T01:20:59Z ", " 2026-11-06T01:20:59Z"}
	out, ops := runMessagesC2Chat(t, false, times...)
	assertMessageLines1762(t, out, []string{
		"[1] 2026-11-06T01:20Z Iris (worker): hello",
		"[2] 2026-11-06T01:20Z Iris (worker): hello",
		"[3] Iris (worker): hello", "[4] Iris (worker): hello", "[5] Iris (worker): hello",
		"[6] Iris (worker): hello", "[7] Iris (worker): hello", "[8] Iris (worker): hello",
	})
	jsonOut, jsonOps := runMessagesC2Chat(t, true, times...)
	var dto struct {
		Messages []struct {
			At string `json:"at"`
		} `json:"messages"`
	}
	if err := json.Unmarshal([]byte(jsonOut), &dto); err != nil {
		t.Fatal(err)
	}
	wantJSONOps := []string{"TeamAppIdentity", "TeamChatReadHead", "TeamChatReadMetadata", "ChannelReadState", "TeamChatMessages"}
	wantTextOps := append(append([]string{}, wantJSONOps...), "TeamAppIdentity") // Existing text-only App description.
	if len(dto.Messages) != len(times) {
		t.Fatalf("JSON/economics: %s %v/%v", jsonOut, jsonOps, ops)
	}
	assertChatOpsEconomy1677(t, jsonOps, wantJSONOps)
	assertChatOpsEconomy1677(t, ops, wantTextOps)
	for i, m := range dto.Messages {
		if m.At != times[i] {
			t.Fatalf("raw timestamp %d: got %q want %q", i, m.At, times[i])
		}
	}
}

// assertChatOpsEconomy1677 pins a chat read's request set and its ordering
// CONSTRAINTS, not one arrival order (cli#1677). The allocator-head and
// read-cursor reads overlap the head and page reads, so their relative arrival
// is not a contract; these are: the exact multiset of operations (a re-added or
// duplicated request changes it), the head probe before the page (its claim is
// about messages seen before the page was asked for), and the cursor read after
// the metadata read it depends on.
func assertChatOpsEconomy1677(t *testing.T, got, want []string) {
	t.Helper()
	a, b := append([]string{}, got...), append([]string{}, want...)
	sort.Strings(a)
	sort.Strings(b)
	if !reflect.DeepEqual(a, b) {
		t.Fatalf("operations %v != baseline set %v", got, want)
	}
	idx := func(op string) int {
		for i, g := range got {
			if g == op {
				return i
			}
		}
		return -1
	}
	if idx("TeamChatReadHead") > idx("TeamChatMessages") {
		t.Fatalf("head probe must precede the page: %v", got)
	}
	if idx("TeamChatReadMetadata") > idx("ChannelReadState") && idx("ChannelReadState") >= 0 {
		t.Fatalf("cursor read must follow the metadata read: %v", got)
	}
}
