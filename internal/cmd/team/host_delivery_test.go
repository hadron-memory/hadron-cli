package team

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/hadron-memory/hadron-cli/internal/api"
	"github.com/hadron-memory/hadron-cli/internal/delivery"
	"github.com/hadron-memory/hadron-cli/internal/hostpoller"
)

type deliveryCall struct {
	op, auth, session string
	vars              map[string]json.RawMessage
}

// deliveryFake answers each operation from a queue of bodies, in order.
func deliveryFake(t *testing.T, bodies map[string][]string) (*hostServer, func() []deliveryCall) {
	t.Helper()
	var mu sync.Mutex
	var calls []deliveryCall
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			OperationName string                     `json:"operationName"`
			Variables     map[string]json.RawMessage `json:"variables"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		mu.Lock()
		calls = append(calls, deliveryCall{body.OperationName, r.Header.Get("Authorization"), r.Header.Get(api.SessionHeader), body.Variables})
		q := bodies[body.OperationName]
		resp := `{"errors":[{"message":"unexpected"}]}`
		if len(q) > 0 {
			resp, bodies[body.OperationName] = q[0], q[1:]
		} else {
			t.Errorf("unexpected operation %q", body.OperationName)
		}
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(resp))
	}))
	t.Cleanup(srv.Close)
	client, err := api.NewClient(srv.URL, "hk_host_key", srv.Client())
	if err != nil {
		t.Fatal(err)
	}
	s := &hostServer{client: client, app: "hrn:app:hadronmemory.com:team", now: func() time.Time { return deliveryNow }}
	return s, func() []deliveryCall { mu.Lock(); defer mu.Unlock(); return append([]deliveryCall{}, calls...) }
}

var deliveryNow = time.Date(2026, 10, 19, 12, 0, 0, 0, time.UTC)

const batchPage1 = `{"data":{"deliveryBatches":{"batches":[{"id":"PROOF-1-SECRET","worker":{"id":"wkr1"},
 "text":"[hadron chat #5] Ada: hi","deliverAfter":"2026-10-19T11:58:00Z","busyDeadline":"2026-10-19T12:08:00Z",
 "interrupt":false,"overflow":0,"ranges":[{"channel":{"id":"ch1"},"incarnation":2,"fromSeq":4,"throughSeq":5,"seqs":[5]}]}],
 "pageInfo":{"hasNextPage":true,"endCursor":"c1"}}}}`

const batchPage2 = `{"data":{"deliveryBatches":{"batches":[{"id":"PROOF-2-SECRET","worker":{"id":"wkr2"},
 "text":"[hadron chat #9] Eli: yo","deliverAfter":"2026-10-19T11:59:00Z","busyDeadline":"2026-10-19T12:09:00Z",
 "interrupt":false,"overflow":3,"ranges":[{"channel":{"id":"ch1"},"incarnation":2,"fromSeq":7,"throughSeq":9,"seqs":[7,9]},
 {"channel":{"id":"ch2"},"incarnation":1,"fromSeq":1,"throughSeq":1,"seqs":[1]}]}],
 "pageInfo":{"hasNextPage":false,"endCursor":"c2"}}}}`

func TestBatchesPageToExhaustionWithTheHostKeyAndNoSession(t *testing.T) {
	s, calls := deliveryFake(t, map[string][]string{"DeliveryBatches": {batchPage1, batchPage2}})
	got, err := s.Batches(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("batches %+v", got)
	}
	b := got[0]
	if b.ID != "PROOF-1-SECRET" || b.WorkerID != "wkr1" || b.Text != "[hadron chat #5] Ada: hi" || b.Interrupt {
		t.Fatalf("batch %+v", b)
	}
	if !b.DeliverAfter.Equal(time.Date(2026, 10, 19, 11, 58, 0, 0, time.UTC)) || !b.BusyDeadline.Equal(time.Date(2026, 10, 19, 12, 8, 0, 0, time.UTC)) {
		t.Fatalf("timing %+v", b)
	}
	if !b.ProofExpiresAt.Equal(deliveryNow.Add(15 * time.Minute)) {
		t.Fatalf("proof expiry %v, want the read time plus 15 minutes", b.ProofExpiresAt)
	}
	if len(b.Ranges) != 1 || !rangeEq(b.Ranges[0], delivery.Range{ChannelID: "ch1", Incarnation: 2, FromSeq: 4, ThroughSeq: 5, Seqs: []int{5}}) {
		t.Fatalf("ranges %+v", b.Ranges)
	}
	if len(got[1].Ranges) != 2 || !rangeEq(got[1].Ranges[1], delivery.Range{ChannelID: "ch2", Incarnation: 1, FromSeq: 1, ThroughSeq: 1, Seqs: []int{1}}) {
		t.Fatalf("second page %+v", got[1])
	}
	for _, b := range got {
		if err := delivery.CheckBatch(b); err != nil {
			t.Fatalf("a well-formed server batch fails CheckBatch: %v", err)
		}
	}
	cs := calls()
	if len(cs) != 2 {
		t.Fatalf("%d calls, want 2 pages", len(cs))
	}
	for i, c := range cs {
		if c.auth != "Bearer hk_host_key" || c.session != "" {
			t.Fatalf("call %d: auth %q session %q: the host key, never a session", i, c.auth, c.session)
		}
		if string(c.vars["first"]) != "200" || string(c.vars["appRef"]) != `"hrn:app:hadronmemory.com:team"` {
			t.Fatalf("call %d vars %v", i, c.vars)
		}
		if _, named := c.vars["workerRefs"]; named {
			t.Fatalf("call %d names workers: the host is the key's", i)
		}
	}
	if _, ok := cs[0].vars["after"]; ok {
		t.Fatal("the first page sends no cursor")
	}
	if string(cs[1].vars["after"]) != `"c1"` {
		t.Fatalf("second page after %s", cs[1].vars["after"])
	}
}

func rangeEq(a, b delivery.Range) bool {
	if a.ChannelID != b.ChannelID || a.Incarnation != b.Incarnation || a.FromSeq != b.FromSeq || a.ThroughSeq != b.ThroughSeq || len(a.Seqs) != len(b.Seqs) {
		return false
	}
	for i := range a.Seqs {
		if a.Seqs[i] != b.Seqs[i] {
			return false
		}
	}
	return true
}

func refusal(code string) string {
	return `{"errors":[{"message":"refused","extensions":{"code":"` + code + `"}}],"data":null}`
}

func TestBatchesScopeRefusalSwitchesDeliveryOff(t *testing.T) {
	s, _ := deliveryFake(t, map[string][]string{"DeliveryBatches": {refusal("APP_KEY_SCOPE_REQUIRED")}})
	if _, err := s.Batches(context.Background()); !errors.Is(err, hostpoller.ErrDeliveryScope) {
		t.Fatalf("err %v, want ErrDeliveryScope", err)
	}
	for _, code := range []string{"HOST_KEY_REQUIRED", "APP_NOT_FOUND", "APP_KEY_SESSION_BOUND"} {
		s, _ := deliveryFake(t, map[string][]string{"DeliveryBatches": {refusal(code)}})
		if _, err := s.Batches(context.Background()); err == nil || errors.Is(err, hostpoller.ErrDeliveryScope) {
			t.Fatalf("%s: err %v, want a read failure, not scope-off", code, err)
		}
	}
}

func TestBatchesRefuseABrokenPage(t *testing.T) {
	badTime := strings.Replace(batchPage2, `"2026-10-19T11:59:00Z"`, `"soon"`, 1)
	stuck := strings.Replace(batchPage1, `"endCursor":"c1"`, `"endCursor":null`, 1)
	for name, page := range map[string]string{"a time that does not parse": badTime, "a next page with no cursor": stuck,
		"no page": `{"data":{"deliveryBatches":null}}`} {
		t.Run(name, func(t *testing.T) {
			s, _ := deliveryFake(t, map[string][]string{"DeliveryBatches": {page, page}})
			got, err := s.Batches(context.Background())
			if err == nil || got != nil {
				t.Fatalf("got %+v err %v, want a refused read (nothing typed)", got, err)
			}
			if strings.Contains(err.Error(), "SECRET") {
				t.Fatalf("error carries a proof: %v", err)
			}
		})
	}
}

func ackBody(acked, unchanged int) string {
	b, _ := json.Marshal(map[string]any{"data": map[string]any{"acknowledgeDeliveryBatches": map[string]int{"acknowledged": acked, "unchanged": unchanged}}})
	return string(b)
}

func TestAckSendsOneBatchAndMapsTheCounts(t *testing.T) {
	cases := []struct {
		name string
		body string
		want delivery.AckResult
		err  bool
	}{
		{"acknowledged", ackBody(1, 0), delivery.Acked, false},
		{"a repeat", ackBody(0, 1), delivery.Unchanged, false},
		{"nothing counted", ackBody(0, 0), "", true},
		{"counted twice", ackBody(1, 1), "", true},
		{"counted wrong", ackBody(2, 0), "", true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			s, calls := deliveryFake(t, map[string][]string{"AcknowledgeDeliveryBatches": {c.body}})
			got, err := s.Ack(context.Background(), "PROOF-1-SECRET", nil)
			if got != c.want || (err != nil) != c.err {
				t.Fatalf("got %q err %v", got, err)
			}
			if err != nil {
				checkClean(t, "ack", err, "")
			}
			cs := calls()
			if len(cs) != 1 || cs[0].auth != "Bearer hk_host_key" || cs[0].session != "" {
				t.Fatalf("calls %+v", cs)
			}
			if string(cs[0].vars["batchIds"]) != `["PROOF-1-SECRET"]` {
				t.Fatalf("batchIds %s", cs[0].vars["batchIds"])
			}
			if _, sent := cs[0].vars["through"]; sent {
				t.Fatal("a whole-batch ack sends no through")
			}
		})
	}
}

func TestAckSendsACoveredPrefix(t *testing.T) {
	s, calls := deliveryFake(t, map[string][]string{"AcknowledgeDeliveryBatches": {ackBody(1, 0)}})
	if _, err := s.Ack(context.Background(), "p", []delivery.Through{{ChannelID: "ch1", Seq: 7}}); err != nil {
		t.Fatal(err)
	}
	if got := string(calls()[0].vars["through"]); got != `[{"channelId":"ch1","seq":7}]` {
		t.Fatalf("through %s", got)
	}
}

func TestAckRefusalsMeanNothingMoved(t *testing.T) {
	for _, code := range []string{"BAD_USER_INPUT", "WAKE_PROOF_STALE", "WAKE_PROOF_INVALID", "WAKE_PROOF_REQUESTER_MISMATCH", "APP_KEY_SCOPE_REQUIRED"} {
		s, _ := deliveryFake(t, map[string][]string{"AcknowledgeDeliveryBatches": {refusal(code)}})
		got, err := s.Ack(context.Background(), "PROOF-SECRET", nil)
		if err == nil || got != "" {
			t.Fatalf("%s: got %q err %v, want an error and no result", code, got, err)
		}
		if strings.Contains(err.Error(), "SECRET") {
			t.Fatalf("%s: error carries the proof: %v", code, err)
		}
	}
}

func TestHoldIsUnavailableWithNoServerCall(t *testing.T) {
	s, calls := deliveryFake(t, map[string][]string{})
	err := s.Hold(context.Background(), "PROOF-SECRET", delivery.HoldDraft, "term_1")
	if !errors.Is(err, delivery.ErrHoldUnavailable) {
		t.Fatalf("err %v, want ErrHoldUnavailable", err)
	}
	if n := len(calls()); n != 0 {
		t.Fatalf("%d server calls, want none", n)
	}
}

func TestDeliveryEventLinesCarryCodesOnly(t *testing.T) {
	e := deliveryEvent(delivery.Event{Time: deliveryNow, Worker: "Iris", WorkerID: "wkr1", Span: "ch1#2:4-5", Kind: delivery.Held, Reason: delivery.HoldDraft, Terminal: "term_1", Unreported: true})
	if e.Kind != "delivery-held" || e.Outcome != "draft" {
		t.Fatalf("event %+v", e)
	}
	if e.Detail != "Iris ch1#2:4-5 in term_1, unreported: no server hold, alarm or escalation" {
		t.Fatalf("detail %q", e.Detail)
	}
	if e := deliveryEvent(delivery.Event{Worker: "Iris", Span: "ch1#2:4-5", Kind: delivery.Delivered}); strings.Contains(e.Detail, "unreported") {
		t.Fatalf("a delivery says unreported: %q", e.Detail)
	}
}

// Rodolfo's §10 P2 on #939 (issuecomment-6262918146): a server or proxy
// error may echo the request or carry message text, and delivery errors
// reach the operator's log. They carry an allowlisted code or a fixed
// class, never upstream text; scope-off is still recognised.
const leak = "PRIVATE-CHAT-TEXT hk_host_key PROOF-SECRET"

type deliveryCoded interface{ DeliveryCode() string }

func TestDeliveryErrorsCarryNoUpstreamText(t *testing.T) {
	cases := []struct {
		name, body, code string
		scope            bool
	}{
		{"a refusal echoing the request", `{"errors":[{"message":"` + leak + `","extensions":{"code":"FORBIDDEN"}}],"data":null}`, "FORBIDDEN", false},
		{"a scope refusal echoing the key", `{"errors":[{"message":"` + leak + `","extensions":{"code":"APP_KEY_SCOPE_REQUIRED"}}],"data":null}`, "APP_KEY_SCOPE_REQUIRED", true},
		{"a code not on the list", `{"errors":[{"message":"` + leak + `","extensions":{"code":"SOMETHING_NEW"}}],"data":null}`, "server-refused", false},
		{"an error with no code", `{"errors":[{"message":"` + leak + `"}],"data":null}`, "server-refused", false},
		{"a code-shaped private token as the code", `{"errors":[{"message":"x","extensions":{"code":"PRIVATE_TOKEN_42"}}],"data":null}`, "server-refused", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			s, _ := deliveryFake(t, map[string][]string{"DeliveryBatches": {c.body}, "AcknowledgeDeliveryBatches": {c.body}})
			_, err := s.Batches(context.Background())
			checkClean(t, "read", err, c.code)
			if errors.Is(err, hostpoller.ErrDeliveryScope) != c.scope {
				t.Fatalf("scope %v, want %v: %v", errors.Is(err, hostpoller.ErrDeliveryScope), c.scope, err)
			}
			_, err = s.Ack(context.Background(), "PROOF-SECRET", nil)
			checkClean(t, "ack", err, c.code)
		})
	}
}

func TestDeliveryTransportErrorsCarryNoUpstreamText(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
		_, _ = w.Write([]byte("<html>" + leak + "</html>"))
	}))
	t.Cleanup(srv.Close)
	client, err := api.NewClient(srv.URL, "hk_host_key", srv.Client())
	if err != nil {
		t.Fatal(err)
	}
	s := &hostServer{client: client, app: "a"}
	_, err = s.Batches(context.Background())
	checkClean(t, "read", err, "")
}

// checkClean: err is a delivery error with a fixed text, no upstream
// content, and (when want is set) that code.
func checkClean(t *testing.T, call string, err error, want string) {
	t.Helper()
	if err == nil {
		t.Fatalf("%s: no error", call)
	}
	for _, bad := range []string{"PRIVATE", "hk_host_key", "PROOF-SECRET", "html", "counted"} {
		if strings.Contains(err.Error(), bad) {
			t.Fatalf("%s error carries %q: %v", call, bad, err)
		}
	}
	var coded deliveryCoded
	if !errors.As(err, &coded) || coded.DeliveryCode() == "" {
		t.Fatalf("%s error declares no delivery code: %v", call, err)
	}
	if want != "" && coded.DeliveryCode() != want {
		t.Fatalf("%s code %q, want %q", call, coded.DeliveryCode(), want)
	}
}

func TestDeliveryEventsWithholdMalformedUpstreamFields(t *testing.T) {
	e := deliveryEvent(delivery.Event{Worker: "Iris", Span: "ch1#2:4-5 " + leak, Kind: delivery.Held, Reason: delivery.HoldDraft, Terminal: "term_1 " + leak})
	if strings.Contains(e.Detail, "PRIVATE") || e.Detail != "Iris withheld in withheld" {
		t.Fatalf("detail %q", e.Detail)
	}
	if e := deliveryEvent(delivery.Event{Worker: "Iris", Span: "ch1#2:4-5,ch2#1:1-1", Terminal: "term_1"}); e.Detail != "Iris ch1#2:4-5,ch2#1:1-1 in term_1" {
		t.Fatalf("plain fields are printed: %q", e.Detail)
	}
}

// Xan #24626: a code is trusted by membership in the known vocabulary,
// never by its shape. Every code the adapter can produce is known.
func TestEveryAdapterCodeIsKnown(t *testing.T) {
	for _, code := range append(append([]string{}, deliveryCodes...), "server-refused", "unauthenticated", "forbidden", "unavailable", "transport-error", "no-page", "no-cursor", "bad-deliver-after", "bad-busy-deadline", "no-result", "bad-count") {
		if !delivery.KnownCode(code) {
			t.Errorf("%s is not a known diagnostic code", code)
		}
	}
	if delivery.KnownCode("PRIVATE_TOKEN_42") {
		t.Fatal("an unknown token is known")
	}
}
