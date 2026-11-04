package team

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/cmdutil"
	"github.com/hadron-memory/hadron-cli/internal/delivery"
	"github.com/hadron-memory/hadron-cli/internal/exitcode"
	"github.com/hadron-memory/hadron-cli/internal/output"
)

type boundaryDeliverer struct {
	count int
	kind  delivery.Kind
}

func (d *boundaryDeliverer) Deliver(_ context.Context, _ delivery.Batch, _ delivery.Recipient) delivery.Outcome {
	d.count++
	return delivery.Outcome{Kind: d.kind}
}

type boundaryFlushWriter struct{ *bytes.Buffer }

func (w boundaryFlushWriter) Flush() error { return io.ErrClosedPipe }

func TestPostHostBoundaryPins(t *testing.T) {
	cases := []struct {
		mode               string
		code, posts, sends int
		message            string
	}{
		{"server", 5, 0, 0, "server differs"}, {"app", 5, 0, 0, "App differs"},
		{"http502", 7, 0, 0, "unavailable"}, {"http401", 3, 0, 0, "authentication"},
		{"registration-denied", 8, 0, 0, "not authorized"}, {"delivery-denied", 8, 0, 0, "not authorized"},
		{"unregistered", 5, 0, 0, "not registered"}, {"not-live", 5, 0, 0, "not live"}, {"other-host", 5, 0, 0, "not live"},
		{"trees", 0, 1, 0, ""}, {"worker-binding", 0, 1, 0, ""}, {"host-changed", 0, 1, 0, ""},
		{"disabled", 2, 0, 0, ""}, {"aliases-before", 2, 0, 0, ""}, {"aliases-run", 0, 1, 0, ""},
		{"flush", 1, 1, 0, ""}, {"held-blocks", 0, 1, 1, ""}, {"success", 0, 1, 2, ""},
	}
	for _, tc := range cases {
		t.Run(tc.mode, func(t *testing.T) {
			dir := t.TempDir()
			t.Setenv(GitDirEnv, t.TempDir())
			t.Setenv("XDG_CONFIG_HOME", t.TempDir())
			t.Setenv("HADRON_TOKEN", "synthetic-post")
			posts, hostCalls := 0, 0
			state := filepath.Join(dir, "state.json")
			writeState := func(id string) {
				t.Helper()
				if err := os.WriteFile(state, []byte(fmt.Sprintf(`{"hostId":%q}`, id)), 0600); err != nil {
					t.Fatal(err)
				}
			}
			writeState("own")
			if tc.mode == "unregistered" {
				writeState("")
			}
			if err := os.Mkdir(filepath.Join(dir, "tree"), 0700); err != nil {
				t.Fatal(err)
			}
			if err := os.Mkdir(filepath.Join(dir, "alias"), 0700); err != nil {
				t.Fatal(err)
			}
			alias := func() {
				t.Helper()
				if err := os.Remove(filepath.Join(dir, "alias")); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(filepath.Join(dir, "tree"), filepath.Join(dir, "alias")); err != nil {
					t.Fatal(err)
				}
			}
			if tc.mode == "aliases-before" {
				alias()
			}
			handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var q struct {
					OperationName string                     `json:"operationName"`
					Variables     map[string]json.RawMessage `json:"variables"`
				}
				_ = json.NewDecoder(r.Body).Decode(&q)
				w.Header().Set("Content-Type", "application/json")
				write := func(v any) { _ = json.NewEncoder(w).Encode(v) }
				if q.OperationName == "TeamAppIdentity" {
					id := "own-app"
					if tc.mode == "app" && string(q.Variables["appRef"]) == `"hrn:app:test:other"` {
						id = "other-app"
					}
					write(map[string]any{"data": map[string]any{"app": map[string]any{"id": id, "name": "Team"}}})
					return
				}
				if q.OperationName == "CreateTeamChatMessage" {
					posts++
					if tc.mode == "host-changed" {
						writeState("changed")
					}
					if tc.mode == "aliases-run" {
						alias()
					}
					write(map[string]any{"data": map[string]any{"createTeamChatMessage": map[string]any{"nodeId": "m", "seq": 17, "body": "new", "at": "2026-10-19T00:00:00Z", "mentions": []string{}}}})
					return
				}
				hostCalls++
				if r.Header.Get("Authorization") != "Bearer synthetic-host" || r.Header.Get("X-Hadron-Session") != "" {
					t.Error("host key/session separation")
				}
				if (tc.mode == "registration-denied" && q.OperationName == "HostRegistrations") || (tc.mode == "delivery-denied" && q.OperationName == "DeliveryBatches") {
					_, _ = io.WriteString(w, `{"errors":[{"message":"PRIVATE-SERVER-TEXT","extensions":{"code":"FORBIDDEN"}}]}`)
					return
				}
				if q.OperationName == "DeliveryBatches" && (tc.mode == "http502" || tc.mode == "http401") {
					status := 502
					if tc.mode == "http401" {
						status = 401
					}
					w.WriteHeader(status)
					_, _ = io.WriteString(w, `{"error":"PRIVATE-SERVER-TEXT"}`)
					return
				}
				if q.OperationName == "HostRegistrations" {
					wid := "w1"
					if tc.mode == "worker-binding" {
						wid = "foreign"
					}
					trees := []string{"tree"}
					if tc.mode == "trees" {
						trees = []string{"elsewhere"}
					}
					workers := []any{map[string]any{"workerId": "w1", "worker": map[string]any{"id": wid, "name": "One"}, "worktrees": trees}, map[string]any{"workerId": "w2", "worker": map[string]any{"id": "w2", "name": "Two"}, "worktrees": []string{"alias"}}}
					live := tc.mode != "not-live" && tc.mode != "other-host"
					regs := []any{map[string]any{"hostId": "own", "live": live, "workers": workers}}
					if tc.mode == "other-host" {
						regs = append([]any{map[string]any{"hostId": "foreign-host", "live": true, "workers": workers}}, regs...)
					}
					if tc.mode == "host-changed" {
						regs = append(regs, map[string]any{"hostId": "changed", "live": true, "workers": workers})
					}
					write(map[string]any{"data": map[string]any{"hostRegistrations": regs}})
					return
				}
				if q.OperationName == "DeliveryBatches" {
					batches := []any{}
					for _, id := range []string{"older", "new"} {
						batches = append(batches, map[string]any{"id": id, "worker": map[string]any{"id": "w1"}, "text": id, "deliverAfter": "2026-10-19T00:00:00Z", "busyDeadline": "2026-10-19T00:01:00Z", "ranges": []any{}})
					}
					write(map[string]any{"data": map[string]any{"deliveryBatches": map[string]any{"batches": batches, "pageInfo": map[string]any{"hasNextPage": false, "endCursor": nil}}}})
					return
				}
				t.Errorf("unexpected operation %s", q.OperationName)
			})
			srv := httptest.NewServer(handler)
			defer srv.Close()
			other := httptest.NewServer(handler)
			defer other.Close()
			hostURL := srv.URL
			if tc.mode == "server" {
				hostURL = other.URL
			}
			app := "hrn:app:test:team"
			if tc.mode == "app" {
				app = "hrn:app:test:other"
			}
			enabled := tc.mode != "disabled"
			if err := os.WriteFile(filepath.Join(dir, "key"), []byte("synthetic-host"), 0600); err != nil {
				t.Fatal(err)
			}
			cfg := fmt.Sprintf("server: %s\napp: %s\nkey_file: key\nstate_file: state.json\nworktrees_root: %s\nworkers:\n  w1:\n    family: claude\n    launch: [fake]\n    worktrees: [tree]\n  w2:\n    family: claude\n    launch: [fake]\n    worktrees: [alias]\ndelivery:\n  enabled: %t\n  orca_bin: /bin/false\n", hostURL, app, dir, enabled)
			path := filepath.Join(dir, "host.yaml")
			if err := os.WriteFile(path, []byte(cfg), 0600); err != nil {
				t.Fatal(err)
			}
			f := cmdutil.NewFactory()
			f.ServerFlag = srv.URL
			f.AppFlag = "hrn:app:test:team"
			f.JSON = true
			streams, out, stderr := output.Test()
			f.IOStreams = streams
			if tc.mode == "flush" {
				streams.Out = boundaryFlushWriter{out}
			}
			d := &boundaryDeliverer{kind: delivery.Delivered}
			if tc.mode == "held-blocks" {
				d.kind = delivery.Held
			}
			actual := preparePostHostDelivery(f)
			prepare := func(ctx context.Context, p string, target postHostTarget) (postHostDeliveryPass, error) {
				pass, err := actual(ctx, p, target)
				if err == nil {
					pass.(*postHostPass).deliver = d
				}
				return pass, err
			}
			c := newCmdTeamChatPostWithHostDelivery(f, prepare)
			c.SetArgs([]string{"new", "--as-me", "--orca-host-config", path})
			err := c.Execute()
			if got := exitcode.FromError(err); got != tc.code {
				t.Errorf("exit %d want %d: %v", got, tc.code, err)
			}
			if posts != tc.posts || d.count != tc.sends {
				t.Errorf("posts/sends=%d/%d want %d/%d", posts, d.count, tc.posts, tc.sends)
			}
			if tc.message != "" && (err == nil || !strings.Contains(err.Error(), tc.message)) {
				t.Errorf("missing fixed reason %q: %v", tc.message, err)
			}
			if tc.mode == "server" && hostCalls != 0 {
				t.Error("cross-server host access")
			}
			if strings.Contains(stderr.String(), "PRIVATE") || (err != nil && strings.Contains(err.Error(), "PRIVATE")) {
				t.Error("raw diagnostic text")
			}
			if tc.mode == "flush" && !errors.Is(err, io.ErrClosedPipe) {
				t.Errorf("flush outcome %v", err)
			}
		})
	}
}
