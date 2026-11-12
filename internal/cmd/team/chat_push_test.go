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
	// emptyReason: workers whose kind outcome (Failed/Held) carries no reason.
	emptyReason map[string]bool
}

func (d *scriptedDeliverer) Deliver(_ context.Context, b delivery.Batch, _ delivery.Recipient) delivery.Outcome {
	d.seen = append(d.seen, b.ID)
	if b.ID == d.cancelAfter && d.cancel != nil {
		d.cancel()
	}
	if k, ok := d.kind[b.WorkerID]; ok {
		if d.emptyReason[b.WorkerID] {
			return delivery.Outcome{Kind: k}
		}
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

var pushText bool

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
	// Each batch covers one seq of channel "ch" (a1=1, b1=2, a2=3, b2=4,
	// c1=5, f1=6) on every read, so its span ch#1:<n>-<n> names it.
	seq := 0
	batch := func(id, w string) string {
		seq++
		return fmt.Sprintf(`{"id":%q,"worker":{"id":%q},"text":"t","deliverAfter":"2026-10-19T00:00:00Z","busyDeadline":"2026-10-19T00:01:00Z","ranges":[{"channel":{"id":"ch"},"incarnation":1,"fromSeq":%d,"throughSeq":%d,"seqs":[%d]}]}`, id, w, seq, seq, seq)
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
			seq = 0 // the same batches on every read
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
	f.JSON = !pushText
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

func pushAcked(t *testing.T, r pushRun, who string) float64 {
	t.Helper()
	for _, e := range r.body["push"].([]any) {
		m := e.(map[string]any)
		if m["recipient"] == who {
			n, ok := m["acked"].(float64)
			if !ok {
				t.Fatalf("no acked count in %s", r.out)
			}
			return n
		}
	}
	t.Fatalf("no %s in %s", who, r.out)
	return -1
}

// pushBatch is one recipient's --json "batch", as text ("<absent>" if missing).
func pushBatch(t *testing.T, r pushRun, who string) string {
	t.Helper()
	list, ok := r.body["push"].([]any)
	if !ok {
		t.Fatalf("no push array in %s", r.out)
	}
	for _, e := range list {
		m := e.(map[string]any)
		if m["recipient"] == who {
			v, ok := m["batch"]
			if !ok {
				return "<absent>"
			}
			return fmt.Sprint(v)
		}
	}
	t.Fatalf("no %s in %s", who, r.out)
	return ""
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
	if n := pushAcked(t, r, "one"); n != 2 {
		t.Fatalf("acked %v, want One's 2 batches", n)
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

// cli#1900: "nothing pushed" names which of the two causes the CLI can
// establish. A token matching no worker registered (with a configured
// worktree) on this host is not-on-host; a matched worker the host offered
// no due batch is no-due-batch. Neither has a batch: "none".
func TestPushNamesNotOnHostApartFromNoDueBatch(t *testing.T) {
	r := runPush(t, []string{"hi", "--push", "--orca-host-config", "HOST"}, []string{"ghost", "one"}, nil, false, true)
	if r.err != nil || len(r.seen) != 0 {
		t.Fatalf("%v %v", r.err, r.seen)
	}
	st := pushStates(t, r)
	if st["ghost"] != "not-pushed:not-on-host" || st["one"] != "not-pushed:no-due-batch" {
		t.Fatalf("report %v", st)
	}
	if b := pushBatch(t, r, "ghost"); b != "none" {
		t.Fatalf("ghost batch %q, want none", b)
	}
	if b := pushBatch(t, r, "one"); b != "none" {
		t.Fatalf("one batch %q, want none", b)
	}
}

// The reason is per recipient, not per post (Victor's FIT): one mention on
// this host is pushed while the other, off it, is not-on-host.
func TestPushReasonIsPerRecipient(t *testing.T) {
	r := runPush(t, []string{"hi", "--push", "--orca-host-config", "HOST"}, []string{"ghost", "one"}, nil, false, false)
	if r.err != nil || strings.Join(r.seen, ",") != "a1,a2" {
		t.Fatalf("%v %v", r.err, r.seen)
	}
	st := pushStates(t, r)
	if st["ghost"] != "not-pushed:not-on-host" || st["one"] != "pushed:<nil>" {
		t.Fatalf("report %v", st)
	}
	if b := pushBatch(t, r, "ghost"); b != "none" {
		t.Fatalf("ghost batch %q, want none", b)
	}
	if b := pushBatch(t, r, "one"); b != "ch#1:1-1" {
		t.Fatalf("one batch %q, want its first batch's span ch#1:1-1", b)
	}
}

// An offered batch keeps its held/draft reason and names the batch that
// held, never the signed proof id.
func TestPushHeldNamesItsBatchSpanNotItsProof(t *testing.T) {
	r := runPush(t, []string{"hi", "--push", "--orca-host-config", "HOST"}, []string{"mary-jane"}, map[string]delivery.Kind{"wkr2": delivery.Held}, false, false)
	if st := pushStates(t, r); st["mary-jane"] != "held:draft" {
		t.Fatalf("report %v", st)
	}
	if b := pushBatch(t, r, "mary-jane"); b != "ch#1:2-2" {
		t.Fatalf("held batch %q, want ch#1:2-2", b)
	}
	if strings.Contains(r.out, `"b1"`) {
		t.Fatalf("receipt carries the batch proof id: %s", r.out)
	}
}

func TestPushTextNamesReasonAndBatch(t *testing.T) {
	pushText = true
	defer func() { pushText = false }()
	r := runPush(t, []string{"hi", "--push", "--orca-host-config", "HOST"}, []string{"ghost", "one"}, nil, false, true)
	for _, want := range []string{"push ghost: not-pushed (not-on-host) batch none\n", "push one: not-pushed (no-due-batch) batch none\n"} {
		if r.err != nil || !strings.Contains(r.out, want) {
			t.Fatalf("text receipt missing %q: %v %q", want, r.err, r.out)
		}
	}
	r = runPush(t, []string{"hi", "--push", "--orca-host-config", "HOST"}, []string{"one"}, nil, false, false)
	if !strings.Contains(r.out, "push one: pushed batch ch#1:1-1\n") {
		t.Fatalf("text receipt: %q", r.out)
	}
}

func TestPushHelpListsTheReasons(t *testing.T) {
	long := newCmdTeamChatPostWithHostDelivery(cmdutil.NewFactory(), nil).Long
	for _, want := range []string{"not-on-host", "no-due-batch", "push-failed", "ack-pending", "batch"} {
		if !strings.Contains(long, want) {
			t.Errorf("--help does not list %q", want)
		}
	}
	if strings.Contains(long, "no-batch") {
		t.Error("--help still names the retired no-batch reason")
	}
}

func TestPushRequiresHostConfigBeforePosting(t *testing.T) {
	r := runPush(t, []string{"hi", "--push"}, []string{"one"}, nil, false, false)
	if r.err == nil || exitcode.FromError(r.err) != exitcode.Usage || r.posts != 0 {
		t.Fatalf("want usage error before posting: %v posts=%d", r.err, r.posts)
	}
}

func TestPushFailureAfterPostKeepsPostAndExit(t *testing.T) {
	// ghost matches no worker here: a failed pass still never claims
	// not-on-host for it, since nothing was established.
	r := runPush(t, []string{"hi", "--push", "--orca-host-config", "HOST"}, []string{"one", "ghost"}, nil, true, false)
	if r.err != nil || r.posts != 1 {
		t.Fatalf("push failure changed the post: %v %d", r.err, r.posts)
	}
	if !strings.Contains(r.stderr, "do not repost") || strings.Contains(r.stderr+r.out, "PRIVATE") {
		t.Fatalf("unsafe recovery text: %q", r.stderr)
	}
	if st := pushStates(t, r); st["one"] != "unconfirmed:push-failed" || st["ghost"] != "unconfirmed:push-failed" {
		t.Fatalf("report %v", st)
	}
	for _, who := range []string{"one", "ghost"} {
		if b := pushBatch(t, r, who); b != "none" {
			t.Fatalf("unconfirmed %s batch %q, want none", who, b)
		}
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
	// kept as pushed; Mary Jane, never reached, is unconfirmed push-failed.
	r := runPushWith(t, []string{"hi", "--push", "--orca-host-config", "HOST"}, []string{"one", "mary-jane"}, nil, false, false, "a1", false)
	if r.err != nil || r.posts != 1 {
		t.Fatalf("post lost: %v %d", r.err, r.posts)
	}
	st := pushStates(t, r)
	if st["one"] != "pushed:<nil>" || st["mary-jane"] != "unconfirmed:push-failed" {
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
		"Mary - Jane": "mary---jane", "Mary  Jane": "mary-jane", "a--b": "a--b", "j.r.r": "j.r.r",
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

func TestPushTypedButAckPendingIsItsOwnStateNeverPushed(t *testing.T) {
	// The only batch is typed but its ack did not land: its own state typed
	// (reason ack-pending), never pushed (acknowledged), never not-pushed.
	r := runPushHook(t, []string{"hi", "--push", "--orca-host-config", "HOST"}, []string{"one"}, func(x *scriptedDeliverer) { x.ackPending = map[string]bool{"wkr1": true} })
	if r.err != nil {
		t.Fatal(r.err)
	}
	if st := pushStates(t, r); st["one"] != "typed:ack-pending" {
		t.Fatalf("report %v", st)
	}
	if n := pushAcked(t, r, "one"); n != 0 {
		t.Fatalf("ack-pending reported %v accepted acks, want 0", n)
	}
	// Never typed again (cor:agt:080:12): nothing queues behind the typed,
	// unacknowledged batch; the post and its exit stand (r.err is nil above).
	if strings.Join(r.seen, ",") != "a1" {
		t.Fatalf("typed %v after an ack-pending batch, want only a1", r.seen)
	}
}

func TestPushSharedTokenOneConfirmedOneUnresolvedIsUnconfirmed(t *testing.T) {
	// Both Marys share mary-jane. The context ends after Mary Jane's batch is
	// typed, before Mary-Jane is reached: the token must not report pushed.
	r := runPushWith(t, []string{"hi", "--push", "--orca-host-config", "HOST"}, []string{"mary-jane"}, nil, false, false, "b1", true)
	if r.err != nil {
		t.Fatal(r.err)
	}
	if st := pushStates(t, r); st["mary-jane"] != "unconfirmed:push-failed" {
		t.Fatalf("masked: %v seen %v", st, r.seen)
	}
}

func TestPushTextReceiptShowsAckPendingWithZeroAcked(t *testing.T) {
	pushText = true
	defer func() { pushText = false }()
	r := runPushHook(t, []string{"hi", "--push", "--orca-host-config", "HOST"}, []string{"one"}, func(x *scriptedDeliverer) { x.ackPending = map[string]bool{"wkr1": true} })
	if r.err != nil || !strings.Contains(r.out, "push one: typed (ack-pending; acked 0)") {
		t.Fatalf("text receipt: %v %q", r.err, r.out)
	}
}

// pushReceipt is the recipient's state:reason, batch span and acked count.
func pushReceipt(t *testing.T, r pushRun, who string) (state, batch string, acked float64) {
	t.Helper()
	return pushStates(t, r)[who], pushBatch(t, r, who), pushAcked(t, r, who)
}

var pushArgs = []string{"hi", "--push", "--orca-host-config", "HOST"}

// cli#1930: the batch is the span of the batch whose note decided the shown
// state+reason. At a state the first batch stays (One: a1=ch#1:1-1, a2=ch#1:3-3).
func TestPushBatchIsTheFirstBatchAtItsState(t *testing.T) {
	// pushed: both batches pushed, the first one names it.
	r := runPush(t, pushArgs, []string{"one"}, nil, false, false)
	if st, b, n := pushReceipt(t, r, "one"); st != "pushed:<nil>" || b != "ch#1:1-1" || n != 2 {
		t.Fatalf("pushed: %s %s %v", st, b, n)
	}
	// typed: a later pending batch does not replace the typed one (cor:agt:080:12).
	r = runPushHook(t, pushArgs, []string{"one"}, func(x *scriptedDeliverer) { x.ackPending = map[string]bool{"wkr1": true} })
	if st, b, n := pushReceipt(t, r, "one"); st != "typed:ack-pending" || b != "ch#1:1-1" || n != 0 {
		t.Fatalf("typed: %s %s %v", st, b, n)
	}
	// not-pushed with a reason: the blocked later batch does not upgrade a reason that exists.
	r = runPush(t, pushArgs, []string{"one"}, map[string]delivery.Kind{"wkr1": delivery.Failed}, false, false)
	if st, b, _ := pushReceipt(t, r, "one"); st != "not-pushed:draft" || b != "ch#1:1-1" {
		t.Fatalf("not-pushed with reason: %s %s", st, b)
	}
}

// A same-state reason upgrade replaces state, reason and batch together: the
// first batch failed with no reason, the blocked second one upgrades it to
// pending and carries its own span (ch#1:3-3).
func TestPushReasonUpgradeCarriesTheUpgradingBatch(t *testing.T) {
	r := runPushHook(t, pushArgs, []string{"one"}, func(x *scriptedDeliverer) {
		x.kind = map[string]delivery.Kind{"wkr1": delivery.Failed}
		x.emptyReason = map[string]bool{"wkr1": true}
	})
	if st, b, _ := pushReceipt(t, r, "one"); st != "not-pushed:pending" || b != "ch#1:3-3" {
		t.Fatalf("upgrade: %s %s, want not-pushed:pending ch#1:3-3", st, b)
	}
}

// Pure fold: a worse state replaces the triple; a lower one never does.
func TestPushOutcomeNoteKeepsTheTripleTogether(t *testing.T) {
	o := &pushOutcome{}
	o.note(pushStateNotPushed, "", "b1")
	o.note(pushStateNotPushed, "pending", "b2") // reason upgrade: all three move
	if o.state != pushStateNotPushed || o.reason != "pending" || o.batch != "b2" {
		t.Fatalf("upgrade %+v", o)
	}
	o.note(pushStateNotPushed, "other", "b3") // reason already set: first stays
	o.note(pushStatePushed, "", "b4")         // lower rank never replaces
	if o.reason != "pending" || o.batch != "b2" {
		t.Fatalf("kept %+v", o)
	}
	o.note(pushStateHeld, "draft", "b5") // worse state replaces all three
	if o.state != pushStateHeld || o.reason != "draft" || o.batch != "b5" {
		t.Fatalf("worse %+v", o)
	}
}

// Two workers share the token mary-jane (wkr2 "Mary Jane": b1=ch#1:2-2, b2=4-4;
// wkr3 "Mary-Jane": c1=ch#1:5-5). The token's receipt is the worse sharer's
// state+reason+batch, and acked still sums over every sharer.
func TestPushSharedTokenFoldCarriesTheDecidingSharersBatch(t *testing.T) {
	mary := []string{"mary-jane"}
	// held on wkr3 beats wkr2's pushes: held:draft with wkr3's batch; acked = wkr2's 2.
	r := runPushWith(t, pushArgs, mary, map[string]delivery.Kind{"wkr3": delivery.Held}, false, false, "", true)
	if st, b, n := pushReceipt(t, r, "mary-jane"); st != "held:draft" || b != "ch#1:5-5" || n != 2 {
		t.Fatalf("held sharer: %s %s %v", st, b, n)
	}
	// typed (wkr2, ack-pending) beats pushed (wkr3): typed with wkr2's first batch,
	// never pushed; acked is wkr3's 1 only.
	pushHook = func(x *scriptedDeliverer) { x.ackPending = map[string]bool{"wkr2": true} }
	defer func() { pushHook = nil }()
	r = runPushWith(t, pushArgs, mary, nil, false, false, "", true)
	if st, b, n := pushReceipt(t, r, "mary-jane"); st != "typed:ack-pending" || b != "ch#1:2-2" || n != 1 {
		t.Fatalf("typed sharer: %s %s %v", st, b, n)
	}
}

// An unresolved sharer at a pass error stays unconfirmed/push-failed, batch none.
func TestPushUnresolvedSharerIsUnconfirmedWithNoBatch(t *testing.T) {
	r := runPushWith(t, pushArgs, []string{"mary-jane"}, nil, false, false, "b1", true)
	if st, b, _ := pushReceipt(t, r, "mary-jane"); st != "unconfirmed:push-failed" || b != "none" {
		t.Fatalf("unresolved: %s %s", st, b)
	}
}
