package team

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Khan/genqlient/graphql"

	"github.com/hadron-memory/hadron-cli/internal/intake"
)

// cli#2734 (hadron-server#4622): the holder lookup reads agentTasksPage and
// follows hasMore. An ended task keeps its external id, so the holder history
// can be longer than one page, and the page the server hands the legacy array
// read is not widened for it: a lookup that stops at page 1 answers "nobody
// holds it" about an item a later page's ACTIVE task holds.

// pagedHoldersClient answers the changes feed like feedClient and the holder
// lookup from a fixed row list, serving rowsPerRead holders per read so a
// multi-page history needs its reads followed. It records the offset of every
// holder read.
type pagedHoldersClient struct {
	feedClient
	rows        []string
	rowsPerRead int
	offsets     []float64
}

func (c *pagedHoldersClient) MakeRequest(ctx context.Context, req *graphql.Request, resp *graphql.Response) error {
	if req.OpName != "IntakeItemHolders" {
		return c.feedClient.MakeRequest(ctx, req, resp)
	}
	b, _ := json.Marshal(req.Variables)
	var v struct {
		Offset *float64 `json:"offset"`
	}
	_ = json.Unmarshal(b, &v)
	off := 0
	if v.Offset != nil {
		off = int(*v.Offset)
	}
	c.offsets = append(c.offsets, float64(off))
	if off < 0 || off > len(c.rows) {
		return fmt.Errorf("unscripted holder offset %d", off)
	}
	rest := c.rows[off:]
	end := c.rowsPerRead
	if end > len(rest) {
		end = len(rest)
	}
	rows := rest[:end]
	items := "null"
	if len(rows) > 0 {
		items = "[" + strings.Join(rows, ",") + "]"
	}
	hasMore := len(rest) > end
	body := fmt.Sprintf(`{"agentTasksPage":{"items":%s,"total":%d,"hasMore":%t}}`, items, len(c.rows), hasMore)
	return json.Unmarshal([]byte(body), resp.Data)
}

// TestReconcileHolderLookupFollowsHasMore2734: the ACTIVE holder of an item
// sits on page 2 of its history; a lookup that stops early marks a change an
// active task already holds. The walk must reach it.
func TestReconcileHolderLookupFollowsHasMore2734(t *testing.T) {
	r, desc, log := scriptAdapter(t, `echo '{}'`)
	store := &intake.CursorStore{Path: filepath.Join(t.TempDir(), "cursors.json")}
	c := &pagedHoldersClient{
		feedClient: feedClient{pages: []string{page(releasedChange("x", "c1"))}},
		// Newest first: a FINISHED predecessor, then the ACTIVE holder. One
		// row per read puts the active holder on page 2.
		rows: []string{
			`{"id":"f1","status":"FINISHED"}`,
			`{"id":"B","status":"STARTED"}`,
		},
		rowsPerRead: 1,
	}
	d, aerr, ferr := reconcile(context.Background(), c, store, "k", "app", r, desc)
	if aerr != nil || ferr != nil {
		t.Fatalf("%v %v", aerr, ferr)
	}
	if d.Read != 1 || d.Skipped != 1 || d.SkippedHeldElsewhere != 1 || d.Marked+d.Untracked+d.Reported != 0 {
		t.Fatalf("the change is held by page 2's ACTIVE task: %+v", d)
	}
	if len(c.offsets) != 2 || c.offsets[0] != 0 || c.offsets[1] != 1 {
		t.Fatalf("the holder read follows hasMore: %v", c.offsets)
	}
	if b, _ := os.ReadFile(log); strings.Contains(string(b), "mark-tracked ") {
		t.Fatal("the adapter was told about an item an active task holds")
	}
}

// TestReconcileHolderLookupStopsAtHasMoreEnd2734: the history fits in one
// page; exactly one read, and the finished-only history is not a holder.
func TestReconcileHolderLookupStopsAtHasMoreEnd2734(t *testing.T) {
	r, desc, log := scriptAdapter(t, `echo '{}'`)
	store := &intake.CursorStore{Path: filepath.Join(t.TempDir(), "cursors.json")}
	c := &pagedHoldersClient{
		feedClient: feedClient{pages: []string{page(releasedChange("x", "c1"))}},
		rows: []string{
			`{"id":"f1","status":"FINISHED"}`,
			`{"id":"f2","status":"CANCELLED"}`,
		},
		rowsPerRead: 2,
	}
	d, aerr, ferr := reconcile(context.Background(), c, store, "k", "app", r, desc)
	if aerr != nil || ferr != nil {
		t.Fatalf("%v %v", aerr, ferr)
	}
	if len(c.offsets) != 1 {
		t.Fatalf("one page is one read: %v", c.offsets)
	}
	if d.Untracked != 1 || !strings.Contains(func() string { b, _ := os.ReadFile(log); return string(b) }(), "untrack ") {
		t.Fatalf("a finished-only history holds nothing: %+v", d)
	}
}
