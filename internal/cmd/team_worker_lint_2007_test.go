package cmd

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/cmd/lintwarn"
	"github.com/hadron-memory/hadron-cli/internal/exitcode"
)

// cli#2007: the CLI reads and shows the three lint pages the server serves
// (cor:agt:040:13), as the bound contract on the issue says. Every control here
// goes through the real command against a fake server that records each
// operation, its variables, its query text and its session header.

type lintCall2007 struct {
	Op      string
	Vars    map[string]any
	Query   string
	Session string
}

type lintFake2007 struct {
	mu    sync.Mutex
	calls []lintCall2007
}

func (l *lintFake2007) ops(op string) []lintCall2007 {
	l.mu.Lock()
	defer l.mu.Unlock()
	var got []lintCall2007
	for _, c := range l.calls {
		if c.Op == op {
			got = append(got, c)
		}
	}
	return got
}

func (l *lintFake2007) lintOps() []lintCall2007 {
	l.mu.Lock()
	defer l.mu.Unlock()
	var got []lintCall2007
	for _, c := range l.calls {
		if strings.Contains(c.Op, "Lint") {
			got = append(got, c)
		}
	}
	return got
}

// handler answers an operation; "" means "not stubbed here": the shared
// older-server default applies, as for every other command's fake.
type handler2007 func(op string, vars map[string]any) string

func newLintFake2007(t *testing.T, h handler2007) (*httptest.Server, *lintFake2007) {
	t.Helper()
	fake := &lintFake2007{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && r.URL.Path == "/capabilities" {
			_, _ = w.Write([]byte(`{"capabilities":[]}`))
			return
		}
		var body struct {
			OperationName string         `json:"operationName"`
			Variables     map[string]any `json:"variables"`
			Query         string         `json:"query"`
		}
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &body)
		fake.mu.Lock()
		fake.calls = append(fake.calls, lintCall2007{body.OperationName, body.Variables, body.Query, r.Header.Get("X-Hadron-Session")})
		fake.mu.Unlock()
		resp := h(body.OperationName, body.Variables)
		if resp == "" {
			resp, _ = unstubbedDefault(body.OperationName)
		}
		if resp == "" {
			t.Errorf("unexpected operation %q", body.OperationName)
			resp = `{"errors":[{"message":"unexpected operation"}]}`
		}
		_, _ = w.Write([]byte(resp))
	}))
	t.Cleanup(srv.Close)
	return srv, fake
}

// lintPage2007 builds LintWarningPage JSON of n WORKER_LINK_MISSING items
// (alternating the two stored fields, both at position 0) with a next cursor.
func lintPage2007(total, n int, next string, compiled bool) string {
	items := make([]string, n)
	for i := range items {
		if compiled {
			items[i] = fmt.Sprintf(`{"kind":"BROKEN_INCLUDE","position":%d,"text":"include %d is broken","targetUrn":null,"pinnedRevision":null,"newerRevision":null,"overriddenNodeLoc":null,"workerField":null}`, i, i)
			continue
		}
		field := []string{"signature", "trailer"}[i%2]
		items[i] = fmt.Sprintf(`{"kind":"WORKER_LINK_MISSING","position":0,"text":"the %s lacks the worker link","targetUrn":null,"pinnedRevision":null,"newerRevision":null,"overriddenNodeLoc":null,"workerField":%q}`, field, field)
	}
	nc := "null"
	if next != "" {
		nc = fmt.Sprintf("%q", next)
	}
	return fmt.Sprintf(`{"items":[%s],"total":%d,"nextCursor":%s}`, strings.Join(items, ","), total, nc)
}

func after2007(vars map[string]any) string {
	if a, ok := vars["after"].(string); ok {
		return a
	}
	return ""
}

// visibleWorker2007 is profileWorkerJSON with the signature and trailer visible
// (the caller may see the worker's internals); profileWorkerJSON itself is masked.
var visibleWorker2007 = strings.Replace(profileWorkerJSON, `"signature":null,"trailer":null`, `"signature":"s","trailer":"t"`, 1)

const workerRef2007 = "hrn:worker:acme.com:eng-team:iris"

func runWorkerGet2007(t *testing.T, worker string, h handler2007, extra ...string) (*lintFake2007, string, string, error) {
	t.Helper()
	teamGitDir(t)
	srv, fake := newLintFake2007(t, func(op string, vars map[string]any) string {
		switch op {
		case "GetWorker":
			return `{"data":{"worker":` + worker + `}}`
		case "GetWorkerProfile":
			return profileNames
		}
		return h(op, vars)
	})
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs(append([]string{"team", "worker", "get", workerRef2007, "--server", srv.URL}, extra...))
	err := root.Execute()
	return fake, out.String(), f.IOStreams.ErrOut.(*strings.Builder).String(), err
}

func sigLint2007(page string) string {
	return `{"data":{"worker":{"id":"` + profileHexID + `","signatureLint":` + page + `}}}`
}

func promptLint2007(servedID string, rev int, page string) string {
	sp := "null"
	if servedID != "" {
		sp = fmt.Sprintf(`{"id":%q,"provenanceRevSeq":%d}`, servedID, rev)
	}
	return `{"data":{"worker":{"id":"` + profileHexID + `","servedPrompt":` + sp + `,"promptLintWarnings":` + page + `}}}`
}

type getJSON2007 struct {
	SignatureLint      *lintwarn.Page `json:"signatureLint"`
	PromptLintWarnings *lintwarn.Page `json:"promptLintWarnings"`
}

func rawKeys2007(t *testing.T, out string) map[string]json.RawMessage {
	t.Helper()
	m := map[string]json.RawMessage{}
	if err := json.Unmarshal([]byte(out), &m); err != nil {
		t.Fatalf("not json: %v\n%s", err, out)
	}
	return m
}

// --- worker get ------------------------------------------------------------

func TestWorkerGetLint2007Present(t *testing.T) {
	_, out, errOut, err := runWorkerGet2007(t, visibleWorker2007, func(op string, v map[string]any) string {
		switch op {
		case "GetWorkerSignatureLint":
			return sigLint2007(lintPage2007(2, 2, "", false))
		case "GetWorkerPromptLint":
			return promptLint2007("rec9", 5, lintPage2007(1, 1, "", true))
		}
		return ""
	})
	if err != nil || errOut != "" {
		t.Fatalf("err=%v stderr=%q", err, errOut)
	}
	// The two WORKER_LINK_MISSING items both carry position 0; the line must
	// name workerField or they cannot be told apart. Compiled lint is an
	// occurrence ordinal, never an offset, and the subject is the provenance
	// revision, never the newest compile.
	for _, want := range []string{
		"signature lint: 2 (the signature and trailer)",
		"WORKER_LINK_MISSING signature: the signature lacks the worker link",
		"WORKER_LINK_MISSING trailer: the trailer lacks the worker link",
		"prompt lint: 1 (the compiled revision recorded for the open session, provenance r5)",
		"BROKEN_INCLUDE occurrence #0: include 0 is broken",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("text missing %q:\n%s", want, out)
		}
	}
	for _, bad := range []string{"no warnings", "up to date", "offset"} {
		if strings.Contains(out, bad) {
			t.Errorf("text must not say %q:\n%s", bad, out)
		}
	}
	_, jout, _, _ := runWorkerGet2007(t, visibleWorker2007, func(op string, v map[string]any) string {
		switch op {
		case "GetWorkerSignatureLint":
			return sigLint2007(lintPage2007(2, 2, "", false))
		case "GetWorkerPromptLint":
			return promptLint2007("rec9", 5, lintPage2007(1, 1, "", true))
		}
		return ""
	}, "--json")
	var got getJSON2007
	if e := json.Unmarshal([]byte(jout), &got); e != nil || got.SignatureLint == nil || len(got.SignatureLint.Items) != 2 || got.SignatureLint.Total != 2 || got.PromptLintWarnings == nil || len(got.PromptLintWarnings.Items) != 1 {
		t.Fatalf("json: %v\n%s", e, jout)
	}
	if w := got.SignatureLint.Items[1].WorkerField; w == nil || *w != "trailer" {
		t.Errorf("workerField must pass through: %+v", got.SignatureLint.Items)
	}
}

func TestWorkerGetLint2007EmptyIsAnswerNotNull(t *testing.T) {
	h := func(op string, v map[string]any) string {
		switch op {
		case "GetWorkerSignatureLint":
			return sigLint2007(lintPage2007(0, 0, "", false))
		case "GetWorkerPromptLint":
			return promptLint2007("rec9", 5, lintPage2007(0, 0, "", true))
		}
		return ""
	}
	_, jout, errOut, err := runWorkerGet2007(t, visibleWorker2007, h, "--json")
	if err != nil || errOut != "" {
		t.Fatalf("err=%v stderr=%q", err, errOut)
	}
	keys := rawKeys2007(t, jout)
	for _, k := range []string{"signatureLint", "promptLintWarnings"} {
		if compact1996(string(keys[k])) != `{"items":[],"total":0,"nextCursor":null}` {
			t.Errorf("%s = %s, want a clean answer with items []", k, keys[k])
		}
	}
	_, out, _, _ := runWorkerGet2007(t, visibleWorker2007, h)
	if !strings.Contains(out, "signature lint: no warnings for the signature and trailer") || !strings.Contains(out, "prompt lint: no warnings for the compiled revision recorded for the open session, provenance r5") {
		t.Errorf("clean text:\n%s", out)
	}
}

func TestWorkerGetLint2007StatesAreDistinct(t *testing.T) {
	cases := []struct {
		name       string
		worker     string
		h          handler2007
		wantSig    string // JSON
		wantPrompt string
		wantText   []string
		notText    []string
		stderr     bool
		lintOps    int
	}{
		{"masked: no read, no line, no stderr", profileWorkerJSON, func(string, map[string]any) string { return "" },
			"null", "null", nil, []string{"lint"}, false, 0},
		{"nothing served: neutral line, no stderr", visibleWorker2007, func(op string, v map[string]any) string {
			switch op {
			case "GetWorkerSignatureLint":
				return sigLint2007(lintPage2007(0, 0, "", false))
			case "GetWorkerPromptLint":
				return promptLint2007("", 0, "null")
			}
			return ""
		}, `{"items":[],"total":0,"nextCursor":null}`, "null",
			[]string{"prompt lint: nothing served to this worker's open session"}, []string{"prompt lint: unavailable", "no warnings for the compiled"}, false, 2},
		{"served but lint null: unavailable plus the stderr class", visibleWorker2007, func(op string, v map[string]any) string {
			switch op {
			case "GetWorkerSignatureLint":
				return sigLint2007(lintPage2007(0, 0, "", false))
			case "GetWorkerPromptLint":
				return promptLint2007("rec9", 5, "null")
			}
			return ""
		}, `{"items":[],"total":0,"nextCursor":null}`, "null",
			[]string{"prompt lint: unavailable (no conclusion about warnings)"}, []string{"no warnings for the compiled", "nothing served"}, true, 2},
		{"signature lint null on a visible template: unavailable", visibleWorker2007, func(op string, v map[string]any) string {
			switch op {
			case "GetWorkerSignatureLint":
				return sigLint2007("null")
			case "GetWorkerPromptLint":
				return promptLint2007("", 0, "null")
			}
			return ""
		}, "null", "null", []string{"signature lint: unavailable (no conclusion about warnings)"}, []string{"no warnings for the signature"}, true, 2},
		{"older server: validation error is unavailable, the command works", visibleWorker2007, func(string, map[string]any) string { return "" },
			"null", "null", []string{"signature lint: unavailable", "prompt lint: unavailable"}, []string{"no warnings for"}, true, 2},
		{"graphql error: unavailable, exit 0", visibleWorker2007, func(op string, v map[string]any) string {
			if strings.Contains(op, "Lint") {
				return gqlErrorJSON("INTERNAL_SERVER_ERROR")
			}
			return ""
		}, "null", "null", []string{"signature lint: unavailable", "prompt lint: unavailable"}, []string{"no warnings for"}, true, 2},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fake, jout, jerr, err := runWorkerGet2007(t, tc.worker, tc.h, "--json")
			if err != nil {
				t.Fatalf("a lint outcome must never fail the command: %v", err)
			}
			keys := rawKeys2007(t, jout)
			for k, want := range map[string]string{"signatureLint": tc.wantSig, "promptLintWarnings": tc.wantPrompt} {
				if got, ok := keys[k]; !ok || compact1996(string(got)) != want {
					t.Errorf("%s = %s (present %v), want %s", k, got, ok, want)
				}
			}
			if n := len(fake.lintOps()); n != tc.lintOps {
				t.Errorf("lint ops sent = %d, want %d", n, tc.lintOps)
			}
			if got := strings.Count(jerr, lintwarn.UnavailableClass); (got == 1) != tc.stderr || got > 1 {
				t.Errorf("stderr class count = %d (want one: %v): %q", got, tc.stderr, jerr)
			}
			_, text, _, err := runWorkerGet2007(t, tc.worker, tc.h)
			if err != nil {
				t.Fatal(err)
			}
			for _, w := range tc.wantText {
				if !strings.Contains(text, w) {
					t.Errorf("text missing %q:\n%s", w, text)
				}
			}
			for _, w := range tc.notText {
				if strings.Contains(text, w) {
					t.Errorf("text must not contain %q:\n%s", w, text)
				}
			}
		})
	}
}

// An open session that is quiet still has a record, so its warnings show: the
// state comes from servedPrompt { id }, never from hasLiveSession.
func TestWorkerGetLint2007IdleOpenSessionStillShowsWarnings(t *testing.T) {
	idle := strings.Replace(visibleWorker2007, `"hasLiveSession":null`, `"hasLiveSession":false`, 1)
	_, out, _, err := runWorkerGet2007(t, idle, func(op string, v map[string]any) string {
		switch op {
		case "GetWorkerSignatureLint":
			return sigLint2007(lintPage2007(0, 0, "", false))
		case "GetWorkerPromptLint":
			return promptLint2007("rec9", 5, lintPage2007(1, 1, "", true))
		}
		return ""
	})
	if err != nil || !strings.Contains(out, "BROKEN_INCLUDE occurrence #0") || strings.Contains(out, "nothing served") {
		t.Fatalf("idle-but-open session must show its warnings: %v\n%s", err, out)
	}
}

// Every new lint op asks for 20 explicitly and follows the cursor to the end.
func TestWorkerGetLint2007PagesBeyondTwentyToTheEnd(t *testing.T) {
	cursors := map[string]string{"": "c1", "c1": "c2", "c2": ""}
	sizes := map[string]int{"": 20, "c1": 20, "c2": 5}
	page := func(v map[string]any, compiled bool) string {
		a := after2007(v)
		return lintPage2007(45, sizes[a], cursors[a], compiled)
	}
	fake, jout, _, err := runWorkerGet2007(t, visibleWorker2007, func(op string, v map[string]any) string {
		switch op {
		case "GetWorkerSignatureLint":
			return sigLint2007(page(v, false))
		case "GetWorkerPromptLint":
			return promptLint2007("rec9", 5, page(v, true))
		}
		return ""
	}, "--json")
	if err != nil {
		t.Fatal(err)
	}
	var got getJSON2007
	_ = json.Unmarshal([]byte(jout), &got)
	if got.SignatureLint == nil || len(got.SignatureLint.Items) != 45 || got.PromptLintWarnings == nil || len(got.PromptLintWarnings.Items) != 45 {
		t.Fatalf("every page must be read to the end: %s", jout)
	}
	for _, op := range []string{"GetWorkerSignatureLint", "GetWorkerPromptLint"} {
		cs := fake.ops(op)
		if len(cs) != 3 {
			t.Fatalf("%s sent %d times, want 3 pages", op, len(cs))
		}
		for i, want := range []string{"", "c1", "c2"} {
			if after2007(cs[i].Vars) != want {
				t.Errorf("%s page %d after = %q, want %q", op, i, after2007(cs[i].Vars), want)
			}
		}
		for _, c := range cs {
			if !strings.Contains(c.Query, "first: 20") || strings.Contains(c.Query, "first: 100") || strings.Contains(c.Query, "first: 50") {
				t.Errorf("%s must send first: 20 explicitly: %s", op, c.Query)
			}
		}
	}
}

// Identity and total checks: nothing partial, nothing from another record.
func TestWorkerGetLint2007IdentityAndTotalShiftsAreUnavailable(t *testing.T) {
	cases := []struct {
		name string
		h    handler2007
	}{
		{"prompt: the served record changes between pages", func(op string, v map[string]any) string {
			switch op {
			case "GetWorkerSignatureLint":
				return sigLint2007(lintPage2007(0, 0, "", false))
			case "GetWorkerPromptLint":
				if after2007(v) == "" {
					return promptLint2007("rec9", 5, lintPage2007(2, 1, "c1", true))
				}
				return promptLint2007("rec10", 5, lintPage2007(2, 1, "", true))
			}
			return ""
		}},
		{"prompt: the provenance revision changes between pages", func(op string, v map[string]any) string {
			switch op {
			case "GetWorkerSignatureLint":
				return sigLint2007(lintPage2007(0, 0, "", false))
			case "GetWorkerPromptLint":
				if after2007(v) == "" {
					return promptLint2007("rec9", 5, lintPage2007(2, 1, "c1", true))
				}
				return promptLint2007("rec9", 6, lintPage2007(2, 1, "", true))
			}
			return ""
		}},
		{"prompt: the lint total drifts between pages", func(op string, v map[string]any) string {
			switch op {
			case "GetWorkerSignatureLint":
				return sigLint2007(lintPage2007(0, 0, "", false))
			case "GetWorkerPromptLint":
				if after2007(v) == "" {
					return promptLint2007("rec9", 5, lintPage2007(2, 1, "c1", true))
				}
				return promptLint2007("rec9", 5, lintPage2007(3, 2, "", true))
			}
			return ""
		}},
		{"prompt: the final page is short", func(op string, v map[string]any) string {
			switch op {
			case "GetWorkerSignatureLint":
				return sigLint2007(lintPage2007(0, 0, "", false))
			case "GetWorkerPromptLint":
				return promptLint2007("rec9", 5, lintPage2007(5, 2, "", true))
			}
			return ""
		}},
		{"signature: another worker's lint", func(op string, v map[string]any) string {
			switch op {
			case "GetWorkerSignatureLint":
				return `{"data":{"worker":{"id":"someone-else","signatureLint":` + lintPage2007(1, 1, "", false) + `}}}`
			case "GetWorkerPromptLint":
				return promptLint2007("", 0, "null")
			}
			return ""
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, jout, jerr, err := runWorkerGet2007(t, visibleWorker2007, tc.h, "--json")
			if err != nil {
				t.Fatal(err)
			}
			var got getJSON2007
			_ = json.Unmarshal([]byte(jout), &got)
			if strings.HasPrefix(tc.name, "prompt") && got.PromptLintWarnings != nil {
				t.Errorf("prompt lint must be null, never partial: %s", jout)
			}
			if strings.HasPrefix(tc.name, "signature") && got.SignatureLint != nil {
				t.Errorf("signature lint of another worker must be null: %s", jout)
			}
			if !strings.Contains(jerr, lintwarn.UnavailableClass) {
				t.Errorf("an unavailable lint prints the fixed class: %q", jerr)
			}
		})
	}
}

// The lint reads go out together with the six existing reads, and carry no
// session header, while the rendered-signature read does (the same caller as
// the worker row the command shows).
func TestWorkerGetLint2007ReadsGoOutTogetherWithoutSession(t *testing.T) {
	teamGitDir(t)
	writeTeamBinding(t)
	reads := []string{"GetWorkerProfile", "GetWorkerTerminal", "GetWorkerModelGroups", "GetWorkerRendered", "GetWorkerAvailability", "GetWorkerBootPromptServe", "GetWorkerSignatureLint", "GetWorkerPromptLint"}
	rv := newRendezvous(t, reads...)
	srv, fake := newLintFake2007(t, func(op string, v map[string]any) string {
		for _, r := range reads {
			if op == r {
				rv.arrive(op)
			}
		}
		switch op {
		case "GetWorker":
			return `{"data":{"worker":` + visibleWorker2007 + `}}`
		case "GetWorkerProfile":
			return profileNames
		case "GetWorkerSignatureLint":
			return sigLint2007(lintPage2007(0, 0, "", false))
		case "GetWorkerPromptLint":
			return promptLint2007("", 0, "null")
		}
		return ""
	})
	rewriteBinding1592(t, func(b map[string]any) { b["workerId"] = profileHexID })
	setTeamBindingServer(t, srv.URL)
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"team", "worker", "get", workerRef2007, "--server", srv.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v\n%s", err, out)
	}
	for _, op := range reads {
		if n := len(fake.ops(op)); n != 1 {
			t.Errorf("%s sent %d times, want once", op, n)
		}
	}
	if r := fake.ops("GetWorkerRendered"); len(r) != 1 || r[0].Session != "s-new" {
		t.Fatalf("rendered read must carry the bound session (control): %+v", r)
	}
	for _, c := range fake.lintOps() {
		if c.Session != "" {
			t.Errorf("worker get lint reads carry no session header: %+v", c)
		}
	}
}

// --- worker update -----------------------------------------------------------

var updatedVisible2007 = strings.Replace(updatedWorkerJSON, `"id":"wkr1"`, `"signature":"s","trailer":null,"id":"wkr1"`, 1)

func runWorkerUpdate2007(t *testing.T, bound bool, h handler2007, argv ...string) (*lintFake2007, string, string, error) {
	t.Helper()
	if bound {
		writeTeamBinding(t)
	} else {
		teamGitDir(t)
	}
	srv, fake := newLintFake2007(t, func(op string, v map[string]any) string {
		switch op {
		case "UpdateWorkerSigning":
			return `{"data":{"updateWorker":` + updatedVisible2007 + `}}`
		case "GetWorker":
			return `{"data":{"worker":` + updatedVisible2007 + `}}`
		}
		return h(op, v)
	})
	if bound {
		setTeamBindingServer(t, srv.URL)
	}
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs(append([]string{"team", "worker", "update", "hrn:worker:acme.com:eng-team:iris", "--signature", "s", "--server", srv.URL}, argv...))
	err := root.Execute()
	return fake, out.String(), f.IOStreams.ErrOut.(*strings.Builder).String(), err
}

func updateLint2007(page string) string {
	return `{"data":{"worker":{"id":"wkr1","signatureLint":` + page + `}}}`
}

func TestWorkerUpdateLint2007ShownAsCurrentCheckAfterTheWrite(t *testing.T) {
	h := func(op string, v map[string]any) string {
		if op == "GetWorkerSignatureLint" {
			return updateLint2007(lintPage2007(1, 1, "", false))
		}
		return ""
	}
	fake, out, errOut, err := runWorkerUpdate2007(t, true, h)
	if err != nil || errOut != "" {
		t.Fatalf("err=%v stderr=%q", err, errOut)
	}
	if !strings.Contains(out, "signature lint (current check): 1 (the signature and trailer)") || !strings.Contains(out, "WORKER_LINK_MISSING signature:") {
		t.Errorf("update text:\n%s", out)
	}
	w, l := fake.ops("UpdateWorkerSigning"), fake.ops("GetWorkerSignatureLint")
	if len(w) != 1 || len(l) != 1 || w[0].Session != "s-new" || l[0].Session != w[0].Session {
		t.Fatalf("the lint read carries the write's session: write=%+v lint=%+v", w, l)
	}
	idx := func(op string) int {
		for i, c := range fake.calls {
			if c.Op == op {
				return i
			}
		}
		return -1
	}
	if idx("GetWorkerSignatureLint") < idx("UpdateWorkerSigning") {
		t.Error("the lint is read AFTER the write, as the current check, not a write-time snapshot")
	}
	_, jout, _, _ := runWorkerUpdate2007(t, false, h, "--json")
	var got getJSON2007
	if e := json.Unmarshal([]byte(jout), &got); e != nil || got.SignatureLint == nil || got.SignatureLint.Total != 1 {
		t.Fatalf("receipt json: %v\n%s", e, jout)
	}
	if _, ok := rawKeys2007(t, jout)["promptLintWarnings"]; ok {
		t.Error("update carries signatureLint only")
	}
}

func TestWorkerUpdateLint2007FailedLintNeverFailsAWrittenEdit(t *testing.T) {
	for name, resp := range map[string]string{
		"older server":  "",
		"graphql error": gqlErrorJSON("INTERNAL_SERVER_ERROR"),
		"null page":     updateLint2007("null"),
		"other worker":  `{"data":{"worker":{"id":"other","signatureLint":` + lintPage2007(1, 1, "", false) + `}}}`,
	} {
		t.Run(name, func(t *testing.T) {
			_, jout, errOut, err := runWorkerUpdate2007(t, true, func(op string, v map[string]any) string {
				if op == "GetWorkerSignatureLint" {
					return resp
				}
				return ""
			}, "--json")
			if err != nil || exitCodeFor(err) != exitcode.OK {
				t.Fatalf("a failed lint read after a successful write exits 0: %v", err)
			}
			keys := rawKeys2007(t, jout)
			if string(keys["signatureLint"]) != "null" || keys["id"] == nil || keys["signature"] == nil {
				t.Errorf("receipt intact, signatureLint null: %s", jout)
			}
			if strings.Count(errOut, lintwarn.UnavailableClass) != 1 {
				t.Errorf("one fixed class on stderr: %q", errOut)
			}
		})
	}
}

// A masked receipt (no signature in the write's result) reads nothing and claims nothing.
func TestWorkerUpdateLint2007MaskedReceiptReadsNothing(t *testing.T) {
	teamGitDir(t)
	srv, fake := newLintFake2007(t, func(op string, v map[string]any) string {
		switch op {
		case "UpdateWorkerSigning":
			return `{"data":{"updateWorker":` + updatedWorkerJSON + `}}`
		case "GetWorker":
			return `{"data":{"worker":` + updatedWorkerJSON + `}}`
		}
		return ""
	})
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"team", "worker", "update", "hrn:worker:acme.com:eng-team:iris", "--signature", "s", "--json", "--server", srv.URL})
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	if n := len(fake.lintOps()); n != 0 {
		t.Errorf("a masked receipt sends no lint read, sent %d", n)
	}
	if string(rawKeys2007(t, out.String())["signatureLint"]) != "null" {
		t.Errorf("masked is null: %s", out)
	}
}

// --- unchanged shapes --------------------------------------------------------

func TestWorkerLint2007UnrelatedCommandsSendNoLintOp(t *testing.T) {
	teamGitDir(t)
	srv, fake := newLintFake2007(t, func(op string, v map[string]any) string {
		if op == "Workers" || op == "WorkersRoster" {
			return `{"data":{"workers":{"total":0,"items":[]}}}`
		}
		return ""
	})
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"team", "worker", "list", "--app", "capp100000000000000000000", "--json", "--server", srv.URL})
	_ = root.Execute()
	if n := len(fake.lintOps()); n != 0 {
		t.Errorf("worker list must not read lint, sent %d", n)
	}
}
