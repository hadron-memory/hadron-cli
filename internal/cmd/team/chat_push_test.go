package team

import (
	"context"
	"encoding/json"
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

// scriptedDeliverer records every batch it is asked to type and answers per
// worker id, so a test can see exactly which terminals --push would touch.
type scriptedDeliverer struct {
	seen []string
	kind map[string]delivery.Kind
}

func (d *scriptedDeliverer) Deliver(_ context.Context, b delivery.Batch, _ delivery.Recipient) delivery.Outcome {
	d.seen = append(d.seen, b.ID)
	if k, ok := d.kind[b.WorkerID]; ok {
		return delivery.Outcome{Kind: k, Reason: "draft"}
	}
	return delivery.Outcome{Kind: delivery.Delivered}
}

type pushRun struct {
	err    error
	out    string
	stderr string
	posts  int
	seen   []string
	body   map[string]any
}

// runPush posts "body" with the server storing `mentions`, against a host
// registering wkr1 "One" and wkr2 "Mary Jane" (two due batches each, plus a
// foreign one), then runs the real postHostPass with a scripted deliverer.
func runPush(t *testing.T, args []string, mentions []string, kind map[string]delivery.Kind, failBatchesAfterPost, noBatches bool) pushRun {
	t.Helper()
	t.Setenv(GitDirEnv, t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("HADRON_TOKEN", "synthetic-post")
	dir := t.TempDir()
	posts := 0
	streams, out, stderr := output.Test()
	ms, _ := json.Marshal(mentions)
	batch := func(id, w string) string {
		return fmt.Sprintf(`{"id":%q,"worker":{"id":%q},"text":"t","deliverAfter":"2026-10-19T00:00:00Z","busyDeadline":"2026-10-19T00:01:00Z","ranges":[]}`, id, w)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var q struct {
			OperationName string `json:"operationName"`
		}
		_ = json.NewDecoder(r.Body).Decode(&q)
		w.Header().Set("Content-Type", "application/json")
		switch q.OperationName {
		case "TeamAppIdentity":
			_, _ = io.WriteString(w, `{"data":{"app":{"id":"canonical-app","urn":"hrn:app:test:team","name":"Team"}}}`)
		case "HostRegistrations":
			_, _ = io.WriteString(w, `{"data":{"hostRegistrations":[{"hostId":"own","live":true,"workers":[{"workerId":"wkr1","worker":{"id":"wkr1","name":"One","urn":"hrn:worker:test:team:one"},"worktrees":["tree1"]},{"workerId":"wkr2","worker":{"id":"wkr2","name":"Mary Jane","urn":"hrn:worker:test:team:mary-jane"},"worktrees":["tree2"]}]}]}}`)
		case "DeliveryBatches":
			if failBatchesAfterPost && posts > 0 {
				_, _ = io.WriteString(w, `{"errors":[{"message":"PRIVATE","extensions":{"code":"INTERNAL_SERVER_ERROR"}}]}`)
				return
			}
			bs := []string{}
			if !noBatches {
				bs = []string{batch("a1", "wkr1"), batch("b1", "wkr2"), batch("a2", "wkr1"), batch("b2", "wkr2"), batch("f1", "foreign")}
			}
			_, _ = io.WriteString(w, `{"data":{"deliveryBatches":{"batches":[`+strings.Join(bs, ",")+`],"pageInfo":{"hasNextPage":false,"endCursor":null}}}}`)
		case "CreateTeamChatMessage":
			posts++
			_, _ = io.WriteString(w, `{"data":{"createTeamChatMessage":{"nodeId":"m1","seq":17,"body":"x","at":"2026-10-19T00:00:00Z","mentions":`+string(ms)+`}}}`)
		default:
			t.Errorf("unexpected operation %s", q.OperationName)
		}
	}))
	defer srv.Close()
	for name, content := range map[string]string{"key": "synthetic-host", "state.json": `{"hostId":"own"}`} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
	}
	cfg := fmt.Sprintf("server: %q\napp: hrn:app:test:team\nkey_file: key\nstate_file: state.json\nworktrees_root: %q\nworkers:\n  wkr1:\n    family: claude\n    launch: [fake]\n    worktrees: [tree1]\n  wkr2:\n    family: claude\n    launch: [fake]\n    worktrees: [tree2]\ndelivery:\n  enabled: true\n  orca_bin: /bin/false\n", srv.URL, dir)
	path := filepath.Join(dir, "host.yaml")
	if err := os.WriteFile(path, []byte(cfg), 0600); err != nil {
		t.Fatal(err)
	}
	f := cmdutil.NewFactory()
	f.ServerFlag = srv.URL
	f.AppFlag = "hrn:app:test:team"
	f.JSON = true
	f.IOStreams = streams
	d := &scriptedDeliverer{kind: kind}
	actual := preparePostHostDelivery(f)
	prepared := false
	prepare := func(ctx context.Context, p string, target postHostTarget) (postHostDeliveryPass, error) {
		prepared = true
		pass, e := actual(ctx, p, target)
		if e == nil {
			pass.(*postHostPass).deliver = d
		}
		return pass, e
	}
	c := newCmdTeamChatPostWithHostDelivery(f, prepare)
	full := append([]string{}, args...)
	for i, a := range full {
		if a == "HOST" {
			full[i] = path
		}
	}
	c.SetArgs(append(full, "--as-me"))
	err := c.Execute()
	res := pushRun{err: err, out: out.String(), stderr: stderr.String(), posts: posts, seen: d.seen}
	_ = prepared
	_ = json.Unmarshal([]byte(res.out), &res.body)
	return res
}

func pushStates(t *testing.T, r pushRun) map[string]string {
	t.Helper()
	list, ok := r.body["push"].([]any)
	if !ok {
		t.Fatalf("no push array in %s", r.out)
	}
	out := map[string]string{}
	for _, e := range list {
		m := e.(map[string]any)
		out[m["recipient"].(string)] = m["state"].(string) + ":" + fmt.Sprint(m["reason"])
	}
	return out
}

func TestPushRecipientsAreStoredMentionsOnly(t *testing.T) {
	// Body text names @mary-jane, but the server stored only "one": the CLI
	// parses nothing, so only One's whole due batch (a1, a2) is typed.
	r := runPush(t, []string{"@mary-jane hi", "--push", "--orca-host-config", "HOST"}, []string{"one"}, nil, false, false)
	if r.err != nil || r.posts != 1 {
		t.Fatalf("post lost: %v %d", r.err, r.posts)
	}
	if strings.Join(r.seen, ",") != "a1,a2" {
		t.Fatalf("typed batches %v, want only One's whole due batch", r.seen)
	}
	if st := pushStates(t, r); len(st) != 1 || st["one"] != "pushed:<nil>" {
		t.Fatalf("report %v", st)
	}
}

func TestPushBroadcastTokensPushToNoOne(t *testing.T) {
	r := runPush(t, []string{"@all hi", "--push", "--orca-host-config", "HOST"}, []string{"all", "team", "team.ada.all"}, nil, false, false)
	if r.err != nil || len(r.seen) != 0 {
		t.Fatalf("broadcast pushed: %v %v", r.err, r.seen)
	}
	if st := pushStates(t, r); len(st) != 0 {
		t.Fatalf("broadcast reported recipients: %v", st)
	}
}

func TestPushMultiwordSlugAndHeldBlocksLaterBatches(t *testing.T) {
	r := runPush(t, []string{"hi", "--push", "--orca-host-config", "HOST"}, []string{"mary-jane", "one"}, map[string]delivery.Kind{"wkr2": delivery.Held}, false, false)
	if r.err != nil || r.posts != 1 {
		t.Fatalf("held push changed the post: %v %d", r.err, r.posts)
	}
	// One batch offered to the held worker, then no more: nothing queues behind a hold.
	if strings.Join(r.seen, ",") != "a1,b1,a2" {
		t.Fatalf("typed %v", r.seen)
	}
	st := pushStates(t, r)
	if st["mary-jane"] != "held:draft" || st["one"] != "pushed:<nil>" {
		t.Fatalf("report %v", st)
	}
}

func TestPushNotPushedIsTheOneStateForNoBatch(t *testing.T) {
	// An unregistered token and a registered worker with nothing due look the same.
	r := runPush(t, []string{"hi", "--push", "--orca-host-config", "HOST"}, []string{"ghost", "one"}, nil, false, true)
	if r.err != nil || len(r.seen) != 0 {
		t.Fatalf("%v %v", r.err, r.seen)
	}
	st := pushStates(t, r)
	if st["ghost"] != "not-pushed:no-batch" || st["one"] != "not-pushed:no-batch" {
		t.Fatalf("report %v", st)
	}
	if strings.Contains(r.out, "not-on-host") {
		t.Fatal("claimed a reason the server does not give")
	}
}

func TestPushRequiresHostConfigBeforePosting(t *testing.T) {
	r := runPush(t, []string{"hi", "--push"}, []string{"one"}, nil, false, false)
	if r.err == nil || exitcode.FromError(r.err) != exitcode.Usage || r.posts != 0 {
		t.Fatalf("want usage error before posting: %v posts=%d", r.err, r.posts)
	}
}

func TestPushFailureAfterPostKeepsPostAndExit(t *testing.T) {
	r := runPush(t, []string{"hi", "--push", "--orca-host-config", "HOST"}, []string{"one"}, nil, true, false)
	if r.err != nil || r.posts != 1 {
		t.Fatalf("push failure changed the post: %v %d", r.err, r.posts)
	}
	if !strings.Contains(r.stderr, "do not repost") || strings.Contains(r.stderr+r.out, "PRIVATE") {
		t.Fatalf("unsafe recovery text: %q", r.stderr)
	}
	if st := pushStates(t, r); st["one"] != "not-pushed:push-failed" {
		t.Fatalf("report %v", st)
	}
}

func TestWithoutPushMentionsDoNotNarrowOrReport(t *testing.T) {
	r := runPush(t, []string{"hi", "--orca-host-config", "HOST"}, []string{"one"}, nil, false, false)
	if r.err != nil || strings.Join(r.seen, ",") != "a1,b1,a2,b2" {
		t.Fatalf("--orca-host-config alone changed: %v %v", r.err, r.seen)
	}
	if _, has := r.body["push"]; has {
		t.Fatal("push key present without --push")
	}
}

func TestPushRecipientsHelper(t *testing.T) {
	got := pushRecipients([]string{"One", " one ", "all", "TEAM", "team.ada.all", "", "mary-jane"})
	if strings.Join(got, ",") != "one,mary-jane" {
		t.Fatalf("%v", got)
	}
}
