package cmd

import (
	"encoding/json"
	"fmt"
	"testing"
)

func chatHits(n int) []string {
	rows := make([]string, n)
	for i := range rows {
		rows[i] = chatMsg(fmt.Sprintf("chats:api:messages:t%03d-iris", i), i+1, `{"author":"iris","body":"hi"}`)
	}
	return rows
}

func findNodesHitsNoTotal(page []string, _ int) string { return chatMessagesResp(page...) }

// cli#967: `chat read` drains the whole chat even when the server serves fewer
// rows per page than asked for; a short page is not the end.
func TestChatReadDrainsPastAClampedPage(t *testing.T) {
	gql, rec := pagedGraphQL(t, map[string]*pagedSource{
		"ChatMessages": {rows: chatHits(130), serve: 50, wrap: findNodesHitsNoTotal},
	}, nil)
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"chat", "read", "--node", "hrn:node:acme.com:tc:chats:api:messages", "--since", "0", "--json", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	var dto struct {
		Messages []struct {
			Seq *int `json:"seq"`
		} `json:"messages"`
	}
	if err := json.Unmarshal([]byte(out.String()), &dto); err != nil {
		t.Fatalf("%v\n%.200s", err, out.String())
	}
	if len(dto.Messages) != 130 {
		t.Fatalf("got %d messages, want 130 (stopped on a short page)", len(dto.Messages))
	}
	if n := len(rec["ChatMessages"].Calls); n != 8 {
		t.Errorf("want 8 requests (two drains of 50+50+30+empty), got %d", n)
	}
}

func TestChatReadLaterPageErrorFails(t *testing.T) {
	gql, _ := pagedGraphQL(t, map[string]*pagedSource{
		"ChatMessages": {rows: chatHits(130), serve: 50, wrap: findNodesHitsNoTotal, failAtCall: 2},
	}, nil)
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"chat", "read", "--node", "hrn:node:acme.com:tc:chats:api:messages", "--since", "0", "--json", "--server", gql.URL})
	if err := root.Execute(); err == nil {
		t.Fatal("a failed page 2 must not read as a complete chat")
	}
}
