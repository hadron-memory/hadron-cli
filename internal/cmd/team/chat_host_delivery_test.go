package team

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/cmdutil"
	"github.com/hadron-memory/hadron-cli/internal/output"
)

type testPostPass func(context.Context) (postHostDeliverySummary, error)

func (f testPostPass) Run(ctx context.Context) (postHostDeliverySummary, error) { return f(ctx) }

type postFixture struct {
	factory     *cmdutil.Factory
	out, stderr *strings.Builder
	posts       int
	failPost    bool
}

func newPostFixture(t *testing.T) *postFixture {
	t.Helper()
	t.Setenv(GitDirEnv, t.TempDir())
	x := &postFixture{out: &strings.Builder{}, stderr: &strings.Builder{}}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var q struct {
			Query string `json:"query"`
		}
		if err := json.NewDecoder(r.Body).Decode(&q); err != nil {
			t.Error(err)
		}
		w.Header().Set("Content-Type", "application/json")
		if strings.Contains(q.Query, "createTeamChatMessage") {
			x.posts++
			if x.failPost {
				_, _ = io.WriteString(w, `{"errors":[{"message":"controlled refusal","extensions":{"code":"FORBIDDEN"}}]}`)
				return
			}
			_, _ = io.WriteString(w, `{"data":{"createTeamChatMessage":{"nodeId":"m1","seq":17,"body":"plain post","at":"2026-10-19T00:00:00Z","authorName":"person","mentions":[]}}}`)
			return
		}
		_, _ = io.WriteString(w, `{"data":{"app":{"id":"app1","name":"Team","urn":"hrn:app:acme.com:team"}}}`)
	}))
	t.Cleanup(srv.Close)
	x.factory = cmdutil.NewFactory()
	x.factory.ServerFlag = srv.URL
	x.factory.AppFlag = "hrn:app:acme.com:team"
	x.factory.JSON = true
	x.factory.IOStreams = &output.IOStreams{In: strings.NewReader(""), Out: x.out, ErrOut: x.stderr}
	// A synthetic env credential avoids the real token store.
	t.Setenv("HADRON_TOKEN", "synthetic-post-credential")
	return x
}
func TestPostHostDeliveryLifecycle(t *testing.T) {
	for _, scenario := range []string{"absent", "success", "preflight-refused", "post-refused", "delivery-failed", "held"} {
		t.Run(scenario, func(t *testing.T) {
			x := newPostFixture(t)
			prepared, runs := 0, 0
			x.failPost = scenario == "post-refused"
			prepare := func(ctx context.Context, path string, target postHostTarget) (postHostDeliveryPass, error) {
				prepared++
				if x.posts != 0 {
					t.Error("preflight ran after posting")
				}
				if path != "host.yaml" || target.AppRef != "hrn:app:acme.com:team" || target.Server != x.factory.ServerFlag {
					t.Errorf("wrong target/path: %+v %q", target, path)
				}
				if scenario == "preflight-refused" {
					return nil, errors.New("preflight refused")
				}
				return testPostPass(func(context.Context) (postHostDeliverySummary, error) {
					runs++
					if x.posts != 1 || !json.Valid([]byte(x.out.String())) {
						t.Error("pass ran before successful post output")
					}
					if scenario == "delivery-failed" {
						return postHostDeliverySummary{}, errors.New("SECRET-UNTRUSTED-ERROR")
					}
					if scenario == "held" {
						return postHostDeliverySummary{Held: 1}, nil
					}
					return postHostDeliverySummary{Delivered: 2}, nil // includes an older unrelated due batch
				}), nil
			}
			cmd := newCmdTeamChatPostWithHostDelivery(x.factory, prepare)
			args := []string{"plain post", "--as-me"}
			if scenario != "absent" {
				args = append(args, "--orca-host-config", "host.yaml")
			}
			cmd.SetArgs(args)
			err := cmd.Execute()
			if scenario == "preflight-refused" || scenario == "post-refused" {
				if err == nil {
					t.Fatal("expected refusal")
				}
			} else if err != nil {
				t.Fatal(err)
			}
			wantPrepared, wantRuns, wantPosts := 1, 1, 1
			if scenario == "absent" {
				wantPrepared, wantRuns = 0, 0
			}
			if scenario == "preflight-refused" {
				wantRuns, wantPosts = 0, 0
			}
			if scenario == "post-refused" {
				wantRuns = 0
			}
			if prepared != wantPrepared || runs != wantRuns || x.posts != wantPosts {
				t.Errorf("prepared/runs/posts=%d/%d/%d want %d/%d/%d", prepared, runs, x.posts, wantPrepared, wantRuns, wantPosts)
			}
			if strings.Contains(x.stderr.String(), "SECRET") {
				t.Fatal("untrusted adapter error leaked")
			}
			if scenario == "held" || scenario == "delivery-failed" {
				if !strings.Contains(x.stderr.String(), "do not repost") {
					t.Errorf("missing recovery: %s", x.stderr)
				}
			}
		})
	}
}
func TestPostHostDeliveryEmptyFlagRefusesBeforePost(t *testing.T) {
	x := newPostFixture(t)
	called := false
	cmd := newCmdTeamChatPostWithHostDelivery(x.factory, func(context.Context, string, postHostTarget) (postHostDeliveryPass, error) {
		called = true
		return nil, nil
	})
	cmd.SetArgs([]string{"plain post", "--as-me", "--orca-host-config="})
	if err := cmd.Execute(); err == nil {
		t.Error("empty flag accepted")
	}
	if called || x.posts != 0 {
		t.Error("empty flag reached host or post")
	}
}

type failedPostReceipt struct{}

func (failedPostReceipt) Write([]byte) (int, error) { return 0, io.ErrClosedPipe }
func TestPostHostDeliveryReceiptFailureDoesNotDeliverOrRepost(t *testing.T) {
	x := newPostFixture(t)
	runs := 0
	x.factory.IOStreams.Out = failedPostReceipt{}
	cmd := newCmdTeamChatPostWithHostDelivery(x.factory, func(context.Context, string, postHostTarget) (postHostDeliveryPass, error) {
		return testPostPass(func(context.Context) (postHostDeliverySummary, error) { runs++; return postHostDeliverySummary{}, nil }), nil
	})
	cmd.SetArgs([]string{"plain post", "--as-me", "--orca-host-config", "host.yaml"})
	if err := cmd.Execute(); !errors.Is(err, io.ErrClosedPipe) {
		t.Fatalf("receipt failure lost: %v", err)
	}
	if x.posts != 1 || runs != 0 {
		t.Fatalf("posts=%d runs=%d", x.posts, runs)
	}
	if !strings.Contains(x.stderr.String(), "post succeeded") {
		t.Fatal("missing truthful receipt-failure notice")
	}
}
func TestPostHostDeliveryIsNotActivatedWithoutLandedAdapter(t *testing.T) {
	x := newPostFixture(t)
	cmd := newCmdTeamChatPost(x.factory)
	if cmd.Flags().Lookup("orca-host-config") != nil {
		t.Fatal("production constructor exposed pending host adapter")
	}
}
