package team

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/api"
	"github.com/hadron-memory/hadron-cli/internal/hostpoller"
)

func agentlessServer(t *testing.T, status int, body string, seen *map[string]any) *hostServer {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get(api.SessionHeader) != "" {
			t.Error("worker attribution leaked into a host-key mutation")
		}
		var req struct {
			OperationName string
			Variables     map[string]any
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Error(err)
		}
		if req.OperationName != "HostEndAgentlessSession" {
			t.Errorf("operation %q", req.OperationName)
		}
		if seen != nil {
			*seen = req.Variables
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	client, err := api.NewClient(srv.URL, "private-test-key", srv.Client())
	if err != nil {
		t.Fatal(err)
	}
	return &hostServer{client: client, app: "app-ref", serverURL: srv.URL}
}

func TestEndAgentlessSendsTheClosedReasonAndTheExactSession2201(t *testing.T) {
	var vars map[string]any
	s := agentlessServer(t, 200, `{"data":{"endAgentlessSession":{"ended":true,"handoff":false,"sessionId":"sess-1"}}}`, &vars)
	got, err := s.EndAgentless(api.WithSession(context.Background(), "caller-session"), hostpoller.AgentlessEnd{AppID: "app-id", WorkerID: "w-1", SessionID: "sess-1"})
	if err != nil || !got.Ended {
		t.Fatalf("%+v %v", got, err)
	}
	in, _ := vars["input"].(map[string]any)
	if in["appRef"] != "app-id" || in["workerRef"] != "w-1" || in["sessionId"] != "sess-1" || in["reason"] != "AGENT_ABSENT" {
		t.Fatalf("input = %+v", in)
	}
}

func TestEndAgentlessAlreadyEndedIsNotEnded2201(t *testing.T) {
	s := agentlessServer(t, 200, `{"data":{"endAgentlessSession":{"ended":false,"handoff":false,"sessionId":"sess-1"}}}`, nil)
	got, err := s.EndAgentless(context.Background(), hostpoller.AgentlessEnd{AppID: "a", WorkerID: "w", SessionID: "sess-1"})
	if err != nil || got.Ended {
		t.Fatalf("%+v %v", got, err)
	}
}

func TestEndAgentlessAnswerForAnotherSessionIsAnError2201(t *testing.T) {
	s := agentlessServer(t, 200, `{"data":{"endAgentlessSession":{"ended":true,"handoff":false,"sessionId":"someone-else"}}}`, nil)
	if _, err := s.EndAgentless(context.Background(), hostpoller.AgentlessEnd{AppID: "a", WorkerID: "w", SessionID: "sess-1"}); err == nil {
		t.Fatal("an end that names another session is not confirmation")
	}
}

func TestEndAgentlessRefusalCarriesItsClosedCode2201(t *testing.T) {
	for _, code := range []string{"WORKER_HAS_OPEN_TASK", "WORKER_NOT_SHORT_LIVED", "HOST_DOES_NOT_RUN_WORKER", "SESSION_NOT_WORKERS"} {
		s := agentlessServer(t, 200, `{"data":null,"errors":[{"message":"no","extensions":{"code":"`+code+`"}}]}`, nil)
		_, err := s.EndAgentless(context.Background(), hostpoller.AgentlessEnd{AppID: "a", WorkerID: "w", SessionID: "s"})
		var refused *hostpoller.AgentlessRefusal
		if !errors.As(err, &refused) || refused.Code != code {
			t.Errorf("%s: %v", code, err)
		}
	}
}

func TestEndAgentlessAGatewayErrorIsNotAProvenRefusal2201(t *testing.T) {
	s := agentlessServer(t, 502, `{"data":null,"errors":[{"message":"bad gateway","extensions":{"code":"WORKER_HAS_OPEN_TASK"}}]}`, nil)
	_, err := s.EndAgentless(context.Background(), hostpoller.AgentlessEnd{AppID: "a", WorkerID: "w", SessionID: "s"})
	var refused *hostpoller.AgentlessRefusal
	if err == nil || errors.As(err, &refused) {
		t.Fatalf("a 5xx envelope may follow a committed write, so it is not a refusal: %v", err)
	}
}

func TestEndAgentlessOnAServerWithoutTheMutationIsUnsupportedNotFailed2201(t *testing.T) {
	s := agentlessServer(t, 400, `{"errors":[{"message":"Cannot query field \"endAgentlessSession\" on type \"Mutation\".","extensions":{"code":"GRAPHQL_VALIDATION_FAILED"}}]}`, nil)
	_, err := s.EndAgentless(context.Background(), hostpoller.AgentlessEnd{AppID: "a", WorkerID: "w", SessionID: "s"})
	var refused *hostpoller.AgentlessRefusal
	if !errors.As(err, &refused) || refused.Code != hostpoller.AgentlessUnsupported {
		t.Fatalf("an older server without S15 is a standing no, not a failure each pass: %v", err)
	}
}

// A success envelope with no mutation result is an ordinary error, never a nil
// dereference (#2207 BLIND F1).
func TestEndAgentlessMissingResultIsAnError2201(t *testing.T) {
	for name, body := range map[string]string{
		"empty data":  `{"data":{}}`,
		"null result": `{"data":{"endAgentlessSession":null}}`,
		"null data":   `{"data":null}`,
	} {
		t.Run(name, func(t *testing.T) {
			s := agentlessServer(t, 200, body, nil)
			if _, err := s.EndAgentless(context.Background(), hostpoller.AgentlessEnd{AppID: "a", WorkerID: "w", SessionID: "s"}); err == nil {
				t.Fatal("no result is not confirmation")
			}
		})
	}
}

// Only the server's four documented no-write refusals, and only when they are
// the WHOLE envelope, are standing refusals; an internal error, an unknown code
// or a mix that hides one beside a refusal stays an error, so it is FAILED and
// visible (#2207 LOCAL F3).
func TestEndAgentlessOnlyTheKnownRefusalsAreRefusals2201(t *testing.T) {
	for name, body := range map[string]string{
		"internal error":           `{"data":null,"errors":[{"message":"boom","extensions":{"code":"INTERNAL_SERVER_ERROR"}}]}`,
		"an unknown code":          `{"data":null,"errors":[{"message":"no","extensions":{"code":"FORBIDDEN"}}]}`,
		"refusal beside internal":  `{"data":null,"errors":[{"message":"no","extensions":{"code":"WORKER_HAS_OPEN_TASK"}},{"message":"boom","extensions":{"code":"INTERNAL_SERVER_ERROR"}}]}`,
		"internal beside refusal":  `{"data":null,"errors":[{"message":"boom","extensions":{"code":"INTERNAL_SERVER_ERROR"}},{"message":"no","extensions":{"code":"WORKER_HAS_OPEN_TASK"}}]}`,
		"refusal beside code-less": `{"data":null,"errors":[{"message":"no","extensions":{"code":"WORKER_HAS_OPEN_TASK"}},{"message":"plain"}]}`,
	} {
		t.Run(name, func(t *testing.T) {
			s := agentlessServer(t, 200, body, nil)
			_, err := s.EndAgentless(context.Background(), hostpoller.AgentlessEnd{AppID: "a", WorkerID: "w", SessionID: "s"})
			var refused *hostpoller.AgentlessRefusal
			if err == nil || errors.As(err, &refused) {
				t.Fatalf("not a standing refusal: %v", err)
			}
		})
	}
}
