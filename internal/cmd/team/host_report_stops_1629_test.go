package team

import (
	"context"
	"errors"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/delivery"
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
