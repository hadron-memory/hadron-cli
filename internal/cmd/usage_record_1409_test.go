package cmd

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"testing"
)

// usageRecordRun drives `hadron usage record` against a fake server that
// counts every mutation request and keeps its variables (cli#1409).
func usageRecordRun(t *testing.T, response string, args ...string) (stdout, stderr string, code int, vars []map[string]any) {
	t.Helper()
	var mu sync.Mutex
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && r.URL.Path == "/capabilities" {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"capabilities":[]}`))
			return
		}
		var body struct {
			OperationName string          `json:"operationName"`
			Variables     json.RawMessage `json:"variables"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		if body.OperationName != "RecordUsage" {
			t.Errorf("unexpected operation %q", body.OperationName)
		}
		var v map[string]any
		_ = json.Unmarshal(body.Variables, &v)
		mu.Lock()
		vars = append(vars, v)
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(response))
	}))
	t.Cleanup(srv.Close)
	f, out := testFactory(t)
	errOut := &strings.Builder{}
	f.IOStreams.ErrOut = errOut
	root := NewRootCmd(f)
	root.SetArgs(append(args, "--server", srv.URL))
	if err := root.Execute(); err != nil {
		code = renderError(f, err)
	}
	return out.String(), errOut.String(), code, vars
}

const usageRecordOK = `{"data":{"recordUsage":true}}`

func TestUsageRecordAllFlagsSendsExactVariables(t *testing.T) {
	out, errOut, code, vars := usageRecordRun(t, usageRecordOK, "usage", "record", "--type", "read",
		"--node-loc", "notes:a", "--session", "s1", "--model", "m1",
		"--tokens-in", "-5", "--tokens-out", "0", "--action-args", `{"k":["v",1],"t":"<b>\n"}`)
	if code != 0 {
		t.Fatalf("code %d: %s %s", code, out, errOut)
	}
	if len(vars) != 1 {
		t.Fatalf("want exactly one request, got %d", len(vars))
	}
	ev, _ := vars[0]["event"].(map[string]any)
	want := `{"actionArgs":{"k":["v",1],"t":"<b>\n"},"model":"m1","nodeLoc":"notes:a","sessionId":"s1","tokensIn":-5,"tokensOut":0,"type":"read"}`
	var wantEv map[string]any
	if err := json.Unmarshal([]byte(want), &wantEv); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(ev, wantEv) {
		t.Fatalf("event = %v\nwant    %v", ev, wantEv)
	}
}

func TestUsageRecordTypeOnlyOmitsEverythingElse(t *testing.T) {
	_, _, code, vars := usageRecordRun(t, usageRecordOK, "usage", "record", "--type", "write")
	if code != 0 || len(vars) != 1 {
		t.Fatalf("code %d requests %d", code, len(vars))
	}
	ev, _ := vars[0]["event"].(map[string]any)
	got, _ := json.Marshal(ev)
	if string(got) != `{"type":"write"}` {
		t.Fatalf("event = %s", got)
	}
}

func TestUsageRecordExplicitZeroIsNotOmitted(t *testing.T) {
	_, _, _, vars := usageRecordRun(t, usageRecordOK, "usage", "record", "--type", "t", "--tokens-in", "0")
	if len(vars) != 1 {
		t.Fatalf("want one request, got %d", len(vars))
	}
	ev, _ := vars[0]["event"].(map[string]any)
	if v, ok := ev["tokensIn"]; !ok || v != float64(0) {
		t.Fatalf("explicit 0 must be sent as 0: %v", ev)
	}
	if _, ok := ev["tokensOut"]; ok {
		t.Fatalf("omitted --tokens-out must be absent: %v", ev)
	}
}

func TestUsageRecordSignedIntBoundariesAreForwarded(t *testing.T) {
	_, _, code, vars := usageRecordRun(t, usageRecordOK, "usage", "record", "--type", "t",
		"--tokens-in", "-2147483648", "--tokens-out", "2147483647")
	if code != 0 || len(vars) != 1 {
		t.Fatalf("code %d requests %d", code, len(vars))
	}
	ev, _ := vars[0]["event"].(map[string]any)
	if ev["tokensIn"] != float64(-2147483648) || ev["tokensOut"] != float64(2147483647) {
		t.Fatalf("event = %v", ev)
	}
}

func TestUsageRecordClientParsingFailuresExit2WithoutARequest(t *testing.T) {
	cases := map[string][]string{
		"invalid json":      {"--type", "t", "--action-args", "{nope"},
		"non-integer":       {"--type", "t", "--tokens-in", "1.5"},
		"word":              {"--type", "t", "--tokens-out", "many"},
		"overflow":          {"--type", "t", "--tokens-in", "2147483648"},
		"underflow":         {"--type", "t", "--tokens-out", "-2147483649"},
		"missing type":      {"--tokens-in", "1"},
		"empty action args": {"--type", "t", "--action-args", ""},
	}
	for name, args := range cases {
		t.Run(name, func(t *testing.T) {
			_, errOut, code, vars := usageRecordRun(t, usageRecordOK, append([]string{"usage", "record"}, args...)...)
			if code != 2 {
				t.Fatalf("exit %d, want 2 (%s)", code, errOut)
			}
			if len(vars) != 0 {
				t.Fatalf("a request was sent: %v", vars)
			}
		})
	}
}

func TestUsageRecordForbiddenPassesThrough(t *testing.T) {
	forbidden := `{"errors":[{"message":"Forbidden","extensions":{"code":"FORBIDDEN"}}]}`
	out, errOut, code, vars := usageRecordRun(t, forbidden, "usage", "record", "--type", "t")
	if code != 8 {
		t.Fatalf("exit %d, want 8 (%s %s)", code, out, errOut)
	}
	if len(vars) != 1 {
		t.Fatalf("no retry allowed: %d requests", len(vars))
	}
	if strings.Contains(out, "recorded") {
		t.Fatalf("success wording on a refusal: %q", out)
	}
}

func TestUsageRecordOutputIsNeutralAndIdentical(t *testing.T) {
	// The server answers true whether or not the node or session was
	// attributed, so the CLI cannot, and must not, say which happened.
	attributed, _, _, _ := usageRecordRun(t, usageRecordOK, "usage", "record", "--type", "read", "--node-loc", "notes:a", "--session", "mine")
	unattributed, _, _, _ := usageRecordRun(t, usageRecordOK, "usage", "record", "--type", "read", "--node-loc", "nope:x", "--session", "theirs")
	if attributed != unattributed {
		t.Fatalf("output differs:\n%q\n%q", attributed, unattributed)
	}
	if strings.TrimSpace(attributed) != "Usage event recorded" {
		t.Fatalf("text = %q", attributed)
	}
	for _, bad := range []string{"attribut", "resum", "session", "node", "memory"} {
		if strings.Contains(strings.ToLower(attributed), bad) {
			t.Fatalf("text claims %q: %q", bad, attributed)
		}
	}
	j1, _, _, _ := usageRecordRun(t, usageRecordOK, "usage", "record", "--type", "read", "--node-loc", "notes:a", "--session", "mine", "--json")
	j2, _, _, _ := usageRecordRun(t, usageRecordOK, "usage", "record", "--type", "read", "--node-loc", "nope:x", "--session", "theirs", "--json")
	if j1 != j2 {
		t.Fatalf("json differs:\n%q\n%q", j1, j2)
	}
	var got map[string]any
	if err := json.Unmarshal([]byte(j1), &got); err != nil || len(got) != 1 || got["recorded"] != true {
		t.Fatalf("json = %q (%v)", j1, err)
	}
}

// The help must disclose the server behaviours the CLI refuses to claim per
// call (cor:dmo:080:02, cor:agt:020:13, cor:agt:020:11 rule 3; cli#1409).
func TestUsageRecordHelpDisclosesAttributionAndSessionEffects(t *testing.T) {
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetOut(out)
	root.SetArgs([]string{"usage", "record", "--help"})
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	help := strings.Join(strings.Fields(out.String()), " ")
	for _, want := range []string{
		"is still recorded, but WITHOUT node or memory attribution",
		"a session you do not own is recorded without attribution",
		"With --session, an event on a live session you own counts as that session's activity; an App key's event can resume its own lapsed session, never a dormant one.",
		"An owned ended, expired or idle session takes the record late, without being revived or counted as activity.",
		"does not validate --type",
		"not a closed list",
	} {
		if !strings.Contains(help, want) {
			t.Errorf("help is missing %q", want)
		}
	}
}

// Explicit empty or whitespace strings are valid wire values: the CLI adds no
// string policy, so they are forwarded and stay distinct from omitted
// (Joost LOCAL P2-1 on cli#1411; gate: no client type/loc/session policy).
func TestUsageRecordEmptyAndWhitespaceStringsAreForwarded(t *testing.T) {
	_, _, code, vars := usageRecordRun(t, usageRecordOK, "usage", "record", "--type", "  ",
		"--node-loc", "", "--session", " ", "--model", "")
	if code != 0 || len(vars) != 1 {
		t.Fatalf("code %d requests %d", code, len(vars))
	}
	ev, _ := vars[0]["event"].(map[string]any)
	want := map[string]any{"type": "  ", "nodeLoc": "", "sessionId": " ", "model": ""}
	if !reflect.DeepEqual(ev, want) {
		t.Fatalf("event = %v want %v", ev, want)
	}
	_, _, code, vars = usageRecordRun(t, usageRecordOK, "usage", "record", "--type", "")
	if code != 0 || len(vars) != 1 {
		t.Fatalf("empty --type: code %d requests %d", code, len(vars))
	}
	if _, _, code, _ = usageRecordRun(t, usageRecordOK, "usage", "record"); code != 2 {
		t.Fatalf("a missing --type must still exit 2, got %d", code)
	}
}

// A false answer, a malformed body and a transport failure are never success
// and never retried (Bruno BLIND P3 on cli#1411).
func TestUsageRecordFailureBranchesAreNotSuccessAndSingleRequest(t *testing.T) {
	cases := map[string]string{
		"false":     `{"data":{"recordUsage":false}}`,
		"malformed": `{"data":{"recordUsage":"yes"}}`,
		"null":      `{"data":null}`,
		"server":    `{"errors":[{"message":"boom","extensions":{"code":"INTERNAL_SERVER_ERROR"}}]}`,
	}
	for name, resp := range cases {
		t.Run(name, func(t *testing.T) {
			out, _, code, vars := usageRecordRun(t, resp, "usage", "record", "--type", "t", "--json")
			if code == 0 {
				t.Fatalf("exit 0 on a failed answer: %s", out)
			}
			if strings.Contains(out, "recorded") {
				t.Fatalf("success output on failure: %q", out)
			}
			if len(vars) != 1 {
				t.Fatalf("%d requests, want exactly 1", len(vars))
			}
		})
	}
}
