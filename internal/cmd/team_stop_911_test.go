package cmd

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/exitcode"
)

// #911 slice B1: `team stop list|set|lift` over server #2022/#2166.

const stopSignalJSON = `{"id":"sig1","level":"PAUSE_DISPATCH","scope":"TEAM","targetWorkerId":"wkr1","targetHostId":null,"setAt":"2026-10-23T10:00:00Z","until":"2026-12-01T08:00:00Z","setByUserId":"usr1","setByHostKey":false,"live":true,"endedAt":null,"endKind":null,"endedByUserId":null,"endedByHostKey":false}`
const endedStopSignalJSON = `{"id":"sig2","level":"HALT","scope":"HOST","targetWorkerId":null,"targetHostId":"host1","setAt":"2026-10-22T10:00:00Z","until":null,"setByUserId":null,"setByHostKey":true,"live":false,"endedAt":"2026-10-22T11:00:00Z","endKind":"LIFTED","endedByUserId":"usr1","endedByHostKey":false}`

// The secret-free contract: a server's free text must never reach the output.
const upstreamSentinel = "UPSTREAM-FREE-TEXT-must-not-print"

func stopResponses() map[string]string {
	r := taskResponses()
	r["TeamStopSignalSet"] = `{"data":{"setTeamStopSignal":` + stopSignalJSON + `}}`
	r["TeamStopSignalLift"] = `{"data":{"liftTeamStopSignal":{"changed":true,"signal":` + endedStopSignalJSON + `}}}`
	r["TeamStopSignals"] = `{"data":{"teamStopSignals":{"signals":[` + stopSignalJSON + `],"pageInfo":{"endCursor":null,"hasNextPage":false}}}}`
	return r
}

func stopCalls(calls *[]attnCall, op string) []attnCall {
	out := []attnCall{}
	for _, c := range *calls {
		if c.Op == op {
			out = append(out, c)
		}
	}
	return out
}

func TestTeamStopSet911WireAndDTO(t *testing.T) {
	calls, out, err := runTeamTask(t, stopResponses(), "team", "stop", "set",
		"--scope", "team", "--level", "pause-dispatch", "--worker", "Iris", "--until", "2026-12-01T10:00:00+02:00", "--json")
	if err != nil {
		t.Fatal(err)
	}
	sets := stopCalls(calls, "TeamStopSignalSet")
	if len(sets) != 1 {
		t.Fatalf("set calls %d", len(sets))
	}
	want := map[string]any{"appRef": "capp100000000000000000000", "level": "PAUSE_DISPATCH", "scope": "TEAM", "worker": "Iris", "until": "2026-12-01T08:00:00Z"}
	if got := sets[0].Vars["input"]; !reflect.DeepEqual(got, want) {
		t.Fatalf("input %v, want %v", got, want)
	}
	// A manager's write: the bound worktree's worker session never rides it
	// (a worker's session does not set on its holder's manager right, R113).
	if sets[0].Session != "" {
		t.Fatalf("stop set carried worker session %q", sets[0].Session)
	}
	var got, wantDTO map[string]any
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("%v: %s", err, out)
	}
	_ = json.Unmarshal([]byte(stopSignalJSON), &wantDTO)
	if !reflect.DeepEqual(got, wantDTO) {
		t.Fatalf("DTO %v, want %v", got, wantDTO)
	}
}

func TestTeamStopSet911OmitsAbsentFields(t *testing.T) {
	for _, tc := range []struct {
		args []string
		want string
	}{
		// A host key may omit --host: the server takes its own host.
		{[]string{"--scope", "host", "--level", "halt"}, `{"appRef":"capp100000000000000000000","level":"HALT","scope":"HOST"}`},
		{[]string{"--scope", "HOST", "--level", "HALT", "--host", "host1"}, `{"appRef":"capp100000000000000000000","host":"host1","level":"HALT","scope":"HOST"}`},
		// No --until: until lifted, never an explicit null.
		{[]string{"--scope", "worker", "--level", "pause", "--worker", "wkr1"}, `{"appRef":"capp100000000000000000000","level":"PAUSE","scope":"WORKER","worker":"wkr1"}`},
		{[]string{"--scope", "worker", "--level", "PAUSE_DISPATCH", "--worker", "wkr1", "--until", "2026-12-01T08:00:00.5Z"}, `{"appRef":"capp100000000000000000000","level":"PAUSE_DISPATCH","scope":"WORKER","until":"2026-12-01T08:00:00.5Z","worker":"wkr1"}`},
	} {
		calls, _, err := runTeamTask(t, stopResponses(), append([]string{"team", "stop", "set"}, tc.args...)...)
		if err != nil {
			t.Fatalf("%v: %v", tc.args, err)
		}
		sets := stopCalls(calls, "TeamStopSignalSet")
		if len(sets) != 1 {
			t.Fatalf("%v: set calls %d", tc.args, len(sets))
		}
		var raw struct {
			Input json.RawMessage `json:"input"`
		}
		_ = json.Unmarshal(sets[0].RawVars, &raw)
		var gotM, wantM map[string]any
		_ = json.Unmarshal(raw.Input, &gotM)
		_ = json.Unmarshal([]byte(tc.want), &wantM)
		if !reflect.DeepEqual(gotM, wantM) {
			t.Fatalf("%v: wire input %s, want %s", tc.args, raw.Input, tc.want)
		}
	}
}

func TestTeamStopSet911LocalRefusalsSendNothing(t *testing.T) {
	for _, args := range [][]string{
		{"--level", "halt", "--worker", "w"},                                                      // no --scope
		{"--scope", "team", "--worker", "w"},                                                      // no --level
		{"--scope", "team", "--level", "stop", "--worker", "w"},                                   // bad level
		{"--scope", "org", "--level", "halt", "--worker", "w"},                                    // bad scope
		{"--scope", "worker", "--level", "halt"},                                                  // worker scope, no target
		{"--scope", "team", "--level", "halt"},                                                    // team scope, no head
		{"--scope", "worker", "--level", "halt", "--worker", "w", "--host", "h"},                  // host on a worker scope
		{"--scope", "host", "--level", "halt", "--worker", "w"},                                   // worker on a host scope
		{"--scope", "worker", "--level", "halt", "--worker", "w", "--until", "tomorrow"},          // not a timestamp
		{"--scope", "worker", "--level", "halt", "--worker", "w", "--until", "2026-12-01T10:00:00"}, // no offset
		{"--scope", "worker", "--level", "halt", "--worker", ""},                                  // empty target
		{"--scope", "worker", "--level", "halt", "--worker", "w", "extra"},                        // stray argument
	} {
		calls, out, err := runTeamTask(t, stopResponses(), append([]string{"team", "stop", "set"}, args...)...)
		if code := exitcode.FromError(err); code != exitcode.Usage {
			t.Errorf("%v: exit %d (%v), want %d", args, code, err, exitcode.Usage)
		}
		if n := len(stopCalls(calls, "TeamStopSignalSet")); n != 0 {
			t.Errorf("%v: %d mutation(s) sent after a local refusal", args, n)
		}
		if out != "" {
			t.Errorf("%v: stdout %q", args, out)
		}
	}
}

func TestTeamStopSet911TextReceiptClaimsOnlyStoredState(t *testing.T) {
	_, out, err := runTeamTask(t, stopResponses(), "team", "stop", "set", "--scope", "team", "--level", "pause-dispatch", "--worker", "Iris")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"sig1", "PAUSE_DISPATCH", "TEAM", "wkr1", "2026-12-01T08:00:00Z", "stored", "not delivered"} {
		if !strings.Contains(out, want) {
			t.Errorf("receipt missing %q:\n%s", want, out)
		}
	}
	for _, never := range []string{"workers stopped", "interrupted", "in effect"} {
		if strings.Contains(strings.ToLower(out), never) {
			t.Errorf("receipt claims %q:\n%s", never, out)
		}
	}
}

func TestTeamStopLift911(t *testing.T) {
	calls, out, err := runTeamTask(t, stopResponses(), "team", "stop", "lift", "sig2", "--json")
	if err != nil {
		t.Fatal(err)
	}
	lifts := stopCalls(calls, "TeamStopSignalLift")
	if len(lifts) != 1 || !reflect.DeepEqual(lifts[0].Vars, map[string]any{"appRef": "capp100000000000000000000", "id": "sig2"}) || lifts[0].Session != "" {
		t.Fatalf("lift calls %+v", lifts)
	}
	var got map[string]any
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("%v: %s", err, out)
	}
	var sig map[string]any
	_ = json.Unmarshal([]byte(endedStopSignalJSON), &sig)
	if !reflect.DeepEqual(got, map[string]any{"changed": true, "signal": sig}) {
		t.Fatalf("lift DTO %v", got)
	}

	// changed=false: it had already ended. Never reported as a lift, and
	// never as every overlapping stop ending.
	r := stopResponses()
	r["TeamStopSignalLift"] = `{"data":{"liftTeamStopSignal":{"changed":false,"signal":` + endedStopSignalJSON + `}}}`
	_, out, err = runTeamTask(t, r, "team", "stop", "lift", "sig2")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "already ended") || strings.Contains(out, "Lifted") {
		t.Fatalf("unchanged lift text:\n%s", out)
	}
	if !strings.Contains(out, "other stop signals") {
		t.Fatalf("lift text must say other signals may remain:\n%s", out)
	}
	for _, never := range []string{"resumed", "all workers"} {
		if strings.Contains(strings.ToLower(out), never) {
			t.Fatalf("lift text claims %q:\n%s", never, out)
		}
	}

	for _, args := range [][]string{{}, {"a", "b"}, {""}} {
		calls, _, err := runTeamTask(t, stopResponses(), append([]string{"team", "stop", "lift"}, args...)...)
		if exitcode.FromError(err) != exitcode.Usage || len(stopCalls(calls, "TeamStopSignalLift")) != 0 {
			t.Errorf("lift %v: err %v", args, err)
		}
	}
}

func TestTeamStopList911DrainsPagesAndMapsFilters(t *testing.T) {
	writeTeamBinding(t)
	r := stopResponses()
	page := 0
	srv, calls := attnServerHook(t, r, func(op string) {
		if op != "TeamStopSignals" {
			return
		}
		page++
		if page == 1 {
			r["TeamStopSignals"] = `{"data":{"teamStopSignals":{"signals":[` + stopSignalJSON + `],"pageInfo":{"endCursor":"c1","hasNextPage":true}}}}`
		} else {
			r["TeamStopSignals"] = `{"data":{"teamStopSignals":{"signals":[` + endedStopSignalJSON + `],"pageInfo":{"endCursor":"c2","hasNextPage":false}}}}`
		}
	})
	setTeamBindingServer(t, srv.URL)
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"--server", srv.URL, "team", "stop", "list", "--worker", "Liam", "--include-ended", "--json"})
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	lists := stopCalls(calls, "TeamStopSignals")
	if len(lists) != 2 {
		t.Fatalf("pages %d", len(lists))
	}
	if !reflect.DeepEqual(lists[0].Vars, map[string]any{"appRef": "capp100000000000000000000", "worker": "Liam", "includeEnded": true, "first": float64(200)}) {
		t.Fatalf("page 1 vars %v", lists[0].Vars)
	}
	if lists[1].Vars["after"] != "c1" {
		t.Fatalf("page 2 vars %v", lists[1].Vars)
	}
	var got []map[string]any
	if err := json.Unmarshal([]byte(out.String()), &got); err != nil || len(got) != 2 || got[0]["id"] != "sig1" || got[1]["id"] != "sig2" {
		t.Fatalf("list %v %s", err, out.String())
	}
}

func TestTeamStopList911OmitsUnsetFiltersAndRendersEmpty(t *testing.T) {
	r := stopResponses()
	r["TeamStopSignals"] = `{"data":{"teamStopSignals":{"signals":[],"pageInfo":{"endCursor":null,"hasNextPage":false}}}}`
	calls, out, err := runTeamTask(t, r, "team", "stop", "list", "--json")
	if err != nil {
		t.Fatal(err)
	}
	lists := stopCalls(calls, "TeamStopSignals")
	if len(lists) != 1 || !reflect.DeepEqual(lists[0].Vars, map[string]any{"appRef": "capp100000000000000000000", "first": float64(200)}) {
		t.Fatalf("list vars %+v", lists)
	}
	if strings.TrimSpace(out) != "[]" {
		t.Fatalf("empty list %q", out)
	}
}

func TestTeamStopList911RefusesAStalledCursor(t *testing.T) {
	for _, pageInfo := range []string{`{"endCursor":null,"hasNextPage":true}`, `{"endCursor":"","hasNextPage":true}`} {
		r := stopResponses()
		r["TeamStopSignals"] = `{"data":{"teamStopSignals":{"signals":[` + stopSignalJSON + `],"pageInfo":` + pageInfo + `}}}`
		_, out, err := runTeamTask(t, r, "team", "stop", "list", "--json")
		if exitcode.FromError(err) != exitcode.Error || out != "" {
			t.Fatalf("%s: err %v out %q", pageInfo, err, out)
		}
	}
	// A cursor that repeats the one just sent never progresses.
	writeTeamBinding(t)
	r := stopResponses()
	r["TeamStopSignals"] = `{"data":{"teamStopSignals":{"signals":[` + stopSignalJSON + `],"pageInfo":{"endCursor":"same","hasNextPage":true}}}}`
	srv, calls := attnServer(t, r)
	setTeamBindingServer(t, srv.URL)
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"--server", srv.URL, "team", "stop", "list", "--json"})
	err := root.Execute()
	if exitcode.FromError(err) != exitcode.Error || out.String() != "" || len(stopCalls(calls, "TeamStopSignals")) != 2 {
		t.Fatalf("repeated cursor: err %v out %q calls %d", err, out.String(), len(stopCalls(calls, "TeamStopSignals")))
	}
}

func TestTeamStop911RefusalsUseFixedSentences(t *testing.T) {
	setArgs := []string{"set", "--scope", "worker", "--level", "halt", "--worker", "w"}
	for _, tc := range []struct {
		code string
		args []string
		exit int
		want string
	}{
		{"STOP_SIGNAL_UNAVAILABLE", setArgs, exitcode.Conflict, "nothing was set"},
		{"APP_MANAGER_REQUIRED", setArgs, exitcode.Forbidden, "manages the App"},
		{"HOST_SCOPE_REQUIRED", setArgs, exitcode.Forbidden, "its own host"},
		{"STOP_SIGNAL_SCOPE_FORBIDDEN", setArgs, exitcode.Forbidden, "scope"},
		{"STOP_SIGNAL_NOT_FOUND", setArgs, exitcode.NotFound, "target"},
		{"STOP_SIGNAL_NOT_FOUND", []string{"lift", "sig9"}, exitcode.NotFound, "no stop signal"},
		{"BAD_USER_INPUT", setArgs, exitcode.Usage, "--until"},
		{"APP_NOT_FOUND", []string{"list"}, exitcode.NotFound, "App"},
		{"FORBIDDEN", []string{"list"}, exitcode.Forbidden, "not permitted"},
		{"UNAUTHENTICATED", []string{"lift", "sig9"}, exitcode.AuthRequired, "auth login"},
		{"SOMETHING_NEW", setArgs, exitcode.Error, "refused"},
	} {
		r := stopResponses()
		body := `{"errors":[{"message":"` + upstreamSentinel + `","extensions":{"code":"` + tc.code + `"}}]}`
		for _, op := range []string{"TeamStopSignalSet", "TeamStopSignalLift", "TeamStopSignals"} {
			r[op] = body
		}
		calls, out, err := runTeamTask(t, r, append([]string{"team", "stop"}, tc.args...)...)
		name := tc.code + " " + tc.args[0]
		if got := exitcode.FromError(err); got != tc.exit {
			t.Errorf("%s: exit %d, want %d (%v)", name, got, tc.exit, err)
			continue
		}
		msg := err.Error()
		if !strings.Contains(msg, tc.want) || strings.Contains(msg, upstreamSentinel) || out != "" {
			t.Errorf("%s: message %q out %q", name, msg, out)
		}
		if tc.code != "SOMETHING_NEW" && !strings.HasPrefix(msg, tc.code+": ") {
			t.Errorf("%s: message must lead with its code: %q", name, msg)
		}
		if tc.code == "SOMETHING_NEW" && strings.Contains(msg, tc.code) {
			t.Errorf("unknown code reflected: %q", msg)
		}
		for _, op := range []string{"TeamStopSignalSet", "TeamStopSignalLift"} {
			if n := len(stopCalls(calls, op)); n > 1 {
				t.Errorf("%s: %s sent %d times", name, op, n)
			}
		}
	}
}

// A lost answer is not a refusal: exit 7, outcome unknown, sent exactly once.
func TestTeamStop911AmbiguousWriteIsSentOnce(t *testing.T) {
	for _, args := range [][]string{{"set", "--scope", "worker", "--level", "halt", "--worker", "w"}, {"lift", "sig1"}} {
		writeTeamBinding(t)
		writes := 0
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			var body struct {
				OperationName string `json:"operationName"`
			}
			_ = json.NewDecoder(r.Body).Decode(&body)
			switch body.OperationName {
			case "TeamStopSignalSet", "TeamStopSignalLift":
				writes++
				http.Error(w, "bad gateway", http.StatusBadGateway)
			default:
				resp, _ := stopResponses()[body.OperationName]
				if resp == "" {
					resp, _ = unstubbedDefault(body.OperationName)
				}
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(resp))
			}
		}))
		t.Cleanup(srv.Close)
		setTeamBindingServer(t, srv.URL)
		f, out := testFactory(t)
		root := NewRootCmd(f)
		root.SetArgs(append([]string{"--server", srv.URL, "team", "stop"}, args...))
		err := root.Execute()
		if exitcode.FromError(err) != exitcode.Unavailable || writes != 1 || out.String() != "" {
			t.Fatalf("%v: exit %d writes %d out %q", args, exitcode.FromError(err), writes, out.String())
		}
		if !strings.Contains(err.Error(), "team stop list") {
			t.Fatalf("%v: ambiguous outcome must point at a read: %v", args, err)
		}
	}
}

func TestTeamStop911HelpKeepsTheNoUseHoldAndDarkNotices(t *testing.T) {
	for _, argv := range [][]string{{"team", "stop", "--help"}, {"team", "stop", "set", "--help"}, {"team", "stop", "lift", "--help"}} {
		f, out := testFactory(t)
		root := NewRootCmd(f)
		root.SetArgs(argv)
		if err := root.Execute(); err != nil {
			t.Fatal(err)
		}
		help := out.String()
		for _, want := range []string{"STOP_SIGNAL_UNAVAILABLE", "hadron-server#2169", "not delivered"} {
			if !strings.Contains(help, want) {
				t.Errorf("%v help missing %q", argv, want)
			}
		}
	}
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"team", "stop", "list", "--help"})
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "covering that worker now") {
		t.Errorf("list --worker must say coverage, not targeting:\n%s", out.String())
	}
}
