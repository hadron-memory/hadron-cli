package team

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Khan/genqlient/graphql"

	"github.com/hadron-memory/hadron-cli/internal/hostpoller"
)

// resetWire is a fake server for the six reset steps: it records each
// request's operation name and input, and answers with body.
type resetWire struct {
	op    string
	input map[string]any
	body  string
}

func newResetWire(t *testing.T, w *resetWire) *hostServer {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		var q struct {
			OperationName string                    `json:"operationName"`
			Variables     map[string]map[string]any `json:"variables"`
		}
		if err := json.NewDecoder(r.Body).Decode(&q); err != nil {
			t.Error(err)
		}
		w.op, w.input = q.OperationName, q.Variables["input"]
		rw.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(rw, w.body)
	}))
	t.Cleanup(srv.Close)
	return &hostServer{client: graphql.NewClient(srv.URL, srv.Client()), app: "hrn:app:acme.com:team"}
}

const resetStepOK = `{"refused":null,"replayed":true,"reset":{"id":"r1","epoch":3,"phase":"stop-granted","reason":null,"workerId":"w1","sessionId":"s1","taskId":"t1","stopGrantedAt":"2026-11-02T00:00:00Z","stopLeaseMs":15000}}`

func TestHostResetStepsSendTypedInputs1043(t *testing.T) {
	proc := hostpoller.ProcessIdentity{PID: 4242, StartTime: "987654"}
	cases := []struct {
		name, op, field string
		call            func(*hostServer) (hostpoller.ResetStep, error)
		want            map[string]any
	}{
		{"claim", "ClaimContextReset", "claimContextReset",
			func(s *hostServer) (hostpoller.ResetStep, error) {
				return s.ClaimReset(context.Background(), "r1", 0, proc)
			},
			map[string]any{"resetId": "r1", "epoch": 0.0, "process": map[string]any{"pid": 4242.0, "startTime": "987654"}}},
		{"recovery", "RegisterContextResetRecovery", "registerContextResetRecovery",
			func(s *hostServer) (hostpoller.ResetStep, error) {
				return s.RegisterResetRecovery(context.Background(), "r1", 3, "hrn:node:acme.com:w:sheds:x", "s1", "t1")
			},
			map[string]any{"resetId": "r1", "epoch": 3.0, "checkpointNode": "hrn:node:acme.com:w:sheds:x", "sessionId": "s1", "taskId": "t1"}},
		{"authorize", "AuthorizeContextResetStop", "authorizeContextResetStop",
			func(s *hostServer) (hostpoller.ResetStep, error) {
				return s.AuthorizeResetStop(context.Background(), "r1", 3, proc)
			},
			map[string]any{"resetId": "r1", "epoch": 3.0, "process": map[string]any{"pid": 4242.0, "startTime": "987654"}}},
		{"stopped", "ReportContextResetAgentStopped", "reportContextResetAgentStopped",
			func(s *hostServer) (hostpoller.ResetStep, error) {
				return s.ReportResetAgentStopped(context.Background(), "r1", 3, 0)
			},
			map[string]any{"resetId": "r1", "epoch": 3.0}},
		{"relaunched", "ReportContextResetRelaunched", "reportContextResetRelaunched",
			func(s *hostServer) (hostpoller.ResetStep, error) {
				return s.ReportResetRelaunched(context.Background(), "r1", 3, proc)
			},
			map[string]any{"resetId": "r1", "epoch": 3.0, "process": map[string]any{"pid": 4242.0, "startTime": "987654"}}},
		{"relaunch-failed", "ReportContextResetRelaunchFailed", "reportContextResetRelaunchFailed",
			func(s *hostServer) (hostpoller.ResetStep, error) {
				return s.ReportResetRelaunchFailed(context.Background(), "r1", 3, "STAGING_FAILED")
			},
			map[string]any{"resetId": "r1", "epoch": 3.0, "reason": "STAGING_FAILED"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			w := &resetWire{body: `{"data":{"` + c.field + `":` + resetStepOK + `}}`}
			got, err := c.call(newResetWire(t, w))
			if err != nil {
				t.Fatal(err)
			}
			if w.op != c.op {
				t.Fatalf("operation %q, want %q", w.op, c.op)
			}
			c.want["appRef"] = "hrn:app:acme.com:team"
			gotIn, _ := json.Marshal(w.input)
			wantIn, _ := json.Marshal(c.want)
			if string(gotIn) != string(wantIn) {
				t.Fatalf("input %s, want %s", gotIn, wantIn)
			}
			want := hostpoller.ResetStep{Replayed: true, Reset: hostpoller.ContextReset{
				ID: "r1", Epoch: 3, Phase: "stop-granted", WorkerID: "w1", SessionID: "s1", TaskID: "t1", StopGranted: true, StopLeaseMs: 15000,
			}}
			if got != want {
				t.Fatalf("step %+v, want %+v", got, want)
			}
		})
	}
}

// TestHostResetStoppedSendsLateStopMs1043 pins the #3255 field: a stop that
// landed after its lease is sent as whole milliseconds rounded UP (a late
// stop is never reported as on time), clamped to the server's 86400000
// bound; an on-time stop omits the key (no late-stop event).
func TestHostResetStoppedSendsLateStopMs1043(t *testing.T) {
	for _, c := range []struct {
		name string
		late time.Duration
		want any // nil = key omitted
	}{
		{"on time", 0, nil},
		{"negative", -time.Second, nil},
		{"one nanosecond", time.Nanosecond, 1.0},
		{"whole ms", 250 * time.Millisecond, 250.0},
		{"rounds up", 250*time.Millisecond + time.Microsecond, 251.0},
		{"at the bound", 24 * time.Hour, 86400000.0},
		{"past the bound", 25 * time.Hour, 86400000.0},
	} {
		t.Run(c.name, func(t *testing.T) {
			w := &resetWire{body: `{"data":{"reportContextResetAgentStopped":` + resetStepOK + `}}`}
			if _, err := newResetWire(t, w).ReportResetAgentStopped(context.Background(), "r1", 3, c.late); err != nil {
				t.Fatal(err)
			}
			in := w.input
			got, present := in["lateStopMs"]
			if c.want == nil {
				if present {
					t.Fatalf("on-time stop must omit lateStopMs, sent %v", in)
				}
				return
			}
			if got != c.want {
				t.Fatalf("lateStopMs %v, want %v", got, c.want)
			}
		})
	}
}

func TestHostResetStepRefusalIsReadNotThrown1043(t *testing.T) {
	w := &resetWire{body: `{"data":{"claimContextReset":{"refused":"PAUSED","replayed":false,"reset":{"id":"r1","epoch":1,"phase":"refused","reason":"PAUSED","workerId":"w1","sessionId":"s1","taskId":"t1","stopGrantedAt":null,"stopLeaseMs":null}}}}`}
	got, err := newResetWire(t, w).ClaimReset(context.Background(), "r1", 0, hostpoller.ProcessIdentity{PID: 1, StartTime: "1"})
	if err != nil {
		t.Fatal(err)
	}
	if got.Refused != "PAUSED" || got.Reset.Reason != "PAUSED" || got.Reset.StopGranted || got.Reset.StopLeaseMs != 0 {
		t.Fatalf("step %+v", got)
	}
}

func TestHostResetStepCodesAreTyped1043(t *testing.T) {
	for _, code := range append(append([]string{}, hostpoller.ResetStepCodes...), "FORBIDDEN") {
		t.Run(code, func(t *testing.T) {
			w := &resetWire{body: `{"errors":[{"message":"controlled","extensions":{"code":"` + code + `"}}],"data":null}`}
			_, err := newResetWire(t, w).ReportResetAgentStopped(context.Background(), "r1", 3, 0)
			if err == nil {
				t.Fatal("no error")
			}
			want := code
			if code == "FORBIDDEN" {
				want = ""
			}
			if got := hostpoller.ResetStepCode(err); got != want {
				t.Fatalf("code %q, want %q (%v)", got, want, err)
			}
		})
	}
}

func TestHostResetStepWithoutAnswerIsAnError1043(t *testing.T) {
	for name, body := range map[string]string{
		"null step":  `{"data":{"claimContextReset":null}}`,
		"null reset": `{"data":{"claimContextReset":{"refused":null,"replayed":false,"reset":null}}}`,
	} {
		w := &resetWire{body: body}
		if _, err := newResetWire(t, w).ClaimReset(context.Background(), "r1", 0, hostpoller.ProcessIdentity{}); err == nil {
			t.Fatalf("%s was accepted", name)
		}
	}
}
