package delivery

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeChat is a team chat with the server's duplicate rule (an identical
// post by the same worker within ten minutes is refused with the earlier
// seq). Knobs model a lost response, a lagging read and failures.
type fakeChat struct {
	mu       sync.Mutex
	msgs     []ChatMessage
	posts    int // accepted posts (stored messages)
	attempts int // Post calls
	now      func() time.Time
	at       map[int]time.Time

	boundID, boundName string
	boundErr           error
	recipient          string
	recipientErr       error
	headErr            error
	readErr            error

	loseResponse bool          // store, then answer an error
	failPost     bool          // answer an error without storing
	hideFromRead bool          // MessagesAfter does not see stored posts
	postGate     chan struct{} // when set, Post blocks until closed
	postEntered  chan struct{}
	recipientN   int
}

func newFakeChat(now func() time.Time) *fakeChat {
	return &fakeChat{now: now, at: map[int]time.Time{}, boundID: "w-hugo", boundName: "Hugo", recipient: "ada",
		msgs: []ChatMessage{{Seq: 1, AuthorWorkerID: "w-x", Body: "earlier"}}}
}

func (c *fakeChat) BoundWorker(context.Context) (string, string, error) {
	return c.boundID, c.boundName, c.boundErr
}

func (c *fakeChat) Recipient(context.Context, string) (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.recipientN++
	return c.recipient, c.recipientErr
}

func (c *fakeChat) Head(context.Context) (int, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.headErr != nil {
		return 0, c.headErr
	}
	return c.msgs[len(c.msgs)-1].Seq, nil
}

func (c *fakeChat) Post(_ context.Context, body string) (int, error) {
	if c.postEntered != nil {
		c.postEntered <- struct{}{}
	}
	if c.postGate != nil {
		<-c.postGate
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.attempts++
	if c.failPost {
		return 0, errors.New("502 before commit")
	}
	for _, m := range c.msgs {
		if m.AuthorWorkerID == c.boundID && m.Body == body && c.now().Sub(c.at[m.Seq]) < 10*time.Minute {
			return 0, &DuplicatePostError{EarlierSeq: m.Seq}
		}
	}
	seq := c.msgs[len(c.msgs)-1].Seq + 1
	c.msgs = append(c.msgs, ChatMessage{Seq: seq, AuthorWorkerID: c.boundID, Body: body})
	c.at[seq] = c.now()
	c.posts++
	if c.loseResponse {
		return 0, errors.New("connection reset by peer")
	}
	return seq, nil
}

func (c *fakeChat) MessagesAfter(_ context.Context, after, limit int) ([]ChatMessage, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.readErr != nil {
		return nil, c.readErr
	}
	out := []ChatMessage{}
	for _, m := range c.msgs {
		if m.Seq > after && len(out) < limit {
			if c.hideFromRead && m.AuthorWorkerID == c.boundID {
				continue
			}
			out = append(out, m)
		}
	}
	return out, nil
}

func (c *fakeChat) bodies() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	var out []string
	for _, m := range c.msgs {
		if m.AuthorWorkerID == c.boundID {
			out = append(out, m.Body)
		}
	}
	return out
}

type reporterFixture struct {
	t     *testing.T
	box   *StopOutbox
	chat  *fakeChat
	clock time.Time
}

func newReporterFixture(t *testing.T) *reporterFixture {
	t.Helper()
	f := &reporterFixture{t: t, box: &StopOutbox{Dir: filepath.Join(t.TempDir(), "stop-dropped")},
		clock: time.Date(2026, 11, 4, 12, 0, 0, 0, time.UTC)}
	f.chat = newFakeChat(func() time.Time { return f.clock })
	if _, err := f.box.Drop(StopDrop{EventID: "ev-1", SignalID: "sig-1", WorkerID: "w-gil", Worker: "Gil",
		Level: "HALT", HoldReason: "draft", HeldAt: f.clock}); err != nil {
		t.Fatal(err)
	}
	return f
}

func (f *reporterFixture) reporter() *StopReporter {
	return &StopReporter{Outbox: f.box, API: f.chat, ReporterWorker: "Hugo", Now: func() time.Time { return f.clock }}
}

func (f *reporterFixture) run(r *StopReporter) ReportRun {
	f.t.Helper()
	run, err := r.Run(context.Background())
	if err != nil {
		f.t.Fatalf("run: %v", err)
	}
	return run
}

func (f *reporterFixture) reported() bool {
	_, err := os.Stat(filepath.Join(f.box.Dir, stopDropBase("ev-1")+".reported"))
	return err == nil
}

func stopDropBase(eventID string) string { return strings.TrimSuffix(stopDropName(eventID), ".json") }

func onlyOutcome(t *testing.T, run ReportRun) ReportOutcome {
	t.Helper()
	if len(run.Outcomes) != 1 {
		t.Fatalf("outcomes = %+v, want one", run.Outcomes)
	}
	return run.Outcomes[0]
}

func TestStopReporterPostsOnceAndRecordsReadBackSeq_1629(t *testing.T) {
	f := newReporterFixture(t)
	out := onlyOutcome(t, f.run(f.reporter()))
	if out.Result != "reported" || out.Seq != 2 {
		t.Fatalf("outcome = %+v", out)
	}
	if !f.reported() {
		t.Fatal("no .reported marker")
	}
	bodies := f.chat.bodies()
	if len(bodies) != 1 || !strings.HasPrefix(bodies[0], "@ada stop-drop ev-1: the HALT notice for worker Gil was not typed (held: draft") {
		t.Fatalf("bodies = %q", bodies)
	}
	if strings.Contains(bodies[0], "@Gil") || strings.Contains(bodies[0], "@gil") {
		t.Fatal("the halted worker must not be mentioned")
	}
	f.clock = f.clock.Add(time.Hour)
	if run := f.run(f.reporter()); len(run.Outcomes) != 0 || f.chat.attempts != 1 {
		t.Fatalf("a reported notice was handled again: %+v, attempts %d", run, f.chat.attempts)
	}
}

func TestStopReporterRetriesAFailedPostAfterReconcile_1629(t *testing.T) {
	f := newReporterFixture(t)
	f.chat.headErr = errors.New("502")
	if out := onlyOutcome(t, f.run(f.reporter())); out.Result != "failed" || f.chat.attempts != 0 {
		t.Fatalf("outcome = %+v attempts %d", out, f.chat.attempts)
	}
	f.chat.headErr = nil
	if out := onlyOutcome(t, f.run(f.reporter())); out.Result != "reported" || f.chat.posts != 1 {
		t.Fatalf("retry outcome = %+v posts %d", out, f.chat.posts)
	}
}

// A post the server committed whose response was lost: the next run, once
// the intent is old enough, finds it by reconciling and does not post again.
func TestStopReporterCommittedPostWithLostResponseIsReconciled_1629(t *testing.T) {
	f := newReporterFixture(t)
	f.chat.loseResponse = true
	if out := onlyOutcome(t, f.run(f.reporter())); out.Result != "failed" || f.chat.posts != 1 {
		t.Fatalf("first outcome = %+v posts %d", out, f.chat.posts)
	}
	f.chat.loseResponse = false
	f.clock = f.clock.Add(time.Minute)
	if out := onlyOutcome(t, f.run(f.reporter())); out.Result != "waiting" || f.chat.attempts != 1 {
		t.Fatalf("a young intent must wait: %+v attempts %d", out, f.chat.attempts)
	}
	f.clock = f.clock.Add(DefaultReconcileAfter)
	out := onlyOutcome(t, f.run(f.reporter()))
	if out.Result != "reconciled" || out.Seq != 2 || f.chat.posts != 1 || f.chat.attempts != 1 || !f.reported() {
		t.Fatalf("outcome = %+v posts %d attempts %d", out, f.chat.posts, f.chat.attempts)
	}
}

// Same loss past ten minutes, where the duplicate rule no longer applies:
// only the reconcile prevents a second post.
func TestStopReporterReconcilesPastTheDuplicateWindow_1629(t *testing.T) {
	f := newReporterFixture(t)
	f.chat.loseResponse = true
	f.run(f.reporter())
	f.chat.loseResponse = false
	f.clock = f.clock.Add(11 * time.Minute)
	if out := onlyOutcome(t, f.run(f.reporter())); out.Result != "reconciled" || f.chat.posts != 1 || f.chat.attempts != 1 {
		t.Fatalf("outcome = %+v posts %d attempts %d", out, f.chat.posts, f.chat.attempts)
	}
}

// The reconcile read misses the committed post (it lags); the re-send inside
// ten minutes is refused TEAM_CHAT_DUPLICATE, and the earlier seq is recorded.
func TestStopReporterRecordsTheDuplicateRefusalsEarlierSeq_1629(t *testing.T) {
	f := newReporterFixture(t)
	f.chat.loseResponse = true
	f.run(f.reporter())
	f.chat.loseResponse = false
	f.chat.hideFromRead = true
	f.clock = f.clock.Add(DefaultReconcileAfter + time.Second)
	r := f.reporter()
	// Read-back sees the post again once the lag clears.
	r.afterPost = func() { f.chat.mu.Lock(); f.chat.hideFromRead = false; f.chat.mu.Unlock() }
	out := onlyOutcome(t, f.run(r))
	if out.Result != "duplicate" || out.Seq != 2 || f.chat.posts != 1 || !f.reported() {
		t.Fatalf("outcome = %+v posts %d", out, f.chat.posts)
	}
}

// A crash after the post was accepted and before the marker: a new process
// posts nothing more.
func TestStopReporterCrashAfterPostBeforeMarker_1629(t *testing.T) {
	f := newReporterFixture(t)
	r := f.reporter()
	r.afterPost = func() { panic("crash after post") }
	func() {
		defer func() { _ = recover() }()
		_, _ = r.Run(context.Background())
	}()
	if f.chat.posts != 1 || f.reported() {
		t.Fatalf("setup: posts %d reported %v", f.chat.posts, f.reported())
	}
	for _, step := range []time.Duration{time.Minute, DefaultReconcileAfter, 20 * time.Minute} {
		f.clock = f.clock.Add(step)
		f.run(f.reporter())
	}
	if f.chat.posts != 1 || f.chat.attempts != 1 || !f.reported() {
		t.Fatalf("posts %d attempts %d reported %v", f.chat.posts, f.chat.attempts, f.reported())
	}
}

// A crash before the intent is durable: nothing was posted, so the next run
// posts exactly once.
func TestStopReporterCrashBeforeIntentPostsOnce_1629(t *testing.T) {
	f := newReporterFixture(t)
	r := f.reporter()
	r.beforeIntent = func() { panic("crash before intent") }
	func() {
		defer func() { _ = recover() }()
		_, _ = r.Run(context.Background())
	}()
	if f.chat.attempts != 0 {
		t.Fatalf("posted without an intent: %d", f.chat.attempts)
	}
	if out := onlyOutcome(t, f.run(f.reporter())); out.Result != "reported" || f.chat.posts != 1 {
		t.Fatalf("outcome = %+v posts %d", out, f.chat.posts)
	}
}

// Two invocations at once on one state dir: one posts, the other finds the
// lock held and does nothing.
func TestStopReporterConcurrentInvocationsPostOnce_1629(t *testing.T) {
	f := newReporterFixture(t)
	f.chat.postGate = make(chan struct{})
	f.chat.postEntered = make(chan struct{}, 2)
	first := make(chan ReportRun, 1)
	go func() {
		run, _ := f.reporter().Run(context.Background())
		first <- run
	}()
	<-f.chat.postEntered // the first holds the lock, inside Post
	second := f.run(f.reporter())
	if !second.Busy || len(second.Outcomes) != 0 {
		t.Fatalf("second run = %+v, want busy", second)
	}
	close(f.chat.postGate)
	if out := onlyOutcome(t, <-first); out.Result != "reported" {
		t.Fatalf("first = %+v", out)
	}
	if f.chat.posts != 1 || f.chat.attempts != 1 {
		t.Fatalf("posts %d attempts %d", f.chat.posts, f.chat.attempts)
	}
}

func TestStopReporterPostsNothingWithoutTheReporterBinding_1629(t *testing.T) {
	for _, tc := range []struct {
		name string
		set  func(c *fakeChat)
	}{
		{"no binding", func(c *fakeChat) { c.boundErr = errors.New("not bound") }},
		{"ended session", func(c *fakeChat) { c.boundErr = errors.New("session ended") }},
		{"another worker", func(c *fakeChat) { c.boundID, c.boundName = "w-gil", "Gil" }},
		{"no worker", func(c *fakeChat) { c.boundID, c.boundName = "", "Hugo" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newReporterFixture(t)
			tc.set(f.chat)
			_, err := f.reporter().Run(context.Background())
			if !errors.Is(err, ErrNoReporterBinding) {
				t.Fatalf("err = %v", err)
			}
			if f.chat.attempts != 0 || f.chat.recipientN != 0 || f.reported() {
				t.Fatalf("attempts %d recipient %d reported %v", f.chat.attempts, f.chat.recipientN, f.reported())
			}
			if _, err := os.Stat(filepath.Join(f.box.Dir, stopDropBase("ev-1")+".sending")); err == nil {
				t.Fatal("an intent was written without a binding")
			}
			pending, _, err := f.reporter().Pending(0)
			if err != nil || pending != 1 {
				t.Fatalf("pending = %d, %v", pending, err)
			}
		})
	}
}

func TestStopReporterRecipientFailureLeavesItPending_1629(t *testing.T) {
	f := newReporterFixture(t)
	f.chat.recipientErr = errors.New("worker not found")
	if out := onlyOutcome(t, f.run(f.reporter())); out.Result != "failed" || f.chat.attempts != 0 || f.reported() {
		t.Fatalf("outcome = %+v attempts %d", out, f.chat.attempts)
	}
}

// The recipient and body are frozen at the first attempt: a reportsTo change
// before the retry sends the same bytes, so the duplicate rule and reconcile
// both still match.
func TestStopReporterRetrySendsTheFrozenBody_1629(t *testing.T) {
	f := newReporterFixture(t)
	f.chat.loseResponse = true
	f.run(f.reporter())
	f.chat.loseResponse = false
	f.chat.recipient = "marco"
	f.chat.hideFromRead = true
	f.clock = f.clock.Add(DefaultReconcileAfter + time.Second)
	r := f.reporter()
	r.afterPost = func() { f.chat.mu.Lock(); f.chat.hideFromRead = false; f.chat.mu.Unlock() }
	f.run(r)
	if bodies := f.chat.bodies(); len(bodies) != 1 || !strings.HasPrefix(bodies[0], "@ada ") {
		t.Fatalf("bodies = %q", bodies)
	}
	if f.chat.recipientN != 1 {
		t.Fatalf("recipient resolved %d times, want once", f.chat.recipientN)
	}
}

// A read-back that does not show this worker's exact body writes no marker.
func TestStopReporterReadBackMismatchWritesNoMarker_1629(t *testing.T) {
	f := newReporterFixture(t)
	r := f.reporter()
	r.afterPost = func() {
		f.chat.mu.Lock()
		f.chat.msgs[len(f.chat.msgs)-1].Body = "edited"
		f.chat.mu.Unlock()
	}
	if out := onlyOutcome(t, f.run(r)); out.Result != "failed" || f.reported() {
		t.Fatalf("outcome = %+v reported %v", out, f.reported())
	}
}

// A reconcile read that fails posts nothing.
func TestStopReporterFailedReconcileReadPostsNothing_1629(t *testing.T) {
	f := newReporterFixture(t)
	f.chat.loseResponse = true
	f.run(f.reporter())
	f.chat.loseResponse = false
	f.chat.readErr = errors.New("502")
	f.clock = f.clock.Add(11 * time.Minute)
	if out := onlyOutcome(t, f.run(f.reporter())); out.Result != "failed" || f.chat.attempts != 1 {
		t.Fatalf("outcome = %+v attempts %d", out, f.chat.attempts)
	}
}

// The body and the intent carry ids and codes only.
func TestStopReporterFilesCarryNoScreenText_1629(t *testing.T) {
	f := newReporterFixture(t)
	f.run(f.reporter())
	entries, _ := os.ReadDir(f.box.Dir)
	for _, e := range entries {
		data, _ := os.ReadFile(filepath.Join(f.box.Dir, e.Name()))
		for _, bad := range []string{"screen", "draft text", "proof"} {
			if strings.Contains(string(data), bad) {
				t.Fatalf("%s carries %q", e.Name(), bad)
			}
		}
		if info, _ := e.Info(); info != nil && !strings.HasPrefix(e.Name(), ".reporter") && info.Mode().Perm() != 0o600 {
			t.Fatalf("%s mode %v", e.Name(), info.Mode().Perm())
		}
	}
}

func TestStopReporterPendingCountsStaleUnreported_1629(t *testing.T) {
	f := newReporterFixture(t)
	f.clock = f.clock.Add(11 * time.Minute)
	pending, stale, err := f.reporter().Pending(10 * time.Minute)
	if err != nil || pending != 1 || stale != 1 {
		t.Fatalf("pending %d stale %d err %v", pending, stale, err)
	}
	f.run(f.reporter())
	pending, stale, _ = f.reporter().Pending(10 * time.Minute)
	if pending != 0 || stale != 0 {
		t.Fatalf("after report: pending %d stale %d", pending, stale)
	}
}

// A failed post that never committed, while ANOTHER worker posted the same
// text: the reconcile must not take that post as ours, so it posts its own.
func TestStopReporterReconcileIgnoresAnotherWorkersIdenticalPost_1629(t *testing.T) {
	f := newReporterFixture(t)
	f.chat.failPost = true
	f.run(f.reporter())
	f.chat.failPost = false
	body := f.chat.bodies()
	if len(body) != 0 {
		t.Fatalf("setup: %q", body)
	}
	data, err := os.ReadFile(filepath.Join(f.box.Dir, stopDropBase("ev-1")+".sending"))
	if err != nil {
		t.Fatal(err)
	}
	var intent stopSending
	if err := json.Unmarshal(data, &intent); err != nil {
		t.Fatal(err)
	}
	f.chat.mu.Lock()
	f.chat.msgs = append(f.chat.msgs, ChatMessage{Seq: 2, AuthorWorkerID: "w-other", Body: intent.Body})
	f.chat.mu.Unlock()
	f.clock = f.clock.Add(DefaultReconcileAfter + time.Second)
	out := onlyOutcome(t, f.run(f.reporter()))
	if out.Result != "reported" || out.Seq != 3 || f.chat.posts != 1 {
		t.Fatalf("outcome = %+v posts %d", out, f.chat.posts)
	}
}
