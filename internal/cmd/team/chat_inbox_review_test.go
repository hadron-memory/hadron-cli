package team

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/cmdutil"
	"github.com/hadron-memory/hadron-cli/internal/exitcode"
	"github.com/hadron-memory/hadron-cli/internal/output"
	"github.com/vektah/gqlparser/v2/gqlerror"
)

func TestInboxReviewFixedPublicRefusals(t *testing.T) {
	for code, want := range map[string]int{"SESSION_NOT_LIVE": 5, "WAKE_PROOF_STALE": 5, "WAKE_PROOF_INVALID": 2, "WAKE_PROOF_REQUESTER_MISMATCH": 8} {
		err := gqlerror.List{{Message: "PRIVATE-UPSTREAM-SENTINEL", Extensions: map[string]any{"code": code}}}
		got := inboxFailure(err, "cannot read inbox")
		if exitcode.FromError(got) != want || !strings.Contains(got.Error(), code) || strings.Contains(got.Error(), "PRIVATE") {
			t.Errorf("%s: %v (%d)", code, got, exitcode.FromError(got))
		}
	}
	unknown := gqlerror.List{{Message: "PRIVATE-UPSTREAM-SENTINEL", Extensions: map[string]any{"code": "PrivateCodeShapedSentinel"}}}
	if got := inboxFailure(unknown, "cannot read inbox").Error(); strings.Contains(got, "Private") || strings.Contains(got, "PRIVATE") {
		t.Fatal("untrusted diagnostics")
	}
}
func TestInboxReviewEmptyHumanAndModeSpecificRecovery(t *testing.T) {
	streams, out, _ := output.Test()
	if err := runInbox(context.Background(), &fakeInbox{}, ownInboxIdentity, streams, false, nil); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "No deliveries") {
		t.Fatal("empty inbox silent")
	}
	for _, jsonMode := range []bool{false, true} {
		streams, out, stderr := output.Test()
		s := inboxFixture()
		s.ackErr = errors.New("PRIVATE transport failure")
		if err := runInbox(context.Background(), s, ownInboxIdentity, streams, jsonMode, nil); exitcode.FromError(err) != 1 {
			t.Fatal(err)
		}
		if strings.Contains(stderr.String(), "PRIVATE") {
			t.Fatal("raw upstream leaked")
		}
		if jsonMode {
			if !strings.Contains(stderr.String(), "--ack-file -") {
				t.Fatal("JSON recovery flag absent")
			}
			var v any
			if err := json.Unmarshal(out.Bytes(), &v); err != nil {
				t.Fatal(err)
			}
		} else {
			if !strings.Contains(stderr.String(), "hadron team chat inbox again") {
				t.Fatal("human recovery impossible")
			}
			if strings.Contains(out.String(), "PRIVATE-PROOF") {
				t.Fatal("proof in human output")
			}
		}
	}
}
func TestInboxReviewBoundIdentityGuards(t *testing.T) {
	for _, mode := range []string{"server", "app"} {
		t.Run(mode, func(t *testing.T) {
			t.Setenv("XDG_CONFIG_HOME", t.TempDir())
			t.Setenv("HADRON_TOKEN", "synthetic")
			dir := t.TempDir()
			t.Setenv(GitDirEnv, dir)
			calls := 0
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				if r.Header.Get("X-Hadron-Session") != "" {
					t.Error("session escaped before scope validation")
				}
				var q struct {
					OperationName string `json:"operationName"`
				}
				_ = json.NewDecoder(r.Body).Decode(&q)
				if q.OperationName != "TeamAppIdentity" {
					t.Errorf("delivery request escaped: %s", q.OperationName)
				}
				_, _ = w.Write([]byte(`{"data":{"app":{"id":"different-app"}}}`))
			}))
			defer srv.Close()
			f := cmdutil.NewFactory()
			f.ServerFlag = srv.URL
			f.IOStreams, _, _ = output.Test()
			b := binding{AppID: "own-app", WorkerID: "self", SessionID: "session", Server: srv.URL}
			if mode == "server" {
				b.Server = "https://bound.example"
			} else {
				f.AppFlag = "hrn:app:test:other"
			}
			raw, _ := json.Marshal(b)
			if err := os.WriteFile(filepath.Join(dir, bindingFileName), raw, 0600); err != nil {
				t.Fatal(err)
			}
			c := newCmdTeamChatInbox(f)
			c.SetArgs([]string{})
			err := c.Execute()
			if exitcode.FromError(err) != 5 {
				t.Fatal(err)
			}
			if mode == "server" && calls != 0 {
				t.Fatalf("cross-server request count %d", calls)
			}
			if mode == "app" && calls != 1 {
				t.Fatalf("expected identity-only call, got %d", calls)
			}
		})
	}
}
func TestInboxReviewCanonicalServer(t *testing.T) {
	for _, v := range []struct{ raw, want string }{{"https://EXAMPLE.test:443/", "https://example.test"}, {"HTTP://Example.test:80/", "http://example.test"}, {"https://user@example.test/", ""}, {"https://example.test/?x=y", ""}, {"https://example.test/#fragment", ""}} {
		if got := deliveryServerURL(v.raw); got != v.want {
			t.Errorf("%s=%s want %s", v.raw, got, v.want)
		}
	}
}

func TestInboxReviewDefinitiveAckRefusalIsNotUnconfirmed(t *testing.T) {
	for _, code := range []string{"SESSION_NOT_LIVE", "WAKE_PROOF_INVALID", "WAKE_PROOF_REQUESTER_MISMATCH", "WAKE_PROOF_STALE"} {
		streams, _, stderr := output.Test()
		s := inboxFixture()
		s.ackErr = gqlerror.List{{Message: "PRIVATE", Extensions: map[string]any{"code": code}}}
		if err := runInbox(context.Background(), s, ownInboxIdentity, streams, false, nil); err == nil {
			t.Fatal("refusal lost")
		}
		if !strings.Contains(stderr.String(), code) || strings.Contains(strings.ToLower(stderr.String()), "unconfirmed") || strings.Contains(stderr.String(), "PRIVATE") {
			t.Fatalf("%s: %s", code, stderr.String())
		}
	}
}
