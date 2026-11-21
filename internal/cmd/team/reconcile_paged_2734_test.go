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
	"github.com/vektah/gqlparser/v2/gqlerror"

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
// holder read. validationErr, when set, is returned for the first holder read
// as a GraphQL validation error (one old-server shape); rawBody, when set, is
// served verbatim as the holder page's data (malformed-answer shapes).
type pagedHoldersClient struct {
	feedClient
	rows          []string
	rowsPerRead   int
	offsets       []float64
	validationErr string
	rawBody       string
}

func (c *pagedHoldersClient) MakeRequest(ctx context.Context, req *graphql.Request, resp *graphql.Response) error {
	if req.OpName != "IntakeItemHolders" {
		return c.feedClient.MakeRequest(ctx, req, resp)
	}
	// The offset is recorded before any scripted refusal so refusal-shape
	// tests can pin "the first read, not paged to the bound" — recorded only
	// after the rawBody return instead, that pin could never fail.
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
	if c.validationErr != "" {
		msg := c.validationErr
		c.validationErr = ""
		// Returned, not just attached: genqlient surfaces only what
		// MakeRequest returns, like the HTTP transport does.
		return &gqlerror.Error{
			Message:    msg,
			Extensions: map[string]any{"code": "GRAPHQL_VALIDATION_FAILED"},
		}
	}
	if c.rawBody != "" {
		return json.Unmarshal([]byte(c.rawBody), resp.Data)
	}
	if off < 0 || off > len(c.rows) {
		return fmt.Errorf("unscripted holder offset %d", off)
	}
	rest := c.rows[off:]
	end := c.rowsPerRead
	if end > len(rest) {
		end = len(rest)
	}
	rows := rest[:end]
	items := "[]"
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

// TestReconcileHolderLookupRefusesEmptyPageWithMore2734: items [] with
// hasMore true can never advance the walk — the read steps by rows served, so
// offset+0 would re-ask the same page until the page bound. It is refused on
// the first page as a server failure (the fatal error, nothing told, cursor
// stays), not paged to the bound.
func TestReconcileHolderLookupRefusesEmptyPageWithMore2734(t *testing.T) {
	r, desc, _ := scriptAdapter(t, `echo '{}'`)
	store := &intake.CursorStore{Path: filepath.Join(t.TempDir(), "cursors.json")}
	c := &pagedHoldersClient{
		feedClient: feedClient{pages: []string{page(releasedChange("x", "c1"))}},
		rows: []string{
			`{"id":"f1","status":"FINISHED"}`,
		},
		rowsPerRead: 0, // an empty page that still claims more remain
	}
	_, aerr, ferr := reconcile(context.Background(), c, store, "k", "app", r, desc)
	if aerr != nil || ferr == nil || !strings.Contains(ferr.Error(), "empty page claims more remain") {
		t.Fatalf("an empty page with hasMore is a refused server failure: aerr=%v ferr=%v", aerr, ferr)
	}
	if len(c.offsets) != 1 {
		t.Fatalf("the refusal fires on the first page, it does not page to the bound: %v", c.offsets)
	}
}

// TestReconcileHolderLookupRefusesMalformedPage2734: the holder read's other
// untrustworthy answers — no page object, null items — end the run as server
// failures on the first page, like the empty-hasMore page, never as "nobody
// holds it" (cli#2734 BLIND round 2: only the empty-hasMore shape was pinned).
func TestReconcileHolderLookupRefusesMalformedPage2734(t *testing.T) {
	for name, body := range map[string]string{
		"no page object": `{}`,
		"null page":      `{"agentTasksPage":null}`,
		"items null":     `{"agentTasksPage":{"items":null,"total":1,"hasMore":false}}`,
	} {
		r, desc, _ := scriptAdapter(t, `echo '{}'`)
		store := &intake.CursorStore{Path: filepath.Join(t.TempDir(), "cursors.json")}
		c := &pagedHoldersClient{
			feedClient: feedClient{pages: []string{page(releasedChange("x", "c1"))}},
			rawBody:    body,
		}
		_, aerr, ferr := reconcile(context.Background(), c, store, "k", "app", r, desc)
		if aerr != nil || ferr == nil || !strings.Contains(ferr.Error(), "malformed agentTasksPage") {
			t.Errorf("%s: a malformed holder page is a refused server failure: aerr=%v ferr=%v", name, aerr, ferr)
		}
		if len(c.offsets) != 1 {
			t.Errorf("%s: the refusal fires on the first read, it does not page to the bound: %v", name, c.offsets)
		}
	}
}

// TestReconcileHolderLookupOldServerHint2734: a server without the paged task
// read (its validation error names agentTasksPage) gets the friendly refusal
// naming hadron-server#4622 — the holder lookup has no raw-error fallback and
// no legacy read to fall back to (cli#2734 BLIND round 2: the branch was
// untested).
func TestReconcileHolderLookupOldServerHint2734(t *testing.T) {
	r, desc, _ := scriptAdapter(t, `echo '{}'`)
	store := &intake.CursorStore{Path: filepath.Join(t.TempDir(), "cursors.json")}
	c := &pagedHoldersClient{
		feedClient:    feedClient{pages: []string{page(releasedChange("x", "c1"))}},
		validationErr: `Cannot query field "agentTasksPage" on type "Query".`,
	}
	_, aerr, ferr := reconcile(context.Background(), c, store, "k", "app", r, desc)
	if aerr != nil || ferr == nil ||
		!strings.Contains(ferr.Error(), "predates hadron-server#4622") {
		t.Fatalf("an old server gets the #4622 hint, not a raw validation error: aerr=%v ferr=%v", aerr, ferr)
	}
}

// TestReconcileHolderLookupStopsAtHasMoreEnd2734: the history fits in one
// page; exactly one read, and — the carried RELEASED rule (cor:agt:120:04,
// cli#2265) — the finished-only history still HOLDS the id, so the release is
// skipped, not untracked: RELEASED untracks only when no task of the App holds
// the id in any state.
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
	if d.Skipped != 1 || d.SkippedHeldElsewhere != 1 || d.Untracked != 0 || strings.Contains(func() string { b, _ := os.ReadFile(log); return string(b) }(), "untrack ") {
		t.Fatalf("a finished-only history still holds the id (any state, cor:agt:120:04): %+v", d)
	}
}
