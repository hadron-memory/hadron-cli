package team

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/cmdutil"
	"github.com/hadron-memory/hadron-cli/internal/exitcode"
	"github.com/hadron-memory/hadron-cli/internal/output"
)

type fakeInbox struct {
	page                  inboxPage
	readErr, ackErr       error
	reads, acks           int
	scopes                []inboxIdentity
	ids                   []string
	beforeRead, beforeAck func()
}

func (s *fakeInbox) Read(_ context.Context, b inboxIdentity) (inboxPage, error) {
	s.reads++
	s.scopes = append(s.scopes, b)
	if s.beforeRead != nil {
		s.beforeRead()
	}
	return s.page, s.readErr
}
func (s *fakeInbox) Ack(_ context.Context, b inboxIdentity, ids []string) (inboxAck, error) {
	s.acks++
	s.scopes = append(s.scopes, b)
	s.ids = append([]string{}, ids...)
	if s.beforeAck != nil {
		s.beforeAck()
	}
	return inboxAck{Acknowledged: 1}, s.ackErr
}
func ownInboxIdentity(context.Context) (inboxIdentity, error) {
	return inboxIdentity{AppID: "app", WorkerID: "self", SessionID: "session", Server: "server"}, nil
}
func inboxFixture() *fakeInbox {
	return &fakeInbox{page: inboxPage{Batches: []inboxBatch{{ID: "PRIVATE-PROOF", WorkerID: "self", Worker: "Self", Text: "[hadron chat #1] exact text", Ranges: []inboxRange{{ChannelID: "ch", Incarnation: 2, FromSeq: 1, ThroughSeq: 1, Seqs: []int{1}}}}}}}
}
func TestInboxCompleteOutputThenAck(t *testing.T) {
	for _, jsonMode := range []bool{false, true} {
		streams, out, stderr := output.Test()
		s := inboxFixture()
		s.beforeAck = func() {
			if out.Len() == 0 {
				t.Error("ack before output")
			}
		}
		if err := runInbox(context.Background(), s, ownInboxIdentity, streams, jsonMode, nil); err != nil {
			t.Fatal(err)
		}
		if s.reads != 1 || s.acks != 1 || len(s.ids) != 1 || s.ids[0] != "PRIVATE-PROOF" {
			t.Fatalf("read/ack/ids %d/%d/%v", s.reads, s.acks, s.ids)
		}
		if len(s.scopes) != 2 || s.scopes[0] != s.scopes[1] || s.scopes[0].SessionID != "session" {
			t.Fatal("requester changed or absent")
		}
		if !jsonMode && out.String() != "[hadron chat #1] exact text\n" {
			t.Errorf("text changed: %q", out)
		}
		if strings.Contains(stderr.String(), "PRIVATE") {
			t.Fatal("proof in stderr")
		}
	}
}

type failInboxWriter struct {
	flush bool
	strings.Builder
}

func (w *failInboxWriter) Write(p []byte) (int, error) {
	if !w.flush {
		return 0, io.ErrClosedPipe
	}
	return w.Builder.Write(p)
}
func (w *failInboxWriter) Flush() error { return io.ErrClosedPipe }
func TestInboxOutputFailureNeverAcks(t *testing.T) {
	for _, flush := range []bool{false, true} {
		streams, _, _ := output.Test()
		streams.Out = &failInboxWriter{flush: flush}
		s := inboxFixture()
		if err := runInbox(context.Background(), s, ownInboxIdentity, streams, true, nil); err == nil {
			t.Error("output failure ignored")
		}
		if s.acks != 0 {
			t.Fatal("ack after output/flush failure")
		}
	}
}
func TestInboxRebindBeforeAckRefuses(t *testing.T) {
	streams, _, _ := output.Test()
	s := inboxFixture()
	n := 0
	identity := func(context.Context) (inboxIdentity, error) {
		n++
		b, _ := ownInboxIdentity(context.Background())
		if n >= 3 {
			b.SessionID = "successor"
		}
		return b, nil
	}
	if err := runInbox(context.Background(), s, identity, streams, true, nil); err == nil {
		t.Fatal("rebind accepted")
	}
	if s.acks != 0 {
		t.Fatal("old proof acked by new binding")
	}
}
func TestInboxReadAndAckFailureHaveNoSecretOutput(t *testing.T) {
	for _, duringAck := range []bool{false, true} {
		streams, out, stderr := output.Test()
		s := inboxFixture()
		poison := exitcode.Newf(exitcode.Forbidden, "SECRET-PROOF-OR-CREDENTIAL")
		if duringAck {
			s.ackErr = poison
		} else {
			s.readErr = poison
		}
		err := runInbox(context.Background(), s, ownInboxIdentity, streams, true, nil)
		if err == nil || exitcode.FromError(err) != exitcode.Forbidden {
			t.Fatalf("refusal lost: %v", err)
		}
		if strings.Contains(stderr.String(), "SECRET") || strings.Contains(err.Error(), "SECRET") {
			t.Fatal("error reflected secret")
		}
		if !duringAck && out.Len() != 0 {
			t.Fatal("read failure printed output")
		}
		if duringAck && !strings.Contains(stderr.String(), "unconfirmed") {
			t.Fatal("missing ack recovery")
		}
	}
}
func TestInboxAckOnlyDoesNotReadOrRedisplay(t *testing.T) {
	streams, out, _ := output.Test()
	s := inboxFixture()
	if err := runInbox(context.Background(), s, ownInboxIdentity, streams, true, []string{"PRIVATE-PROOF"}); err != nil {
		t.Fatal(err)
	}
	if s.reads != 0 || s.acks != 1 || strings.Contains(out.String(), "PRIVATE") || strings.Contains(out.String(), "exact text") {
		t.Fatal("ack-only read or disclosed proof/text")
	}
}
func TestInboxRejectsForeignAndUnreadableScope(t *testing.T) {
	streams, out, _ := output.Test()
	s := inboxFixture()
	s.page.Batches[0].WorkerID = "foreign"
	if err := runInbox(context.Background(), s, ownInboxIdentity, streams, true, nil); err == nil {
		t.Fatal("foreign worker output accepted")
	}
	if out.Len() != 0 || s.acks != 0 {
		t.Fatal("foreign metadata output or ack")
	}
	s = inboxFixture()
	bad := func(context.Context) (inboxIdentity, error) { return inboxIdentity{}, errors.New("PRIVATE-BINDING") }
	if err := runInbox(context.Background(), s, bad, streams, true, nil); err == nil {
		t.Fatal("bad binding accepted")
	}
	if s.reads != 0 || s.acks != 0 {
		t.Fatal("bad binding reached transport")
	}
}

func TestInboxAckInputRefusals(t *testing.T) {
	for _, input := range []string{`null`, `{}`, `{"batchIds":null}`, `{"batchIds":[]}`, `{"batchIds":[""]}`, `{"batchIds":["PRIVATE"],"extra":true}`, `{"batchIds":["PRIVATE"]} {}`, `{"batchIds":"PRIVATE"}`} {
		if _, err := decodeInboxAckFile(strings.NewReader(input)); err == nil || strings.Contains(err.Error(), "PRIVATE") {
			t.Fatalf("unsafe input result: %v", err)
		}
	}
	ids, err := decodeInboxAckFile(strings.NewReader(`{"batchIds":["PRIVATE"]}`))
	if err != nil || len(ids) != 1 || ids[0] != "PRIVATE" {
		t.Fatal("valid proof input refused")
	}
}
func TestInboxEmptyPageAndCancellationDoNotAck(t *testing.T) {
	streams, out, _ := output.Test()
	s := &fakeInbox{}
	if err := runInbox(context.Background(), s, ownInboxIdentity, streams, true, nil); err != nil {
		t.Fatal(err)
	}
	var page inboxPage
	if err := json.Unmarshal(out.Bytes(), &page); err != nil {
		t.Fatal(err)
	}
	if s.acks != 0 || page.Batches == nil || len(page.Batches) != 0 {
		t.Fatal("empty page acknowledgment or null array")
	}
	ctx, cancel := context.WithCancel(context.Background())
	s = inboxFixture()
	s.beforeRead = cancel
	streams, out, _ = output.Test()
	if err := runInbox(ctx, s, ownInboxIdentity, streams, true, nil); err == nil {
		t.Fatal("cancel ignored")
	}
	if s.acks != 0 || out.Len() != 0 {
		t.Fatal("cancelled read displayed or acked")
	}
}

func TestInboxCommandRecoveryAndTTYRefusal(t *testing.T) {
	streams, out, _ := output.Test()
	streams.In = strings.NewReader(`{"batchIds":["PRIVATE"]}`)
	source := inboxFixture()
	cmd := newCmdTeamChatInboxWithTransport(&cmdutil.Factory{IOStreams: streams, JSON: true}, source, ownInboxIdentity)
	cmd.SetArgs([]string{"--ack-file", "-"})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	if source.reads != 0 || source.acks != 1 || strings.Contains(out.String(), "PRIVATE") {
		t.Fatal("recovery read or proof disclosure")
	}
	streams, _, _ = output.TestTTY(`{"batchIds":["PRIVATE"]}`)
	source = inboxFixture()
	cmd = newCmdTeamChatInboxWithTransport(&cmdutil.Factory{IOStreams: streams}, source, ownInboxIdentity)
	cmd.SetArgs([]string{"--ack-file", "-"})
	if err := cmd.Execute(); err == nil || exitcode.FromError(err) != exitcode.Usage {
		t.Fatal("terminal stdin accepted")
	}
	if source.reads != 0 || source.acks != 0 {
		t.Fatal("terminal input reached server")
	}
}
