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
