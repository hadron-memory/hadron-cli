package team

import (
	"bytes"
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/spf13/cobra"

	"github.com/hadron-memory/hadron-cli/internal/cmdutil"
	"github.com/hadron-memory/hadron-cli/internal/delivery"
	"github.com/hadron-memory/hadron-cli/internal/output"
)

// hadron-cli#1629: the reporter's server adapter. Posts carry the bound
// session (never a post as the person), a TEAM_CHAT_DUPLICATE refusal becomes
// the earlier seq, reads never advance a read mark, and the recipient comes
// from the server (reportsTo, else the holder), not from a host roster.

func reporterAPIFake(t *testing.T, bodies map[string][]string) (*stopReporterAPI, func() []deliveryCall) {
	t.Helper()
	s, calls := deliveryFake(t, bodies)
	return &stopReporterAPI{client: s.client, app: "app1", b: &binding{SessionID: "sess-hugo", WorkerID: "w-hugo", WorkerName: "Hugo"}}, calls
}

func TestReportStopsPostCarriesTheBoundSession_1629(t *testing.T) {
	a, calls := reporterAPIFake(t, map[string][]string{"CreateTeamChatMessage": {
		`{"data":{"createTeamChatMessage":{"nodeId":"n","seq":42,"body":"b","at":"2026-11-04T12:00:00Z","authorWorkerId":"w-hugo","authorName":"Hugo","sessionId":"sess-hugo","mentions":[]}}}`}})
	seq, err := a.Post(context.Background(), "b")
	if err != nil || seq != 42 {
		t.Fatalf("seq %d err %v", seq, err)
	}
	cs := calls()
	if len(cs) != 1 || string(cs[0].vars["sessionRef"]) != `"sess-hugo"` {
		t.Fatalf("calls %+v", cs)
	}
	if _, ok := cs[0].vars["replyToSeq"]; ok {
		t.Fatalf("sends replyToSeq: %s", cs[0].vars["replyToSeq"])
	}
}

func TestReportStopsDuplicateRefusalIsTheEarlierSeq_1629(t *testing.T) {
	a, _ := reporterAPIFake(t, map[string][]string{"CreateTeamChatMessage": {
		`{"errors":[{"message":"Identical to your message #7","extensions":{"code":"TEAM_CHAT_DUPLICATE","earlierSeq":7,"channelId":"c"}}]}`}})
	_, err := a.Post(context.Background(), "b")
	var dup *delivery.DuplicatePostError
	if !errors.As(err, &dup) || dup.EarlierSeq != 7 {
		t.Fatalf("err = %v", err)
	}
}

func TestReportStopsOtherRefusalIsAnError_1629(t *testing.T) {
	a, _ := reporterAPIFake(t, map[string][]string{"CreateTeamChatMessage": {
		`{"errors":[{"message":"rate","extensions":{"code":"TEAM_CHAT_POST_RATE_LIMITED"}}]}`}})
	_, err := a.Post(context.Background(), "b")
	var dup *delivery.DuplicatePostError
	if err == nil || errors.As(err, &dup) {
		t.Fatalf("err = %v", err)
	}
}

func TestReportStopsReadsNeverAdvanceAReadMark_1629(t *testing.T) {
	a, calls := reporterAPIFake(t, map[string][]string{
		"TeamChatReadHeadAttributed": {`{"data":{"teamChatMessages":{"items":[{"seq":99}]}}}`},
		"TeamChatMessagesAttributed": {`{"data":{"teamChatMessages":{"total":1,"items":[{"nodeId":"n","seq":100,"body":"x","at":"2026-11-04T12:00:00Z","authorWorkerId":"w-hugo","authorName":"Hugo","mentions":[]}]}}}`},
	})
	head, err := a.Head(context.Background())
	if err != nil || head != 99 {
		t.Fatalf("head %d err %v", head, err)
	}
	msgs, err := a.MessagesAfter(context.Background(), 99, 200)
	if err != nil || len(msgs) != 1 || msgs[0] != (delivery.ChatMessage{Seq: 100, AuthorWorkerID: "w-hugo", Body: "x"}) {
		t.Fatalf("msgs %+v err %v", msgs, err)
	}
	cs := calls()
	if len(cs) != 2 || string(cs[1].vars["sinceSeq"]) != "99" || string(cs[1].vars["limit"]) != "200" {
		t.Fatalf("calls %+v", cs)
	}
}

func TestReportStopsRecipientIsReportsToElseHolder_1629(t *testing.T) {
	a, _ := reporterAPIFake(t, map[string][]string{"GetWorker": {
		`{"data":{"worker":{"id":"w-gil","name":"Gil","slug":"gil","reportsTo":{"id":"w-ada","name":"Ada","urn":null}}}}`,
		`{"data":{"worker":{"id":"w-ada","name":"Ada","slug":"ada"}}}`,
	}})
	if got, err := a.Recipient(context.Background(), "w-gil"); err != nil || got != "ada" {
		t.Fatalf("recipient %q err %v", got, err)
	}
	a, _ = reporterAPIFake(t, map[string][]string{
		"GetWorker": {`{"data":{"worker":{"id":"w-ada","name":"Ada","slug":"ada","reportsTo":null,"heldByUserId":"u1"}}}`},
		"GetUser":   {`{"data":{"user":{"id":"u1","handle":"holger","name":"H"}}}`},
	})
	if got, err := a.Recipient(context.Background(), "w-ada"); err != nil || got != "holger" {
		t.Fatalf("holder recipient %q err %v", got, err)
	}
	a, _ = reporterAPIFake(t, map[string][]string{"GetWorker": {`{"data":{"worker":{"id":"w-x","name":"X","slug":"x","reportsTo":null,"heldByUserId":null}}}`}})
	if _, err := a.Recipient(context.Background(), "w-x"); err == nil {
		t.Fatal("no reportsTo and no visible holder must be an error, not a guess")
	}
}

func reportStopsRun(t *testing.T, args ...string) (string, error) {
	t.Helper()
	io, out, _ := output.Test()
	f := &cmdutil.Factory{IOStreams: io}
	root := &cobra.Command{Use: "host", SilenceUsage: true, SilenceErrors: true}
	root.PersistentFlags().BoolVar(&f.JSON, "json", false, "")
	root.AddCommand(newCmdTeamHostReportStops(f))
	root.SetArgs(append([]string{"report-stops"}, args...))
	root.SetOut(&bytes.Buffer{})
	err := root.Execute()
	return out.String(), err
}

// UX P3 (b38108c9): the dry run labels its second count.
func TestReportStopsDryRunLabelsTheStaleCount_1629(t *testing.T) {
	cfg := hostConfig(t, "")
	box := &delivery.StopOutbox{Dir: filepath.Join(filepath.Dir(cfg), "delivery", "stop-dropped")}
	if _, err := box.Drop(delivery.StopDrop{EventID: "ev-1", WorkerID: "w", Worker: "Gil", Level: "HALT",
		HoldReason: "draft", HeldAt: time.Now().Add(-time.Hour)}); err != nil {
		t.Fatal(err)
	}
	out, err := reportStopsRun(t, "--config", cfg, "--reporter-worker", "Hugo", "--dry-run")
	if err != nil || out != "pending 1, of which held over 10m0s: 1\n" {
		t.Fatalf("out %q err %v", out, err)
	}
}

// UX P3 (b38108c9): a run with nothing pending says so instead of printing nothing.
func TestReportStopsEmptyRunSaysNothingToReport_1629(t *testing.T) {
	cfg := hostConfig(t, "")
	out, err := reportStopsRun(t, "--config", cfg, "--reporter-worker", "Hugo")
	if err != nil || out != "nothing to report\n" {
		t.Fatalf("out %q err %v", out, err)
	}
}

func sessionBody(workerID, name string, ended bool) string {
	end := "null"
	if ended {
		end = `"2026-11-04T11:00:00Z"`
	}
	w, wk := "null", "null"
	if workerID != "" {
		w = `"` + workerID + `"`
		wk = `{"id":"` + workerID + `","name":"` + name + `"}`
	}
	return `{"data":{"session":{"id":"sess-1","workerId":` + w + `,"worker":` + wk + `,"endedAt":` + end + `}}}`
}

// LOCAL P2 (b38108c9): the reporter's identity is the server's session
// worker, never the binding file alone.
func TestReportStopsIdentityComesFromTheServerSession_1629(t *testing.T) {
	b := &binding{SessionID: "sess-1", WorkerID: "w-hugo", WorkerName: "Hugo"}
	for name, tc := range map[string]struct {
		body string
		ok   bool
	}{
		"same worker":           {sessionBody("w-hugo", "Hugo", false), true},
		"another worker":        {sessionBody("w-gil", "Gil", false), false},
		"not a worker session":  {sessionBody("", "", false), false},
		"ended":                 {sessionBody("w-hugo", "Hugo", true), false},
		"nested worker differs": {`{"data":{"session":{"id":"sess-1","workerId":"w-hugo","worker":{"id":"w-gil","name":"Hugo"},"endedAt":null}}}`, false},
		"not reported":          {`{"data":{"session":null}}`, false},
	} {
		s, _ := deliveryFake(t, map[string][]string{"GetTeamSession": {tc.body}})
		id, wname, err := reporterSessionIdentity(context.Background(), s.client, b)
		if tc.ok != (err == nil) {
			t.Errorf("%s: err %v", name, err)
		}
		if tc.ok && (id != "w-hugo" || wname != "Hugo") {
			t.Errorf("%s: identity %q %q", name, id, wname)
		}
	}
}

// The server's name is what --reporter-worker is checked against.
func TestReportStopsIdentityReturnsTheServersName_1629(t *testing.T) {
	b := &binding{SessionID: "sess-1", WorkerID: "w-hugo", WorkerName: "Stale"}
	s, _ := deliveryFake(t, map[string][]string{"GetTeamSession": {sessionBody("w-hugo", "Hugo", false)}})
	if _, name, err := reporterSessionIdentity(context.Background(), s.client, b); err != nil || name != "Hugo" {
		t.Fatalf("name %q err %v", name, err)
	}
}

// identityOnlyAPI is the real adapter whose BoundWorker runs the real
// identity proof against a fake server, with the binding given.
type identityOnlyAPI struct {
	*stopReporterAPI
	bind *binding
}

func (a identityOnlyAPI) BoundWorker(ctx context.Context) (string, string, error) {
	id, name, err := reporterSessionIdentity(ctx, a.client, a.bind)
	if err != nil {
		return "", "", err
	}
	a.b = a.bind
	return id, name, nil
}

// LOCAL P2 (b38108c9): a binding labelled Hugo that carries Gil's session
// sends ZERO posts.
func TestReportStopsMismatchedSessionSendsZeroPosts_1629(t *testing.T) {
	a, calls := reporterAPIFake(t, map[string][]string{"GetTeamSession": {sessionBody("w-gil", "Gil", false)}})
	box := &delivery.StopOutbox{Dir: filepath.Join(t.TempDir(), "stop-dropped")}
	if _, err := box.Drop(delivery.StopDrop{EventID: "ev-1", WorkerID: "w-gil", Worker: "Gil", Level: "HALT",
		HoldReason: "draft", HeldAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	rep := &delivery.StopReporter{Outbox: box, ReporterWorker: "Hugo",
		API: identityOnlyAPI{stopReporterAPI: a, bind: &binding{SessionID: "session-gil", WorkerID: "w-hugo", WorkerName: "Hugo"}}}
	_, err := rep.Run(context.Background())
	if !errors.Is(err, delivery.ErrNoReporterBinding) {
		t.Fatalf("err %v", err)
	}
	for _, c := range calls() {
		if c.op != "GetTeamSession" {
			t.Fatalf("a call after the identity refusal: %s", c.op)
		}
	}
}
