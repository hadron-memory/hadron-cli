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
	// cancelAfter cancels the command's context once this batch is typed.
	cancelAfter string
	cancel      context.CancelFunc
	// ackPending: workers whose delivery is typed but whose ack did not land.
	ackPending map[string]bool
}

func (d *scriptedDeliverer) Deliver(_ context.Context, b delivery.Batch, _ delivery.Recipient) delivery.Outcome {
	d.seen = append(d.seen, b.ID)
	if b.ID == d.cancelAfter && d.cancel != nil {
		d.cancel()
	}
	if k, ok := d.kind[b.WorkerID]; ok {
		return delivery.Outcome{Kind: k, Reason: "draft"}
	}
	return delivery.Outcome{Kind: delivery.Delivered, AckPending: d.ackPending[b.WorkerID]}
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
var pushWkr1Name = "One"

func runPushNamed(t *testing.T, wkr1 string, mentions []string) pushRun {
	pushWkr1Name = wkr1
	defer func() { pushWkr1Name = "One" }()
	return runPush(t, []string{"hi", "--push", "--orca-host-config", "HOST"}, mentions, nil, false, false)
}

func runPushHook(t *testing.T, args, mentions []string, hook func(*scriptedDeliverer)) pushRun {
	pushHook = hook
	defer func() { pushHook = nil }()
	return runPush(t, args, mentions, nil, false, false)
}

var pushHook func(*scriptedDeliverer)

func runPush(t *testing.T, args []string, mentions []string, kind map[string]delivery.Kind, failBatchesAfterPost, noBatches bool) pushRun {
	return runPushWith(t, args, mentions, kind, failBatchesAfterPost, noBatches, "", false)
}

// runPushWith adds cancelAfter (cancel the context once that batch is typed)
// and twinNames (a second worker whose name slugs like the first's).
func runPushWith(t *testing.T, args []string, mentions []string, kind map[string]delivery.Kind, failBatchesAfterPost, noBatches bool, cancelAfter string, twinNames bool) pushRun {
	t.Helper()
	t.Setenv(GitDirEnv, t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("HADRON_TOKEN", "synthetic-post")
	dir := t.TempDir()
	posts := 0
	twin, twinWorker := "Mary Jane", ""
	if twinNames {
		twinWorker = `,{"workerId":"wkr3","worker":{"id":"wkr3","name":"Mary-Jane","urn":"hrn:worker:test:team:mary-jane-2"},"worktrees":["tree3"]}`
	}
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
			_, _ = io.WriteString(w, `{"data":{"hostRegistrations":[{"hostId":"own","live":true,"workers":[{"workerId":"wkr1","worker":{"id":"wkr1","name":"`+pushWkr1Name+`","urn":"hrn:worker:test:team:one"},"worktrees":["tree1"]},{"workerId":"wkr2","worker":{"id":"wkr2","name":"`+twin+`","urn":"hrn:worker:test:team:mary-jane"},"worktrees":["tree2"]}`+twinWorker+`]}]}}`)
		case "DeliveryBatches":
			if failBatchesAfterPost && posts > 0 {
				_, _ = io.WriteString(w, `{"errors":[{"message":"PRIVATE","extensions":{"code":"INTERNAL_SERVER_ERROR"}}]}`)
				return
			}
			bs := []string{}
			if !noBatches {
				bs = []string{batch("a1", "wkr1"), batch("b1", "wkr2"), batch("a2", "wkr1"), batch("b2", "wkr2"), batch("c1", "wkr3"), batch("f1", "foreign")}
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
	cfg := fmt.Sprintf("server: %q\napp: hrn:app:test:team\nkey_file: key\nstate_file: state.json\nworktrees_root: %q\nworkers:\n  wkr1:\n    family: claude\n    launch: [fake]\n    worktrees: [tree1]\n  wkr2:\n    family: claude\n    launch: [fake]\n    worktrees: [tree2]\n  wkr3:\n    family: claude\n    launch: [fake]\n    worktrees: [tree3]\ndelivery:\n  enabled: true\n  orca_bin: /bin/false\n", srv.URL, dir)
	path := filepath.Join(dir, "host.yaml")
	if err := os.WriteFile(path, []byte(cfg), 0600); err != nil {
		t.Fatal(err)
	}
	f := cmdutil.NewFactory()
	f.ServerFlag = srv.URL
	f.AppFlag = "hrn:app:test:team"
	f.JSON = true
	f.IOStreams = streams
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	d := &scriptedDeliverer{kind: kind, cancelAfter: cancelAfter, cancel: cancel}
	if pushHook != nil {
		pushHook(d)
	}
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
	err := c.ExecuteContext(ctx)
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

func TestPushPartialSuccessSurvivesALaterFailure(t *testing.T) {
	// One's first batch is typed, then the context ends: that delivery is
	// kept as pushed; Mary Jane, never reached, is not-pushed push-failed.
	r := runPushWith(t, []string{"hi", "--push", "--orca-host-config", "HOST"}, []string{"one", "mary-jane"}, nil, false, false, "a1", false)
	if r.err != nil || r.posts != 1 {
		t.Fatalf("post lost: %v %d", r.err, r.posts)
	}
	st := pushStates(t, r)
	if st["one"] != "pushed:<nil>" || st["mary-jane"] != "not-pushed:push-failed" {
		t.Fatalf("report %v seen %v", st, r.seen)
	}
	if !strings.Contains(r.stderr, "do not repost") {
		t.Fatalf("no recovery note: %q", r.stderr)
	}
}

func TestPushSharedTokenAddressesEveryWorkerAndReportsTheWorst(t *testing.T) {
	// "Mary Jane" and "Mary-Jane" share the server token mary-jane: the
	// mention addresses both (as the server's matching does), the report
	// folds both outcomes, and an unmentioned worker is never touched.
	r := runPushWith(t, []string{"hi", "--push", "--orca-host-config", "HOST"}, []string{"mary-jane"}, nil, false, false, "", true)
	if r.err != nil {
		t.Fatal(r.err)
	}
	if strings.Join(r.seen, ",") != "b1,b2,c1" {
		t.Fatalf("typed %v, want both Marys' batches and not One's", r.seen)
	}
	if st := pushStates(t, r); st["mary-jane"] != "pushed:<nil>" {
		t.Fatalf("report %v", st)
	}
	// A held draft on one of the two must not be masked by the other's push.
	r = runPushWith(t, []string{"hi", "--push", "--orca-host-config", "HOST"}, []string{"mary-jane"}, map[string]delivery.Kind{"wkr3": delivery.Held}, false, false, "", true)
	if st := pushStates(t, r); st["mary-jane"] != "held:draft" {
		t.Fatalf("held masked: %v", st)
	}
}

func TestPushFailedDeliveryIsNeverReportedPushed(t *testing.T) {
	r := runPush(t, []string{"hi", "--push", "--orca-host-config", "HOST"}, []string{"one"}, map[string]delivery.Kind{"wkr1": delivery.Failed}, false, false)
	if r.err != nil {
		t.Fatal(r.err)
	}
	if st := pushStates(t, r); !strings.HasPrefix(st["one"], "not-pushed:") {
		t.Fatalf("report %v", st)
	}
}

func TestMentionTokenOfMatchesTheServer(t *testing.T) {
	for name, want := range map[string]string{
		"Mary Jane": "mary-jane", "mary.jane": "mary.jane", "mary_jane": "mary_jane",
		"Mary - Jane": "mary---jane", "a--b": "a--b", "j.r.r": "j.r.r",
		"  Ada  ": "ada", "-x-": "x", "...": "", "": "", "İx": "i-x",
	} {
		if got := mentionTokenOf(name); got != want {
			t.Errorf("mentionTokenOf(%q) = %q, want %q", name, got, want)
		}
	}
}

func TestPushDotNamedWorkerIsNotAddressedByADashedMention(t *testing.T) {
	// Riana P2-1: a worker named "mary.jane" has the server token mary.jane;
	// a post stored as @mary-jane must not type into it (nor ack for it).
	r2 := runPushNamed(t, "mary.jane", []string{"mary-jane"})
	if strings.Join(r2.seen, ",") != "b1,b2" {
		t.Fatalf("typed %v: the dotted worker's batches must not be typed", r2.seen)
	}
	r3 := runPushNamed(t, "mary.jane", []string{"mary.jane"})
	if strings.Join(r3.seen, ",") != "a1,a2" {
		t.Fatalf("typed %v: a dotted mention must match the dotted worker", r3.seen)
	}
}

func TestPushTypedButAckPendingIsQualifiedNotUnqualifiedPushed(t *testing.T) {
	// The only batch is typed but its ack did not land: pushed (it was
	// typed, never "not typed"), qualified ack-pending, never bare pushed.
	r := runPushHook(t, []string{"hi", "--push", "--orca-host-config", "HOST"}, []string{"one"}, func(x *scriptedDeliverer) { x.ackPending = map[string]bool{"wkr1": true} })
	if r.err != nil {
		t.Fatal(r.err)
	}
	if st := pushStates(t, r); st["one"] != "pushed:ack-pending" {
		t.Fatalf("report %v", st)
	}
}
