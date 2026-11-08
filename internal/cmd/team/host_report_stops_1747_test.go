package team

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/spf13/cobra"

	"github.com/hadron-memory/hadron-cli/internal/cmdutil"
	"github.com/hadron-memory/hadron-cli/internal/delivery"
	"github.com/hadron-memory/hadron-cli/internal/output"
)

// hadron-cli#1747: command-level pins for the normal (non-dry-run) output of
// report-stops. The repo pins already covered --dry-run; these drive the real
// command path — a binding under GitDirEnv, HADRON_TOKEN, --server at a fake
// GraphQL server — and pin the recorded mismatch/not-found lines, the default
// error arm's SafeLine sanitisation and 1024-rune bound, the status-file
// diagnostic suffixes, control-byte safety, and the no-repost marker rule.
// Tests-only per the T0 Light gate: no production change.

// reportStopsFake1747 answers each GraphQL operation from a handler function
// (dynamic, unlike deliveryFake's fixed queue: the read-back leg must echo the
// body the post leg just received). An operation with no handler fails the
// test — that is the no-repost control.
type reportStopsFake1747 struct {
	t        *testing.T
	srv      *httptest.Server
	mu       sync.Mutex
	handlers map[string]func(vars map[string]json.RawMessage) string
	calls    []string
	posted   string // the body variable of the last CreateTeamChatMessage
}

func newReportStopsFake1747(t *testing.T, handlers map[string]func(vars map[string]json.RawMessage) string) *reportStopsFake1747 {
	t.Helper()
	f := &reportStopsFake1747{t: t, handlers: handlers}
	f.srv = httptest.NewServer(http.HandlerFunc(f.serve))
	t.Cleanup(f.srv.Close)
	return f
}

func (f *reportStopsFake1747) serve(w http.ResponseWriter, r *http.Request) {
	var body struct {
		OperationName string                     `json:"operationName"`
		Variables     map[string]json.RawMessage `json:"variables"`
	}
	_ = json.NewDecoder(r.Body).Decode(&body)
	f.mu.Lock()
	f.calls = append(f.calls, body.OperationName)
	if body.OperationName == "CreateTeamChatMessage" {
		f.posted = string(body.Variables["body"])
	}
	h := f.handlers[body.OperationName]
	f.mu.Unlock()
	if h == nil {
		f.t.Errorf("unexpected operation %q", body.OperationName)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"errors":[{"message":"unexpected"}]}`))
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write([]byte(h(body.Variables)))
}

func (f *reportStopsFake1747) count(op string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := 0
	for _, c := range f.calls {
		if c == op {
			n++
		}
	}
	return n
}

func (f *reportStopsFake1747) dropHandlers() {
	f.mu.Lock()
	f.handlers = nil
	f.mu.Unlock()
}

// ev1747Base is the outbox base name for event "ev-1" (sha256 prefix, as the
// poller names drops).
func ev1747Base() string {
	h := sha256.Sum256([]byte("ev-1"))
	return hex.EncodeToString(h[:])[:32]
}

const sessionOK1747 = `{"data":{"session":{"id":"sess-hugo","workerId":"w-hugo","worker":{"id":"w-hugo","name":"Hugo"},"endedAt":null}}}`

// workerWithBoss1747 answers every GetWorker with Gil reporting to Ada.
const workerWithBoss1747 = `{"data":{"worker":{"id":"w-gil","name":"Gil","slug":"gil","reportsTo":{"id":"w-ada","name":"Ada","urn":null}}}}`

const postOK1747 = `{"data":{"createTeamChatMessage":{"nodeId":"n","seq":2,"body":"b","at":"2026-11-08T01:00:00Z","authorWorkerId":"w-hugo","authorName":"Hugo","sessionId":"sess-hugo","mentions":[]}}}`

func msgBody1747(seq int, authorWorkerID, body string) string {
	b, _ := json.Marshal(body)
	return `{"data":{"teamChatMessages":{"total":1,"items":[{"nodeId":"n","seq":` + strconv.Itoa(seq) +
		`,"body":` + string(b) + `,"at":"2026-11-08T01:00:00Z","authorWorkerId":"` + authorWorkerID + `","authorName":"Hugo","mentions":[]}]}}}`
}

// write1747 writes a small file (a hand-made .sending) into a directory.
func write1747(t *testing.T, dir, name, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
}

// stopFixture1747 builds an isolated outbox (one dropped halt for the named
// worker), a git dir whose binding is Hugo's live worker session, and a fake
// server. No real config, token store or home directory is read.
func stopFixture1747(t *testing.T, worker string, handlers map[string]func(vars map[string]json.RawMessage) string) (cfg, outboxDir string, fake *reportStopsFake1747) {
	t.Helper()
	t.Setenv(GitDirEnv, t.TempDir())
	t.Setenv("HADRON_TOKEN", "tok-1747")
	cfg = hostConfig(t, "")
	outboxDir = delivery.StopOutboxDir(filepath.Dir(cfg))
	box := &delivery.StopOutbox{Dir: outboxDir}
	if _, err := box.Drop(delivery.StopDrop{EventID: "ev-1", SignalID: "sig-1", WorkerID: "w-gil", Worker: worker, Level: "HALT",
		HoldReason: "draft", HeldAt: time.Date(2026, 11, 8, 0, 0, 0, 0, time.UTC)}); err != nil {
		t.Fatal(err)
	}
	binding := `{"sessionId":"sess-hugo","workerId":"w-hugo","workerName":"Hugo","workerRole":"qa-engineer","appId":"app-1","agentId":"ag-1","server":"","startedAt":"2026-11-08T00:00:00Z"}`
	write1747(t, os.Getenv(GitDirEnv), "hadron-team-session.json", binding)
	return cfg, outboxDir, newReportStopsFake1747(t, handlers)
}

// run1747 runs the real command against the fixture's server and binding.
func run1747(t *testing.T, srv *httptest.Server, cfg string, args ...string) (string, error) {
	t.Helper()
	io, out, _ := output.Test()
	f := &cmdutil.Factory{IOStreams: io, ServerFlag: srv.URL, HTTPClient: srv.Client()}
	root := &cobra.Command{Use: "host", SilenceUsage: true, SilenceErrors: true}
	root.PersistentFlags().BoolVar(&f.JSON, "json", false, "")
	root.PersistentFlags().StringVar(&f.AppFlag, "app", "", "")
	root.AddCommand(newCmdTeamHostReportStops(f))
	root.SetArgs(append([]string{"report-stops", "--app", "hrn:app:example.com:team"}, args...))
	root.SetOut(&bytes.Buffer{})
	err := root.Execute()
	return out.String(), err
}

// acceptedSending1747 is an intent whose accepted post #2 is being read back.
func acceptedSending1747() string {
	return `{"eventId":"ev-1","body":"frozen","headSeq":1,"firstAttemptAt":"2026-11-07T00:00:00Z","authorWorkerId":"w-hugo","acceptedSeq":2,"acceptedAuthorWorkerId":"w-hugo"}`
}

// TestReportStopsRecordedFailureText_1747 pins the two recorded-seq arms of
// the normal-run line exactly, and that --json keeps the OLD error prose
// while the human line follows the new gated wording.
func TestReportStopsRecordedFailureText_1747(t *testing.T) {
	cases := []struct {
		name string
		read string // the read-back page the server answers
		want string
	}{
		{"mismatch body", msgBody1747(2, "w-hugo", "different"),
			"failed ev-1 Gil #2: read-back of #2 does not match (body); needs a person\n"},
		{"mismatch author and body", msgBody1747(2, "w-gil", "different"),
			"failed ev-1 Gil #2: read-back of #2 does not match (author, body); needs a person\n"},
		{"not found", `{"data":{"teamChatMessages":{"total":0,"items":[]}}}`,
			"failed ev-1 Gil #2: #2 not found on read-back; needs a person if it persists\n"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			handlers := map[string]func(vars map[string]json.RawMessage) string{
				"GetTeamSession":             func(map[string]json.RawMessage) string { return sessionOK1747 },
				"TeamChatMessagesAttributed": func(map[string]json.RawMessage) string { return tc.read },
			}
			cfg, dir, fake := stopFixture1747(t, "Gil", handlers)
			write1747(t, dir, ev1747Base()+".sending", acceptedSending1747())
			out, err := run1747(t, fake.srv, cfg, "--config", cfg, "--reporter-worker", "Hugo")
			if out != tc.want {
				t.Fatalf("human %q err %v", out, err)
			}
			if c := code(err); c != 7 {
				t.Fatalf("exit %d, want 7 (failed reports stay pending)", c)
			}
		})
	}
	t.Run("json keeps the old mismatch prose", func(t *testing.T) {
		handlers := map[string]func(vars map[string]json.RawMessage) string{
			"GetTeamSession":             func(map[string]json.RawMessage) string { return sessionOK1747 },
			"TeamChatMessagesAttributed": func(map[string]json.RawMessage) string { return msgBody1747(2, "w-hugo", "different") },
		}
		cfg, dir, fake := stopFixture1747(t, "Gil", handlers)
		write1747(t, dir, ev1747Base()+".sending", acceptedSending1747())
		out, err := run1747(t, fake.srv, cfg, "--config", cfg, "--reporter-worker", "Hugo", "--json")
		if c := code(err); c != 7 {
			t.Fatalf("exit %d, want 7: %s", c, out)
		}
		var run delivery.ReportRun
		if json.Unmarshal([]byte(out), &run) != nil || run.Busy || len(run.Outcomes) != 1 {
			t.Fatalf("json %s", out)
		}
		o := run.Outcomes[0]
		// ReportOutcome.error preserves the old mismatch text; the human line
		// above deliberately follows the new gated table.
		if o.Error != "reading back #2: not this notice's post; needs a person" ||
			o.Reason != "readback-mismatch" || o.Field != "body" || o.Result != "failed" || o.Seq != 2 {
			t.Fatalf("outcome %+v", o)
		}
	})
}

// TestReportStopsDefaultArmIsSafeAndBounded_1747 pins the default error arm:
// control bytes in the error text are replaced, the text is capped at 1024
// runes (exactly 1024 passes whole; 1025 truncates to 1023 plus an ellipsis).
func TestReportStopsDefaultArmIsSafeAndBounded_1747(t *testing.T) {
	esc := "\x1b]0;pwned\x07\x1b[31m"
	fresh := func(msg string) map[string]func(vars map[string]json.RawMessage) string {
		refusal := `{"errors":[{"message":` + jsonString1747(msg) + `,"extensions":{"code":"GRAPHQL_VALIDATION_FAILED"}}],"data":null}`
		return map[string]func(vars map[string]json.RawMessage) string{
			"GetTeamSession":             func(map[string]json.RawMessage) string { return sessionOK1747 },
			"GetWorker":                  func(map[string]json.RawMessage) string { return workerWithBoss1747 },
			"TeamChatReadHeadAttributed": func(map[string]json.RawMessage) string { return `{"data":{"teamChatMessages":{"items":[{"seq":1}]}}}` },
			"CreateTeamChatMessage":      func(map[string]json.RawMessage) string { return refusal },
		}
	}
	// First leg: capture the exact error text the JSON run carries.
	msg0 := esc + strings.Repeat("x", 600)
	cfg, _, fake := stopFixture1747(t, "Gil", fresh(msg0))
	out, err := run1747(t, fake.srv, cfg, "--config", cfg, "--reporter-worker", "Hugo", "--json")
	if c := code(err); c != 7 {
		t.Fatalf("exit %d: %s", c, out)
	}
	var run delivery.ReportRun
	if json.Unmarshal([]byte(out), &run) != nil || len(run.Outcomes) != 1 {
		t.Fatalf("json %s", out)
	}
	err1 := run.Outcomes[0].Error
	n1 := utf8.RuneCountInString(err1)
	delta := 1024 - n1
	if delta < -600 {
		t.Fatalf("error already %d runes; cannot shrink into the prefix", n1)
	}
	// Second leg: an error of exactly 1024 runes passes whole.
	msg2 := esc + strings.Repeat("x", 600+delta)
	expected2 := strings.TrimSuffix(err1, msg0+")") + msg2 + ")"
	if got := utf8.RuneCountInString(expected2); got != 1024 {
		t.Fatalf("expected error is %d runes, want exactly 1024", got)
	}
	cfg, _, fake = stopFixture1747(t, "Gil", fresh(msg2))
	out, err = run1747(t, fake.srv, cfg, "--config", cfg, "--reporter-worker", "Hugo")
	want := "failed ev-1 Gil: " + output.SafeLine(expected2, 1024) + "\n"
	if out != want || strings.Contains(out, "\x1b") || strings.Contains(out, "…") {
		t.Fatalf("at the bound:\n got %q\nwant %q", out, want)
	}
	if c := code(err); c != 7 {
		t.Fatalf("exit %d", c)
	}
	// Third leg: one rune more truncates to 1023 runes plus an ellipsis.
	msg3 := msg2 + "y"
	expected3 := strings.TrimSuffix(expected2, ")") + "y)"
	cfg, _, fake = stopFixture1747(t, "Gil", fresh(msg3))
	out, err = run1747(t, fake.srv, cfg, "--config", cfg, "--reporter-worker", "Hugo")
	want = "failed ev-1 Gil: " + output.SafeLine(expected3, 1024) + "\n"
	if out != want || !strings.Contains(out, "…") || strings.Contains(out, "\x1b") {
		t.Fatalf("past the bound:\n got %q\nwant %q", out, want)
	}
	segment := strings.TrimSuffix(strings.TrimPrefix(out, "failed ev-1 Gil: "), "\n")
	if n := utf8.RuneCountInString(segment); n != 1024 {
		t.Fatalf("error segment is %d runes, want 1024 (1023 plus the ellipsis)", n)
	}
}

// TestReportStopsForgedWorkerIsSanitised_1747: the halted worker's name is
// poller-recorded data on the normal line; an escape-sequence forgery must
// not reach the terminal.
func TestReportStopsForgedWorkerIsSanitised_1747(t *testing.T) {
	handlers := map[string]func(vars map[string]json.RawMessage) string{
		"GetTeamSession":             func(map[string]json.RawMessage) string { return sessionOK1747 },
		"TeamChatMessagesAttributed": func(map[string]json.RawMessage) string { return msgBody1747(2, "w-hugo", "different") },
	}
	cfg, dir, fake := stopFixture1747(t, "Gi\x1b]0;pwned\x07l", handlers)
	write1747(t, dir, ev1747Base()+".sending", acceptedSending1747())
	out, err := run1747(t, fake.srv, cfg, "--config", cfg, "--reporter-worker", "Hugo")
	want := "failed ev-1 Gi ]0;pwned l #2: read-back of #2 does not match (body); needs a person\n"
	if out != want || strings.Contains(out, "\x1b") {
		t.Fatalf("human %q err %v", out, err)
	}
}

// TestReportStopsStatusFileSuffixes_1747 pins the two diagnostic suffixes:
// a status-file write failure appends "(status file not updated)", a removal
// failure after a durable marker appends "(status file not removed)" and the
// marker still wins.
func TestReportStopsStatusFileSuffixes_1747(t *testing.T) {
	t.Run("write failure", func(t *testing.T) {
		handlers := map[string]func(vars map[string]json.RawMessage) string{
			"GetTeamSession":             func(map[string]json.RawMessage) string { return sessionOK1747 },
			"TeamChatMessagesAttributed": func(map[string]json.RawMessage) string { return msgBody1747(2, "w-hugo", "different") },
		}
		cfg, dir, fake := stopFixture1747(t, "Gil", handlers)
		write1747(t, dir, ev1747Base()+".sending", acceptedSending1747())
		// A directory where the status file would be written: the synced
		// rename cannot land, the result and exit are unchanged.
		if err := os.Mkdir(filepath.Join(dir, ev1747Base()+".lastfail"), 0700); err != nil {
			t.Fatal(err)
		}
		out, err := run1747(t, fake.srv, cfg, "--config", cfg, "--reporter-worker", "Hugo")
		want := "failed ev-1 Gil #2: read-back of #2 does not match (body); needs a person (status file not updated)\n"
		if out != want {
			t.Fatalf("human %q err %v", out, err)
		}
		if c := code(err); c != 7 {
			t.Fatalf("exit %d, want 7", c)
		}
	})
	t.Run("remove failure after a durable marker", func(t *testing.T) {
		handlers := map[string]func(vars map[string]json.RawMessage) string{
			"GetTeamSession":             func(map[string]json.RawMessage) string { return sessionOK1747 },
			"TeamChatMessagesAttributed": func(map[string]json.RawMessage) string { return msgBody1747(2, "w-hugo", "frozen") },
		}
		cfg, dir, fake := stopFixture1747(t, "Gil", handlers)
		write1747(t, dir, ev1747Base()+".sending", acceptedSending1747())
		// A non-empty directory where the status file would be removed: the
		// marker is already durable, so the outcome stands with the suffix.
		stale := filepath.Join(dir, ev1747Base()+".lastfail")
		if err := os.Mkdir(stale, 0700); err != nil {
			t.Fatal(err)
		}
		write1747(t, stale, "keep", "sentinel")
		out, err := run1747(t, fake.srv, cfg, "--config", cfg, "--reporter-worker", "Hugo")
		want := "reconciled ev-1 Gil #2 (status file not removed)\n"
		if out != want || err != nil {
			t.Fatalf("human %q err %v", out, err)
		}
		if _, e := os.Stat(filepath.Join(dir, ev1747Base()+".reported")); e != nil {
			t.Fatalf("marker missing: %v", e)
		}
	})
}

// TestReportStopsReportsOnceThenNothing_1747 is the positive control and the
// no-repost control: a fresh notice is posted exactly once and recorded; the
// next run makes no server call at all and says there is nothing to report.
func TestReportStopsReportsOnceThenNothing_1747(t *testing.T) {
	handlers := map[string]func(vars map[string]json.RawMessage) string{
		"GetTeamSession":             func(map[string]json.RawMessage) string { return sessionOK1747 },
		"GetWorker":                  func(map[string]json.RawMessage) string { return workerWithBoss1747 },
		"TeamChatReadHeadAttributed": func(map[string]json.RawMessage) string { return `{"data":{"teamChatMessages":{"items":[{"seq":1}]}}}` },
		"CreateTeamChatMessage":      func(map[string]json.RawMessage) string { return postOK1747 },
	}
	cfg, dir, fake := stopFixture1747(t, "Gil", handlers)
	// The read-back echoes the body the post leg just carried.
	handlers["TeamChatMessagesAttributed"] = func(map[string]json.RawMessage) string {
		fake.mu.Lock()
		posted := fake.posted
		fake.mu.Unlock()
		var body string
		_ = json.Unmarshal([]byte(posted), &body)
		return msgBody1747(2, "w-hugo", body)
	}
	out, err := run1747(t, fake.srv, cfg, "--config", cfg, "--reporter-worker", "Hugo")
	if out != "reported ev-1 Gil #2\n" || err != nil {
		t.Fatalf("human %q err %v", out, err)
	}
	if n := fake.count("CreateTeamChatMessage"); n != 1 {
		t.Fatalf("%d posts, want exactly one", n)
	}
	if _, e := os.Stat(filepath.Join(dir, ev1747Base()+".reported")); e != nil {
		t.Fatalf("marker missing: %v", e)
	}
	// No-repost: with the marker durable, the next run talks to no one.
	fake.dropHandlers()
	out, err = run1747(t, fake.srv, cfg, "--config", cfg, "--reporter-worker", "Hugo")
	if out != "nothing to report\n" || err != nil {
		t.Fatalf("second run %q err %v", out, err)
	}
}

// TestReportStopsDryRunHostileCacheFields_1747: the dry-run line prints the
// cached reason and field as they are, so only the cache's reason/field
// whitelist stands between a forged file and the terminal. A hostile field
// never becomes a cached observation.
func TestReportStopsDryRunHostileCacheFields_1747(t *testing.T) {
	cfg := hostConfig(t, "")
	dir := delivery.StopOutboxDir(filepath.Dir(cfg))
	box := &delivery.StopOutbox{Dir: dir}
	if _, err := box.Drop(delivery.StopDrop{EventID: "event", Worker: "worker", HeldAt: time.Now().Add(-time.Hour)}); err != nil {
		t.Fatal(err)
	}
	h := sha256.Sum256([]byte("event"))
	base := hex.EncodeToString(h[:])[:32]
	write1747(t, dir, base+".sending", `{"acceptedSeq":17}`)
	write1747(t, dir, base+".lastfail", `{"v":1,"eventId":"event","seq":17,"reason":"readback-mismatch","field":"\u001b]0;pwned\u0007","lastObservedAt":"2026-11-06T15:00:00.123Z"}`)
	out, err := reportStopsRun(t, "--config", cfg, "--dry-run")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "\x1b") || strings.Contains(out, "needs a person?") {
		t.Fatalf("hostile field reached the dry-run line: %q", out)
	}
	if out != "pending 1, of which held over 10m0s: 1\n" {
		t.Fatalf("human %q", out)
	}
}

func jsonString1747(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}
