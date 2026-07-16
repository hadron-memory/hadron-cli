package cmd

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// chatMsg builds one findNodes hit for a ChatMessages response.
func chatMsg(loc string, seq int, data string) string {
	return `{"node":{"loc":"` + loc + `","seq":` + itoa(seq) + `,"data":` + data + `}}`
}

func itoa(n int) string { b, _ := json.Marshal(n); return string(b) }

func chatMessagesResp(hits ...string) string {
	return `{"data":{"findNodes":{"hits":[` + strings.Join(hits, ",") + `]}}}`
}

func TestChatReadJSON(t *testing.T) {
	gql, captured := captureGraphQL(t, map[string]string{
		"ChatMessages": chatMessagesResp(
			chatMsg("chats:api:messages:t1-iris", 2, `{"author":"iris","role":"Backend","body":"first"}`),
			chatMsg("chats:api:messages:t2-rufus", 5, `{"author":"rufus","body":"@iris second"}`),
		),
	})
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"chat", "read", "--node", "acme.com::tc::chats:api:messages", "--since", "1", "--json", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	// The findNodes filter must scope to the memory and the derived prefix.
	var vars struct {
		Filter struct {
			MemoryIds []string `json:"memoryIds"`
			LocPrefix string   `json:"locPrefix"`
		} `json:"filter"`
	}
	_ = json.Unmarshal(captured["ChatMessages"], &vars)
	if len(vars.Filter.MemoryIds) != 1 || vars.Filter.MemoryIds[0] != "acme.com::tc" {
		t.Errorf("memory filter: %+v", vars.Filter)
	}
	if vars.Filter.LocPrefix != "chats:api:messages:" {
		t.Errorf("--node's loc should become the message prefix, got %q", vars.Filter.LocPrefix)
	}
	var dto struct {
		Messages []struct {
			Seq    int    `json:"seq"`
			Author string `json:"author"`
			Body   string `json:"body"`
		} `json:"messages"`
		NextSince int `json:"nextSince"`
	}
	if err := json.Unmarshal([]byte(out.String()), &dto); err != nil {
		t.Fatalf("--json invalid: %v\n%s", err, out.String())
	}
	if len(dto.Messages) != 2 || dto.Messages[0].Author != "iris" || dto.Messages[1].Body != "@iris second" {
		t.Errorf("messages: %+v", dto.Messages)
	}
	if dto.NextSince != 5 {
		t.Errorf("nextSince should be the max seq, got %d", dto.NextSince)
	}
}

// --since filters out messages at or below the given seq (client-side).
func TestChatReadSinceFilter(t *testing.T) {
	gql, _ := captureGraphQL(t, map[string]string{
		"ChatMessages": chatMessagesResp(
			chatMsg("chats:api:messages:t1-iris", 2, `{"author":"iris","body":"old"}`),
			chatMsg("chats:api:messages:t2-rufus", 5, `{"author":"rufus","body":"new"}`),
		),
	})
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"chat", "read", "--node", "acme.com::tc::chats:api:messages", "--since", "3", "--json", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if strings.Contains(out.String(), "old") || !strings.Contains(out.String(), "new") {
		t.Errorf("--since 3 should drop seq<=3, got %s", out.String())
	}
}

// The human transcript renders "[seq] author (role): body".
func TestChatReadTranscript(t *testing.T) {
	gql, _ := captureGraphQL(t, map[string]string{
		"ChatMessages": chatMessagesResp(
			chatMsg("chats:api:messages:t1-iris", 2, `{"author":"iris","role":"Backend","body":"hello"}`),
		),
	})
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"chat", "read", "--node", "acme.com::tc::chats:api:messages", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if !strings.Contains(out.String(), "[2] iris (Backend): hello") {
		t.Errorf("transcript format wrong: %s", out.String())
	}
}

func TestChatPost(t *testing.T) {
	gql, captured := captureGraphQL(t, map[string]string{
		"CreateNode": `{"data":{"createNode":{"id":"n1","memoryId":"mem1","loc":"chats:api:messages:STAMP-iris","name":"Message from iris","nodeType":"message","tags":[],"seq":7,"isRunnable":false,"updatedAt":"2026-06-21T00:00:00Z"}}}`,
	})
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"chat", "post", "--node", "acme.com::tc::chats:api:messages", "--handle", "iris",
		"--role", "Backend", "--body", "@rufus schema looks good", "--json", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	var vars struct {
		Input struct {
			Loc      string          `json:"loc"`
			NodeType string          `json:"nodeType"`
			Data     json.RawMessage `json:"data"`
			Edges    json.RawMessage `json:"edges"`
		} `json:"input"`
	}
	_ = json.Unmarshal(captured["CreateNode"], &vars)
	if vars.Input.NodeType != "message" {
		t.Errorf("nodeType should be message, got %q", vars.Input.NodeType)
	}
	// loc = <prefix>:<colon/dot-free stamp>-<handle>; the id segment carries no colon.
	if !strings.HasPrefix(vars.Input.Loc, "chats:api:messages:") || !strings.HasSuffix(vars.Input.Loc, "-iris") {
		t.Errorf("loc shape wrong: %q", vars.Input.Loc)
	}
	idSeg := vars.Input.Loc[strings.LastIndex(vars.Input.Loc, ":")+1:]
	if strings.ContainsAny(idSeg, ".") {
		t.Errorf("stamp must strip dots, got id segment %q", idSeg)
	}
	var data struct {
		Author   string   `json:"author"`
		Body     string   `json:"body"`
		Role     string   `json:"role"`
		Mentions []string `json:"mentions"`
	}
	_ = json.Unmarshal(vars.Input.Data, &data)
	if data.Author != "iris" || data.Role != "Backend" || data.Body != "@rufus schema looks good" {
		t.Errorf("data payload wrong: %+v", data)
	}
	if len(data.Mentions) != 1 || data.Mentions[0] != "rufus" {
		t.Errorf("mentions should be parsed from body, got %v", data.Mentions)
	}
	// No --reply-to: edges omitted entirely.
	if len(vars.Input.Edges) > 0 && string(vars.Input.Edges) != "null" {
		t.Errorf("no reply must omit edges, got %s", vars.Input.Edges)
	}
	if !strings.Contains(out.String(), "\"seq\": 7") && !strings.Contains(out.String(), "\"seq\":7") {
		t.Errorf("post should surface the new seq, got %s", out.String())
	}
}

// --reply-to adds an inline reply edge from the new message to the target loc.
func TestChatPostReplyEdge(t *testing.T) {
	gql, captured := captureGraphQL(t, map[string]string{
		"CreateNode": `{"data":{"createNode":{"id":"n1","memoryId":"mem1","loc":"chats:api:messages:STAMP-iris","name":"m","nodeType":"message","tags":[],"seq":8,"isRunnable":false,"updatedAt":"2026-06-21T00:00:00Z"}}}`,
	})
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"chat", "post", "--node", "acme.com::tc::chats:api:messages", "--handle", "iris",
		"--body", "done", "--reply-to", "chats:api:messages:t1-rufus", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	var vars struct {
		Input struct {
			Edges []struct {
				TargetId string `json:"targetId"`
				Name     string `json:"name"`
			} `json:"edges"`
		} `json:"input"`
	}
	_ = json.Unmarshal(captured["CreateNode"], &vars)
	if len(vars.Input.Edges) != 1 || vars.Input.Edges[0].Name != "reply" || vars.Input.Edges[0].TargetId != "chats:api:messages:t1-rufus" {
		t.Errorf("reply edge wrong: %+v", vars.Input.Edges)
	}
}

// Identity and coordinates fall back to the project-local .hadron/config.json,
// so a configured agent posts with just --body.
func TestChatPostUsesProjectConfig(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, ".hadron"), 0o755); err != nil {
		t.Fatal(err)
	}
	cfg := `{"handle":"iris","chat":{"memory":"acme.com::tc","messagesLoc":"chats:api:messages","role":"Backend"}}`
	if err := os.WriteFile(filepath.Join(dir, ".hadron", "config.json"), []byte(cfg), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Chdir(dir)

	gql, captured := captureGraphQL(t, map[string]string{
		"CreateNode": `{"data":{"createNode":{"id":"n1","memoryId":"mem1","loc":"chats:api:messages:STAMP-iris","name":"m","nodeType":"message","tags":[],"seq":1,"isRunnable":false,"updatedAt":"2026-06-21T00:00:00Z"}}}`,
	})
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"chat", "post", "--body", "hi from config", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	var vars struct {
		Input struct {
			MemoryId string          `json:"memoryId"`
			Loc      string          `json:"loc"`
			Data     json.RawMessage `json:"data"`
		} `json:"input"`
	}
	_ = json.Unmarshal(captured["CreateNode"], &vars)
	if vars.Input.MemoryId != "acme.com::tc" {
		t.Errorf("memory should come from config, got %q", vars.Input.MemoryId)
	}
	if !strings.HasPrefix(vars.Input.Loc, "chats:api:messages:") || !strings.HasSuffix(vars.Input.Loc, "-iris") {
		t.Errorf("handle/prefix should come from config, got loc %q", vars.Input.Loc)
	}
	var data struct {
		Author string `json:"author"`
		Role   string `json:"role"`
	}
	_ = json.Unmarshal(vars.Input.Data, &data)
	if data.Author != "iris" || data.Role != "Backend" {
		t.Errorf("author/role should come from config, got %+v", data)
	}
}

// --body-file reads the message from a file (composed multi-line body).
func TestChatPostBodyFile(t *testing.T) {
	dir := t.TempDir()
	msg := "line one\nline two @rufus\n"
	file := filepath.Join(dir, "msg.md")
	if err := os.WriteFile(file, []byte(msg), 0o600); err != nil {
		t.Fatal(err)
	}
	gql, captured := captureGraphQL(t, map[string]string{
		"CreateNode": `{"data":{"createNode":{"id":"n1","memoryId":"mem1","loc":"chats:api:messages:STAMP-iris","name":"m","nodeType":"message","tags":[],"seq":3,"isRunnable":false,"updatedAt":"2026-06-21T00:00:00Z"}}}`,
	})
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"chat", "post", "--node", "acme.com::tc::chats:api:messages", "--handle", "iris",
		"--body-file", file, "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	var vars struct {
		Input struct {
			Data json.RawMessage `json:"data"`
		} `json:"input"`
	}
	_ = json.Unmarshal(captured["CreateNode"], &vars)
	var data struct {
		Body     string   `json:"body"`
		Mentions []string `json:"mentions"`
	}
	_ = json.Unmarshal(vars.Input.Data, &data)
	if data.Body != msg {
		t.Errorf("body should be the file's contents verbatim, got %q", data.Body)
	}
	if len(data.Mentions) != 1 || data.Mentions[0] != "rufus" {
		t.Errorf("mentions parsed from a file body too, got %v", data.Mentions)
	}
}

// --body and --body-file are mutually exclusive; neither is a usage error.
func TestChatPostBodySourceExclusive(t *testing.T) {
	cases := [][]string{
		{"--body", "hi", "--body-file", "/tmp/x"}, // both
		{}, // neither
	}
	for _, extra := range cases {
		f, _ := testFactory(t)
		root := NewRootCmd(f)
		args := append([]string{"chat", "post", "--node", "acme.com::tc::chats:api:messages", "--handle", "iris"}, extra...)
		root.SetArgs(append(args, "--server", "http://127.0.0.1:1"))
		if err := root.Execute(); err == nil {
			t.Fatalf("expected a usage error for body args %v", extra)
		}
	}
}

// A single chat.node URN in config supplies both memory and message location.
func TestChatPostUsesNodeConfig(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, ".hadron"), 0o755); err != nil {
		t.Fatal(err)
	}
	cfg := `{"handle":"iris","chat":{"node":"acme.com::tc::team-chat:api:messages","role":"Backend"}}`
	if err := os.WriteFile(filepath.Join(dir, ".hadron", "config.json"), []byte(cfg), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Chdir(dir)

	gql, captured := captureGraphQL(t, map[string]string{
		"CreateNode": `{"data":{"createNode":{"id":"n1","memoryId":"mem1","loc":"team-chat:api:messages:STAMP-iris","name":"m","nodeType":"message","tags":[],"seq":2,"isRunnable":false,"updatedAt":"2026-06-21T00:00:00Z"}}}`,
	})
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"chat", "post", "--body", "hi", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	var vars struct {
		Input struct {
			MemoryId string `json:"memoryId"`
			Loc      string `json:"loc"`
		} `json:"input"`
	}
	_ = json.Unmarshal(captured["CreateNode"], &vars)
	if vars.Input.MemoryId != "acme.com::tc" {
		t.Errorf("chat.node should supply the memory, got %q", vars.Input.MemoryId)
	}
	if !strings.HasPrefix(vars.Input.Loc, "team-chat:api:messages:") {
		t.Errorf("chat.node's loc should be the message prefix, got %q", vars.Input.Loc)
	}
}

// --node packs memory + loc, so it's mutually exclusive with -m / --messages-loc.
func TestChatNodeExclusiveWithMemory(t *testing.T) {
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"chat", "read", "--node", "a::b::c:d", "-m", "a::b", "--server", "http://127.0.0.1:1"})
	if err := root.Execute(); err == nil {
		t.Fatal("expected mutual-exclusivity error for --node with -m")
	}
}

func TestChatNodeRejectsAmbiguousSingleColonURN(t *testing.T) {
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"chat", "read", "--node", "acme.com:tc:chats:api:messages", "--server", "http://127.0.0.1:1"})
	err := root.Execute()
	if err == nil || !strings.Contains(err.Error(), "not a fully-qualified node URN") {
		t.Fatalf("expected ambiguous single-colon node URN rejection, got %v", err)
	}
}

// post best-effort materializes the message-parent node (nodeType chat) so the
// chat is a real, copyable node — alongside the message itself.
func TestChatPostMaterializesParent(t *testing.T) {
	var locs []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Variables struct {
				Input struct {
					Loc string `json:"loc"`
				} `json:"input"`
			} `json:"variables"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		if body.Variables.Input.Loc != "" {
			locs = append(locs, body.Variables.Input.Loc)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":{"createNode":{"id":"n1","memoryId":"mem1","loc":"team-chat:api:messages:x-iris","name":"m","nodeType":"message","tags":[],"seq":1,"isRunnable":false,"updatedAt":"2026-06-21T00:00:00Z"}}}`))
	}))
	t.Cleanup(srv.Close)

	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"chat", "post", "--node", "acme.com::tc::team-chat:api:messages",
		"--handle", "iris", "--body", "hi", "--server", srv.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	var sawParent, sawMessage bool
	for _, l := range locs {
		if l == "team-chat:api:messages" {
			sawParent = true
		}
		if strings.HasPrefix(l, "team-chat:api:messages:") {
			sawMessage = true
		}
	}
	if !sawParent {
		t.Errorf("post should materialize the parent loc team-chat:api:messages, saw %v", locs)
	}
	if !sawMessage {
		t.Errorf("post should create the message under the parent, saw %v", locs)
	}
}

func TestChatReadRequiresCoords(t *testing.T) {
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	// --messages-loc without -m/--node and (in the test's cwd) no config → error.
	root.SetArgs([]string{"chat", "read", "--messages-loc", "chats:api:messages", "--server", "http://127.0.0.1:1"})
	err := root.Execute()
	if err == nil || !strings.Contains(err.Error(), "no chat") {
		t.Fatalf("expected missing-coordinates usage error, got %v", err)
	}
}
