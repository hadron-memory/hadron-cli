package cmd

import (
	"encoding/json"
	"strings"
	"testing"
)

func chatReadArgs(srvURL string, extra ...string) []string {
	return append([]string{"team", "chat", "read", "--app", "acme.com:eng-team", "--json", "--server", srvURL}, extra...)
}

func seqRange(from, to int) []int {
	out := []int{}
	for i := from; i <= to; i++ {
		out = append(out, i)
	}
	return out
}

// cli#967: a bounded read asks for 20 unless told otherwise, and says when the
// default page was full.
func TestTeamChatReadDefaultsTo20AndSaysWhenThePageIsFull(t *testing.T) {
	srv, seen := chatServer(t, teamChatPage(100, seqRange(81, 100)...))
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs(chatReadArgs(srv.URL))
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	if l := (*seen)[0].Limit; l == nil || *l != 20 {
		t.Fatalf("default limit sent as %v, want 20", l)
	}
	errOut := f.IOStreams.ErrOut.(*strings.Builder).String()
	for _, want := range []string{"full page of 20", "more may exist"} {
		if !strings.Contains(errOut, want) {
			t.Errorf("stderr missing %q: %q", want, errOut)
		}
	}
}

func TestTeamChatReadExplicitLimitIsRespectedWithoutCue(t *testing.T) {
	srv, seen := chatServer(t, teamChatPage(100, seqRange(1, 20)...))
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs(chatReadArgs(srv.URL, "--limit", "20"))
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	if l := (*seen)[0].Limit; l == nil || *l != 20 {
		t.Fatalf("explicit limit sent as %v", l)
	}
	if e := f.IOStreams.ErrOut.(*strings.Builder).String(); strings.Contains(e, "more may exist") {
		t.Errorf("explicit --limit needs no cue: %q", e)
	}
}

// A short but NON-EMPTY page is not the end of a forward walk (a server clamp,
// or a mention filter's window): --all keeps going until an empty page.
func TestTeamChatReadAllContinuesPastAShortNonEmptyPage(t *testing.T) {
	srv, seen := chatServer(t,
		teamChatPage(9, seqRange(1, 5)...),
		teamChatPage(9, seqRange(6, 9)...),
		teamChatPage(9))
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs(chatReadArgs(srv.URL, "--all"))
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	var dto struct {
		Messages []struct {
			Seq int `json:"seq"`
		} `json:"messages"`
	}
	if err := json.Unmarshal([]byte(out.String()), &dto); err != nil {
		t.Fatal(err)
	}
	if len(dto.Messages) != 9 || len(*seen) != 3 {
		t.Fatalf("got %d messages in %d pages, want 9 in 3", len(dto.Messages), len(*seen))
	}
}

// A server that ignores sinceSeq and keeps returning the same page must not
// loop the walk nor duplicate its rows.
func TestTeamChatReadAllStopsWhenTheCursorDoesNotAdvance(t *testing.T) {
	same := teamChatPage(5, seqRange(1, 5)...)
	srv, seen := chatServer(t, same, same)
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs(chatReadArgs(srv.URL, "--all"))
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	var dto struct {
		Messages []struct {
			Seq int `json:"seq"`
		} `json:"messages"`
	}
	if err := json.Unmarshal([]byte(out.String()), &dto); err != nil {
		t.Fatal(err)
	}
	if len(dto.Messages) != 5 || len(*seen) != 2 {
		t.Fatalf("got %d messages in %d pages, want 5 in 2 (no duplicates)", len(dto.Messages), len(*seen))
	}
}
