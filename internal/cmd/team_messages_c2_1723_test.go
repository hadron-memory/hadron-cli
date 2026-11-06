package cmd

import (
	"encoding/json"
	"os"
	"reflect"
	"strings"
	"testing"
)

func messagesC2Page(t *testing.T) string {
	t.Helper()
	times := []any{"2026-11-06T01:20:59.987654321Z", "2026-11-06T03:20:15+02:00", "2026-01-01T00:05:00+01:00", nil, "not-a-time\nFORGED"}
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

func runMessagesC2Chat(t *testing.T, asJSON bool) (string, []string) {
	t.Helper()
	r := chatReadResponses()
	r["TeamChatMessages"] = messagesC2Page(t)
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
	if !strings.Contains(out, "\n  urn: hrn:worker:acme.com:eng-team:iris\n") {
		t.Fatalf("URN absent: %s", out)
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

func TestMessagesC2ChatTimes(t *testing.T) {
	out, ops := runMessagesC2Chat(t, false)
	wantLines := []string{
		"[1] 2026-11-06T01:20Z Iris (worker): hello",
		"[2] 2026-11-06T01:20Z Iris (worker): hello",
		"[3] 2025-12-31T23:05Z Iris (worker): hello",
		"[4] Iris (worker): hello",
		"[5] Iris (worker): hello",
	}
	for _, line := range wantLines {
		if !strings.Contains(out, line+"\n") {
			t.Errorf("missing %q in %s", line, out)
		}
	}
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
	if !reflect.DeepEqual(ops, wantOps) {
		t.Fatalf("operations %v != baseline %v", ops, wantOps)
	}
	raw, _ := runMessagesC2Chat(t, true)
	want, err := os.ReadFile("testdata/messages_c2_chat_base.json")
	if err != nil {
		t.Fatal(err)
	}
	if raw != string(want) {
		t.Fatalf("JSON changed:\n%s", raw)
	}
}
