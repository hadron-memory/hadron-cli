package team

import (
	"encoding/json"
	"fmt"
	"github.com/gofrs/flock"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/api"
	"github.com/hadron-memory/hadron-cli/internal/cmdutil"
	"github.com/hadron-memory/hadron-cli/internal/exitcode"
	"github.com/hadron-memory/hadron-cli/internal/output"
)

type checkFixture struct {
	mode                            string
	requests, marks, pages, cursors int
	artifact                        string
}

func TestChatCheckProductionTransport(t *testing.T) {
	for _, mode := range []string{"normal", "empty", "page_error", "path_error", "wrong_page_incarnation", "lost_confirmed", "lost_legacy_retry", "lost_twice", "lost_mismatch", "wrong_mark_incarnation", "mismatch", "binding_changed", "ended", "unavailable_metadata"} {
		t.Run(mode, func(t *testing.T) {
			t.Setenv("HADRON_TOKEN", "fixture")
			t.Setenv("XDG_CONFIG_HOME", t.TempDir())
			t.Setenv("XDG_CACHE_HOME", t.TempDir())
			dir := t.TempDir()
			t.Setenv(GitDirEnv, dir)
			streams, out, notice := output.Test()
			state := &checkFixture{mode: mode}
			var boundFile string
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var q struct {
					OperationName, Query string
					Variables            map[string]json.RawMessage
				}
				_ = json.NewDecoder(r.Body).Decode(&q)
				state.requests++
				if r.Header.Get(api.SessionHeader) != "s1" {
					t.Errorf("missing own session on %s", q.OperationName)
				}
				if r.Header.Get("Authorization") != "Bearer fixture" {
					t.Error("credential changed")
				}
				w.Header().Set("Content-Type", "application/json")
				switch q.OperationName {
				case "CheckChatSession":
					ended := "null"
					if mode == "ended" {
						ended = `"2026-01-01"`
					}
					fmt.Fprintf(w, `{"data":{"session":{"id":"s1","appId":"app","workerId":"worker","endedAt":%s,"isLive":true}}}`, ended)
				case "CheckTeamChannel":
					if mode == "unavailable_metadata" {
						_, _ = io.WriteString(w, `{"data":{"teamChatMessages":{"channelId":null,"channelIncarnation":null}}}`)
						return
					}
					if !strings.Contains(q.Query, "advanceReadState: false") {
						t.Error("channel sample can mark")
					}
					_, _ = io.WriteString(w, `{"data":{"teamChatMessages":{"channelId":"chan","channelIncarnation":2}}}`)
				case "CheckChatCursor":
					state.cursors++
					cursor := 10
					label := "null"
					currentInc := 2
					if mode == "lost_mismatch" && state.cursors > 1 {
						currentInc = 3
					}
					if state.cursors > 1 && mode == "lost_confirmed" {
						cursor = 900
						label = "2"
					}
					fmt.Fprintf(w, `{"data":{"channelReadState":{"channelId":"chan","channelIncarnation":%d,"cursorIncarnation":%s,"lastSeenSeq":%d}}}`, currentInc, label, cursor)
				case "CheckChatHead":
					if !strings.Contains(q.Query, "advanceReadState: false") || string(q.Variables["incarnation"]) != "2" {
						t.Error("head not fenced")
					}
					_, _ = io.WriteString(w, `{"data":{"channelMessages":{"channelId":"chan","channelIncarnation":2,"items":[{"seq":900}]}}}`)
				case "CheckChatPage":
					state.pages++
					if !strings.Contains(q.Query, "advanceReadState: false") || string(q.Variables["incarnation"]) != "2" || string(q.Variables["worker"]) != `"worker"` || string(q.Variables["since"]) != "10" {
						t.Error("missing page boundary")
					}
					if mode == "page_error" {
						w.WriteHeader(502)
						return
					}
					inc := 2
					if mode == "wrong_page_incarnation" {
						inc = 3
					}
					ms := []map[string]any{}
					// Deliberately short pages: a short page does NOT terminate the drain.
					if mode != "empty" {
						switch string(q.Variables["before"]) {
						case "901":
							ms = []map[string]any{{"seq": 600, "body": "@all older", "nodeId": "n600"}, {"seq": 850, "body": "newer", "nodeId": "n850"}}
						case "600":
							ms = []map[string]any{{"seq": 80, "body": "oldest", "nodeId": "n80"}}
						case "80":
						default:
							t.Error("wrong backward boundary", string(q.Variables["before"]))
						}
					}
					if mode == "binding_changed" {
						_ = os.WriteFile(boundFile, []byte(`{"appId":"app","workerId":"worker","sessionId":"s2","server":"http://localhost"}`), 0600)
					}
					_ = json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{"channelMessages": map[string]any{"channelId": "chan", "channelIncarnation": inc, "items": ms}}})
				case "CheckChatMark":
					state.marks++
					if string(q.Variables["through"]) != "900" || string(q.Variables["incarnation"]) != "2" || string(q.Variables["channelRef"]) != `"chan"` || string(q.Variables["session"]) != `"s1"` {
						t.Error("mark drift", q.Variables)
					}
					if mode != "empty" {
						text := notice.String()
						start := strings.Index(text, `"`)
						end := strings.Index(text[start+1:], `"`)
						if start < 0 || end < 0 {
							t.Fatal("mark before artifact path")
						}
						state.artifact = text[start+1 : start+1+end]
						data, e := os.ReadFile(state.artifact)
						if e != nil {
							t.Error(e)
						}
						var a checkArtifact
						_ = json.Unmarshal(data, &a)
						if len(a.Messages) != 3 || a.Messages[0].Seq != 80 || a.Messages[2].Seq != 850 || a.SampledHead != 900 {
							t.Errorf("incomplete artifact %+v", a)
						}
					}
					if mode == "mismatch" {
						_, _ = io.WriteString(w, `{"errors":[{"message":"PRIVATE_SENTINEL","extensions":{"code":"CHANNEL_INCARNATION_CHANGED"}}]}`)
						return
					}
					if mode == "lost_twice" || (state.marks == 1 && (mode == "lost_confirmed" || mode == "lost_legacy_retry" || mode == "lost_mismatch")) {
						w.WriteHeader(502)
						return
					}
					incarnation := 2
					if mode == "wrong_mark_incarnation" {
						incarnation = 3
					}
					_, _ = fmt.Fprintf(w, `{"data":{"markOwnTeamChatRead":{"workerId":"worker","channelId":"chan","channelIncarnation":%d,"cursorIncarnation":null,"lastSeenSeq":900}}}`, incarnation)
				default:
					t.Errorf("unexpected operation %s", q.OperationName)
				}
			}))
			defer srv.Close()
			b, _ := json.Marshal(binding{AppID: "app", WorkerID: "worker", SessionID: "s1", Server: srv.URL})
			boundFile = filepath.Join(dir, bindingFileName)
			_ = os.WriteFile(boundFile, b, 0600)
			f := cmdutil.NewFactory()
			f.ServerFlag = srv.URL
			f.IOStreams = streams
			f.JSON = true
			if mode == "path_error" {
				f.IOStreams.ErrOut = checkBrokenWriter{}
			}
			c := newCmdTeamChatCheck(f)
			c.SetArgs(nil)
			err := c.Execute()
			code := exitcode.FromError(err)
			want := 0
			switch mode {
			case "empty":
				want = 9
			case "page_error":
				want = 7
			case "path_error":
				want = 1
			case "wrong_page_incarnation", "mismatch", "binding_changed", "ended":
				want = 5
			case "unavailable_metadata":
				want = 4
			case "lost_twice", "lost_mismatch", "wrong_mark_incarnation":
				want = 10
			}
			if code != want {
				t.Fatalf("exit=%d want=%d err=%v out=%s notice=%s", code, want, err, out.String(), notice.String())
			}
			switch mode {
			case "path_error", "page_error", "wrong_page_incarnation", "binding_changed", "ended", "unavailable_metadata":
				if state.marks != 0 {
					t.Fatal("mark after pre-mark failure")
				}
			case "lost_twice", "lost_legacy_retry":
				if state.marks != 2 {
					t.Fatal(state.marks)
				}
			default:
				if state.marks != 1 {
					t.Fatal(state.marks)
				}
			}
			if strings.Contains(out.String()+notice.String(), "PRIVATE_SENTINEL") {
				t.Fatal("upstream diagnostic leak")
			}
			if state.artifact != "" {
				fi, _ := os.Stat(state.artifact)
				di, _ := os.Stat(filepath.Dir(state.artifact))
				if fi.Mode().Perm() != 0600 || di.Mode().Perm() != 0700 {
					t.Fatal("artifact permissions")
				}
			}
		})
	}
}

type checkBrokenWriter struct{}

func (checkBrokenWriter) Write([]byte) (int, error) { return 0, io.ErrClosedPipe }
func TestChatCheckRegistered(t *testing.T) {
	c := NewCmdTeam(cmdutil.NewFactory())
	found, _, e := c.Find([]string{"chat", "check"})
	if e != nil || found.Name() != "check" {
		t.Fatal(e, found)
	}
}

func TestChatCheckLocalBoundaryRefusals(t *testing.T) {
	for _, mode := range []string{"unbound", "server", "app", "lock"} {
		t.Run(mode, func(t *testing.T) {
			t.Setenv("HADRON_TOKEN", "fixture")
			t.Setenv("XDG_CONFIG_HOME", t.TempDir())
			dir := t.TempDir()
			t.Setenv(GitDirEnv, dir)
			calls := 0
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				_, _ = io.WriteString(w, `{"data":{"app":{"id":"other-app","name":"Other","urn":null}}}`)
			}))
			defer srv.Close()
			b := binding{AppID: "app", WorkerID: "worker", SessionID: "s1", Server: srv.URL}
			if mode == "server" {
				b.Server = "https://other.invalid"
			}
			path := filepath.Join(dir, bindingFileName)
			raw, _ := json.Marshal(b)
			if mode != "unbound" {
				_ = os.WriteFile(path, raw, 0600)
			}
			f := cmdutil.NewFactory()
			f.ServerFlag = srv.URL
			f.IOStreams, _, _ = output.Test()
			if mode == "app" {
				f.AppFlag = "hrn:app:example.com:other"
			}
			if mode == "lock" {
				lock := flock.New(path + ".chat-check.lock")
				ok, e := lock.TryLock()
				if e != nil || !ok {
					t.Fatal(e)
				}
				defer func() { _ = lock.Unlock() }()
			}
			c := newCmdTeamChatCheck(f)
			err := c.Execute()
			want := 5
			if mode == "unbound" {
				want = 2
			}
			if exitcode.FromError(err) != want {
				t.Fatal(err)
			}
			expected := 0
			if mode == "app" {
				expected = 1
			}
			if calls != expected {
				t.Fatal("unexpected transport", calls)
			}
		})
	}
}
