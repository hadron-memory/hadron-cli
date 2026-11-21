package cmd

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// cli#2858: the unread-chat advisory `team session log` prints on stderr is
// derived from the bound worker's SERVER-side read cursor (cor:dmo:090:06: unread is the gap
// between the channel's head and the attendee's server-side read state), never from the
// worktree's local hint, which only a few kinds of read ever move. Where the cursor cannot be
// read the advisory says so and states no count and no `--since` remedy.

const (
	unread2858CursorStub = `{"data":{"channelReadState":{"channelId":"ch1","channelIncarnation":3,"cursorIncarnation":3,"lastSeenSeq":131328}}}`
	unread2858Channel    = `{"data":{"app":{"id":"capp100000000000000000000","defaultChannel":{"id":"ch1"}}}}`
)

// unread2858Run runs `team session log` in a worktree bound with the given binding file and
// answers the log's own writes plus the given extra stubs. It returns stderr and what was sent
// per operation.
func unread2858Run(t *testing.T, binding string, extra map[string]string) (string, map[string]json.RawMessage) {
	t.Helper()
	dir := teamGitDir(t)
	if err := os.WriteFile(filepath.Join(dir, "hadron-team-session.json"), []byte(binding), 0o600); err != nil {
		t.Fatal(err)
	}
	stubs := map[string]string{
		"UpdateTeamSession": `{"data":{"updateSession":{"id":"s-new","agentId":"agt1","workerId":"wkr1","userId":"u1",
			"type":"DEVELOPER","repo":null,"branch":null,"prNumber":371,
			"startedAt":"2026-08-11T10:00:00Z","endedAt":null,"host":null,"tool":null,
			"transcriptPath":null,"llmModel":null}}}`,
		"RecordTeamWork": `{"data":{"recordTeamWork":{"nodeId":"w1","sessionId":"s-new","workerId":"wkr1","workerName":"Iris",
			"tool":"claude-code","kind":"pr","ref":"hadron-memory/hadron-cli#371","action":"worked-on",
			"at":"2026-08-13T10:00:00Z","detail":null}}}`,
	}
	for k, v := range extra {
		stubs[k] = v
	}
	gql, captured := captureGraphQL(t, stubs)
	f, _ := testFactory(t)
	errOut := f.IOStreams.ErrOut.(*strings.Builder)
	root := NewRootCmd(f)
	root.SetArgs([]string{"team", "session", "log", "--pr", "371", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("session log: %v", err)
	}
	return errOut.String(), captured
}

func TestSessionLogUnreadCountsFromTheServerCursor2858(t *testing.T) {
	// The local hint says 90 (bindingChatSeenFixture); the server cursor says 131328. The
	// count and the remedy must follow the server: 111k messages is the bogus catch-up the
	// issue reports.
	errOut, captured := unread2858Run(t, bindingChatSeenFixture, map[string]string{
		"TeamDefaultChannel": unread2858Channel,
		"CheckChatCursor":    unread2858CursorStub,
		"TeamChatMessages":   `{"data":{"teamChatMessages":{"total":136,"items":[]}}}`,
	})
	var vars map[string]any
	_ = json.Unmarshal(captured["TeamChatMessages"], &vars)
	if vars["sinceSeq"] != float64(131328) {
		t.Errorf("the count must start at the server read cursor, not the local hint: %v", vars)
	}
	if !strings.Contains(errOut, "136 new messages in the team chat since you last read") || !strings.Contains(errOut, "chat read --since 131328") {
		t.Errorf("want the count and a remedy at the server cursor, got %q", errOut)
	}
	if strings.Contains(errOut, "--since 90") {
		t.Errorf("the stale local hint must not be offered as a remedy: %q", errOut)
	}
}

func TestSessionLogUnreadNeedsNoLocalHint2858(t *testing.T) {
	// A worktree whose binding carries no hint at all (the QA report: mark-read left no
	// chat-seen key) still has a server cursor, so it gets a count, not "never read".
	errOut, _ := unread2858Run(t, bindingWithTeamFixture, map[string]string{
		"TeamDefaultChannel": unread2858Channel,
		"CheckChatCursor":    unread2858CursorStub,
		"TeamChatMessages":   `{"data":{"teamChatMessages":{"total":5,"items":[]}}}`,
	})
	if !strings.Contains(errOut, "5 new messages in the team chat since you last read") {
		t.Errorf("a binding without a local hint must still count from the server cursor: %q", errOut)
	}
	if strings.Contains(errOut, "no contiguous team-chat read cursor") {
		t.Errorf("the local-hint wording is gone: %q", errOut)
	}
}

func TestSessionLogUnreadSaysSoWhenTheCursorCannotBeRead2858(t *testing.T) {
	notCount := func(t *testing.T, errOut string, captured map[string]json.RawMessage) {
		t.Helper()
		if !strings.Contains(errOut, "cannot read your server-side team-chat read position") {
			t.Errorf("must say it cannot know: %q", errOut)
		}
		if strings.Contains(errOut, "new message") || strings.Contains(errOut, "--since") {
			t.Errorf("no count and no --since remedy when the position is unknown: %q", errOut)
		}
		if _, called := captured["TeamChatMessages"]; called {
			t.Error("nothing to count from: the chat must not be counted against a guessed start")
		}
	}
	cases := map[string]map[string]string{
		"the cursor read fails": {
			"TeamDefaultChannel": unread2858Channel,
			"CheckChatCursor":    `{"errors":[{"message":"boom","extensions":{"code":"INTERNAL_SERVER_ERROR"}}]}`,
		},
		"the worker has no cursor row": {
			"TeamDefaultChannel": unread2858Channel,
			"CheckChatCursor":    `{"data":{"channelReadState":null}}`,
		},
		"a legacy cursor (no incarnation)": {
			"TeamDefaultChannel": unread2858Channel,
			"CheckChatCursor":    `{"data":{"channelReadState":{"channelId":"ch1","channelIncarnation":3,"cursorIncarnation":null,"lastSeenSeq":12}}}`,
		},
		"a cursor from a previous channel incarnation": {
			"TeamDefaultChannel": unread2858Channel,
			"CheckChatCursor":    `{"data":{"channelReadState":{"channelId":"ch1","channelIncarnation":3,"cursorIncarnation":2,"lastSeenSeq":12}}}`,
		},
		"the App has no team chat": {
			"TeamDefaultChannel": `{"data":{"app":{"id":"capp100000000000000000000","defaultChannel":null}}}`,
		},
	}
	for name, stubs := range cases {
		t.Run(name, func(t *testing.T) {
			// A local hint is present on purpose: it must not stand in for the unknown.
			errOut, captured := unread2858Run(t, bindingChatSeenFixture, stubs)
			notCount(t, errOut, captured)
		})
	}
}

func TestSessionLogUnreadIsSilentWhenCaughtUp2858(t *testing.T) {
	errOut, _ := unread2858Run(t, bindingChatSeenFixture, map[string]string{
		"TeamDefaultChannel": unread2858Channel,
		"CheckChatCursor":    unread2858CursorStub,
		"TeamChatMessages":   `{"data":{"teamChatMessages":{"total":0,"items":[]}}}`,
	})
	if strings.Contains(errOut, "team chat") || strings.Contains(errOut, "team-chat") {
		t.Errorf("caught up on the server must say nothing: %q", errOut)
	}
}

func TestSessionLogUnreadMentionsCountStartsAtTheServerCursor2858(t *testing.T) {
	errOut, captured := unread2858Run(t, bindingChatSeenFixture, map[string]string{
		"TeamDefaultChannel": unread2858Channel,
		"CheckChatCursor":    unread2858CursorStub,
		"TeamChatMessages":   `{"data":{"teamChatMessages":{"total":7,"items":[]}}}`,
	})
	if !strings.Contains(errOut, "7 new messages in the team chat since you last read (7 mentioning you)") {
		t.Errorf("want the count and the mentions clause: %q", errOut)
	}
	var vars map[string]any
	_ = json.Unmarshal(captured["TeamChatMessages"], &vars)
	if vars["sinceSeq"] != float64(131328) || vars["mentionsRef"] == nil {
		t.Errorf("the mentions count must also start at the server cursor: %v", vars)
	}
}
