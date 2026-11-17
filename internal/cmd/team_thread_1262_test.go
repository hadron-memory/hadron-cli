package cmd

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/api/gen"
	"github.com/hadron-memory/hadron-cli/internal/exitcode"
)

// cli#1262: `--thread <seq>` is the SERVER's replyToSeq read filter
// (hadron-server#2847), never a client-side filter over a paged channel.

const teamThreadReplies = `{"data":{"teamChatMessages":{"total":2,"items":[
	{"nodeId":"n8","seq":8,"body":"first reply","at":"t1","authorUserId":"u2","authorWorkerId":null,"authorName":"rufus","sessionId":null,"replyToSeq":5,"mentions":[]},
	{"nodeId":"n9","seq":9,"body":"second reply","at":"t2","authorUserId":"u2","authorWorkerId":null,"authorName":"rufus","sessionId":null,"replyToSeq":5,"mentions":[]}]}}}`

const teamThreadHead = `{"data":{"teamChatMessages":{"total":9,"items":[{"seq":9}]}}}`

// GraphQL validates the STATIC document, so omitting the variable does not hide
// the argument: an ordinary read that carried replyToSeq would be refused by a
// server older than #2847 (review:old-server-graphql-document-compatibility).
func TestOrdinaryReadDocumentsNeverCarryTheThreadArgument(t *testing.T) {
	for name, doc := range map[string]string{
		"TeamChatMessages":           gen.TeamChatMessages_Operation,
		"TeamChatMessagesAttributed": gen.TeamChatMessagesAttributed_Operation,
		"ChannelMessages":            gen.ChannelMessages_Operation,
	} {
		if strings.Contains(doc, "$replyToSeq") {
			t.Errorf("%s must not carry replyToSeq; --thread has its own operation", name)
		}
	}
	for name, doc := range map[string]string{
		"TeamChatThread":           gen.TeamChatThread_Operation,
		"TeamChatThreadAttributed": gen.TeamChatThreadAttributed_Operation,
		"ChannelThread":            gen.ChannelThread_Operation,
	} {
		if !strings.Contains(doc, "replyToSeq: $replyToSeq") {
			t.Errorf("%s must send the filter", name)
		}
	}
}

func bindTeamThread(t *testing.T) string {
	t.Helper()
	dir := teamGitDir(t)
	path := filepath.Join(dir, "hadron-team-session.json")
	if err := os.WriteFile(path, []byte(bindingWithTeamFixture), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func threadVars(t *testing.T, raw json.RawMessage) map[string]any {
	t.Helper()
	var vars map[string]any
	if err := json.Unmarshal(raw, &vars); err != nil {
		t.Fatalf("variables: %v (%s)", err, raw)
	}
	return vars
}

func TestTeamChatReadThreadSendsTheServerFilterAndComposes(t *testing.T) {
	bindTeamThread(t)
	gql, captured := captureGraphQL(t, map[string]string{
		"TeamChatThread":   teamThreadReplies,
		"TeamChatReadHead": teamThreadHead,
	})
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"team", "chat", "read", "--thread", "5", "--since", "7", "--limit", "3", "--json", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	vars := threadVars(t, captured["TeamChatThread"])
	if vars["replyToSeq"] != float64(5) || vars["sinceSeq"] != float64(7) || vars["limit"] != float64(3) {
		t.Errorf("filter must go to the server and compose with the cursors: %v", vars)
	}
	var dto struct {
		Messages []struct {
			Seq        int  `json:"seq"`
			ReplyToSeq *int `json:"replyToSeq"`
		} `json:"messages"`
		NextSince int `json:"nextSince"`
	}
	if err := json.Unmarshal([]byte(out.String()), &dto); err != nil {
		t.Fatalf("json: %v (%s)", err, out.String())
	}
	if len(dto.Messages) != 2 || dto.Messages[0].Seq != 8 || dto.Messages[1].Seq != 9 || dto.NextSince != 9 {
		t.Errorf("messages: %s", out.String())
	}
}

// An ordinary read must not mention the filter at all: an explicit null would
// read as "clear" and an old server would refuse the unknown argument.
func TestTeamChatReadWithoutThreadOmitsTheFilter(t *testing.T) {
	bindTeamThread(t)
	gql, captured := captureGraphQL(t, map[string]string{
		"TeamChatMessages": teamThreadReplies,
		"TeamChatReadHead": teamThreadHead,
		"TeamAppIdentity":  teamAppIdentityJSON,
	})
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"team", "chat", "read", "--since", "0", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if _, has := threadVars(t, captured["TeamChatMessages"])["replyToSeq"]; has {
		t.Error("replyToSeq must be omitted when --thread is not given")
	}
}

// A thread read is a FILTERED window: its highest seq says nothing about the
// messages in between, so it must not become the reader's watermark nor mark
// the server read state (read-state semantics identical to --mentions).
func TestTeamChatReadThreadNeverAdvancesReadState(t *testing.T) {
	path := bindTeamThread(t)
	gql, captured := captureGraphQL(t, map[string]string{
		"TeamChatThread":   teamThreadReplies,
		"TeamChatReadHead": teamThreadHead,
		"TeamAppIdentity":  teamAppIdentityJSON,
	})
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"team", "chat", "read", "--thread", "5", "--since", "0", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var b struct {
		ChatSeenSeq *int `json:"chatSeenSeq"`
	}
	if err := json.Unmarshal(data, &b); err != nil {
		t.Fatal(err)
	}
	if b.ChatSeenSeq != nil {
		t.Errorf("a thread read recorded watermark %d; it must record none", *b.ChatSeenSeq)
	}
	for op := range captured {
		if strings.Contains(op, "MarkOwnTeamChatRead") || strings.Contains(op, "MarkRead") {
			t.Errorf("a thread read must not mark server read state, saw %s", op)
		}
	}
}

// A seq that names no message is a typed refusal with its own exit code, never
// an empty page: an empty page would read as "no replies" when the seq was wrong.
// A message that exists with no replies IS a valid empty page (exit 0).
func TestTeamChatReadThreadUnknownSeqIsNotFoundButEmptyThreadIsNot(t *testing.T) {
	bindTeamThread(t)
	unknown := `{"errors":[{"message":"replyToSeq 99 does not name a message in this team chat.","extensions":{"code":"TEAM_CHAT_REPLY_NOT_FOUND"}}]}`
	gql, _ := captureGraphQL(t, map[string]string{
		"TeamChatThread":   unknown,
		"TeamChatReadHead": teamThreadHead,
	})
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"team", "chat", "read", "--thread", "99", "--since", "0", "--server", gql.URL})
	err := root.Execute()
	if code := exitCodeFor(err); code != exitcode.NotFound {
		t.Fatalf("unknown thread seq: exit %d, want %d; err: %v", code, exitcode.NotFound, err)
	}
	if err == nil || !strings.Contains(err.Error(), "replyToSeq 99") {
		t.Errorf("the server's message must surface: %v", err)
	}

	gql2, _ := captureGraphQL(t, map[string]string{
		"TeamChatThread":   `{"data":{"teamChatMessages":{"total":0,"items":[]}}}`,
		"TeamChatReadHead": teamThreadHead,
	})
	f2, out := testFactory(t)
	root2 := NewRootCmd(f2)
	root2.SetArgs([]string{"team", "chat", "read", "--thread", "5", "--since", "0", "--json", "--server", gql2.URL})
	if err := root2.Execute(); err != nil {
		t.Fatalf("a message with no replies is a valid empty thread, got: %v", err)
	}
	var dto struct {
		Messages []any `json:"messages"`
	}
	if err := json.Unmarshal([]byte(out.String()), &dto); err != nil || dto.Messages == nil || len(dto.Messages) != 0 {
		t.Errorf("empty thread must render messages: [] (%v): %s", err, out.String())
	}
}

func TestThreadFlagRefusesNonPositiveSeqBeforeAnyRoundTrip(t *testing.T) {
	for _, args := range [][]string{
		{"team", "chat", "read", "--thread", "0"},
		{"team", "chat", "read", "--thread", "-3"},
		{"channel", "read", "r", "--thread", "0"},
		{"channel", "read", "r", "--thread", "-3"},
	} {
		gql, captured := captureGraphQL(t, map[string]string{})
		f, _ := testFactory(t)
		root := NewRootCmd(f)
		root.SetArgs(append(args, "--server", gql.URL))
		err := root.Execute()
		if code := exitCodeFor(err); code != exitcode.Usage {
			t.Errorf("%v: exit %d, want %d; err: %v", args, code, exitcode.Usage, err)
		}
		if len(captured) != 0 {
			t.Errorf("%v: the refusal must precede every round trip, saw %v", args, captured)
		}
	}
}

func TestChannelReadThreadSendsTheServerFilterAndComposes(t *testing.T) {
	gql, captured := captureGraphQL(t, map[string]string{
		"ChannelThread": `{"data":{"channelMessages":{"total":1,"items":[` + messageJSON + `]}}}`,
	})
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"channel", "read", "r", "--thread", "42", "--since", "10", "--before", "900", "--limit", "5", "--json", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	vars := threadVars(t, captured["ChannelThread"])
	if vars["replyToSeq"] != float64(42) || vars["sinceSeq"] != float64(10) || vars["beforeSeq"] != float64(900) || vars["limit"] != float64(5) {
		t.Errorf("filter must go to the server and compose with the cursors: %v", vars)
	}
	if !strings.Contains(out.String(), `"nextSince"`) {
		t.Errorf("output: %s", out.String())
	}

	// Omitted, never null, when not given.
	gql2, captured2 := captureGraphQL(t, map[string]string{
		"ChannelMessages": `{"data":{"channelMessages":{"total":0,"items":[]}}}`,
	})
	f2, _ := testFactory(t)
	root2 := NewRootCmd(f2)
	root2.SetArgs([]string{"channel", "read", "r", "--server", gql2.URL})
	if err := root2.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if _, has := threadVars(t, captured2["ChannelMessages"])["replyToSeq"]; has {
		t.Error("replyToSeq must be omitted when --thread is not given")
	}
}

func TestChannelReadThreadUnknownSeqIsNotFoundButEmptyThreadIsNot(t *testing.T) {
	unknown := `{"errors":[{"message":"replyToSeq 99 does not name a message in this channel.","extensions":{"code":"TEAM_CHAT_REPLY_NOT_FOUND"}}]}`
	gql, _ := captureGraphQL(t, map[string]string{"ChannelThread": unknown})
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"channel", "read", "r", "--thread", "99", "--server", gql.URL})
	err := root.Execute()
	if code := exitCodeFor(err); code != exitcode.NotFound {
		t.Fatalf("unknown thread seq: exit %d, want %d; err: %v", code, exitcode.NotFound, err)
	}

	gql2, _ := captureGraphQL(t, map[string]string{
		"ChannelThread": `{"data":{"channelMessages":{"total":0,"items":[]}}}`,
	})
	f2, out := testFactory(t)
	root2 := NewRootCmd(f2)
	root2.SetArgs([]string{"channel", "read", "r", "--thread", "42", "--server", gql2.URL})
	if err := root2.Execute(); err != nil {
		t.Fatalf("a message with no replies is a valid empty thread, got: %v", err)
	}
	if !strings.Contains(out.String(), "no messages") {
		t.Errorf("output: %s", out.String())
	}
}
