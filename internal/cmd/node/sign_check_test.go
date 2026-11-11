package node

import (
	"context"
	"encoding/json"
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
	c := newLocalChecker(context.Background(), f, "n1", 251)
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
