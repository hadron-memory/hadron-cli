package team

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/vektah/gqlparser/v2"
	"github.com/vektah/gqlparser/v2/ast"

	"github.com/hadron-memory/hadron-cli/internal/api"
	"github.com/hadron-memory/hadron-cli/internal/hostpoller"
	"github.com/hadron-memory/hadron-cli/internal/hostrelaunch"
)

// hadron-cli#2816: what the host key asks for when a C5 freshness check
// reads ONE worker (hadron-server#4693's `worker` selector on
// hostDesiredState). The read names its worker, and only the host key drives
// it (cor:agt:070:04: no session header, whoever the caller is).
//
// Its three outcomes stay apart (cor:agt:020:03, cor:agt:020:08):
//
//   - a row: the worker's fresh state;
//   - no row: the selector's own answer, returned as an empty list — this
//     host key is not shown the worker as desired on this host;
//   - any failure: an error. A failed read is never an empty success (item 5).
//
// An older server has no selector and refuses the document before executing
// it. Only that refusal, and only when the host-wide read then succeeds, sends
// the next reads to the host-wide list (the worker's row kept out of it
// locally); the refusal is remembered and looked at again every
// refusedReprobeEvery, so an upgrade is noticed without a restart.

const (
	targetedOp2816 = "HostRelaunchWorker"
	wideOp2816     = "HostRelaunchSnapshot"
	reportOp2816   = "HealthSweepSetTerminals"

	// row2816 is the worker "w" with its session "still-open".
	row2816 = `{"workerId":"w","family":"claude","reason":"HOLDS_TASK","desired":"RUNNING","openSessionId":"still-open","worktrees":["repo/w"]}`
	// otherRow2816 is another worker of the same host.
	otherRow2816 = `{"workerId":"other","family":"claude","reason":"LONG_LIVED","desired":"RUNNING","worktrees":["repo/o"]}`
)

// wireStep2816 is one scripted answer to the host server's next request. op is
// the operation the step is written for; a request of another operation is
// reported, and still answered, so the test sees what was done with it.
type wireStep2816 struct {
	op     string
	status int // 0 = 200
	body   string
}

// wireCall2816 is one request the host server sent.
type wireCall2816 struct {
	op      string
	query   string
	session string
	vars    map[string]any
}

// wire2816 answers the host server from a script, in order, and records every
// request. A request beyond the script is reported.
type wire2816 struct {
	mu     sync.Mutex
	calls  []wireCall2816
	steps  []wireStep2816
	url    string
	source *hostServer
}

func newWire2816(t *testing.T, steps ...wireStep2816) *wire2816 {
	t.Helper()
	w := &wire2816{steps: steps}
	server := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		var req struct {
			OperationName string
			Query         string
			Variables     map[string]any
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Errorf("the host server's request does not parse: %v", err)
			http.Error(rw, "bad request", http.StatusBadRequest)
			return
		}
		w.mu.Lock()
		w.calls = append(w.calls, wireCall2816{op: req.OperationName, query: req.Query, session: r.Header.Get(api.SessionHeader), vars: req.Variables})
		n := len(w.calls)
		w.mu.Unlock()
		if n > len(w.steps) {
			t.Errorf("request %d (%s) has no scripted answer: %d were written", n, req.OperationName, len(w.steps))
			http.Error(rw, "unscripted", http.StatusInternalServerError)
			return
		}
		step := w.steps[n-1]
		if step.op != "" && step.op != req.OperationName {
			t.Errorf("request %d is %s; this step answers %s", n, req.OperationName, step.op)
		}
		rw.Header().Set("Content-Type", "application/json")
		if step.status != 0 {
			rw.WriteHeader(step.status)
		}
		_, _ = rw.Write([]byte(step.body))
	}))
	t.Cleanup(server.Close)
	client, err := api.NewClient(server.URL, "private-host-key", server.Client())
	if err != nil {
		t.Fatal(err)
	}
	w.url = server.URL
	w.source = &hostServer{client: client, app: "app-ref", serverURL: server.URL}
	return w
}

// asked fails unless the requests since request `from` are exactly these
// operations, in order.
func (w *wire2816) asked(t *testing.T, from int, ops ...string) {
	t.Helper()
	w.mu.Lock()
	defer w.mu.Unlock()
	var got []string
	for _, call := range w.calls[from:] {
		got = append(got, call.op)
	}
	if strings.Join(got, ",") != strings.Join(ops, ",") {
		t.Fatalf("requests=%v, want %v", got, ops)
	}
}

// document2816 is a request's GraphQL document without its whitespace.
func document2816(query string) string { return strings.Join(strings.Fields(query), "") }

// desired2816 is a successful answer for host "h1" whose workers are the rows.
func desired2816(rows ...string) string {
	return `{"data":{"myApp":{"id":"app-id"},"hostDesiredState":{"hostId":"h1","sweepMode":"APPLY","workers":[` + strings.Join(rows, ",") + `]}}}`
}

// refused2816 is an older server's answer to the targeted document: refused
// before execution, because hostDesiredState has no `worker` argument there.
func refused2816() wireStep2816 {
	return wireStep2816{
		op:     targetedOp2816,
		status: http.StatusBadRequest,
		body:   `{"errors":[{"message":"Unknown argument \"worker\" on field \"Query.hostDesiredState\".","extensions":{"code":"GRAPHQL_VALIDATION_FAILED"}}]}`,
	}
}

// validation2816 is a refusal of another kind, to tell the selector's refusal apart from.
func validation2816(errorsJSON string) wireStep2816 {
	return wireStep2816{op: targetedOp2816, status: http.StatusBadRequest, body: `{"errors":[` + errorsJSON + `]}`}
}

func TestRelaunchWorkerReadsOneWorkerWithTheHostKey2816(t *testing.T) {
	wantRow := hostpoller.RelaunchWorker{WorkerID: "w", Family: "claude", Desired: "RUNNING", Reason: "HOLDS_TASK", SessionID: "still-open", Worktrees: []string{"repo/w"}}
	// The caller drives a session; the host key's read carries none of it.
	read := func(t *testing.T, w *wire2816, host, worker string) (hostpoller.RelaunchSnapshot, error) {
		t.Helper()
		return w.source.RelaunchWorker(api.WithSession(context.Background(), "a-caller-session"), host, worker)
	}

	t.Run("the worker's row is its fresh state", func(t *testing.T) {
		w := newWire2816(t, wireStep2816{op: targetedOp2816, body: desired2816(row2816)})
		got, err := read(t, w, "h1", "w")
		if err != nil {
			t.Fatal(err)
		}
		want := hostpoller.RelaunchSnapshot{AppID: "app-id", HostID: "h1", Server: w.url, SweepMode: "APPLY", Workers: []hostpoller.RelaunchWorker{wantRow}}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("read %+v, want %+v", got, want)
		}
		w.asked(t, 0, targetedOp2816)
		call := w.calls[0]
		if want := map[string]any{"appRef": "app-ref", "hostId": "h1", "worker": "w"}; !reflect.DeepEqual(call.vars, want) {
			t.Errorf("variables=%v, want %v", call.vars, want)
		}
		if call.session != "" {
			t.Errorf("the read carried session %q: the host key drives no session", call.session)
		}
		// The selector is an argument of the document, not a variable's value
		// in some other read: an older server refuses the document itself.
		doc := document2816(call.query)
		if !strings.HasPrefix(doc, "query"+targetedOp2816+"(") || !strings.Contains(doc, "hostDesiredState(appRef:$appRef,hostId:$hostId,worker:$worker)") {
			t.Errorf("the document does not name the worker selector: %q", call.query)
		}
	})

	t.Run("no row is the answer, not an error", func(t *testing.T) {
		w := newWire2816(t, wireStep2816{op: targetedOp2816, body: desired2816()})
		got, err := read(t, w, "h1", "w")
		if err != nil {
			t.Fatalf("the selector's empty answer came back as a failure: %v", err)
		}
		if got.Workers == nil || len(got.Workers) != 0 {
			t.Fatalf("workers=%v, want an empty list", got.Workers)
		}
		if got.AppID != "app-id" || got.HostID != "h1" || got.Server != w.url || got.SweepMode != "APPLY" {
			t.Fatalf("an empty answer lost the identity it is an answer about: %+v", got)
		}
		w.asked(t, 0, targetedOp2816)
	})

	t.Run("a failed read is a failure", func(t *testing.T) {
		const selector = `{"message":"Unknown argument \"worker\" on field \"Query.hostDesiredState\".","extensions":{"code":"GRAPHQL_VALIDATION_FAILED"}}`
		for _, c := range []struct {
			name string
			step wireStep2816
			want string // in the error; "" = nothing is asserted about its text
		}{
			{"the host is unknown", wireStep2816{op: targetedOp2816, body: `{"errors":[{"message":"no such host","extensions":{"code":"HOST_NOT_FOUND"}}]}`}, "HOST_NOT_FOUND"},
			{"the transport failed", wireStep2816{op: targetedOp2816, status: 503, body: `unavailable`}, "503"},
			{"no App", wireStep2816{op: targetedOp2816, body: `{"data":{"myApp":null,"hostDesiredState":{"hostId":"h1","sweepMode":"APPLY","workers":[]}}}`}, ""},
			{"no desired state", wireStep2816{op: targetedOp2816, body: `{"data":{"myApp":{"id":"app-id"},"hostDesiredState":null}}`}, ""},
			{"another host's answer", wireStep2816{op: targetedOp2816, body: `{"data":{"myApp":{"id":"app-id"},"hostDesiredState":{"hostId":"h2","sweepMode":"APPLY","workers":[` + row2816 + `]}}}`}, ""},
			{"another worker's row", wireStep2816{op: targetedOp2816, body: desired2816(otherRow2816)}, ""},
			{"the worker twice", wireStep2816{op: targetedOp2816, body: desired2816(row2816, row2816)}, ""},
			{"a null row", wireStep2816{op: targetedOp2816, body: desired2816("null")}, ""},
			{"a row without an id", wireStep2816{op: targetedOp2816, body: desired2816(`{"family":"claude"}`)}, ""},
			{"the selector's refusal is mixed with a denial", validation2816(selector + `,{"message":"denied","extensions":{"code":"FORBIDDEN"}}`), ""},
			{"another argument is refused", validation2816(`{"message":"Unknown argument \"sweepMode\" on field \"Query.hostDesiredState\".","extensions":{"code":"GRAPHQL_VALIDATION_FAILED"}}`), ""},
		} {
			t.Run(c.name, func(t *testing.T) {
				w := newWire2816(t, c.step)
				got, err := read(t, w, "h1", "w")
				if err == nil {
					t.Fatalf("the failed read came back as a snapshot: %+v", got)
				}
				if c.want != "" && !strings.Contains(err.Error(), c.want) {
					t.Errorf("the error lost %q: %v", c.want, err)
				}
				if c.name == "another worker's row" && strings.Contains(err.Error(), "other") {
					t.Errorf("the local error names a worker: %v", err)
				}
				// One request: no failure sends the read to the host-wide list.
				w.asked(t, 0, targetedOp2816)
			})
		}
	})

	t.Run("no worker to name", func(t *testing.T) {
		for _, worker := range []string{"", " ", "\tw", "w\n"} {
			w := newWire2816(t)
			if got, err := read(t, w, "h1", worker); err == nil {
				t.Errorf("worker %q read %+v; a blank or padded name is not a worker", worker, got)
			}
			if len(w.calls) != 0 {
				t.Errorf("worker %q sent %d requests", worker, len(w.calls))
			}
		}
	})

	t.Run("no identity to read with", func(t *testing.T) {
		for _, c := range []struct {
			name, serverURL, host string
		}{
			{"no server URL", "", "h1"},
			{"no host", "https://private.invalid", ""},
		} {
			w := newWire2816(t)
			s := &hostServer{client: w.source.client, app: "app-ref", serverURL: c.serverURL}
			if got, err := s.RelaunchWorker(context.Background(), c.host, "w"); err == nil {
				t.Errorf("%s: read %+v", c.name, got)
			}
			if len(w.calls) != 0 {
				t.Errorf("%s: sent %d requests", c.name, len(w.calls))
			}
		}
	})

	t.Run("an older server", func(t *testing.T) {
		// The selector's refusal, then the host-wide list: the read keeps the
		// worker's own row and is shown no other.
		w := newWire2816(t, refused2816(), wireStep2816{op: wideOp2816, body: desired2816(otherRow2816, row2816)})
		got, err := read(t, w, "h1", "w")
		if err != nil {
			t.Fatal(err)
		}
		if len(got.Workers) != 1 || !reflect.DeepEqual(got.Workers[0], wantRow) {
			t.Fatalf("workers=%+v, want the one worker asked for", got.Workers)
		}
		w.asked(t, 0, targetedOp2816, wideOp2816)
		if w.calls[1].session != "" {
			t.Errorf("the host-wide fallback carried session %q", w.calls[1].session)
		}
	})
}

// The refusal is remembered: the next reads go straight to the host-wide list
// until refusedReprobeEvery has passed, and the one read after that asks for
// the selector again, so a server upgraded while the host runs is noticed. A
// refusal is remembered only when the host-wide read then succeeds.
func TestRelaunchWorkerRemembersAnOlderServerAndLooksAgain2816(t *testing.T) {
	start := time.Date(2026, 11, 22, 12, 0, 0, 0, time.UTC)
	type beat struct {
		name  string
		after time.Duration // since the previous beat
		reply []wireStep2816
		ops   []string // the requests the beat expects
		fails bool
	}
	for _, c := range []struct {
		name  string
		beats []beat
	}{
		{"a server that stays older", []beat{
			// The beats land at start, +1s, +30m−1s (just inside the window),
			// +30m exactly (the window's end — the reprobe boundary, as the
			// upgraded-server case's own end beat also sits at exactly +30m)
			// and +30m+1s.
			{"at the first read", 0, []wireStep2816{refused2816(), {op: wideOp2816, body: desired2816(row2816)}}, []string{targetedOp2816, wideOp2816}, false},
			{"a second later", time.Second, []wireStep2816{{op: wideOp2816, body: desired2816(row2816)}}, []string{wideOp2816}, false},
			{"just inside the window", refusedReprobeEvery - 2*time.Second, []wireStep2816{{op: wideOp2816, body: desired2816(row2816)}}, []string{wideOp2816}, false},
			{"at the window's end", time.Second, []wireStep2816{refused2816(), {op: wideOp2816, body: desired2816(row2816)}}, []string{targetedOp2816, wideOp2816}, false},
			{"a second after that", time.Second, []wireStep2816{{op: wideOp2816, body: desired2816(row2816)}}, []string{wideOp2816}, false},
		}},
		{"a server upgraded while the host runs", []beat{
			{"at the first read", 0, []wireStep2816{refused2816(), {op: wideOp2816, body: desired2816(row2816)}}, []string{targetedOp2816, wideOp2816}, false},
			{"inside the window", time.Minute, []wireStep2816{{op: wideOp2816, body: desired2816(row2816)}}, []string{wideOp2816}, false},
			{"at the window's end", refusedReprobeEvery - time.Minute, []wireStep2816{{op: targetedOp2816, body: desired2816(row2816)}}, []string{targetedOp2816}, false},
			{"a second after that", time.Second, []wireStep2816{{op: targetedOp2816, body: desired2816(row2816)}}, []string{targetedOp2816}, false},
		}},
		{"a refusal counts only when the host-wide read succeeds", []beat{
			{"the host-wide read fails too", 0, []wireStep2816{refused2816(), {op: wideOp2816, status: 503, body: `unavailable`}}, []string{targetedOp2816, wideOp2816}, true},
			{"so the next read asks for the selector again", 0, []wireStep2816{refused2816(), {op: wideOp2816, body: desired2816(row2816)}}, []string{targetedOp2816, wideOp2816}, false},
			{"and is remembered from there", 0, []wireStep2816{{op: wideOp2816, body: desired2816(row2816)}}, []string{wideOp2816}, false},
		}},
	} {
		t.Run(c.name, func(t *testing.T) {
			steps := make([]wireStep2816, 0, 2*len(c.beats))
			for _, b := range c.beats {
				steps = append(steps, b.reply...)
			}
			w := newWire2816(t, steps...)
			clock := start
			w.source.now = func() time.Time { return clock }
			for _, b := range c.beats {
				clock = clock.Add(b.after)
				from := len(w.calls)
				_, err := w.source.RelaunchWorker(context.Background(), "h1", "w")
				if (err != nil) != b.fails {
					t.Errorf("%s: error=%v, want failed=%v", b.name, err, b.fails)
				}
				w.asked(t, from, b.ops...)
			}
			if len(w.calls) != len(steps) {
				t.Errorf("%d requests for %d scripted answers", len(w.calls), len(steps))
			}
		})
	}
}

// The repair report's fresh read reads the worker it reports. It reports
// nothing on a read that failed, and nothing for a worker whose fresh row is
// not the session that was observed dark.
func TestTheRepairReportsFreshReadIsTheTargetedRead2816(t *testing.T) {
	stored := wireStep2816{op: reportOp2816, body: `{"data":{"setHostWorkerTerminals":{"stored":[{"id":"w","urn":"private"}],"refused":[]}}}`}
	for _, c := range []struct {
		name  string
		reply []wireStep2816
		want  string // in the error; "" = the report is stored
	}{
		{"reported after a read of that worker alone", []wireStep2816{{op: targetedOp2816, body: desired2816(row2816)}, stored}, ""},
		{"the read failed", []wireStep2816{{op: targetedOp2816, status: 503, body: `unavailable`}}, "503"},
		{"the read named no row", []wireStep2816{{op: targetedOp2816, body: desired2816()}}, "repair target session changed"},
		{"the read named another session", []wireStep2816{{op: targetedOp2816, body: desired2816(`{"workerId":"w","family":"claude","reason":"HOLDS_TASK","desired":"RUNNING","openSessionId":"a-newer-session","worktrees":["repo/w"]}`)}}, "repair target session changed"},
	} {
		t.Run(c.name, func(t *testing.T) {
			w := newWire2816(t, c.reply...)
			err := w.source.ReportRelaunch(api.WithSession(context.Background(), "a-caller-session"),
				hostrelaunch.Target{AppID: "app-id", HostID: "h1", WorkerID: "w", SessionID: "still-open", Server: w.url, Worktree: "/private/worktree"},
				hostrelaunch.Result{Status: hostrelaunch.Applied, Reason: "TERMINAL_MISSING", Attempt: 1, AbsentObservedAt: time.Date(2026, 11, 15, 12, 0, 0, 0, time.UTC)})
			if (err != nil) != (c.want != "") {
				t.Fatalf("error=%v, want %q", err, c.want)
			}
			if c.want != "" && !strings.Contains(err.Error(), c.want) {
				t.Errorf("the error lost %q: %v", c.want, err)
			}
			ops := []string{targetedOp2816}
			if c.want == "" {
				ops = append(ops, reportOp2816)
			}
			w.asked(t, 0, ops...)
			if w.calls[0].vars["worker"] != "w" {
				t.Errorf("the fresh read asked for %v", w.calls[0].vars)
			}
			for i, call := range w.calls {
				if call.session != "" {
					t.Errorf("request %d carried session %q", i+1, call.session)
				}
			}
		})
	}
}

// The selector is what an older server lacks (hadron-server#4693): the
// committed schema takes the targeted document, and the same schema without
// exactly the `worker` argument of hostDesiredState refuses it, by that name,
// while still taking the host-wide document the fallback sends.
func TestTheTargetedReadNeedsTheWorkerSelectorAnOlderServerLacks2816(t *testing.T) {
	targeted := newWire2816(t, wireStep2816{op: targetedOp2816, body: desired2816(row2816)})
	if _, err := targeted.source.RelaunchWorker(context.Background(), "h1", "w"); err != nil {
		t.Fatal(err)
	}
	wide := newWire2816(t, wireStep2816{op: wideOp2816, body: desired2816(row2816)})
	if _, err := wide.source.RelaunchSnapshot(context.Background(), "h1"); err != nil {
		t.Fatal(err)
	}
	document, plain := targeted.calls[0].query, wide.calls[0].query

	snapshot, err := os.ReadFile("../../../schema/schema.graphql")
	if err != nil {
		t.Fatal(err)
	}
	committed, err := gqlparser.LoadSchema(&ast.Source{Name: "committed", Input: string(snapshot)})
	if err != nil {
		t.Fatal(err)
	}
	for name, query := range map[string]string{"the targeted document": document, "the host-wide document": plain} {
		if _, errs := gqlparser.LoadQuery(committed, query); len(errs) > 0 {
			t.Fatalf("the committed schema rejects %s: %v", name, errs)
		}
	}
	if selector(committed, "hostDesiredState", "worker") == nil {
		t.Fatal("the committed schema has no worker selector on hostDesiredState")
	}

	older, err := gqlparser.LoadSchema(&ast.Source{Name: "older", Input: string(snapshot)})
	if err != nil {
		t.Fatal(err)
	}
	field := older.Types["Query"].Fields.ForName("hostDesiredState")
	var kept ast.ArgumentDefinitionList
	for _, arg := range field.Arguments {
		if arg.Name != "worker" {
			kept = append(kept, arg)
		}
	}
	// The older server really lacks exactly the selector: the removal removed
	// one argument, so a refusal below is about it and nothing else.
	if len(kept) != len(field.Arguments)-1 {
		t.Fatalf("removing the selector removed %d of %d arguments, want exactly one", len(field.Arguments)-len(kept), len(field.Arguments))
	}
	field.Arguments = kept
	if _, errs := gqlparser.LoadQuery(older, document); len(errs) == 0 || !strings.Contains(errs.Error(), `"worker"`) {
		t.Fatalf("an older server would take the targeted document: %v", errs)
	}
	if _, errs := gqlparser.LoadQuery(older, plain); len(errs) > 0 {
		t.Fatalf("an older server must take the host-wide document: %v", errs)
	}
}

// selector is a field's argument of that name in the schema, or nil.
func selector(schema *ast.Schema, field, name string) *ast.ArgumentDefinition {
	for _, arg := range schema.Types["Query"].Fields.ForName(field).Arguments {
		if arg.Name == name {
			return arg
		}
	}
	return nil
}
