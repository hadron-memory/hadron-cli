package team

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/Khan/genqlient/graphql"

	"github.com/hadron-memory/hadron-cli/internal/exitcode"
	"github.com/hadron-memory/hadron-cli/internal/intake"
)

// cli#1962: the reconcile pass across RUNS. T is the server's opaque cursor in
// the CLI's store, so what a first run answers a second run never repeats, and
// a change the adapter refused is tried again. (Single-run mapping is pinned
// through the command in internal/cmd/find_untracked_reconcile_1962_test.go;
// a command test cannot span two runs because the cursor is keyed by server.)

// feedClient answers IntakeExternalIdChanges from a script, recording `after`.
type feedClient struct {
	pages []string // JSON "agentTaskExternalIdChanges" bodies, one per call
	after []string
	// holders answers IntakeActiveHolder: external id -> the ACTIVE task id that
	// holds it (absent = nobody). holderErr fails every lookup (cli#2265).
	holders     map[string]string
	holderErr   error
	holderCalls []string
}

func (c *feedClient) MakeRequest(_ context.Context, req *graphql.Request, resp *graphql.Response) error {
	if req.OpName == "IntakeActiveHolder" {
		b, _ := json.Marshal(req.Variables)
		var v struct {
			ExternalID string `json:"externalId"`
		}
		_ = json.Unmarshal(b, &v)
		c.holderCalls = append(c.holderCalls, v.ExternalID)
		if c.holderErr != nil {
			return c.holderErr
		}
		body := `[]`
		if id, ok := c.holders[v.ExternalID]; ok {
			body = fmt.Sprintf(`[{"id":%q}]`, id)
		}
		return json.Unmarshal([]byte(`{"agentTasks":`+body+`}`), resp.Data)
	}
	b, _ := json.Marshal(req.Variables)
	var v struct {
		After *string `json:"after"`
	}
	_ = json.Unmarshal(b, &v)
	a := "<omitted>"
	if v.After != nil {
		a = *v.After
	}
	c.after = append(c.after, a)
	if len(c.pages) == 0 {
		return fmt.Errorf("unscripted read")
	}
	page := c.pages[0]
	c.pages = c.pages[1:]
	return json.Unmarshal([]byte(`{"agentTaskExternalIdChanges":`+page+`}`), resp.Data)
}

func releasedChange(ext, cursor string) string {
	const kind = "RELEASED"
	return fmt.Sprintf(`{"cursor":%q,"kind":%q,"externalId":%q,"task":{"id":"t","handle":"AT1","portalUrl":null,"assignee":null}}`, cursor, kind, ext)
}

func page(changes ...string) string {
	return fmt.Sprintf(`{"changes":[%s],"hasMore":false,"nextCursor":null}`, strings.Join(changes, ","))
}

func scriptAdapter(t *testing.T, untrack string) (*intake.Runner, intake.Describe, string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("shell-script adapter fixtures need a POSIX shell")
	}
	dir := t.TempDir()
	log := filepath.Join(dir, "log")
	path := filepath.Join(dir, "adapter")
	script := "#!/bin/sh\nreq=$(cat)\nprintf '%s %s\\n' \"$1\" \"$req\" >> '" + log + "'\ncase \"$1\" in\nuntrack) " + untrack + ";;\n*) echo '{}';;\nesac\n"
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	desc := intake.Describe{Name: "fake", ContractVersion: 1, Calls: []string{"describe", "list-untracked", "mark-tracked", "untrack"}}
	return &intake.Runner{Path: path, Timeout: 5 * time.Second}, desc, log
}

func untrackCalls(t *testing.T, log string) int {
	t.Helper()
	b, _ := os.ReadFile(log)
	return strings.Count(string(b), "untrack ")
}

func TestReconcileSecondRunStartsAfterTheServersCursorAndIsIdempotent(t *testing.T) {
	r, desc, log := scriptAdapter(t, `echo '{}'`)
	store := &intake.CursorStore{Path: filepath.Join(t.TempDir(), "cursors.json")}
	key := intake.CursorKey("https://s", "app", "fake")

	first := &feedClient{pages: []string{page(releasedChange("https://x/1", "cur-A"), releasedChange("https://x/2", "cur-B"))}}
	d1, aerr, ferr := reconcile(context.Background(), first, store, key, "app", r, desc)
	if aerr != nil || ferr != nil || d1.Untracked != 2 || !d1.Complete {
		t.Fatalf("first run: %+v %v %v", d1, aerr, ferr)
	}
	if first.after[0] != "<omitted>" {
		t.Fatalf("no cursor yet, so after must be omitted: %v", first.after)
	}

	second := &feedClient{pages: []string{page()}}
	d2, aerr, ferr := reconcile(context.Background(), second, store, key, "app", r, desc)
	if aerr != nil || ferr != nil || d2.Read != 0 || !d2.Complete {
		t.Fatalf("second run: %+v %v %v", d2, aerr, ferr)
	}
	if second.after[0] != "cur-B" {
		t.Fatalf("the second run reads after the last answered change: %v", second.after)
	}
	if n := untrackCalls(t, log); n != 2 {
		t.Fatalf("nothing is answered twice: %d untrack calls", n)
	}
	// Another adapter of the same App has its own cursor and still sees everything.
	other := &feedClient{pages: []string{page()}}
	_, _, _ = reconcile(context.Background(), other, store, intake.CursorKey("https://s", "app", "other"), "app", r, desc)
	if other.after[0] != "<omitted>" {
		t.Fatalf("the cursor is per adapter: %v", other.after)
	}
}

func TestReconcileRefusedChangeIsRetriedByTheNextRun(t *testing.T) {
	r, desc, log := scriptAdapter(t, `case "$req" in *https://x/2*) echo nope >&2; exit 3;; *) echo '{}';; esac`)
	store := &intake.CursorStore{Path: filepath.Join(t.TempDir(), "cursors.json")}
	key := intake.CursorKey("https://s", "app", "fake")

	first := &feedClient{pages: []string{page(
		releasedChange("https://x/1", "c1"), releasedChange("https://x/2", "c2"), releasedChange("https://x/3", "c3"))}}
	d1, aerr, ferr := reconcile(context.Background(), first, store, key, "app", r, desc)
	if ferr != nil || aerr == nil || d1.Complete || d1.Error == nil || d1.Untracked != 1 {
		t.Fatalf("first run must stop at the refused change: %+v %v %v", d1, aerr, ferr)
	}
	if n := untrackCalls(t, log); n != 2 {
		t.Fatalf("c3 must not be tried past the failure: %d calls", n)
	}

	second := &feedClient{pages: []string{page(releasedChange("https://x/2", "c2"), releasedChange("https://x/3", "c3"))}}
	// Two calls in run one, then one retry refusal in run two: three total.
	d2, aerr, ferr := reconcile(context.Background(), second, store, key, "app", r, desc)
	if ferr != nil || aerr == nil || d2.Complete || d2.Error == nil || d2.Read != 1 || d2.Untracked != 0 || untrackCalls(t, log) != 3 {
		t.Fatalf("retry must stop at the same refused change: %+v %v %v", d2, aerr, ferr)
	}
	if second.after[0] != "c1" {
		t.Fatalf("the cursor must stay after the last answered change (c1): %v", second.after)
	}
}

// An empty page that still promises more is not "caught up".
func TestReconcileEmptyPageWithHasMoreIsNotComplete(t *testing.T) {
	r, desc, _ := scriptAdapter(t, `echo '{}'`)
	store := &intake.CursorStore{Path: filepath.Join(t.TempDir(), "cursors.json")}
	c := &feedClient{pages: []string{`{"changes":[],"hasMore":true,"nextCursor":null}`}}
	d, aerr, ferr := reconcile(context.Background(), c, store, "k", "app", r, desc)
	if aerr == nil || ferr != nil || d.Complete || d.Error == nil || len(c.after) != 1 {
		t.Fatalf("must fail loudly (exit 1) without spinning: %+v %v %v reads=%d", d, aerr, ferr, len(c.after))
	}
}

// Joost (cli#1975 LOCAL): two runs on ONE feed (a ticker and a hand run) must
// not overlap. Without a per-feed lock the second reads the same stored cursor
// while the first is still inside the adapter, repeats its effect, and a
// delayed older run can roll the cursor back over a newer one's.
func TestReconcileSameFeedRunsAreSerialized(t *testing.T) {
	r, desc, log := scriptAdapter(t, `: > "$(dirname "$0")/inA"; while [ ! -f "$(dirname "$0")/release" ]; do sleep 0.05; done; echo '{}'`)
	dir := filepath.Dir(r.Path)
	store := &intake.CursorStore{Path: filepath.Join(t.TempDir(), "cursors.json")}
	key := intake.CursorKey("https://s", "app", "fake")

	a := &feedClient{pages: []string{page(releasedChange("https://x/1", "c1"))}}
	aDone := make(chan struct{})
	go func() {
		defer close(aDone)
		_, _, _ = reconcile(context.Background(), a, store, key, "app", r, desc)
	}()
	for i := 0; i < 200; i++ {
		if _, err := os.Stat(filepath.Join(dir, "inA")); err == nil {
			break
		}
		time.Sleep(25 * time.Millisecond)
	}

	b := &feedClient{pages: []string{page()}}
	bDone := make(chan struct{})
	go func() {
		defer close(bDone)
		_, _, _ = reconcile(context.Background(), b, store, key, "app", r, desc)
	}()
	select {
	case <-bDone:
		t.Fatal("a second run on the same feed finished while the first was still in the adapter")
	case <-time.After(400 * time.Millisecond):
	}
	if len(b.after) != 0 {
		t.Fatalf("the second run must not even read the feed until the first is done: %v", b.after)
	}

	if err := os.WriteFile(filepath.Join(dir, "release"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	<-aDone
	<-bDone
	if len(b.after) != 1 || b.after[0] != "c1" {
		t.Fatalf("the second run starts after what the first answered: %v", b.after)
	}
	if n := untrackCalls(t, log); n != 1 {
		t.Fatalf("the effect happened once: %d untrack calls", n)
	}
}

// A run that cannot get the feed in time is busy: exit 5, nothing told, nothing read.
func TestReconcileBusyFeedExitsConflict(t *testing.T) {
	old := intake.FeedLockWait
	intake.FeedLockWait = 150 * time.Millisecond
	t.Cleanup(func() { intake.FeedLockWait = old })
	r, desc, _ := scriptAdapter(t, `echo '{}'`)
	store := &intake.CursorStore{Path: filepath.Join(t.TempDir(), "cursors.json")}
	key := intake.CursorKey("https://s", "app", "fake")

	held := make(chan struct{})
	free := make(chan struct{})
	go func() {
		_ = store.Serialize(context.Background(), key, func() error { close(held); <-free; return nil })
	}()
	<-held
	c := &feedClient{pages: []string{page()}}
	_, _, fatal := reconcile(context.Background(), c, store, key, "app", r, desc)
	close(free)
	var coded *exitcode.CodedError
	if !errors.As(fatal, &coded) || coded.Code != exitcode.Conflict || len(c.after) != 0 {
		t.Fatalf("want a busy Conflict with no read: %v reads=%v", fatal, c.after)
	}
}

// Gloria (cli#1975 BLIND P2): a page that does not advance the cursor must be
// refused before any adapter effect, not replayed 25 pages a run for ever.
func TestReconcileRefusesANonAdvancingCursor(t *testing.T) {
	r, desc, log := scriptAdapter(t, `echo '{}'`)
	store := &intake.CursorStore{Path: filepath.Join(t.TempDir(), "cursors.json")}
	key := intake.CursorKey("https://s", "app", "fake")
	if err := store.Set(key, "c0"); err != nil {
		t.Fatal(err)
	}
	// hasMore, a non-empty page, and a nextCursor equal to the `after` sent.
	stuck := `{"changes":[` + releasedChange("https://x/1", "c0") + `],"hasMore":true,"nextCursor":"c0"}`
	c := &feedClient{pages: []string{stuck, stuck, stuck}}
	d, aerr, ferr := reconcile(context.Background(), c, store, key, "app", r, desc)
	if ferr != nil || aerr == nil || d.Complete || d.Error == nil {
		t.Fatalf("a stuck cursor must fail the pass (exit 1), not read as 'more next run': %+v %v %v", d, aerr, ferr)
	}
	if len(c.after) != 1 || untrackCalls(t, log) != 0 {
		t.Fatalf("refused before any effect and without paging on: reads=%v untrack=%d", c.after, untrackCalls(t, log))
	}
	if got, _ := store.Get(key); got != "c0" {
		t.Fatalf("no cursor persisted: %q", got)
	}
}

// A REASSIGNED change whose task now has no assignee names no worker to mark.
func TestReconcileReassignedWithoutAssigneeIsSkipped(t *testing.T) {
	r, desc, log := scriptAdapter(t, `echo '{}'`)
	store := &intake.CursorStore{Path: filepath.Join(t.TempDir(), "cursors.json")}
	pg := `{"changes":[{"cursor":"c1","kind":"REASSIGNED","externalId":"https://x/1","task":{"id":"t","handle":"AT1","portalUrl":null,"assignee":null}}],"hasMore":false,"nextCursor":null}`
	d, aerr, ferr := reconcile(context.Background(), &feedClient{pages: []string{pg}}, store, "k", "app", r, desc)
	b, _ := os.ReadFile(log)
	if aerr != nil || ferr != nil || d.Marked != 0 || d.Skipped != 1 || strings.Contains(string(b), "mark-tracked") {
		t.Fatalf("no worker, no mark: %+v %v %v %s", d, aerr, ferr, b)
	}
}

// Joost (cli#1975 LOCAL delta): an empty page that says hasMore and carries a
// nextCursor must be refused BEFORE the checkpoint, so the stored cursor never
// moves past state nothing answered.
func TestReconcileEmptyHasMorePageDoesNotMoveTheCheckpoint(t *testing.T) {
	r, desc, _ := scriptAdapter(t, `echo '{}'`)
	store := &intake.CursorStore{Path: filepath.Join(t.TempDir(), "cursors.json")}
	key := intake.CursorKey("https://s", "app", "fake")
	if err := store.Set(key, "c-old"); err != nil {
		t.Fatal(err)
	}
	c := &feedClient{pages: []string{`{"changes":[],"hasMore":true,"nextCursor":"c-unanswered"}`}}
	d, aerr, ferr := reconcile(context.Background(), c, store, key, "app", r, desc)
	if aerr == nil || ferr != nil || d.Complete || d.Error == nil {
		t.Fatalf("must fail loudly: %+v %v %v", d, aerr, ferr)
	}
	if got, _ := store.Get(key); got != "c-old" {
		t.Fatalf("the checkpoint must stay at the last answered state, got %q", got)
	}
}

func TestReconcilePageCapResumesWithoutError1976(t *testing.T) {
	r, desc, _ := scriptAdapter(t, `echo '{}'`)
	store := &intake.CursorStore{Path: filepath.Join(t.TempDir(), "cursors.json")}
	pages := make([]string, 26)
	for i := range pages {
		pages[i] = fmt.Sprintf(`{"changes":[{"cursor":"c%d","kind":"FINISHED","externalId":"x","task":null}],"hasMore":true,"nextCursor":"c%d"}`, i+1, i+1)
	}
	c := &feedClient{pages: pages}
	d, aerr, ferr := reconcile(context.Background(), c, store, "k", "app", r, desc)
	if aerr != nil || ferr != nil || d.Complete || d.Error != nil || d.Read != 25 || d.Skipped != 25 || d.SkippedKind != 25 || len(c.after) != 25 || len(c.pages) != 1 {
		t.Fatalf("cap must stop after 25 pages without error: %+v %v %v reads=%d", d, aerr, ferr, len(c.after))
	}
	if got, err := store.Get("k"); err != nil || got != "c25" {
		t.Fatalf("checkpoint: %q %v", got, err)
	}
	next := &feedClient{pages: []string{page()}}
	d, aerr, ferr = reconcile(context.Background(), next, store, "k", "app", r, desc)
	if aerr != nil || ferr != nil || !d.Complete || next.after[0] != "c25" {
		t.Fatalf("resume: %+v %v %v %v", d, aerr, ferr, next.after)
	}
}

func TestReconcileSkippedReasonsRemainAdditive1976(t *testing.T) {
	r, desc, _ := scriptAdapter(t, `echo '{}'`)
	desc.Calls = []string{"describe", "list-untracked"}
	store := &intake.CursorStore{Path: filepath.Join(t.TempDir(), "cursors.json")}
	c := &feedClient{pages: []string{page("null", releasedChange("x", "c1"),
		`{"cursor":"c2","kind":"FINISHED","externalId":"x","task":null}`,
		`{"cursor":"c3","kind":"REASSIGNED","externalId":"x","task":{"id":"t","handle":"AT1","assignee":null}}`,
		`{"cursor":"c4","kind":"CANCELLED","externalId":"x","task":null}`,
		`{"cursor":"c5","kind":"REASSIGNED","externalId":"x","task":{"id":"t","handle":"AT1","assignee":{"id":"w"}}}`,
		`{"cursor":"c6","kind":"CANCELLED","externalId":"x","task":{"id":"t","handle":"AT1"}}`)}}
	d, aerr, ferr := reconcile(context.Background(), c, store, "k", "app", r, desc)
	if aerr != nil || ferr != nil {
		t.Fatalf("%v %v", aerr, ferr)
	}
	if d.Skipped != d.SkippedNull+d.SkippedUndeclared+d.SkippedKind+d.SkippedNoAssignee+d.SkippedNoTask {
		t.Fatalf("skip reasons do not partition total: %+v", d)
	}
	b, err := json.Marshal(d)
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatal(err)
	}
	for key, want := range map[string]float64{"skipped": 7, "skippedNull": 1, "skippedUndeclared": 3, "skippedKind": 1, "skippedNoAssignee": 1, "skippedNoTask": 1} {
		if got[key] != want {
			t.Errorf("%s = %v, want %v; %s", key, got[key], want, b)
		}
	}
}

// cli#2265 (server#4217): an ended task keeps its external id, so a change for
// it can arrive after a NEWER active task claimed the item. The pass checks who
// holds the item now before telling the adapter anything.
func heldElsewhereRun(t *testing.T, change string, holders map[string]string) (reconcileDTO, string, error, error) {
	t.Helper()
	r, desc, log := scriptAdapter(t, `echo '{}'`)
	desc.Calls = []string{"describe", "list-untracked", "mark-tracked", "untrack", "report"}
	store := &intake.CursorStore{Path: filepath.Join(t.TempDir(), "cursors.json")}
	c := &feedClient{pages: []string{page(change)}, holders: holders}
	d, aerr, ferr := reconcile(context.Background(), c, store, "k", "app", r, desc)
	b, _ := os.ReadFile(log)
	return d, string(b), aerr, ferr
}

func TestReconcileSkipsAChangeWhenADifferentActiveTaskHoldsTheItem2265(t *testing.T) {
	for name, change := range map[string]string{
		"CANCELLED":  `{"cursor":"c1","kind":"CANCELLED","externalId":"x","task":{"id":"A","handle":"AT1"}}`,
		"RELEASED":   `{"cursor":"c1","kind":"RELEASED","externalId":"x","task":{"id":"A","handle":"AT1"}}`,
		"REASSIGNED": `{"cursor":"c1","kind":"REASSIGNED","externalId":"x","task":{"id":"A","handle":"AT1","assignee":{"id":"w"}}}`,
	} {
		t.Run(name, func(t *testing.T) {
			d, log, aerr, ferr := heldElsewhereRun(t, change, map[string]string{"x": "B"})
			if aerr != nil || ferr != nil {
				t.Fatalf("%v %v", aerr, ferr)
			}
			if log != "" {
				t.Fatalf("the adapter was told about an item another task holds: %q", log)
			}
			if d.Read != 1 || d.Skipped != 1 || d.SkippedHeldElsewhere != 1 || d.Marked+d.Untracked+d.Reported != 0 {
				t.Fatalf("%+v", d)
			}
		})
	}
}

func TestReconcileActsAsBeforeWhenNobodyOrTheChangesTaskHoldsTheItem2265(t *testing.T) {
	change := `{"cursor":"c1","kind":"REASSIGNED","externalId":"x","task":{"id":"A","handle":"AT1","assignee":{"id":"w"}}}`
	for name, holders := range map[string]map[string]string{"nobody": {}, "the change's own task": {"x": "A"}} {
		t.Run(name, func(t *testing.T) {
			d, log, aerr, ferr := heldElsewhereRun(t, change, holders)
			if aerr != nil || ferr != nil {
				t.Fatalf("%v %v", aerr, ferr)
			}
			if !strings.Contains(log, "mark-tracked ") || d.Marked != 1 || d.Skipped != 0 {
				t.Fatalf("%+v %q", d, log)
			}
		})
	}
	// RELEASED with nobody holding it untracks exactly as before.
	d, log, _, _ := heldElsewhereRun(t, releasedChange("x", "c1"), nil)
	if d.Untracked != 1 || !strings.Contains(log, "untrack ") {
		t.Fatalf("%+v %q", d, log)
	}
}

func TestReconcileHolderLookupFailureLeavesTheCursorAndActsOnNothing2265(t *testing.T) {
	r, desc, log := scriptAdapter(t, `echo '{}'`)
	store := &intake.CursorStore{Path: filepath.Join(t.TempDir(), "cursors.json")}
	c := &feedClient{pages: []string{page(releasedChange("x", "c1"))}, holderErr: errors.New("boom")}
	d, aerr, ferr := reconcile(context.Background(), c, store, "k", "app", r, desc)
	if aerr != nil || ferr == nil {
		t.Fatalf("want a fatal lookup error, got %v / %v", aerr, ferr)
	}
	if untrackCalls(t, log) != 0 {
		t.Fatal("acted without knowing the holder")
	}
	if d.Complete || d.Error == nil {
		t.Fatalf("%+v", d)
	}
	if got, _ := store.Get("k"); got != "" {
		t.Fatalf("the cursor moved past an unanswered change: %q", got)
	}
}
