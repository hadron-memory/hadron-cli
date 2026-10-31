package cmd

// cli#1463: `hadron team worker compile` with no worker compiles the App's
// boot prompts in one batch. Changed sources only by default, decided by the
// server's RECOMPILE_AVAILABLE lint on each compiled node (cor:agt:040:13);
// --all forces every live worker; a dry run unless --apply; never approves.

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/api/gen"
	"github.com/hadron-memory/hadron-cli/internal/exitcode"
)

const batchApp = "hrn:app:acme.com:eng-team"

type batchWorker struct {
	id, name     string
	retired      bool
	pointer      string // compiled node URN the worker's pointer names; "" = none
	deleted      bool   // bootPromptTargetDeleted
	revision     int    // the compiled node's current revision
	lint         string // "recompile", "clean", "null", "broken", "fail" (the whole lint read fails)
	compile      string // "ok", "502", a server error code
	after        string // revision read after a lost response: "landed", "same", "error"
	pointerAfter string // pointer read after a lost response (no baseline pointer)
}

type batchCall struct {
	Op   string
	Vars map[string]any
}

type batchFake struct {
	mu      sync.Mutex
	calls   []batchCall
	workers []batchWorker
}

func (b *batchFake) ops(op string) []batchCall {
	b.mu.Lock()
	defer b.mu.Unlock()
	var out []batchCall
	for _, c := range b.calls {
		if c.Op == op {
			out = append(out, c)
		}
	}
	return out
}

func (b *batchFake) byID(id string) *batchWorker {
	for i := range b.workers {
		if b.workers[i].id == id {
			return &b.workers[i]
		}
	}
	return nil
}

func (b *batchFake) byPointer(ref string) *batchWorker {
	for i := range b.workers {
		if b.workers[i].pointer == ref && ref != "" {
			return &b.workers[i]
		}
	}
	return nil
}

func batchGQLErr(code string) string {
	return fmt.Sprintf(`{"data":null,"errors":[{"message":"refused: %s","extensions":{"code":%q}}]}`, code, code)
}

func newBatchFake(t *testing.T, workers []batchWorker) (*httptest.Server, *batchFake) {
	t.Helper()
	fake := &batchFake{workers: workers}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			OperationName string         `json:"operationName"`
			Variables     map[string]any `json:"variables"`
		}
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &body)
		op := body.OperationName
		fake.mu.Lock()
		fake.calls = append(fake.calls, batchCall{Op: op, Vars: body.Variables})
		fake.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		switch op {
		case "GetBoundSessionEnd", "ChannelReadState", "TeamAppIdentity":
			_, _ = w.Write([]byte(`{"data":{}}`))
		case "WorkersBootPointers":
			items := []string{}
			for _, wk := range fake.workers {
				if wk.retired && body.Variables["includeRetired"] != true {
					continue // the server hides retired castings
				}
				retired := "null"
				if wk.retired {
					retired = `"2026-01-01T00:00:00Z"`
				}
				ptr := "null"
				if wk.pointer != "" {
					ptr = fmt.Sprintf("%q", wk.pointer)
				}
				items = append(items, fmt.Sprintf(`{"id":%q,"urn":"hrn:worker:acme.com:eng-team:%s","name":%q,"retiredAt":%s,"bootPromptRef":%s,"bootPromptTargetDeleted":%v}`,
					wk.id, strings.ToLower(wk.name), wk.name, retired, ptr, wk.deleted))
			}
			// Page like the server: the full total, the [offset, offset+limit) slice.
			total := len(items)
			off, lim := 0, total
			if v, ok := body.Variables["offset"].(float64); ok {
				off = min(int(v), total)
			}
			if v, ok := body.Variables["limit"].(float64); ok {
				lim = int(v)
			}
			items = items[off:min(off+lim, total)]
			fmt.Fprintf(w, `{"data":{"workers":{"total":%d,"items":[%s]}}}`, total, strings.Join(items, ","))
		case "CompiledPromptLintBatch":
			refs, _ := body.Variables["refs"].([]any)
			for _, ref := range refs {
				if wk := fake.byPointer(fmt.Sprint(ref)); wk != nil && wk.lint == "fail" {
					// The whole lint read fails: a denied or unreachable provider.
					w.WriteHeader(http.StatusBadGateway)
					_, _ = w.Write([]byte("bad gateway"))
					return
				}
			}
			nodes, unavailable := []string{}, []string{}
			for _, ref := range refs {
				wk := fake.byPointer(fmt.Sprint(ref))
				if wk == nil || wk.lint == "broken" {
					unavailable = append(unavailable, fmt.Sprintf("%q", ref))
					continue
				}
				lint := `{"items":[],"nextCursor":null}`
				switch wk.lint {
				case "recompile":
					lint = `{"items":[{"kind":"BROKEN_INCLUDE"},{"kind":"RECOMPILE_AVAILABLE"}],"nextCursor":null}`
				case "null":
					lint = "null"
				}
				nodes = append(nodes, fmt.Sprintf(`{"id":"n-%s","urn":%q,"revision":%d,"lintWarnings":%s}`, wk.id, wk.pointer, wk.revision, lint))
			}
			fmt.Fprintf(w, `{"data":{"nodeBatch":{"truncated":false,"omitted":[],"unavailable":[%s],"nodes":[%s]}}}`, strings.Join(unavailable, ","), strings.Join(nodes, ","))
		case "CompileWorkerBootPrompt":
			wk := fake.byID(fmt.Sprint(body.Variables["workerRef"]))
			switch {
			case wk == nil:
				_, _ = w.Write([]byte(batchGQLErr("NOT_FOUND")))
			case wk.compile == "502":
				w.WriteHeader(http.StatusBadGateway)
				_, _ = w.Write([]byte("bad gateway"))
			case wk.compile == "ok" || wk.compile == "":
				ref := wk.pointer
				if ref == "" {
					ref = "hrn:node:acme.com:eng-team-install:boot:workers:" + wk.id + ":compiled"
				}
				fmt.Fprintf(w, `{"data":{"compileWorkerBootPrompt":{"node":{"urn":%q},"revNo":%d,"contentHash":"h-%s","pointerSet":true,"approvalIssued":false}}}`, ref, wk.revision+1, wk.id)
			default:
				_, _ = w.Write([]byte(batchGQLErr(wk.compile)))
			}
		case "CompiledNodeRevision":
			wk := fake.byPointer(fmt.Sprint(body.Variables["ref"]))
			switch {
			case wk == nil || wk.after == "error":
				w.WriteHeader(http.StatusBadGateway)
				_, _ = w.Write([]byte("bad gateway"))
			case wk.after == "landed":
				fmt.Fprintf(w, `{"data":{"node":{"revision":%d}}}`, wk.revision+1)
			default:
				fmt.Fprintf(w, `{"data":{"node":{"revision":%d}}}`, wk.revision)
			}
		case "WorkerBootPointer":
			wk := fake.byID(fmt.Sprint(body.Variables["ref"]))
			if wk == nil || wk.pointerAfter == "error" {
				w.WriteHeader(http.StatusBadGateway)
				_, _ = w.Write([]byte("bad gateway"))
				return
			}
			ptr := "null"
			if wk.pointerAfter != "" {
				ptr = fmt.Sprintf("%q", wk.pointerAfter)
			}
			fmt.Fprintf(w, `{"data":{"worker":{"bootPromptRef":%s}}}`, ptr)
		default:
			t.Errorf("unexpected operation %q", op)
			_, _ = w.Write([]byte(`{"errors":[{"message":"unexpected operation"}]}`))
		}
	}))
	t.Cleanup(srv.Close)
	return srv, fake
}

func cptr(id string) string {
	return "hrn:node:acme.com:eng-team-install:boot:workers:" + id + ":compiled"
}

// The standard team: one worker per bucket.
func batchTeam() []batchWorker {
	return []batchWorker{
		{id: "w-ann", name: "Ann", pointer: cptr("w-ann"), revision: 3, lint: "recompile", compile: "ok"},
		{id: "w-bob", name: "Bob", pointer: cptr("w-bob"), revision: 5, lint: "clean", compile: "ok"},
		{id: "w-cal", name: "Cal", compile: "BOOT_PROMPT_NO_SOURCE"},
		{id: "w-dee", name: "Dee", pointer: cptr("w-dee"), revision: 2, lint: "null", compile: "ok"},
		{id: "w-eve", name: "Eve", compile: "BOOT_PROMPT_SOURCE_INVALID"},
		{id: "w-fay", name: "Fay", retired: true, compile: "ok"},
	}
}

type batchOut struct {
	App       string           `json:"app"`
	Applied   bool             `json:"applied"`
	All       bool             `json:"all"`
	ToCompile []map[string]any `json:"toCompile"`
	Compiled  []map[string]any `json:"compiled"`
	Unchanged []map[string]any `json:"unchanged"`
	Skipped   []map[string]any `json:"skipped"`
	Failed    []map[string]any `json:"failed"`
	Unknown   []map[string]any `json:"unknown"`
}

func runBatch(t *testing.T, workers []batchWorker, args ...string) (*batchFake, string, map[string]json.RawMessage, error) {
	t.Helper()
	srv, fake := newBatchFake(t, workers)
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs(append([]string{"--server", srv.URL, "--app", batchApp, "team", "worker", "compile"}, args...))
	err := root.Execute()
	var raw map[string]json.RawMessage
	if strings.Contains(strings.Join(args, " "), "--json") && strings.TrimSpace(out.String()) != "" {
		if jerr := json.Unmarshal([]byte(out.String()), &raw); jerr != nil {
			t.Fatalf("--json output is not one JSON object: %v\n%s", jerr, out.String())
		}
	}
	return fake, out.String(), raw, err
}

func decodeBatch(t *testing.T, out string) batchOut {
	t.Helper()
	var d batchOut
	if err := json.Unmarshal([]byte(out), &d); err != nil {
		t.Fatalf("decode: %v\n%s", err, out)
	}
	return d
}

func batchNames(rows []map[string]any) string {
	n := []string{}
	for _, r := range rows {
		n = append(n, fmt.Sprint(r["name"]))
	}
	return strings.Join(n, ",")
}

func TestBatchCompileDryRunWritesNothingAndShowsEveryBucket(t *testing.T) {
	fake, out, raw, err := runBatch(t, batchTeam(), "--json")
	if err == nil || exitCodeFor(err) == 0 {
		// Dee's lint is unavailable: the plan could not decide for one worker.
		t.Fatalf("an undecided worker must not exit 0: %v", err)
	}
	if n := len(fake.ops("CompileWorkerBootPrompt")); n != 0 {
		t.Fatalf("a dry run sent %d compile mutation(s)", n)
	}
	for _, key := range []string{"toCompile", "compiled", "unchanged", "skipped", "failed", "unknown"} {
		if string(raw[key]) == "null" || raw[key] == nil {
			t.Fatalf("%s must be an array, never null: %s", key, out)
		}
	}
	d := decodeBatch(t, out)
	if d.Applied || d.All || d.App != batchApp {
		t.Fatalf("header wrong: %+v", d)
	}
	if got := batchNames(d.ToCompile); got != "Ann,Cal,Eve" {
		t.Fatalf("toCompile = %s, want Ann,Cal,Eve (changed + first compiles)", got)
	}
	if got := batchNames(d.Unchanged); got != "Bob" {
		t.Fatalf("unchanged = %s", got)
	}
	if got := batchNames(d.Unknown); got != "Dee" {
		t.Fatalf("unknown = %s (null lint is unknown, not compiled)", got)
	}
	if len(d.Compiled)+len(d.Skipped)+len(d.Failed) != 0 {
		t.Fatalf("a dry run reported results: %s", out)
	}
	if strings.Contains(out, "Fay") {
		t.Fatalf("retired worker listed: %s", out)
	}
}

func TestBatchCompileApplyClassifiesEachWorker(t *testing.T) {
	fake, out, _, err := runBatch(t, batchTeam(), "--apply", "--json")
	if got := exitCodeFor(err); got != exitcode.Conflict {
		t.Fatalf("exit = %d (%v), want %d for the failed source", got, err, exitcode.Conflict)
	}
	compiles := fake.ops("CompileWorkerBootPrompt")
	sent := []string{}
	for _, c := range compiles {
		sent = append(sent, fmt.Sprint(c.Vars["workerRef"]))
	}
	if strings.Join(sent, ",") != "w-ann,w-cal,w-eve" {
		t.Fatalf("compiled by id exactly once each, got %v", sent)
	}
	d := decodeBatch(t, out)
	if !d.Applied || batchNames(d.Compiled) != "Ann" || batchNames(d.Unchanged) != "Bob" || batchNames(d.Skipped) != "Cal" || batchNames(d.Failed) != "Eve" || batchNames(d.Unknown) != "Dee" {
		t.Fatalf("buckets wrong: %s", out)
	}
	ann := d.Compiled[0]
	if ann["compiledNodeRef"] != cptr("w-ann") || ann["revision"] != float64(4) {
		t.Fatalf("compiled entry lacks ref/revision: %v", ann)
	}
	if ann["approveCommand"] != "hadron node approve "+cptr("w-ann")+" --revision 4" {
		t.Fatalf("approve line wrong: %v", ann["approveCommand"])
	}
	if d.Skipped[0]["code"] != "BOOT_PROMPT_NO_SOURCE" || d.Failed[0]["code"] != "BOOT_PROMPT_SOURCE_INVALID" {
		t.Fatalf("codes lost: %s", out)
	}
	if strings.Contains(out, `"approvalIssued":true`) || len(fake.ops("ApproveNode")) != 0 {
		t.Fatalf("batch compile must never approve")
	}
}

func TestBatchCompileClassifiesEveryBootCodeByServerCode(t *testing.T) {
	for code, bucket := range map[string]string{
		"BOOT_PROMPT_NO_SOURCE":                    "skipped",
		"BOOT_PROMPT_TARGET_RETIRED":               "skipped",
		"BOOT_PROMPT_SOURCE_INVALID":               "failed",
		"BOOT_PROMPT_INCLUDE_RETIRED":              "failed",
		"BOOT_PROMPT_INCLUDE_REVISION_UNAVAILABLE": "failed",
		"BOOT_PROMPT_COMPILE_NOT_PERMITTED":        "failed",
	} {
		t.Run(code, func(t *testing.T) {
			_, out, _, err := runBatch(t, []batchWorker{{id: "w-1", name: "One", compile: code}}, "--apply", "--json")
			d := decodeBatch(t, out)
			rows := map[string][]map[string]any{"skipped": d.Skipped, "failed": d.Failed}[bucket]
			if len(rows) != 1 || rows[0]["code"] != code {
				t.Fatalf("%s should land in %s: %s", code, bucket, out)
			}
			if bucket == "skipped" && err != nil {
				t.Fatalf("a skip is not a failure: %v", err)
			}
			if bucket == "failed" && exitCodeFor(err) == 0 {
				t.Fatalf("a failure must not exit 0")
			}
		})
	}
}

func TestBatchCompileLostResponseReadsRevisionOnceAndContinues(t *testing.T) {
	for _, tc := range []struct{ after, outcome string }{
		{"landed", "landed"}, {"same", "not-landed"}, {"error", "unknown"},
	} {
		t.Run(tc.after, func(t *testing.T) {
			team := []batchWorker{
				{id: "w-ann", name: "Ann", pointer: cptr("w-ann"), revision: 3, lint: "recompile", compile: "502", after: tc.after},
				{id: "w-cal", name: "Cal", compile: "ok"},
			}
			fake, out, _, err := runBatch(t, team, "--apply", "--json")
			if got := exitCodeFor(err); got != exitcode.Unavailable {
				t.Fatalf("exit = %d (%v), want %d after a lost response", got, err, exitcode.Unavailable)
			}
			ann := 0
			for _, c := range fake.ops("CompileWorkerBootPrompt") {
				if c.Vars["workerRef"] == "w-ann" {
					ann++
				}
			}
			if ann != 1 {
				t.Fatalf("lost compile was sent %d times; never retry", ann)
			}
			if n := len(fake.ops("CompiledNodeRevision")); n != 1 {
				t.Fatalf("want exactly one revision read after the loss, got %d", n)
			}
			d := decodeBatch(t, out)
			if batchNames(d.Unknown) != "Ann" || d.Unknown[0]["outcome"] != tc.outcome {
				t.Fatalf("lost response bucket wrong: %s", out)
			}
			if d.Unknown[0]["revisionBefore"] != float64(3) {
				t.Fatalf("baseline missing: %v", d.Unknown[0])
			}
			if batchNames(d.Compiled) != "Cal" {
				t.Fatalf("the batch must continue after a lost response: %s", out)
			}
		})
	}
}

func TestBatchCompileLostFirstCompileReadsThePointerOnce(t *testing.T) {
	team := []batchWorker{{id: "w-cal", name: "Cal", compile: "502", pointerAfter: cptr("w-cal")}}
	fake, out, _, err := runBatch(t, team, "--apply", "--json")
	if exitCodeFor(err) != exitcode.Unavailable {
		t.Fatalf("exit %v", err)
	}
	if len(fake.ops("WorkerBootPointer")) != 1 || len(fake.ops("CompileWorkerBootPrompt")) != 1 {
		t.Fatalf("want one compile and one pointer read: %+v", fake.calls)
	}
	d := decodeBatch(t, out)
	if batchNames(d.Unknown) != "Cal" || d.Unknown[0]["outcome"] != "landed" || d.Unknown[0]["compiledNodeRef"] != cptr("w-cal") {
		t.Fatalf("first-compile loss: %s", out)
	}
}

func TestBatchCompileAllForcesEveryLiveWorker(t *testing.T) {
	fake, out, _, _ := runBatch(t, batchTeam(), "--all", "--apply", "--json")
	sent := []string{}
	for _, c := range fake.ops("CompileWorkerBootPrompt") {
		sent = append(sent, fmt.Sprint(c.Vars["workerRef"]))
	}
	if strings.Join(sent, ",") != "w-ann,w-bob,w-cal,w-dee,w-eve" {
		t.Fatalf("--all compiles every live worker once, got %v", sent)
	}
	d := decodeBatch(t, out)
	if !d.All || batchNames(d.Compiled) != "Ann,Bob,Dee" || len(d.Unchanged) != 0 || len(d.Unknown) != 0 {
		t.Fatalf("--all buckets: %s", out)
	}
}

func TestBatchCompileOnlyFiltersAndRefusesUnknownNamesBeforeWriting(t *testing.T) {
	fake, out, _, err := runBatch(t, batchTeam(), "--only", "ann,Bob", "--apply", "--json")
	if err != nil {
		t.Fatal(err)
	}
	d := decodeBatch(t, out)
	if batchNames(d.Compiled) != "Ann" || batchNames(d.Unchanged) != "Bob" || len(fake.ops("CompileWorkerBootPrompt")) != 1 {
		t.Fatalf("--only did not filter: %s", out)
	}

	fake, _, _, err = runBatch(t, batchTeam(), "--only", "Ann,Zed,Fay", "--apply")
	if exitCodeFor(err) != exitcode.NotFound || !strings.Contains(err.Error(), "Zed") || !strings.Contains(err.Error(), "Fay") {
		t.Fatalf("unknown or retired --only names must be reported: %v", err)
	}
	if n := len(fake.ops("CompileWorkerBootPrompt")); n != 0 {
		t.Fatalf("refused --only still wrote %d compile(s)", n)
	}
}

func TestBatchCompileReadsOnlyWhatItNeeds(t *testing.T) {
	team := []batchWorker{}
	for i := 0; i < 250; i++ {
		id := fmt.Sprintf("w-%03d", i)
		team = append(team, batchWorker{id: id, name: fmt.Sprintf("W%03d", i), pointer: cptr(id), revision: 1, lint: "clean"})
	}
	fake, out, _, err := runBatch(t, team, "--json")
	if err != nil {
		t.Fatal(err)
	}
	// Every worker on both roster pages is planned, not just the first page.
	if d := decodeBatch(t, out); len(d.Unchanged) != 250 || len(d.ToCompile) != 0 || len(d.Unknown) != 0 {
		t.Fatalf("all 250 workers must be read across roster pages: unchanged=%d toCompile=%d unknown=%d", len(d.Unchanged), len(d.ToCompile), len(d.Unknown))
	}
	fake.mu.Lock()
	defer fake.mu.Unlock()
	counts := map[string]int{}
	for _, c := range fake.calls {
		counts[c.Op]++
		if c.Op == "WorkersBootPointers" && c.Vars["includeRetired"] == true {
			t.Fatalf("the roster read must exclude retired workers")
		}
	}
	for op := range counts {
		switch op {
		case "WorkersBootPointers", "CompiledPromptLintBatch", "GetBoundSessionEnd", "ChannelReadState":
		default:
			// No name resolution, no per-worker read, and no include-target
			// reads: the CLI follows the server's verdict, it re-implements none.
			t.Fatalf("unexpected read %s on the plan path: %v", op, counts)
		}
	}
	// 250 workers: two roster pages and two batch pages (200-node cap), not
	// one request per worker.
	if counts["WorkersBootPointers"] != 2 || counts["CompiledPromptLintBatch"] != 2 {
		t.Fatalf("request count grows with the roster: %v", counts)
	}
}

func TestBatchCompileFlagsNeedNoWorkerArgument(t *testing.T) {
	for _, args := range [][]string{{"Ann", "--all"}, {"Ann", "--apply"}, {"Ann", "--only", "Bob"}} {
		fake, _, _, err := runBatch(t, batchTeam(), args...)
		if exitCodeFor(err) != exitcode.Usage || len(fake.calls) != 0 {
			t.Fatalf("%v: want a usage refusal before any request, got %v", args, err)
		}
	}
}

func TestBatchCompileHumanOutputPrintsApproveLinesNeverRunsThem(t *testing.T) {
	_, out, _, _ := runBatch(t, batchTeam(), "--apply")
	if !strings.Contains(out, "hadron node approve "+cptr("w-ann")+" --revision 4") {
		t.Fatalf("approve line missing: %s", out)
	}
	if !strings.Contains(out, "nothing was approved") {
		t.Fatalf("must say nothing was approved: %s", out)
	}
	// No-source skips are most of a real App's staff: one line, not a row each.
	if !strings.Contains(out, "skipped, 1 with no source (BOOT_PROMPT_NO_SOURCE): Cal\n") {
		t.Fatalf("no-source skips not folded: %s", out)
	}
	_, out, _, _ = runBatch(t, batchTeam())
	if !strings.Contains(out, "DRY RUN: 3 to compile (1 changed, 2 with no compiled prompt yet)") || !strings.Contains(out, "--apply") {
		t.Fatalf("dry run must say so: %s", out)
	}
	if !strings.Contains(out, "with no compiled prompt yet (each is skipped if it has no source): Cal, Eve\n") {
		t.Fatalf("first compiles not folded: %s", out)
	}
}

// The selections themselves (review:assert-the-query-not-the-capture): the
// roster read stays slim, and the plan never selects bodies or provenance.
func TestBatchCompileSelectionsStaySlim(t *testing.T) {
	for name, doc := range map[string]string{
		"WorkersBootPointers":     gen.WorkersBootPointers_Operation,
		"CompiledPromptLintBatch": gen.CompiledPromptLintBatch_Operation,
	} {
		for _, field := range []string{"prompt", "signature", "trailer", "content", "provenance", "servedPrompt", "WorkerFields"} {
			if strings.Contains(doc, field) {
				t.Fatalf("%s selects %q", name, field)
			}
		}
	}
}

// A lint read that fails as a whole (denied, unreachable) leaves the plan
// undecided, it does not abort it: the default run reports every compiled
// worker as unknown with the read's exit code and compiles nothing for them;
// --all still compiles them, with no baseline revision.
func TestBatchCompileLintReadFailureLeavesWorkersUnknownAndAllStillCompiles(t *testing.T) {
	team := func() []batchWorker {
		return []batchWorker{
			{id: "w-ann", name: "Ann", pointer: cptr("w-ann"), revision: 3, lint: "fail", compile: "ok"},
			{id: "w-bob", name: "Bob", pointer: cptr("w-bob"), revision: 5, lint: "clean", compile: "ok"},
			{id: "w-cal", name: "Cal", compile: "BOOT_PROMPT_NO_SOURCE"},
		}
	}

	fake, out, _, err := runBatch(t, team(), "--apply", "--json")
	if exitCodeFor(err) != exitcode.Unavailable {
		t.Fatalf("a failed lint read exits with its own code (7), got %v\n%s", err, out)
	}
	d := decodeBatch(t, out)
	if batchNames(d.Unknown) != "Ann,Bob" || len(d.Unchanged) != 0 || batchNames(d.Skipped) != "Cal" {
		t.Fatalf("default run after a failed lint read: %s", out)
	}
	for _, u := range d.Unknown {
		if u["outcome"] != "" || !strings.Contains(fmt.Sprint(u["reason"]), "staleness check failed") {
			t.Fatalf("an undecided worker names the failed read and was not compiled: %v", u)
		}
	}
	sent := []string{}
	for _, c := range fake.ops("CompileWorkerBootPrompt") {
		sent = append(sent, fmt.Sprint(c.Vars["workerRef"]))
	}
	if strings.Join(sent, ",") != "w-cal" {
		t.Fatalf("only the first compile may be sent when the lint read fails, got %v", sent)
	}

	fake, out, _, err = runBatch(t, team(), "--all", "--apply", "--json")
	if err != nil {
		t.Fatalf("--all must not be aborted by a failed lint read: %v\n%s", err, out)
	}
	d = decodeBatch(t, out)
	if batchNames(d.Compiled) != "Ann,Bob" || batchNames(d.Skipped) != "Cal" || len(d.Unknown) != 0 {
		t.Fatalf("--all after a failed lint read: %s", out)
	}
	if n := len(fake.ops("CompileWorkerBootPrompt")); n != 3 {
		t.Fatalf("--all compiles each live worker once, got %d compiles", n)
	}
}

// An --only that names no worker (`--only=`, `--only ""`, `--only " , "`) is a
// usage error, never a dropped filter that compiles the whole App.
func TestBatchCompileEmptyOnlyIsUsageNotAWholeAppRun(t *testing.T) {
	for _, args := range [][]string{
		{"--only=", "--apply"},
		{"--only", "", "--apply"},
		{"--only", " , ", "--apply"},
	} {
		fake, out, _, err := runBatch(t, batchTeam(), args...)
		if exitCodeFor(err) != exitcode.Usage {
			t.Fatalf("%q: want exit 2, got %v\n%s", args, err, out)
		}
		if n := len(fake.ops("CompileWorkerBootPrompt")); n != 0 {
			t.Fatalf("%q: an empty --only still wrote %d compile(s)", args, n)
		}
	}
	// With a worker argument, an empty --only is still a batch flag: exit 2.
	_, _, _, err := runBatch(t, batchTeam(), "Ann", "--only=")
	if exitCodeFor(err) != exitcode.Usage {
		t.Fatalf("worker argument with --only= must exit 2, got %v", err)
	}
}
