package cmd

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/api"
)

// cli#1690 (S1 of hadron-server#3493): `comment add` starts the caller's
// thread, or appends to the one they already have, in one call. These pins
// drive the COMPILED binary against a stateful fake that holds the threads, so
// the create-then-reply race is a real sequence of requests, not a stub.

type addCall struct {
	op, session, auth string
	vars              map[string]json.RawMessage
}

type addThread struct {
	id       string
	resolved bool
	deleted  bool
}

// addServer is the stateful fake: one caller, any number of threads. A create
// is refused with COMMENT_OPEN_THREAD_EXISTS exactly when the caller has an
// open (unresolved, undeleted) thread; a reply to a deleted thread is refused
// with COMMENT_DELETED and never reopens a resolved one.
type addServer struct {
	mu      sync.Mutex
	calls   []addCall
	threads map[string]*addThread
	next    int
	// between runs after a refused create and before the reply reaches the
	// server: the window in which the thread can change under the caller.
	between func(s *addServer)
	// createOverride / replyOverride replace the server's answer wholesale.
	createOverride, replyOverride func(n int) (status int, body string)
}

func newAddServer() *addServer { return &addServer{threads: map[string]*addThread{}} }

func (s *addServer) seed(id string, resolved bool) {
	s.threads[id] = &addThread{id: id, resolved: resolved}
}

func (s *addServer) ops() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []string
	for _, c := range s.calls {
		out = append(out, c.op)
	}
	return out
}

func (s *addServer) openThread() *addThread {
	for _, t := range s.threads {
		if !t.resolved && !t.deleted {
			return t
		}
	}
	return nil
}

func addComment(id, body string, root bool) string {
	b, _ := json.Marshal(body)
	out := strings.Replace(commentResult, `"id":"c1"`, `"id":"`+id+`"`, 1)
	out = strings.Replace(out, `"body":"body"`, `"body":`+string(b), 1)
	if !root {
		out = strings.Replace(out, `"isTopLevel":true`, `"isTopLevel":false`, 1)
	}
	return out
}

func addRefusal(code string, ext map[string]any) string {
	e := map[string]any{"code": code}
	for k, v := range ext {
		e[k] = v
	}
	b, _ := json.Marshal(map[string]any{"errors": []any{map[string]any{"message": "refused: " + code, "extensions": e}}})
	return string(b)
}

func (s *addServer) handler(t *testing.T) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"capabilities":[]}`))
			return
		}
		var req struct {
			Op   string                     `json:"operationName"`
			Vars map[string]json.RawMessage `json:"variables"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		if req.Op == "GetBoundSessionEnd" { // #914's notice read, answered open and not counted
			w.Header().Set("Content-Type", "application/json")
			resp, _ := unstubbedDefault(req.Op)
			_, _ = w.Write([]byte(resp))
			return
		}
		s.mu.Lock()
		defer s.mu.Unlock()
		s.calls = append(s.calls, addCall{req.Op, r.Header.Get(api.SessionHeader), r.Header.Get("Authorization"), req.Vars})
		n := len(s.calls)
		w.Header().Set("Content-Type", "application/json")
		respond := func(status int, body string) {
			w.WriteHeader(status)
			_, _ = w.Write([]byte(body))
		}
		var body string
		_ = json.Unmarshal(req.Vars["body"], &body)
		switch req.Op {
		case "CreateComment":
			if s.createOverride != nil {
				st, b := s.createOverride(n)
				if st == -1 { // drop the connection: a transport error
					hj, _ := w.(http.Hijacker)
					c, _, _ := hj.Hijack()
					_ = c.Close()
					return
				}
				respond(st, b)
				return
			}
			if open := s.openThread(); open != nil {
				respond(200, addRefusal("COMMENT_OPEN_THREAD_EXISTS", map[string]any{"threadId": open.id}))
				if s.between != nil {
					s.between(s)
				}
				return
			}
			s.next++
			id := fmt.Sprintf("new%d", s.next)
			s.threads[id] = &addThread{id: id}
			respond(200, `{"data":{"createComment":`+addComment(id, body, true)+`}}`)
		case "ReplyToComment":
			if s.replyOverride != nil {
				st, b := s.replyOverride(n)
				respond(st, b)
				return
			}
			var ref string
			_ = json.Unmarshal(req.Vars["ref"], &ref)
			th := s.threads[ref]
			switch {
			case th == nil:
				respond(200, addRefusal("COMMENT_NOT_FOUND", nil))
			case th.deleted:
				respond(200, addRefusal("COMMENT_DELETED", nil))
			default:
				respond(200, `{"data":{"replyToComment":`+addComment("r1", body, false)+`}}`)
			}
		default:
			t.Errorf("unexpected op %s", req.Op)
			respond(200, `{"data":{}}`)
		}
	}
}

type addRun struct {
	stdout, stderr string
	code           int
}

func runAdd(t *testing.T, s *addServer, stdin string, args ...string) (addRun, *httptest.Server) {
	t.Helper()
	bin := footerBinary(t)
	teamGitDir(t)
	testFactory(t)
	srv := httptest.NewServer(s.handler(t))
	t.Cleanup(srv.Close)
	writeTeamBinding(t)
	setTeamBindingServer(t, srv.URL)
	cmd := exec.Command(bin, append(args, "--server", srv.URL)...)
	var so, se bytes.Buffer
	cmd.Stdout, cmd.Stderr = &so, &se
	cmd.Stdin = strings.NewReader(stdin)
	err := cmd.Run()
	code := 0
	if ee, ok := err.(*exec.ExitError); ok {
		code = ee.ExitCode()
	} else if err != nil {
		t.Fatal(err)
	}
	return addRun{so.String(), se.String(), code}, srv
}

const addTarget = "hrn:node:acme.com:kb:target"

func addJSON(t *testing.T, out string) map[string]any {
	t.Helper()
	var dto map[string]any
	if err := json.Unmarshal([]byte(out), &dto); err != nil {
		t.Fatalf("not JSON: %v\n%s", err, out)
	}
	return dto
}

func rawStr(t *testing.T, raw json.RawMessage) string {
	t.Helper()
	var s string
	if err := json.Unmarshal(raw, &s); err != nil {
		t.Fatalf("%s: %v", raw, err)
	}
	return s
}

func TestCommentAddStartsWhenThereIsNoOpenThread(t *testing.T) {
	s := newAddServer()
	got, _ := runAdd(t, s, "", "comment", "add", addTarget, "--body", "hello")
	if got.code != 0 || strings.Join(s.ops(), ",") != "CreateComment" {
		t.Fatalf("code=%d ops=%v err=%s", got.code, s.ops(), got.stderr)
	}
	if first := strings.SplitN(got.stdout, "\n", 2)[0]; first != "started thread new1" {
		t.Fatalf("first line %q", first)
	}
	s = newAddServer()
	got, _ = runAdd(t, s, "", "comment", "add", addTarget, "--body", "hello", "--json")
	dto := addJSON(t, got.stdout)
	if dto["outcome"] != "started" || dto["threadId"] != "new1" || dto["composedQuote"] != false {
		t.Fatal(got.stdout)
	}
	if c, _ := dto["comment"].(map[string]any); c["id"] != "new1" || c["body"] != "hello" {
		t.Fatal(got.stdout)
	}
}

// Red on main: add was an alias of create, so a conflict exited 5 and sent no
// reply. Here it must reply, once, to the thread the refusal named.
func TestCommentAddRepliesToTheOpenThread(t *testing.T) {
	s := newAddServer()
	s.seed("T1", false)
	got, _ := runAdd(t, s, "", "comment", "add", addTarget, "--body", "second note")
	if got.code != 0 || strings.Join(s.ops(), ",") != "CreateComment,ReplyToComment" {
		t.Fatalf("code=%d ops=%v err=%s", got.code, s.ops(), got.stderr)
	}
	reply := s.calls[1]
	if rawStr(t, reply.vars["ref"]) != "T1" || rawStr(t, reply.vars["body"]) != "second note" {
		t.Fatalf("reply vars %v", reply.vars)
	}
	if s.calls[0].auth != reply.auth || s.calls[0].session != reply.session || reply.session == "" {
		t.Fatalf("calls must share one bound credential: %+v / %+v", s.calls[0], reply)
	}
	if first := strings.SplitN(got.stdout, "\n", 2)[0]; first != "replied to your open thread T1" {
		t.Fatalf("first line %q", first)
	}
	s = newAddServer()
	s.seed("T1", false)
	got, _ = runAdd(t, s, "", "comment", "add", addTarget, "--body", "second note", "--json")
	dto := addJSON(t, got.stdout)
	if dto["outcome"] != "replied" || dto["threadId"] != "T1" || dto["composedQuote"] != false {
		t.Fatal(got.stdout)
	}
	if c, _ := dto["comment"].(map[string]any); c["id"] != "r1" {
		t.Fatal(got.stdout)
	}
}

func TestCommentAddStartsWhenTheEarlierThreadWasResolved(t *testing.T) {
	s := newAddServer()
	s.seed("T0", true)
	got, _ := runAdd(t, s, "", "comment", "add", addTarget, "--body", "again", "--json")
	if got.code != 0 || strings.Join(s.ops(), ",") != "CreateComment" || addJSON(t, got.stdout)["outcome"] != "started" {
		t.Fatalf("ops=%v out=%s", s.ops(), got.stdout)
	}
}

// Everything that is not a typed open-thread conflict with a usable threadId
// is passed through unchanged: one request, no reply, no retry, nothing on stdout.
func TestCommentAddPassesEverythingElseThroughUnchanged(t *testing.T) {
	conflict := func(ext map[string]any) string { return addRefusal("COMMENT_OPEN_THREAD_EXISTS", ext) }
	two, _ := json.Marshal(map[string]any{"errors": []any{
		map[string]any{"message": "first", "extensions": map[string]any{"code": "COMMENT_FORBIDDEN"}},
		map[string]any{"message": "second", "extensions": map[string]any{"code": "COMMENT_OPEN_THREAD_EXISTS", "threadId": "T9"}},
	}})
	cases := []struct {
		name   string
		status int
		body   string
		want   int
	}{
		{"forbidden", 200, addRefusal("COMMENT_FORBIDDEN", nil), 8},
		{"target not found", 200, addRefusal("COMMENT_TARGET_NOT_FOUND", nil), 4},
		{"not commentable", 200, addRefusal("COMMENT_MEMORY_NOT_COMMENTABLE", nil), 2},
		{"target is comment", 200, addRefusal("COMMENT_TARGET_IS_COMMENT", nil), 2},
		{"impersonation", 200, addRefusal("COMMENT_IMPERSONATION_REFUSED", nil), 8},
		{"threadId missing", 200, conflict(nil), 5},
		{"threadId empty", 200, conflict(map[string]any{"threadId": ""}), 5},
		{"threadId null", 200, conflict(map[string]any{"threadId": nil}), 5},
		{"threadId number", 200, conflict(map[string]any{"threadId": 7}), 5},
		{"threadId object", 200, conflict(map[string]any{"threadId": map[string]any{"a": 1}}), 5},
		{"conflict is not the first error", 200, string(two), 8},
		{"message mentions the code, no typed code", 200, `{"errors":[{"message":"COMMENT_OPEN_THREAD_EXISTS threadId T9","extensions":{"code":"COMMENT_FORBIDDEN","threadId":"T9"}}]}`, 8},
		{"message only, no code", 200, `{"errors":[{"message":"COMMENT_OPEN_THREAD_EXISTS threadId=T9"}]}`, -1},
		{"malformed body", 200, `{"data":{"createComment":null}}`, 7},
		{"5xx carrying a typed conflict envelope", 502, conflict(map[string]any{"threadId": "T9"}), -1},
		{"5xx", 500, `{"error":"boom"}`, -1},
		{"transport error", -1, "", -1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := newAddServer()
			s.createOverride = func(int) (int, string) { return tc.status, tc.body }
			got, _ := runAdd(t, s, "", "comment", "add", addTarget, "--body", "x")
			if strings.Join(s.ops(), ",") != "CreateComment" || got.stdout != "" {
				t.Fatalf("ops=%v stdout=%q", s.ops(), got.stdout)
			}
			if got.code == 0 || (tc.want > 0 && got.code != tc.want) {
				t.Fatalf("exit=%d want %d stderr=%s", got.code, tc.want, got.stderr)
			}
		})
	}
}

func TestCommentAddReplyRefusedKeepsTheReplysCodeAndNamesTheThread(t *testing.T) {
	for code, want := range map[string]int{"COMMENT_DELETED": 5, "COMMENT_NOT_FOUND": 4, "COMMENT_FORBIDDEN": 8} {
		t.Run(code, func(t *testing.T) {
			s := newAddServer()
			s.seed("T1", false)
			s.replyOverride = func(int) (int, string) { return 200, addRefusal(code, nil) }
			got, _ := runAdd(t, s, "", "comment", "add", addTarget, "--body", "x")
			if got.code != want || got.stdout != "" || !strings.Contains(got.stderr, "T1") || !strings.Contains(got.stderr, code) {
				t.Fatalf("exit=%d stdout=%q stderr=%s", got.code, got.stdout, got.stderr)
			}
			if strings.Join(s.ops(), ",") != "CreateComment,ReplyToComment" {
				t.Fatalf("ops=%v", s.ops())
			}
			s = newAddServer()
			s.seed("T1", false)
			s.replyOverride = func(int) (int, string) { return 200, addRefusal(code, nil) }
			got, _ = runAdd(t, s, "", "comment", "add", addTarget, "--body", "x", "--json")
			var env struct {
				Error struct {
					Code       int            `json:"code"`
					Extensions map[string]any `json:"extensions"`
				} `json:"error"`
			}
			if err := json.Unmarshal([]byte(got.stdout), &env); err != nil {
				t.Fatalf("%v\n%s", err, got.stdout)
			}
			if env.Error.Code != want || env.Error.Extensions["threadId"] != "T1" || env.Error.Extensions["code"] != code {
				t.Fatalf("envelope %s", got.stdout)
			}
		})
	}
}

func TestCommentAddRaceNeverReopensRecreatesOrLoops(t *testing.T) {
	t.Run("resolved in between: the reply lands, the thread stays resolved", func(t *testing.T) {
		s := newAddServer()
		s.seed("T1", false)
		s.between = func(s *addServer) { s.threads["T1"].resolved = true }
		got, _ := runAdd(t, s, "", "comment", "add", addTarget, "--body", "x", "--json")
		if got.code != 0 || addJSON(t, got.stdout)["outcome"] != "replied" || strings.Join(s.ops(), ",") != "CreateComment,ReplyToComment" || !s.threads["T1"].resolved {
			t.Fatalf("code=%d ops=%v out=%s", got.code, s.ops(), got.stdout)
		}
	})
	t.Run("deleted in between: the reply's refusal, no second create", func(t *testing.T) {
		s := newAddServer()
		s.seed("T1", false)
		s.between = func(s *addServer) { s.threads["T1"].deleted = true }
		got, _ := runAdd(t, s, "", "comment", "add", addTarget, "--body", "x")
		if got.code != 5 || strings.Join(s.ops(), ",") != "CreateComment,ReplyToComment" || !strings.Contains(got.stderr, "COMMENT_DELETED") {
			t.Fatalf("code=%d ops=%v err=%s", got.code, s.ops(), got.stderr)
		}
	})
}

func TestCommentAddBodySourcesSendIdenticalBytesOnBothPaths(t *testing.T) {
	const text = "line one\n\n  indented `code` ünï\n"
	file := filepath.Join(t.TempDir(), "body.md")
	if err := os.WriteFile(file, []byte(text), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, stdin string
		args        []string
	}{
		{"--body", "", []string{"--body", text}},
		{"--body-file", "", []string{"--body-file", file}},
		{"--body -", text, []string{"--body", "-"}},
	} {
		for _, seeded := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/open=%t", tc.name, seeded), func(t *testing.T) {
				s := newAddServer()
				if seeded {
					s.seed("T1", false)
				}
				got, _ := runAdd(t, s, tc.stdin, append([]string{"comment", "add", addTarget}, tc.args...)...)
				if got.code != 0 {
					t.Fatalf("exit=%d %s", got.code, got.stderr)
				}
				for _, c := range s.calls {
					if rawStr(t, c.vars["body"]) != text {
						t.Fatalf("%s sent %q", c.op, c.vars["body"])
					}
				}
				if want := map[bool]int{false: 1, true: 2}[seeded]; len(s.calls) != want {
					t.Fatalf("calls=%d", len(s.calls))
				}
			})
		}
	}
}

func TestCommentAddQuoteAndAnchor(t *testing.T) {
	t.Run("start path sends them as create does", func(t *testing.T) {
		s := newAddServer()
		got, _ := runAdd(t, s, "", "comment", "add", addTarget, "--body", "b", "--quote", "q1\nq2", "--anchor-revision", "2", "--json")
		v := s.calls[0].vars
		if got.code != 0 || rawStr(t, v["quote"]) != "q1\nq2" || string(v["anchor"]) != "2" || addJSON(t, got.stdout)["composedQuote"] != false {
			t.Fatalf("vars=%v out=%s", v, got.stdout)
		}
	})
	t.Run("reply path folds both into the reply body, losslessly", func(t *testing.T) {
		s := newAddServer()
		s.seed("T1", false)
		got, _ := runAdd(t, s, "", "comment", "add", addTarget, "--body", "the text", "--quote", "q1\nq2", "--anchor-revision", "2", "--json")
		want := "> On revision 2:\n> q1\n> q2\n\nthe text"
		if got.code != 0 || rawStr(t, s.calls[1].vars["body"]) != want || addJSON(t, got.stdout)["composedQuote"] != true {
			t.Fatalf("body=%s out=%s", s.calls[1].vars["body"], got.stdout)
		}
		if _, ok := s.calls[1].vars["quote"]; ok {
			t.Fatal("replyToComment takes no quote")
		}
	})
	t.Run("quote alone / anchor alone", func(t *testing.T) {
		for args, want := range map[string]string{"--quote": "> only\n\nb", "--anchor-revision": "> On revision 3:\n\nb"} {
			s := newAddServer()
			s.seed("T1", false)
			val := map[string]string{"--quote": "only", "--anchor-revision": "3"}[args]
			runAdd(t, s, "", "comment", "add", addTarget, "--body", "b", args, val)
			if got := rawStr(t, s.calls[1].vars["body"]); got != want {
				t.Fatalf("%s: %q want %q", args, got, want)
			}
		}
	})
	t.Run("the text output says the quote was folded", func(t *testing.T) {
		s := newAddServer()
		s.seed("T1", false)
		got, _ := runAdd(t, s, "", "comment", "add", addTarget, "--body", "b", "--quote", "q")
		if !strings.Contains(got.stdout, "quote") || !strings.Contains(strings.SplitN(got.stdout, "\n", 3)[1], "folded") {
			t.Fatalf("no fold note: %s", got.stdout)
		}
	})
}

func TestCommentAddKeepsCreateFlagValidation(t *testing.T) {
	for _, args := range [][]string{
		{"add", addTarget},
		{"add", addTarget, "--body", "x", "--body-file", "f"},
		{"add", addTarget, "--body", "  "},
		{"add", addTarget, "--body", "x", "--anchor-revision", "0"},
		{"add", "bare-loc", "--body", "x"},
	} {
		s := newAddServer()
		got, _ := runAdd(t, s, "", append([]string{"comment"}, args...)...)
		if got.code != 2 || len(s.calls) != 0 {
			t.Fatalf("%v: exit=%d calls=%d", args, got.code, len(s.calls))
		}
	}
}

func TestCommentCreateIsUnchangedAndNoLongerAliasedByAdd(t *testing.T) {
	s := newAddServer()
	s.seed("T1", false)
	got, _ := runAdd(t, s, "", "comment", "create", addTarget, "--body", "x")
	if got.code != 5 || strings.Join(s.ops(), ",") != "CreateComment" || got.stdout != "" || !strings.Contains(got.stderr, "threadId: T1") {
		t.Fatalf("create must still refuse: exit=%d ops=%v err=%s", got.code, s.ops(), got.stderr)
	}
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"comment", "create", "--help"})
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out.String(), "Aliases") || !strings.Contains(out.String(), "comment add") {
		t.Fatalf("create help must drop the alias and point at add:\n%s", out.String())
	}
}
