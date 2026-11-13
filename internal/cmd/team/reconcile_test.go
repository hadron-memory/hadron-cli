package team

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/Khan/genqlient/graphql"

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
}

func (c *feedClient) MakeRequest(_ context.Context, req *graphql.Request, resp *graphql.Response) error {
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

func released(ext, cursor string) string {
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

	first := &feedClient{pages: []string{page(released("https://x/1", "cur-A"), released("https://x/2", "cur-B"))}}
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
		released("https://x/1", "c1"), released("https://x/2", "c2"), released("https://x/3", "c3"))}}
	d1, aerr, ferr := reconcile(context.Background(), first, store, key, "app", r, desc)
	if ferr != nil || aerr == nil || d1.Complete || d1.Error == nil || d1.Untracked != 1 {
		t.Fatalf("first run must stop at the refused change: %+v %v %v", d1, aerr, ferr)
	}
	if n := untrackCalls(t, log); n != 2 {
		t.Fatalf("c3 must not be tried past the failure: %d calls", n)
	}

	second := &feedClient{pages: []string{page(released("https://x/2", "c2"), released("https://x/3", "c3"))}}
	_, _, _ = reconcile(context.Background(), second, store, key, "app", r, desc)
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
	if aerr != nil || ferr != nil || d.Complete || len(c.after) != 1 {
		t.Fatalf("must stop incomplete without spinning: %+v %v %v reads=%d", d, aerr, ferr, len(c.after))
	}
}
