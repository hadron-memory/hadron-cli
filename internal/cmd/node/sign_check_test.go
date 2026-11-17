package node

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"testing"

	"github.com/Khan/genqlient/graphql"
)

// fakeHistory serves NodeRevisionTexts pages as a server that clamps the page
// size below the one requested (cli#879, BLIND P3): the checker must advance
// by what came back, never by what it asked for.
type fakeHistory struct {
	revisions []int // revNo, newest first
	clamp     int
	calls     int
}

func (f *fakeHistory) MakeRequest(_ context.Context, req *graphql.Request, resp *graphql.Response) error {
	f.calls++
	raw, _ := json.Marshal(req.Variables)
	var v struct {
		Limit  *int `json:"limit"`
		Offset *int `json:"offset"`
	}
	_ = json.Unmarshal(raw, &v)
	off := 0
	if v.Offset != nil {
		off = *v.Offset
	}
	end := off + f.clamp
	if end > len(f.revisions) {
		end = len(f.revisions)
	}
	items := ""
	for i := off; i < end; i++ {
		if items != "" {
			items += ","
		}
		items += fmt.Sprintf(`{"revNo":%d,"name":"t%d","abstract":null,"content":"c%d"}`, f.revisions[i], f.revisions[i], f.revisions[i])
	}
	body := fmt.Sprintf(`{"nodeRevisionsPage":{"hasMore":%t,"items":[%s]}}`, end < len(f.revisions), items)
	return json.Unmarshal([]byte(body), resp.Data)
}

func TestSnapshotScanAdvancesByTheItemsReturned(t *testing.T) {
	var revs []int
	for r := 250; r >= 1; r-- {
		revs = append(revs, r)
	}
	f := &fakeHistory{revisions: revs, clamp: 30}
	c := newLocalChecker(context.Background(), f, "n1", "nid")
	for _, want := range []int{250, 121, 1} {
		got, err := c.text(want)
		if err != nil || got == nil || *got.title != fmt.Sprintf("t%d", want) {
			t.Fatalf("revision %d: %+v, %v (a clamped page skipped it)", want, got, err)
		}
	}
	if f.calls < 250/30 {
		t.Errorf("calls = %d: the scan stopped early", f.calls)
	}
}

// alwaysMore serves a history that never ends: every page is full and says
// hasMore (cli#1907 item 1). A checker that trusts hasMore without a bound
// never returns; the guard turns that hang into a failure.
type alwaysMore struct {
	calls int
	pages int // NodeRevisionTexts requests
}

const alwaysMoreGuard = 100000

func (f *alwaysMore) MakeRequest(_ context.Context, req *graphql.Request, resp *graphql.Response) error {
	f.calls++
	if f.calls > alwaysMoreGuard {
		return fmt.Errorf("guard: the history loop made %d requests without stopping", f.calls)
	}
	switch req.OpName {
	case "NodeRevisionTexts":
		f.pages++
		raw, _ := json.Marshal(req.Variables)
		var v struct {
			Offset *int `json:"offset"`
		}
		_ = json.Unmarshal(raw, &v)
		off := 0
		if v.Offset != nil {
			off = *v.Offset
		}
		items := ""
		for i := 0; i < revisionPageSize; i++ {
			if items != "" {
				items += ","
			}
			items += fmt.Sprintf(`{"revNo":%d,"name":"t","abstract":null,"content":"c"}`, 1000000+off+i)
		}
		return json.Unmarshal([]byte(fmt.Sprintf(`{"nodeRevisionsPage":{"hasMore":true,"items":[%s]}}`, items)), resp.Data)
	case "NodeRawText":
		return json.Unmarshal([]byte(`{"node":{"id":"nid","revision":999999999,"name":"cur","abstract":null,"content":"cur"}}`), resp.Data)
	case "GetSigningRequest":
		return json.Unmarshal([]byte(`{"signingRequest":{"id":"sr1","nodeId":"nid","revision":3,"approvalHash":"h","payloadVersion":"hadron-signature-v3","state":"SIGNED","createdAt":"","expiresAt":""}}`), resp.Data)
	}
	return fmt.Errorf("unexpected operation %s", req.OpName)
}

// A history longer than the page cap is a failure to rebuild, never a
// signature state (cli#1907; cor:dmo:130:01, :04).
func TestSnapshotScanIsBoundedAndReportsAnError(t *testing.T) {
	f := &alwaysMore{}
	c := newLocalChecker(context.Background(), f, "n1", "nid")
	got, err := c.text(3)
	if err == nil || got != nil {
		t.Fatalf("text = %+v, %v: a capped history must be an error, not a missing revision", got, err)
	}
	if !errors.Is(err, errSnapshotHistoryCapped) {
		t.Fatalf("err = %v, want errSnapshotHistoryCapped", err)
	}
	if f.pages != maxRevisionPages {
		t.Errorf("history pages read = %d, want exactly the cap, %d", f.pages, maxRevisionPages)
	}
}

func TestCheckOnACappedHistoryIsNotASignatureState(t *testing.T) {
	f := &alwaysMore{}
	c := newLocalChecker(context.Background(), f, "n1", "nid")
	sig := signatureDTO{ID: "s1", Revision: 3, SigningRequestID: "sr1", PayloadVersion: "hadron-signature-v3"}
	for i := 0; i < 2; i++ { // the second signature must not rescan or fall to a state
		state, err := c.check(sig)
		if err == nil || state != "" {
			t.Fatalf("check #%d = %q, %v: want no state and an error", i, state, err)
		}
		if !errors.Is(err, errSnapshotHistoryCapped) {
			t.Fatalf("check #%d err = %v", i, err)
		}
		if localFails(state) {
			t.Fatalf("a cap hit must not add a --check failure state")
		}
	}
	if f.pages != maxRevisionPages {
		t.Errorf("history pages read = %d, want %d once: the capped scan was repeated or ran long", f.pages, maxRevisionPages)
	}
}

// A history exactly at the cap that ends there is complete, not capped.
func TestSnapshotScanAtTheCapBoundaryStillSucceeds(t *testing.T) {
	n := maxRevisionPages * revisionPageSize
	var revs []int
	for r := n; r >= 1; r-- {
		revs = append(revs, r)
	}
	f := &fakeHistory{revisions: revs, clamp: revisionPageSize}
	c := newLocalChecker(context.Background(), f, "n1", "nid")
	got, err := c.text(1)
	if err != nil || got == nil {
		t.Fatalf("revision 1 at the boundary: %+v, %v", got, err)
	}
}
