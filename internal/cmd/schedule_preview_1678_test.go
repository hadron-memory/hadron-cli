package cmd

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/exitcode"
)

const summary1678 = `{"name":"Weekly","reads":["App memories"],"writes":["Run memory"],"cannotDo":["No outside access"],"tools":["search"],"limits":["Budget 100"],"schedule":"Runs on the schedule \"0 6 * * *\" (UTC)","flows":[{"name":"Main","urn":"hrn:node:acme.com:ops:entry","tools":[],"writes":[]}]}`

var headHash1678 = strings.Repeat("a", 64)

func reply1678() string {
	return `{"data":{"previewAgentSchedule":{"outcome":"ADOPTABLE","hash":"` + headHash1678 + `","reason":null,"summary":` + summary1678 + `}}}`
}
func runPreview1678(t *testing.T, reply string, args ...string) (string, string, []map[string]any, error) {
	t.Helper()
	calls := []map[string]any{}
	var mu sync.Mutex
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req map[string]any
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Error(err)
		}
		mu.Lock()
		calls = append(calls, req)
		mu.Unlock()
		if reply == "HTTP502" {
			w.WriteHeader(502)
			fmt.Fprint(w, "bad gateway")
			return
		}
		if reply == "DROP" {
			conn, _, err := w.(http.Hijacker).Hijack()
			if err != nil {
				t.Error(err)
				return
			}
			_ = conn.Close()
			return
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, reply)
	}))
	defer srv.Close()
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs(append([]string{"schedule", "preview", "--server", srv.URL}, args...))
	err := root.Execute()
	if err != nil {
		renderError(f, err)
	}
	mu.Lock()
	captured := append([]map[string]any{}, calls...)
	mu.Unlock()
	return out.String(), f.IOStreams.ErrOut.(*strings.Builder).String(), captured, err
}
func previewArgs1678() []string {
	return []string{"--app", "acme.com:ops", "--entry", "acme.com::ops::entry", "--cron", "0 6 * * *"}
}
func TestSchedulePreview1678DefaultsAndJSON(t *testing.T) {
	out, _, calls, err := runPreview1678(t, reply1678(), append(previewArgs1678(), "--json")...)
	if err != nil || len(calls) != 1 {
		t.Fatalf("%v calls%v %s", err, calls, out)
	}
	if calls[0]["operationName"] != "PreviewAgentSchedule" || !strings.Contains(calls[0]["query"].(string), "query PreviewAgentSchedule") {
		t.Fatal(calls)
	}
	wantInput := map[string]any{"appRef": "hrn:app:acme.com:ops", "entryNodeUrn": "hrn:node:acme.com::ops::entry", "cron": "0 6 * * *", "enabled": true}
	if !reflect.DeepEqual(calls[0]["variables"].(map[string]any)["input"], wantInput) {
		t.Fatal(calls)
	}
	var got map[string]any
	var summary map[string]any
	if err := json.Unmarshal([]byte(summary1678), &summary); err != nil {
		t.Fatal(err)
	}
	want := map[string]any{"outcome": "ADOPTABLE", "hash": headHash1678, "reason": nil, "summary": summary}
	if json.Unmarshal([]byte(out), &got) != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("%s", out)
	}
}
func TestSchedulePreview1678Options(t *testing.T) {
	args := []string{"--app", "acme.com:ops", "--entry", "hrn:node:acme.com:ops:entry", "--run-at", "2030-01-01T00:00:00Z", "--tz", "Europe/Berlin", "--agent", "agent1", "--ai-config", "named", "--as-self", "--disabled", "--arg", "x=2", "--budget-tokens", "100", "--budget-actions", "3"}
	_, _, calls, err := runPreview1678(t, reply1678(), args...)
	if err != nil || len(calls) != 1 {
		t.Fatal(err, calls)
	}
	want := map[string]any{"appRef": "hrn:app:acme.com:ops", "entryNodeUrn": "hrn:node:acme.com:ops:entry", "runAt": "2030-01-01T00:00:00Z", "timezone": "Europe/Berlin", "agentRef": "agent1", "aiConfigName": "named", "runAsSelf": true, "enabled": false, "eventData": map[string]any{"x": float64(2)}, "budgetTokens": float64(100), "budgetActions": float64(3)}
	if !reflect.DeepEqual(calls[0]["variables"].(map[string]any)["input"], want) {
		t.Fatal(calls)
	}
}
func TestSchedulePreview1678Outcomes(t *testing.T) {
	for _, asJSON := range []bool{false, true} {
		for _, reason := range []string{"MEMORY_UNREADABLE", "OTHER"} {
			reply := `{"data":{"previewAgentSchedule":{"outcome":"CANNOT_BE_ADOPTED","reason":"` + reason + `","hash":null,"summary":null}}}`
			args := previewArgs1678()
			if asJSON {
				args = append(args, "--json")
			}
			out, stderr, calls, err := runPreview1678(t, reply, args...)
			if exitCodeFor(err) != exitcode.Conflict || len(calls) != 1 || stderr != "" {
				t.Fatalf("%v %q %v", err, stderr, calls)
			}
			if asJSON {
				var got map[string]any
				if err := json.Unmarshal([]byte(out), &got); err != nil {
					t.Fatal(err)
				}
				want := map[string]any{"outcome": "CANNOT_BE_ADOPTED", "reason": reason, "hash": nil, "summary": nil}
				if !reflect.DeepEqual(got, want) {
					t.Fatal(out)
				}
			} else if out != "CANNOT_BE_ADOPTED: "+reason+"\n" {
				t.Fatal(out)
			}
		}
	}
	out, _, _, err := runPreview1678(t, reply1678(), previewArgs1678()...)
	if err != nil {
		t.Fatal(err)
	}
	for _, fact := range []string{"ADOPTABLE", headHash1678, "Weekly", "App memories", "Run memory", "No outside access", "search", "Budget 100", `Runs on the schedule "0 6 * * *" (UTC)`, "Main", "hrn:node:acme.com:ops:entry"} {
		if !strings.Contains(out, fact) {
			t.Fatalf("missing%q: %s", fact, out)
		}
	}
}
func TestSchedulePreview1678NoTrafficUsage(t *testing.T) {
	cases := [][]string{{"--name", "n"}, {"--scope", "x"}, {"--policy", "{}"}, {"--run-at", "2030-01-01T00:00:00Z"}, {"--cron", ""}, {"--budget-tokens", "0"}, {"--arg", "bad"}}
	for _, extra := range cases {
		_, _, calls, err := runPreview1678(t, reply1678(), append(previewArgs1678(), extra...)...)
		if exitCodeFor(err) != 2 || len(calls) != 0 {
			t.Fatalf("%v %v %v", extra, err, calls)
		}
	}
	_, _, calls, err := runPreview1678(t, reply1678(), "--app", "acme.com:ops", "--entry", "acme.com::ops::entry")
	if exitCodeFor(err) != 2 || len(calls) != 0 {
		t.Fatal(err, calls)
	}
}
func TestSchedulePreview1678RefusalsAndMalformed(t *testing.T) {
	for code, want := range map[string]int{"UNAUTHENTICATED": 3, "NOT_FOUND": 4, "FORBIDDEN": 8, "BAD_USER_INPUT": 2} {
		_, _, calls, err := runPreview1678(t, `{"errors":[{"message":"refused","extensions":{"code":"`+code+`"}}]}`, previewArgs1678()...)
		if exitCodeFor(err) != want || len(calls) != 1 {
			t.Fatalf("%s %v %v", code, err, calls)
		}
	}
	for _, reply := range []string{`{"data":{"previewAgentSchedule":null}}`, `{"data":{"previewAgentSchedule":{}}}`, `{"data":{"previewAgentSchedule":{"outcome":"ADOPTABLE","hash":"x","summary":null}}}`, `{"errors":[{"message":"Cannot query field previewAgentSchedule","extensions":{"code":"GRAPHQL_VALIDATION_FAILED"}}]}`, `{"data":{"previewAgentSchedule":{"outcome":"CANNOT_BE_ADOPTED","reason":"UNKNOWN","hash":null,"summary":null}}}`} {
		out, _, calls, err := runPreview1678(t, reply, previewArgs1678()...)
		if err == nil || len(calls) != 1 || strings.Contains(out, "ADOPTABLE") {
			t.Fatal(err, calls, out)
		}
	}
}
func TestSchedulePreview1678NoOracleAndSafe(t *testing.T) {
	reply := `{"data":{"previewAgentSchedule":{"outcome":"CANNOT_BE_ADOPTED","reason":"MEMORY_UNREADABLE","hash":null,"summary":null}}}`
	a := previewArgs1678()
	b := previewArgs1678()
	b[3] = "acme.com::ops::unreadable"
	outA, errA, _, _ := runPreview1678(t, reply, a...)
	outB, errB, _, _ := runPreview1678(t, reply, b...)
	if outA != outB || errA != errB {
		t.Fatal(outA, outB)
	}
	safeReply := strings.ReplaceAll(reply1678(), "Weekly", "Weekly\\u001b[31m")
	out, _, _, err := runPreview1678(t, safeReply, previewArgs1678()...)
	if err != nil || strings.Contains(out, "\x1b") {
		t.Fatal(err, out)
	}
}
func TestSchedulePreview1678Help(t *testing.T) {
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"schedule", "preview", "--help"})
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	for _, s := range []string{"snapshot", "no reservation", "no authority to create", "preview does not validate policy or scope; create may refuse them even after an ADOPTABLE preview", "human authorization", "server decides"} {
		if !strings.Contains(out.String(), s) {
			t.Fatalf("missing %q: %s", s, out.String())
		}
	}
}

func TestSchedulePreview1678TransportAndEmptyLists(t *testing.T) {
	for _, reply := range []string{"HTTP502", "DROP"} {
		for _, asJSON := range []bool{false, true} {
			args := previewArgs1678()
			if asJSON {
				args = append(args, "--json")
			}
			_, _, calls, err := runPreview1678(t, reply, args...)
			if exitCodeFor(err) != 7 || len(calls) != 1 {
				t.Fatalf("%s %v %v", reply, err, calls)
			}
		}
	}
	reply := reply1678()
	for _, field := range []string{"reads", "writes", "cannotDo", "tools", "limits", "flows"} {
		var data map[string]any
		if err := json.Unmarshal([]byte(reply), &data); err != nil {
			t.Fatal(err)
		}
		v := data["data"].(map[string]any)["previewAgentSchedule"].(map[string]any)
		v["summary"].(map[string]any)[field] = []any{}
		b, _ := json.Marshal(data)
		out, _, _, err := runPreview1678(t, string(b), append(previewArgs1678(), "--json")...)
		if err != nil {
			t.Fatal(err)
		}
		var got map[string]any
		if err := json.Unmarshal([]byte(out), &got); err != nil {
			t.Fatal(err)
		}
		list := got["summary"].(map[string]any)[field]
		if !reflect.DeepEqual(list, []any{}) {
			t.Fatal(out)
		}
	}
}
func TestSchedulePreview1678WithheldNeverPrintsIdentity(t *testing.T) {
	reply := `{"data":{"previewAgentSchedule":{"outcome":"CANNOT_BE_ADOPTED","reason":"MEMORY_UNREADABLE","hash":"SECRET","summary":{"name":"SECRET"}}}}`
	for _, asJSON := range []bool{false, true} {
		args := previewArgs1678()
		if asJSON {
			args = append(args, "--json")
		}
		out, stderr, _, err := runPreview1678(t, reply, args...)
		if exitCodeFor(err) != 7 || strings.Contains(out+stderr, "SECRET") {
			t.Fatal(err, out, stderr)
		}
	}
}

// A typed exit-5 refusal is also an error, so err != nil cannot pin rejection
// of an unknown reason. Neither output mode may echo a provider-made identity.
func TestSchedulePreview1678UnknownReasonIsMalformed(t *testing.T) {
	const secret = "acme-secret-memory"
	reply := `{"data":{"previewAgentSchedule":{"outcome":"CANNOT_BE_ADOPTED","reason":"` + secret + `","hash":null,"summary":null}}}`
	for _, asJSON := range []bool{false, true} {
		args := previewArgs1678()
		if asJSON {
			args = append(args, "--json")
		}
		out, stderr, calls, err := runPreview1678(t, reply, args...)
		if exitCodeFor(err) != 7 || len(calls) != 1 || strings.Contains(out+stderr, secret) || strings.Contains(out+stderr, "ADOPTABLE") {
			t.Fatalf("json=%v exit=%d calls=%d out=%q stderr=%q", asJSON, exitCodeFor(err), len(calls), out, stderr)
		}
	}
}

func TestSchedulePreview1678EveryHumanCellIsSafe(t *testing.T) {
	// Distinct markers pin each field's presence and keep newline flattening
	// observable without forbidding the renderer's own layout newlines.
	cell := func(label string) string { return "<" + label + ">\x1b\nTAIL" }
	labels := []string{"name", "schedule", "reads", "writes", "cannotDo", "tools", "limits", "flow-name", "flow-urn", "flow-tools", "flow-writes"}
	summary := map[string]any{"name": cell("name"), "schedule": cell("schedule")}
	for _, field := range []string{"reads", "writes", "cannotDo", "tools", "limits"} {
		summary[field] = []string{cell(field)}
	}
	summary["flows"] = []any{map[string]any{"name": cell("flow-name"), "urn": cell("flow-urn"), "tools": []string{cell("flow-tools")}, "writes": []string{cell("flow-writes")}}}
	reply, err := json.Marshal(map[string]any{"data": map[string]any{"previewAgentSchedule": map[string]any{"outcome": "ADOPTABLE", "hash": headHash1678, "reason": nil, "summary": summary}}})
	if err != nil {
		t.Fatal(err)
	}
	out, _, _, err := runPreview1678(t, string(reply), previewArgs1678()...)
	if err != nil || strings.ContainsRune(out, '\x1b') {
		t.Fatal(err, out)
	}
	for _, label := range labels {
		if !strings.Contains(out, "<"+label+">  TAIL") {
			t.Fatalf("cell %s missing or newline not flattened: %q", label, out)
		}
	}
	out, _, _, err = runPreview1678(t, string(reply), append(previewArgs1678(), "--json")...)
	if err != nil {
		t.Fatal(err)
	}
	var dto map[string]any
	if err := json.Unmarshal([]byte(out), &dto); err != nil {
		t.Fatal(err)
	}
	// JSON is a separate contract: retain every original server value.
	var wire map[string]any
	if err := json.Unmarshal(reply, &wire); err != nil {
		t.Fatal(err)
	}
	want := wire["data"].(map[string]any)["previewAgentSchedule"].(map[string]any)["summary"]
	if !reflect.DeepEqual(dto["summary"], want) {
		t.Fatal(out)
	}
}

// Every row changes just one field in an otherwise valid typed result, so
// neighbouring guards cannot mask a missing check.
func TestSchedulePreview1678SingleMalformedField(t *testing.T) {
	cases := []struct {
		name   string
		change func(map[string]any)
	}{
		{"withheld-hash", func(v map[string]any) { v["outcome"], v["reason"], v["summary"] = "CANNOT_BE_ADOPTED", "OTHER", nil }},
		{"withheld-summary", func(v map[string]any) { v["outcome"], v["reason"], v["hash"] = "CANNOT_BE_ADOPTED", "OTHER", nil }},
		{"hash-shape", func(v map[string]any) { v["hash"] = "x" }},
		{"adoptable-reason", func(v map[string]any) { v["reason"] = "OTHER" }},
		{"summary-null", func(v map[string]any) { v["summary"] = nil }},
		{"schedule-null", func(v map[string]any) { v["summary"].(map[string]any)["schedule"] = nil }},
		{"schedule-empty", func(v map[string]any) { v["summary"].(map[string]any)["schedule"] = "" }},
		{"flow-name-empty", func(v map[string]any) {
			v["summary"].(map[string]any)["flows"].([]any)[0].(map[string]any)["name"] = ""
		}},
		{"flow-urn-empty", func(v map[string]any) { v["summary"].(map[string]any)["flows"].([]any)[0].(map[string]any)["urn"] = "" }},
		{"flow-tools-null", func(v map[string]any) {
			v["summary"].(map[string]any)["flows"].([]any)[0].(map[string]any)["tools"] = nil
		}},
		{"flow-writes-null", func(v map[string]any) {
			v["summary"].(map[string]any)["flows"].([]any)[0].(map[string]any)["writes"] = nil
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var wire map[string]any
			if err := json.Unmarshal([]byte(reply1678()), &wire); err != nil {
				t.Fatal(err)
			}
			tc.change(wire["data"].(map[string]any)["previewAgentSchedule"].(map[string]any))
			reply, err := json.Marshal(wire)
			if err != nil {
				t.Fatal(err)
			}
			for _, asJSON := range []bool{false, true} {
				args := previewArgs1678()
				if asJSON {
					args = append(args, "--json")
				}
				out, stderr, calls, err := runPreview1678(t, string(reply), args...)
				if exitCodeFor(err) != 7 || len(calls) != 1 || !strings.Contains(out+stderr, "malformed") {
					t.Fatalf("json=%v exit=%d calls=%d out=%q stderr=%q", asJSON, exitCodeFor(err), len(calls), out, stderr)
				}
			}
		})
	}
}

func TestSchedulePreview1678RefusalTextAndServerCode(t *testing.T) {
	const message = "refusal\x1b\nTAIL"
	reply, err := json.Marshal(map[string]any{"errors": []any{map[string]any{"message": message, "extensions": map[string]any{"code": "FORBIDDEN"}}}})
	if err != nil {
		t.Fatal(err)
	}
	_, stderr, _, err := runPreview1678(t, string(reply), previewArgs1678()...)
	if exitCodeFor(err) != 8 || strings.ContainsRune(stderr, '\x1b') || !strings.Contains(stderr, "refusal  TAIL") {
		t.Fatal(err, stderr)
	}
	out, _, _, err := runPreview1678(t, string(reply), append(previewArgs1678(), "--json")...)
	if exitCodeFor(err) != 8 {
		t.Fatal(err)
	}
	var envelope struct {
		Error struct {
			Code       int    `json:"code"`
			ServerCode string `json:"serverCode"`
			Message    string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal([]byte(out), &envelope); err != nil {
		t.Fatal(err)
	}
	if envelope.Error.Code != 8 || envelope.Error.ServerCode != "FORBIDDEN" || !strings.Contains(envelope.Error.Message, message) {
		t.Fatal(out)
	}
}
