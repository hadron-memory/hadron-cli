package team

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/cmdutil"
	"github.com/hadron-memory/hadron-cli/internal/exitcode"
	"github.com/hadron-memory/hadron-cli/internal/hostpoller"
	"github.com/hadron-memory/hadron-cli/internal/orcaterm"
	"github.com/hadron-memory/hadron-cli/internal/output"
)

const title2017Status = `[{"contract":"terminal-status@1","worker":"Iris","status":"idle","prompt":"empty","reason":null,"lastTurnAt":null,"checkedAt":"2026-10-31T02:00:00Z"},` +
	`{"contract":"terminal-status@1","worker":"Otto","status":"idle","prompt":"empty","reason":null,"lastTurnAt":null,"checkedAt":"2026-10-31T02:00:00Z"}]`

func title2017Regs(irisName string) string {
	return `{"data":{"hostRegistrations":[{"hostId":"own","live":true,"workers":[` +
		`{"workerId":"w1","worker":{"id":"w1","name":"` + irisName + `","urn":"hrn:worker:test:team:iris"},"worktrees":["iris"]},` +
		`{"workerId":"w2","worker":{"id":"w2","name":"Otto","urn":"hrn:worker:test:team:otto"},"worktrees":["otto"]}]}]}}`
}

// fakeOrca2017 is a stateful Orca: list reports the current titles, rename
// changes them, so a second sweep sees what the first one did.
type fakeOrca2017 struct {
	mu        sync.Mutex
	terminals []map[string]any
	renames   [][]string
	failList  bool
	failOn    string
	failBody  string // the rename failure answer; default is a stale-handle code
	// cli#2166: bins records the binary each runner was built for; version is
	// what `orca --version` prints (failVersion makes it fail); orcaBin, when
	// set, becomes delivery.orca_bin in the host config.
	bins        []string
	version     string
	failVersion bool
	orcaBin     string
}

func (o *fakeOrca2017) run(_ context.Context, argv []string) orcaterm.Result {
	o.mu.Lock()
	defer o.mu.Unlock()
	if len(argv) == 1 && argv[0] == "--version" {
		if o.failVersion {
			return orcaterm.Result{Code: 1, Stderr: "boom"}
		}
		return orcaterm.Result{Stdout: o.version + "\n"}
	}
	switch strings.Join(argv[:2], " ") {
	case "terminal list":
		if o.failList {
			return orcaterm.Result{Code: 1}
		}
		raw, _ := json.Marshal(map[string]any{"ok": true, "result": map[string]any{"terminals": o.terminals}})
		return orcaterm.Result{Stdout: string(raw)}
	case "terminal rename":
		o.renames = append(o.renames, argv)
		var handle, title string
		for i, a := range argv {
			if a == "--terminal" {
				handle = argv[i+1]
			}
			if strings.HasPrefix(a, "--title=") {
				title = strings.TrimPrefix(a, "--title=")
			}
		}
		if strings.Contains(","+o.failOn+",", ","+handle+",") {
			if o.failBody != "" {
				return orcaterm.Result{Stdout: o.failBody}
			}
			return orcaterm.Result{Stdout: `{"ok":false,"error":{"code":"terminal_handle_stale"}}`}
		}
		for _, t := range o.terminals {
			if t["tabId"] == o.tabOf(handle) {
				t["title"] = title
			}
		}
		return orcaterm.Result{Stdout: `{"ok":true,"result":{}}`}
	}
	return orcaterm.Result{Code: 1, Stderr: "unexpected call"}
}

func (o *fakeOrca2017) tabOf(handle string) any {
	for _, t := range o.terminals {
		if t["handle"] == handle {
			return t["tabId"]
		}
	}
	return nil
}

func term2017(handle, tab, worktree, title string) map[string]any {
	return map[string]any{"handle": handle, "tabId": tab, "worktreePath": worktree, "title": title, "connected": true, "writable": true}
}

func newOrca2017() *fakeOrca2017 {
	return &fakeOrca2017{version: "1.4.247", terminals: []map[string]any{
		term2017("h1", "tab1", "/wt/iris", "Iris SHED recovery"),
		term2017("h1b", "tab1", "/wt/iris", "Iris SHED recovery"), // a second pane of the same tab
		term2017("h2", "tab2", "/wt/iris", "orca@host: ~/orca/workspaces"),
		term2017("h3", "tab3", "/wt/otto", "Otto"),
		term2017("h4", "tab4", "/wt/plain", "scratch shell"),
		term2017("h5", "tab5", "/wt/stranger", "Stranger [Role]"),
	}}
}

func title2017Run(t *testing.T, o *fakeOrca2017, irisName string, args ...string) (string, int, error) {
	t.Helper()
	return title2017RunMode(t, o, irisName, true, args...)
}

// title2017RunMode is title2017Run with the output mode chosen: asJSON false
// drives the human (text) branch of the receipt.
func title2017RunMode(t *testing.T, o *fakeOrca2017, irisName string, asJSON bool, args ...string) (string, int, error) {
	t.Helper()
	oldRunner, oldBound := orcaRunner, healthBoundWorker
	orcaRunner = func(bin string) orcaterm.Runner {
		o.mu.Lock()
		o.bins = append(o.bins, bin)
		o.mu.Unlock()
		return o.run
	}
	healthBoundWorker = func(wt string) string {
		return map[string]string{"/wt/iris": "w1", "/wt/otto": "w2", "/wt/stranger": "wX"}[wt]
	}
	t.Cleanup(func() { orcaRunner, healthBoundWorker = oldRunner, oldBound })
	dir := t.TempDir()
	key := "SYNTHETIC_HOST_KEY_2017"
	var mu sync.Mutex
	writes := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var q struct {
			OperationName string `json:"operationName"`
		}
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &q)
		// cli#2166: Orca's version is the local receipt's, never the platform's
		// (cor:agt:070:01 / :070:09 fix the host and sweep record shapes).
		if o.version != "" && strings.Contains(string(raw), o.version) {
			t.Errorf("Orca's version reached the server: %s", raw)
		}
		w.Header().Set("Content-Type", "application/json")
		switch q.OperationName {
		case "HostRegistrations":
			_, _ = io.WriteString(w, title2017Regs(irisName))
		case "HealthSweepSetTerminals":
			mu.Lock()
			writes++
			mu.Unlock()
			_, _ = io.WriteString(w, `{"data":{"setHostWorkerTerminals":{"stored":[{"id":"w1","urn":"u1"},{"id":"w2","urn":"u2"}],"refused":[]}}}`)
		default:
			t.Errorf("unexpected %s", q.OperationName)
		}
	}))
	defer srv.Close()
	for n, s := range map[string]string{"key": key, "state.json": `{"hostId":"own"}`} {
		if e := os.WriteFile(filepath.Join(dir, n), []byte(s), 0600); e != nil {
			t.Fatal(e)
		}
	}
	cfg := fmt.Sprintf("server: %q\napp: hrn:app:test:team\nkey_file: key\nstate_file: state.json\nworktrees_root: %q\nworkers:\n  w1:\n    family: codex\n    launch: [fake]\n    worktrees: [iris]\n  w2:\n    family: codex\n    launch: [fake]\n    worktrees: [otto]\n", srv.URL, dir)
	if o.orcaBin != "" {
		cfg += fmt.Sprintf("delivery:\n  orca_bin: %q\n", o.orcaBin)
	}
	path := filepath.Join(dir, "host.yaml")
	if e := os.WriteFile(path, []byte(cfg), 0600); e != nil {
		t.Fatal(e)
	}
	f := cmdutil.NewFactory()
	streams, out, _ := output.Test()
	f.IOStreams = streams
	f.JSON = asJSON
	c := newCmdTeamHealthWithReader(f, func(context.Context, string, *hostpoller.Config) ([]byte, int, error) {
		return []byte(strings.Replace(title2017Status, `"worker":"Iris"`, `"worker":"`+irisName+`"`, 1)), 0, nil
	})
	c.SetArgs(append([]string{"sweep", "--config", path}, args...))
	err := c.Execute()
	mu.Lock()
	defer mu.Unlock()
	return out.String(), writes, err
}

func renamedHandles(o *fakeOrca2017) []string {
	got := []string{}
	for _, argv := range o.renames {
		for i, a := range argv {
			if a == "--terminal" {
				got = append(got, argv[i+1]+"="+strings.TrimPrefix(argv[i+2], "--title="))
			}
		}
	}
	return got
}

type titleReceipt2017 struct {
	DryRun  bool           `json:"dryRun"`
	Renames []healthRename `json:"renames"`
}

func decodeTitleReceipt(t *testing.T, out string) titleReceipt2017 {
	t.Helper()
	var r titleReceipt2017
	if err := json.Unmarshal([]byte(out), &r); err != nil {
		t.Fatalf("%v: %s", err, out)
	}
	return r
}

// A manual run changes nothing, lists what it would rename, and still stores
// the status batch (the dry run covers only the title pass).
func TestHealthTitlesManualRunIsDryRun2017(t *testing.T) {
	o := newOrca2017()
	out, writes, err := title2017Run(t, o, "Iris")
	if err != nil || writes != 1 {
		t.Fatalf("err=%v writes=%d", err, writes)
	}
	if len(o.renames) != 0 {
		t.Fatalf("dry run renamed: %v", o.renames)
	}
	r := decodeTitleReceipt(t, out)
	if !r.DryRun || len(r.Renames) != 2 {
		t.Fatalf("receipt: %+v", r)
	}
	for _, x := range r.Renames {
		if x.Applied || x.To != "Iris" || x.Name != "Iris" || x.Worker != "w1" || x.Error != nil {
			t.Fatalf("rename entry: %+v", x)
		}
	}
}

// An applying sweep sets every tab of a worker's worktree to the bare name,
// once per tab, and touches no tab without a registered worker. A second sweep
// renames nothing.
func TestHealthTitlesApplyAndIdempotent2017(t *testing.T) {
	o := newOrca2017()
	out, _, err := title2017Run(t, o, "Iris", "--dry-run=false")
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(renamedHandles(o), ","); got != "h1=Iris,h2=Iris" {
		t.Fatalf("renames: %s (%v)", got, o.renames)
	}
	r := decodeTitleReceipt(t, out)
	if r.DryRun || len(r.Renames) != 2 || !r.Renames[0].Applied || !r.Renames[1].Applied {
		t.Fatalf("receipt: %+v", r)
	}
	o.renames = nil
	out, _, err = title2017Run(t, o, "Iris", "--dry-run=false")
	if err != nil || len(o.renames) != 0 {
		t.Fatalf("second sweep renamed: err=%v %v", err, o.renames)
	}
	if r = decodeTitleReceipt(t, out); len(r.Renames) != 0 {
		t.Fatalf("second receipt: %+v", r)
	}
	for _, tm := range o.terminals {
		switch tm["handle"] {
		case "h4":
			if tm["title"] != "scratch shell" {
				t.Fatalf("plain shell touched: %v", tm)
			}
		case "h5":
			if tm["title"] != "Stranger [Role]" {
				t.Fatalf("unregistered worker's tab touched: %v", tm)
			}
		}
	}
}

// The title is the scrubbed bare name, never the raw one.
func TestHealthTitlesUseScrubbedName2017(t *testing.T) {
	o := newOrca2017()
	if _, _, err := title2017Run(t, o, "Ir[i,s]", "--dry-run=false"); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(renamedHandles(o), ","); got != "h1=Ir i s,h2=Ir i s" {
		t.Fatalf("renames: %s", got)
	}
}

// One tab failing to rename does not stop the others, and the sweep says so.
func TestHealthTitlesRenameFailureIsReported2017(t *testing.T) {
	o := newOrca2017()
	o.failOn = "h1,h1b" // both panes of tab1 are stale
	out, _, err := title2017Run(t, o, "Iris", "--dry-run=false")
	if exitcode.FromError(err) != exitcode.Error {
		t.Fatalf("want exit 1, got %v", err)
	}
	if len(o.renames) != 3 {
		t.Fatalf("later tab skipped after a failure: %v", o.renames)
	}
	r := decodeTitleReceipt(t, out)
	if len(r.Renames) != 3 || r.Renames[0].Error == nil || r.Renames[1].Error == nil || r.Renames[0].Applied || r.Renames[2].Error != nil || !r.Renames[2].Applied {
		t.Fatalf("receipt: %+v", r)
	}
}

// One stale pane handle must not hide the tab: the sibling pane's working
// handle is tried, and a tab that ended up renamed is not a failed sweep.
func TestHealthTitlesFailedPaneFallsThroughToSibling2017(t *testing.T) {
	o := newOrca2017()
	o.failOn = "h1"
	out, _, err := title2017Run(t, o, "Iris", "--dry-run=false")
	if err != nil {
		t.Fatalf("tab1 was renamed through h1b, want exit 0, got %v", err)
	}
	r := decodeTitleReceipt(t, out)
	if len(r.Renames) != 3 || r.Renames[0].Error == nil || !r.Renames[1].Applied || r.Renames[1].Terminal != "h1b" {
		t.Fatalf("receipt: %+v", r)
	}
}

// A host with no delivery section still has orca on PATH (install accepts
// either), so an empty delivery.orca_bin runs "orca", never an empty name.
func TestHealthOrcaBinFallsBackToPath2017(t *testing.T) {
	if got := healthOrcaBin(&hostpoller.Config{}); got != "orca" {
		t.Fatalf("empty orca_bin -> %q", got)
	}
	cfg := &hostpoller.Config{}
	cfg.Delivery.OrcaBin = fakeExecutable2166(t)
	if got := healthOrcaBin(cfg); got != cfg.Delivery.OrcaBin {
		t.Fatalf("configured orca_bin -> %q", got)
	}
}

// No registered worker means nothing to retitle and no Orca call to fail.
func TestHealthTitlesNoWorkersMakesNoOrcaCall2017(t *testing.T) {
	old := orcaRunner
	orcaRunner = func(string) orcaterm.Runner {
		t.Error("orca was reached with no registered workers")
		return func(context.Context, []string) orcaterm.Result { return orcaterm.Result{Code: 1} }
	}
	t.Cleanup(func() { orcaRunner = old })
	got, err := healthTitles(context.Background(), &hostpoller.Config{}, map[string]string{}, true)
	if err != nil || got == nil || len(got) != 0 {
		t.Fatalf("got %v, %v", got, err)
	}
}

// A terminal list that fails after the status batch was stored is a failed
// sweep, not a silent clean one; the stored batch stands.
func TestHealthTitlesListFailureFailsSweep2017(t *testing.T) {
	o := newOrca2017()
	o.failList = true
	_, writes, err := title2017Run(t, o, "Iris", "--dry-run=false")
	if exitcode.FromError(err) != exitcode.Error || writes != 1 {
		t.Fatalf("err=%v writes=%d", err, writes)
	}
}

// stubNoTabs gives a sweep test an Orca with no terminals, so the title pass
// has nothing to do and does not reach for a real Orca.
func stubNoTabs(t *testing.T) {
	t.Helper()
	old := orcaRunner
	orcaRunner = func(string) orcaterm.Runner {
		return func(context.Context, []string) orcaterm.Result {
			return orcaterm.Result{Stdout: `{"ok":true,"result":{"terminals":[]}}`}
		}
	}
	t.Cleanup(func() { orcaRunner = old })
}

// cli#2040 item 1: the text (non-JSON) branch of the title pass. Every test
// above runs JSON mode, so the three verbs were unpinned.
func TestHealthTitlesTextModeVerbs2040(t *testing.T) {
	line := func(out, name, verb string) string {
		for _, l := range strings.Split(out, "\n") {
			if strings.HasPrefix(l, "title  "+name+"  "+verb) {
				return l
			}
		}
		return ""
	}
	// dry run: would rename, no orca note
	o := newOrca2017()
	out, _, err := title2017RunMode(t, o, "Iris", false)
	if err != nil || len(o.renames) != 0 {
		t.Fatalf("err=%v renames=%v", err, o.renames)
	}
	if l := line(out, "Iris", "would rename"); !strings.Contains(l, `"Iris SHED recovery" -> "Iris"`) {
		t.Fatalf("dry-run line missing in:\n%s", out)
	}
	// applied: renamed
	o = newOrca2017()
	out, _, err = title2017RunMode(t, o, "Iris", false, "--dry-run=false")
	if err != nil {
		t.Fatal(err)
	}
	if line(out, "Iris", "renamed") == "" || strings.Contains(out, "would rename") || strings.Contains(out, "rename failed") {
		t.Fatalf("applied lines wrong:\n%s", out)
	}
	// failed with an Orca code: the code is shown beside the constant
	o = newOrca2017()
	o.failOn = "h1,h1b"
	out, _, err = title2017RunMode(t, o, "Iris", false, "--dry-run=false")
	if exitcode.FromError(err) != exitcode.Error {
		t.Fatalf("want exit 1, got %v", err)
	}
	if l := line(out, "Iris", "rename failed (rename_failed; orca: terminal_handle_stale)"); l == "" {
		t.Fatalf("failure line wrong:\n%s", out)
	}
	// failed with no Orca code: the constant alone, no empty orca note
	o = newOrca2017()
	o.failOn = "h1,h1b"
	o.failBody = "not json"
	out, _, _ = title2017RunMode(t, o, "Iris", false, "--dry-run=false")
	if line(out, "Iris", "rename failed (rename_failed)") == "" || strings.Contains(out, "orca:") {
		t.Fatalf("code-less failure line wrong:\n%s", out)
	}
}

// cli#2040 item 2: orcaCode rides beside the unchanged error constant.
func TestHealthTitlesReceiptCarriesOrcaCode2040(t *testing.T) {
	o := newOrca2017()
	o.failOn = "h1,h1b"
	out, _, _ := title2017Run(t, o, "Iris", "--dry-run=false")
	r := decodeTitleReceipt(t, out)
	failed, ok := r.Renames[0], r.Renames[2]
	if failed.Error == nil || *failed.Error != "rename_failed" {
		t.Fatalf("error must stay the constant: %+v", failed)
	}
	if failed.OrcaCode == nil || *failed.OrcaCode != "terminal_handle_stale" {
		t.Fatalf("orcaCode: %+v", failed)
	}
	if ok.OrcaCode != nil {
		t.Fatalf("a renamed tab has no orcaCode: %+v", ok)
	}
	// the wire key is present (null) on every row, never omitted
	if !strings.Contains(out, `"orcaCode": null`) && !strings.Contains(out, `"orcaCode":null`) {
		t.Fatalf("orcaCode must render null when absent:\n%s", out)
	}
	// no code from Orca: null, error unchanged
	o = newOrca2017()
	o.failOn = "h1,h1b"
	o.failBody = "not json"
	out, _, _ = title2017Run(t, o, "Iris", "--dry-run=false")
	r = decodeTitleReceipt(t, out)
	if r.Renames[0].Error == nil || r.Renames[0].OrcaCode != nil {
		t.Fatalf("code-less failure: %+v", r.Renames[0])
	}
}

func fakeExecutable2166(t *testing.T) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "orca")
	if err := os.WriteFile(p, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	return p
}

// cli#2166 item 1: the helper pin alone let the call site drift. The runner
// factory must receive the RESOLVED binary: "orca" when nothing is configured,
// the configured path when it resolves. Reverting healthTitles to
// cfg.Delivery.OrcaBin passes an empty name here and fails this test.
func TestHealthTitlesRunnerReceivesResolvedBin2166(t *testing.T) {
	o := newOrca2017()
	if _, _, err := title2017Run(t, o, "Iris", "--dry-run=false"); err != nil {
		t.Fatal(err)
	}
	if len(o.bins) == 0 {
		t.Fatal("the title pass never built a runner")
	}
	for _, b := range o.bins {
		if b != "orca" {
			t.Fatalf("no orca_bin configured: runner built for %q, want \"orca\"", o.bins)
		}
	}
	o = newOrca2017()
	o.orcaBin = fakeExecutable2166(t)
	if _, _, err := title2017Run(t, o, "Iris", "--dry-run=false"); err != nil {
		t.Fatal(err)
	}
	for _, b := range o.bins {
		if b != o.orcaBin {
			t.Fatalf("configured orca_bin: runner built for %q, want %q", o.bins, o.orcaBin)
		}
	}
}

// cli#2166 item 2: a configured delivery.orca_bin that no longer resolves (gone,
// a directory, not executable) falls back to "orca" on PATH rather than failing
// every sweep with an exec error; a resolving one is used as written.
func TestHealthOrcaBinUnresolvableFallsBack2166(t *testing.T) {
	dir := t.TempDir()
	notExec := filepath.Join(dir, "orca-noexec")
	if err := os.WriteFile(notExec, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	for name, bin := range map[string]string{
		"missing": filepath.Join(dir, "gone"), "directory": dir, "not executable": notExec,
	} {
		cfg := &hostpoller.Config{}
		cfg.Delivery.OrcaBin = bin
		if got := healthOrcaBin(cfg); got != "orca" {
			t.Errorf("%s: orca_bin %q -> %q, want the PATH fallback", name, bin, got)
		}
	}
}

// cli#2166 item 3: the receipt carries Orca's version (local only; the helper
// fails the test if it reaches the server), and a failing or empty version call
// never fails the sweep.
func TestHealthTitlesReceiptCarriesOrcaVersion2166(t *testing.T) {
	o := newOrca2017()
	out, _, err := title2017Run(t, o, "Iris", "--dry-run=false")
	if err != nil {
		t.Fatal(err)
	}
	var r struct {
		OrcaVersion *string `json:"orcaVersion"`
	}
	if e := json.Unmarshal([]byte(out), &r); e != nil || r.OrcaVersion == nil || *r.OrcaVersion != "1.4.247" {
		t.Fatalf("orcaVersion: %v %+v\n%s", e, r, out)
	}
	for name, mod := range map[string]func(*fakeOrca2017){
		"fails": func(o *fakeOrca2017) { o.failVersion = true },
		"empty": func(o *fakeOrca2017) { o.version = "" },
	} {
		o := newOrca2017()
		mod(o)
		out, _, err := title2017Run(t, o, "Iris", "--dry-run=false")
		if err != nil {
			t.Fatalf("%s: a version failure failed the sweep: %v", name, err)
		}
		if !strings.Contains(out, `"orcaVersion": null`) && !strings.Contains(out, `"orcaVersion":null`) {
			t.Fatalf("%s: orcaVersion must render null:\n%s", name, out)
		}
	}
	text, _, err := title2017RunMode(t, newOrca2017(), "Iris", false, "--dry-run=false")
	if err != nil || !strings.Contains(text, "orca  1.4.247") {
		t.Fatalf("text receipt lacks the version line: %v\n%s", err, text)
	}
}

// Only a version-shaped first line is carried: control bytes, an error body or
// an over-long token are no answer (null), and a plain build-suffixed version
// is kept whole.
func TestHealthTitlesOrcaVersionMustLookLikeOne2166(t *testing.T) {
	for in, want := range map[string]string{
		"1.4.247":               "1.4.247",
		"v1.4.247+build.5":      "v1.4.247+build.5",
		"1.4.247\x1b[31m":       "",
		"{\"ok\":true}":         "",
		"orca 1.4.247":          "",
		strings.Repeat("1", 65): "",
	} {
		o := newOrca2017()
		o.version = in
		out, _, err := title2017Run(t, o, "Iris", "--dry-run=false")
		if err != nil {
			t.Fatal(err)
		}
		var r struct {
			OrcaVersion *string `json:"orcaVersion"`
		}
		if e := json.Unmarshal([]byte(out), &r); e != nil {
			t.Fatalf("%v %s", e, out)
		}
		got := ""
		if r.OrcaVersion != nil {
			got = *r.OrcaVersion
		}
		if got != want {
			t.Errorf("version %q -> %q, want %q", in, got, want)
		}
	}
}
