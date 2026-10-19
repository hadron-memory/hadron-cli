package team

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/hadron-memory/hadron-cli/internal/api"
	"github.com/hadron-memory/hadron-cli/internal/cmdutil"
	"github.com/hadron-memory/hadron-cli/internal/delivery"
	"github.com/hadron-memory/hadron-cli/internal/output"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPostProductionHostFlag(t *testing.T) {
	if newCmdTeamChatPost(cmdutil.NewFactory()).Flags().Lookup("orca-host-config") == nil {
		t.Fatal("production host delivery opt-in missing")
	}
}

type recordingPostDeliverer struct {
	seen   []string
	before func()
}

func (d *recordingPostDeliverer) Deliver(_ context.Context, b delivery.Batch, r delivery.Recipient) delivery.Outcome {
	if d.before != nil {
		d.before()
	}
	d.seen = append(d.seen, b.ID)
	return delivery.Outcome{Kind: delivery.Delivered}
}
func TestPostHostProductionPreparationAndFreshDuePass(t *testing.T) {
	for _, mode := range []string{"success", "preflight-refused", "post-pass-refused", "missing-registration", "absent"} {
		t.Run(mode, func(t *testing.T) {
			t.Setenv(GitDirEnv, t.TempDir())
			t.Setenv("XDG_CONFIG_HOME", t.TempDir())
			t.Setenv("HADRON_TOKEN", "synthetic-post")
			dir := t.TempDir()
			posts, hostReads, pages := 0, 0, 0
			streams, out, stderr := output.Test()
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var q struct {
					OperationName string                     `json:"operationName"`
					Variables     map[string]json.RawMessage `json:"variables"`
				}
				if err := json.NewDecoder(r.Body).Decode(&q); err != nil {
					t.Error(err)
				}
				w.Header().Set("Content-Type", "application/json")
				switch q.OperationName {
				case "TeamAppIdentity":
					if r.Header.Get("Authorization") != "Bearer synthetic-post" {
						t.Error("host credential used for posting identity")
					}
					_, _ = io.WriteString(w, `{"data":{"app":{"id":"canonical-app","urn":"hrn:app:test:team","name":"Team"}}}`)
				case "HostRegistrations", "DeliveryBatches":
					hostReads++
					if r.Header.Get("Authorization") != "Bearer synthetic-host" || r.Header.Get(api.SessionHeader) != "" {
						t.Error("host credential/session isolation broken")
					}
					if mode == "preflight-refused" || (mode == "post-pass-refused" && posts > 0) {
						_, _ = io.WriteString(w, `{"errors":[{"message":"PRIVATE-UPSTREAM","extensions":{"code":"APP_KEY_SCOPE_REQUIRED"}}]}`)
						return
					}
					if q.OperationName == "HostRegistrations" {
						if mode == "missing-registration" {
							_, _ = io.WriteString(w, `{"data":{"hostRegistrations":[]}}`)
							return
						}
						_, _ = io.WriteString(w, `{"data":{"hostRegistrations":[{"hostId":"own","live":true,"workers":[{"workerId":"wkr1","worker":{"id":"wkr1","name":"One","urn":"hrn:worker:test:team:one"},"worktrees":["tree"]}]}]}}`)
						return
					}
					pages++
					if _, ok := q.Variables["after"]; !ok {
						_, _ = io.WriteString(w, `{"data":{"deliveryBatches":{"batches":[],"pageInfo":{"hasNextPage":true,"endCursor":"next"}}}}`)
						return
					}
					// One older unrelated due batch, the new post, and an excluded foreign recipient.
					_, _ = io.WriteString(w, `{"data":{"deliveryBatches":{"batches":[{"id":"old-proof","worker":{"id":"wkr1"},"text":"older unrelated","deliverAfter":"2026-10-19T00:00:00Z","busyDeadline":"2026-10-19T00:01:00Z","ranges":[]},{"id":"new-proof","worker":{"id":"wkr1"},"text":"new post","deliverAfter":"2026-10-19T00:00:00Z","busyDeadline":"2026-10-19T00:01:00Z","ranges":[]},{"id":"foreign-proof","worker":{"id":"foreign"},"text":"foreign","deliverAfter":"2026-10-19T00:00:00Z","busyDeadline":"2026-10-19T00:01:00Z","ranges":[]}],"pageInfo":{"hasNextPage":false,"endCursor":null}}}}`)
				case "CreateTeamChatMessage":
					if r.Header.Get("Authorization") != "Bearer synthetic-post" {
						t.Error("host key used to post")
					}
					posts++
					_, _ = io.WriteString(w, `{"data":{"createTeamChatMessage":{"nodeId":"m1","seq":17,"body":"new post","at":"2026-10-19T00:00:00Z","mentions":[]}}}`)
				default:
					t.Errorf("unexpected operation: %s (must never register)", q.OperationName)
				}
			}))
			defer srv.Close()
			if err := os.WriteFile(filepath.Join(dir, "key"), []byte("synthetic-host"), 0600); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(dir, "state.json"), []byte(`{"hostId":"own"}`), 0600); err != nil {
				t.Fatal(err)
			}
			cfg := fmt.Sprintf("server: %q\napp: hrn:app:test:team\nkey_file: key\nstate_file: state.json\nworktrees_root: %q\nworkers:\n  wkr1:\n    family: claude\n    launch: [fake]\n    worktrees: [tree]\ndelivery:\n  enabled: true\n  orca_bin: /bin/false\n", srv.URL, dir)
			path := filepath.Join(dir, "host.yaml")
			if err := os.WriteFile(path, []byte(cfg), 0600); err != nil {
				t.Fatal(err)
			}
			f := cmdutil.NewFactory()
			f.ServerFlag = srv.URL
			f.AppFlag = "hrn:app:test:team"
			f.JSON = true
			f.IOStreams = streams
			d := &recordingPostDeliverer{before: func() {
				if posts != 1 || !strings.Contains(out.String(), `"seq": 17`) {
					t.Error("delivery before confirmed post output")
				}
			}}
			actual := preparePostHostDelivery(f)
			prepare := func(ctx context.Context, path string, target postHostTarget) (postHostDeliveryPass, error) {
				p, e := actual(ctx, path, target)
				if e == nil {
					p.(*postHostPass).deliver = d
				}
				return p, e
			}
			c := newCmdTeamChatPostWithHostDelivery(f, prepare)
			args := []string{"new post", "--as-me"}
			if mode != "absent" {
				args = append(args, "--orca-host-config", path)
			}
			c.SetArgs(args)
			err := c.Execute()
			if mode == "preflight-refused" || mode == "missing-registration" {
				if err == nil || posts != 0 {
					t.Fatalf("preflight allowed post: %v %d", err, posts)
				}
				return
			}
			if err != nil || posts != 1 {
				t.Fatalf("post result lost: %v %d", err, posts)
			}
			switch mode {
			case "success":
				if strings.Join(d.seen, ",") != "old-proof,new-proof" || pages != 4 {
					t.Fatalf("due work/paging: %v %d", d.seen, pages)
				}
			case "absent":
				if hostReads != 0 || len(d.seen) != 0 {
					t.Fatal("host touched without flag")
				}
			case "post-pass-refused":
				if len(d.seen) != 0 || !strings.Contains(stderr.String(), "do not repost") || strings.Contains(stderr.String(), "PRIVATE-UPSTREAM") {
					t.Fatal("unsafe post recovery")
				}
			}
		})
	}
}
