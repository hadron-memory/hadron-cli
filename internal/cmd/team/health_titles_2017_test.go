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
}

func (o *fakeOrca2017) run(_ context.Context, argv []string) orcaterm.Result {
	o.mu.Lock()
	defer o.mu.Unlock()
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
		if handle == o.failOn {
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
	return &fakeOrca2017{terminals: []map[string]any{
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
	oldRunner, oldBound := orcaRunner, healthBoundWorker
	orcaRunner = func(string) orcaterm.Runner { return o.run }
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
		_ = json.NewDecoder(r.Body).Decode(&q)
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
	path := filepath.Join(dir, "host.yaml")
	if e := os.WriteFile(path, []byte(cfg), 0600); e != nil {
		t.Fatal(e)
	}
	f := cmdutil.NewFactory()
	streams, out, _ := output.Test()
	f.IOStreams = streams
	f.JSON = true
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
	o.failOn = "h1"
	out, _, err := title2017Run(t, o, "Iris", "--dry-run=false")
	if exitcode.FromError(err) != exitcode.Error {
		t.Fatalf("want exit 1, got %v", err)
	}
	if len(o.renames) != 2 {
		t.Fatalf("later tab skipped after a failure: %v", o.renames)
	}
	r := decodeTitleReceipt(t, out)
	if len(r.Renames) != 2 || r.Renames[0].Error == nil || r.Renames[0].Applied || r.Renames[1].Error != nil || !r.Renames[1].Applied {
		t.Fatalf("receipt: %+v", r)
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
