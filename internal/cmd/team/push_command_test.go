package team

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/cmdutil"
	"github.com/hadron-memory/hadron-cli/internal/delivery"
	"github.com/hadron-memory/hadron-cli/internal/exitcode"
	"github.com/hadron-memory/hadron-cli/internal/orcaterm"
	"github.com/hadron-memory/hadron-cli/internal/output"
)

// Command-level GraphQL and Orca transports are fakes. Only the disposable git binding directory is real.
func TestPushCommandOutcomesPrivacyAndNoMutations(t *testing.T) {
	for _, mode := range []string{"sent", "output-failure", "locked", "unknown", "busy", "unconfirmed", "wrong-tool", "ended", "foreign-app", "missing-server", "sender-ended", "server-error", "other-worker-only", "two-recipient-terminals", "lapsed-sender", "lapsed-recipient", "missing-liveness", "unknown-liveness", "sender-lapses-before-input"} {
		t.Run(mode, func(t *testing.T) {
			t.Setenv("HADRON_TOKEN", "hdr_user_FAKE_PRIVATE_KEY")
			t.Setenv("XDG_CONFIG_HOME", t.TempDir())
			cache := t.TempDir()
			t.Setenv("XDG_CACHE_HOME", cache)
			senderDir := t.TempDir()
			t.Setenv(GitDirEnv, senderDir)
			target := t.TempDir()
			if out, err := exec.Command("git", "init", "-q", target).CombinedOutput(); err != nil {
				t.Fatalf("git init %s: %v", out, err)
			}
			var senderLiveReads atomic.Int32
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method == http.MethodGet {
					_, _ = io.WriteString(w, `{"capabilities":[]}`)
					return
				}
				var q struct {
					OperationName string
					Variables     map[string]any
				}
				if json.NewDecoder(r.Body).Decode(&q) != nil {
					t.Error("bad query")
				}
				w.Header().Set("Content-Type", "application/json")
				switch q.OperationName {
				case "GetBoundSessionLiveness":
					id := q.Variables["id"].(string)
					if mode == "unknown-liveness" {
						_, _ = io.WriteString(w, `{"errors":[{"message":"PRIVATE-LIVENESS"}]}`)
						return
					}
					if mode == "missing-liveness" {
						_, _ = io.WriteString(w, `{"data":{"session":null}}`)
						return
					}
					live := !(mode == "lapsed-sender" && id == "sender-session" || mode == "lapsed-recipient" && id == "recipient-session" || mode == "sender-lapses-before-input" && id == "sender-session" && senderLiveReads.Add(1) > 1)
					_, _ = fmt.Fprintf(w, `{"data":{"session":{"id":%q,"endedAt":null,"isLive":%t}}}`, id, live)
				case "GetTeamSession":
					id := q.Variables["id"].(string)
					worker := "recipient"
					tool := "claude-code"
					ended := "null"
					if id == "other-session" {
						worker = "other-worker"
					}
					if id == "sender-session" {
						worker = "sender"
					}
					if mode == "wrong-tool" && worker == "recipient" {
						tool = "codex"
					}
					if mode == "ended" && worker == "recipient" || mode == "sender-ended" && worker == "sender" {
						ended = `"2026-01-01T00:00:00Z"`
					}
					_, _ = fmt.Fprintf(w, `{"data":{"session":{"id":%q,"workerId":%q,"tool":%q,"endedAt":%s}}}`, id, worker, tool, ended)
				case "GetWorker":
					if mode == "server-error" {
						_, _ = io.WriteString(w, `{"errors":[{"message":"PRIVATE-SERVER-PAYLOAD","extensions":{"code":"INTERNAL_SERVER_ERROR"}}]}`)
						return
					}
					id := "recipient"
					name := "Iris"
					if q.Variables["ref"] == "sender" {
						id = "sender"
						name = "Jane"
					}
					app := "app"
					if mode == "foreign-app" && id == "recipient" {
						app = "other"
					}
					_, _ = fmt.Fprintf(w, `{"data":{"worker":{"id":%q,"appId":%q,"name":%q,"retiredAt":null}}}`, id, app, name)
				default:
					t.Errorf("unexpected operation %s (push must query only)", q.OperationName)
					_, _ = io.WriteString(w, `{"errors":[{"message":"unexpected"}]}`)
				}
			}))
			defer srv.Close()
			b := binding{WorkerID: "sender", SessionID: "sender-session", AppID: "app", Server: srv.URL}
			data, _ := json.Marshal(b)
			if err := os.WriteFile(filepath.Join(senderDir, bindingFileName), data, 0600); err != nil {
				t.Fatal(err)
			}
			b.WorkerID = "recipient"
			b.SessionID = "recipient-session"
			if mode == "other-worker-only" {
				b.WorkerID = "other-worker"
				b.SessionID = "other-session"
			}
			if mode == "missing-server" {
				b.Server = ""
			}
			data, _ = json.Marshal(b)
			if err := os.WriteFile(filepath.Join(target, ".git", bindingFileName), data, 0600); err != nil {
				t.Fatal(err)
			}
			sends := 0
			var payload string
			original := pushRunner
			t.Cleanup(func() { pushRunner = original })
			pushRunner = func() orcaterm.Runner {
				return func(_ context.Context, args []string) orcaterm.Result {
					switch args[1] {
					case "list":
						rows := []any{map[string]any{"handle": "terminal", "worktreePath": target, "agentIdentity": "claude", "connected": true, "writable": true}}
						if mode == "two-recipient-terminals" {
							rows = append(rows, map[string]any{"handle": "terminal2", "worktreePath": target, "agentIdentity": "claude", "connected": true, "writable": true})
						}
						return envelope(map[string]any{"terminals": rows})
					case "send":
						sends++
						for i, a := range args {
							if a == "--text" {
								payload = args[i+1]
							}
						}
						return envelope(map[string]any{"send": map[string]any{"accepted": true, "prompt": map[string]any{"stages": []string{"input_accepted", "turn_started"}}}})
					case "read":
						lines := pushIdle("claude")
						if mode == "unknown" {
							lines = []string{"PRIVATE-SCREEN unknown"}
						}
						if mode == "busy" {
							lines = append([]string{"Claude Code v2.1.303", "Working (1s)"}, lines[len(lines)-4:]...)
						}
						if sends > 0 && (mode == "sent" || mode == "output-failure") {
							lines = append([]string{payload, "Working (1s)"}, lines[len(lines)-4:]...)
						}
						return envelope(map[string]any{"terminal": map[string]any{"source": "screen", "tail": lines, "draft": ""}})
					}
					t.Error("unsupported fake operation")
					return orcaterm.Result{Code: 1}
				}
			}
			if mode == "locked" {
				held := make(chan struct{})
				release := make(chan struct{})
				done := make(chan error, 1)
				sum := sha256.Sum256([]byte("terminal"))
				go func() {
					done <- delivery.WithWorkerLock(filepath.Join(cache, "hadron", "team-push"), hex.EncodeToString(sum[:]), func(func() bool) error { close(held); <-release; return nil })
				}()
				<-held
				defer func() {
					close(release)
					if err := <-done; err != nil {
						t.Error(err)
					}
				}()
			}
			f := cmdutil.NewFactory()
			f.ServerFlag = srv.URL
			f.JSON = true
			streams, out, stderr := output.Test()
			f.IOStreams = streams
			if mode == "output-failure" {
				f.IOStreams.Out = pushFailWriter{}
			}
			cmd := newCmdTeamPush(f)
			cmd.SilenceErrors = true
			cmd.SilenceUsage = true
			cmd.SetArgs([]string{"hrn:worker:example.com:team:iris", "--text", "PRIVATE-MESSAGE-CANARY", "--wait-timeout", "200ms", "--confirm-timeout", "200ms", "--poll-interval", "1ms"})
			err := cmd.Execute()
			code := exitcode.FromError(err)
			if err == nil {
				code = 0
			}
			want := 11
			if mode == "sent" {
				want = 0
			}
			if mode == "busy" || mode == "locked" {
				want = 10
			}
			if mode == "unconfirmed" || mode == "output-failure" {
				want = 12
			}
			if mode == "sender-ended" || mode == "server-error" || mode == "lapsed-sender" || mode == "missing-liveness" || mode == "unknown-liveness" {
				want = 2
			}
			if mode == "foreign-app" {
				want = 4
			}
			if code != want {
				t.Fatalf("code%d want%d error%v output%s", code, want, err, out.String())
			}
			if mode == "sent" || mode == "unconfirmed" || mode == "output-failure" {
				if sends != 1 {
					t.Fatalf("sends%d", sends)
				}
			} else if sends != 0 {
				t.Fatalf("unsafe send%d", sends)
			}
			printed := out.String() + stderr.String()
			if err != nil {
				printed += err.Error()
			}
			for _, secret := range []string{"PRIVATE-MESSAGE-CANARY", "PRIVATE-SCREEN", "PRIVATE-SERVER-PAYLOAD", "hdr_user_FAKE_PRIVATE_KEY"} {
				if strings.Contains(printed, secret) {
					t.Fatalf("private sentinel escaped output")
				}
			}
			if (want == 0 || want >= 9) && mode != "output-failure" {
				var dto map[string]any
				if json.Unmarshal(out.Bytes(), &dto) != nil || dto["phase"] == nil || dto["reason"] == nil || dto["recipient"] != "recipient" {
					t.Fatal("invalid public DTO")
				}
			}
			_ = filepath.WalkDir(cache, func(path string, d os.DirEntry, err error) error {
				if err == nil && !d.IsDir() {
					b, _ := os.ReadFile(path)
					if strings.Contains(string(b), "PRIVATE-") {
						t.Error("private text persisted")
					}
				}
				return nil
			})
		})
	}
}

type pushFailWriter struct{}

func (pushFailWriter) Write([]byte) (int, error) { return 0, fmt.Errorf("PRIVATE-OUTPUT-ERROR") }
