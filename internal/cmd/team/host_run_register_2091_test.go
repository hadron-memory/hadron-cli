package team

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Khan/genqlient/graphql"

	"github.com/hadron-memory/hadron-cli/internal/hostpoller"
)

// hadron-cli#2091: the registration asks for refusedWorkers in a separate
// document and falls back to the plain one once, on schema skew only, and
// remembers it (Wolfgang, #108251: no poller may break against an old server).

const regAnswer2091 = `"hostId":"h1","label":"box1","pollIntervalSeconds":30,"registeredAt":"2026-10-14T12:00:00Z","refreshedAt":"2026-10-14T12:00:00Z","live":true,"workers":[]`

const skew2091 = `{"errors":[{"message":"Cannot query field \"refusedWorkers\" on type \"HostRegistration\".","extensions":{"code":"GRAPHQL_VALIDATION_FAILED"}}]}`

func regServer2091(t *testing.T, answers map[string]string) (*hostServer, func() []string) {
	t.Helper()
	var mu sync.Mutex
	var ops []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			OperationName string `json:"operationName"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		mu.Lock()
		ops = append(ops, body.OperationName)
		resp, ok := answers[body.OperationName]
		mu.Unlock()
		if !ok {
			t.Errorf("unexpected operation %q", body.OperationName)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(resp))
	}))
	t.Cleanup(srv.Close)
	s := &hostServer{client: graphql.NewClient(srv.URL, srv.Client()), app: "hrn:app:acme.com:eng-team"}
	return s, func() []string { mu.Lock(); defer mu.Unlock(); return append([]string{}, ops...) }
}

func TestRegisterFallsBackOnceOnSchemaSkewAndRemembersIt(t *testing.T) {
	s, ops := regServer2091(t, map[string]string{
		"RegisterHostWithRefused": skew2091,
		"RegisterHost":            `{"data":{"registerHost":{` + regAnswer2091 + `}}}`,
	})
	for i := 0; i < 3; i++ {
		got, err := s.Register(context.Background(), hostpoller.Registration{Label: "box1", PollIntervalSeconds: 30})
		if err != nil {
			t.Fatalf("poll %d: %v", i+1, err)
		}
		if got.HostID != "h1" || got.RefusedKnown || len(got.Refused) != 0 {
			t.Fatalf("poll %d: an old server's refusals are unknown, not empty: %+v", i+1, got)
		}
	}
	want := "RegisterHostWithRefused,RegisterHost,RegisterHost,RegisterHost"
	if strings.Join(ops(), ",") != want {
		t.Fatalf("calls = %v, want %s (the extended document is tried once)", ops(), want)
	}
}

func TestRegisterUsesTheExtendedAnswerWhenTheServerHasTheField(t *testing.T) {
	s, ops := regServer2091(t, map[string]string{
		"RegisterHostWithRefused": `{"data":{"registerHost":{` + regAnswer2091 + `,"refusedWorkers":[{"worker":"Synthetic","code":"HOST_WORKER_NOT_ASSIGNED"}]}}}`,
	})
	for i := 0; i < 2; i++ {
		got, err := s.Register(context.Background(), hostpoller.Registration{Label: "box1", PollIntervalSeconds: 30})
		if err != nil || !got.RefusedKnown || len(got.Refused) != 1 || got.Refused[0].Worker != "Synthetic" {
			t.Fatalf("got %+v, %v", got, err)
		}
	}
	if strings.Join(ops(), ",") != "RegisterHostWithRefused,RegisterHostWithRefused" {
		t.Fatalf("calls = %v: a capable server is never asked the plain document", ops())
	}
}

// Only schema skew falls back; any other refusal is the registration's own
// failure and is not asked again as a different document.
func TestRegisterDoesNotFallBackOnAnyOtherRefusal(t *testing.T) {
	s, ops := regServer2091(t, map[string]string{
		"RegisterHostWithRefused": `{"errors":[{"message":"bad registration","extensions":{"code":"BAD_HOST_REGISTRATION"}}]}`,
	})
	if _, err := s.Register(context.Background(), hostpoller.Registration{Label: "box1", PollIntervalSeconds: 30}); err == nil {
		t.Fatal("a refused registration is an error")
	}
	if strings.Join(ops(), ",") != "RegisterHostWithRefused" {
		t.Fatalf("calls = %v", ops())
	}
}

// Skew on both documents is not proof the field is missing: nothing is pinned.
func TestRegisterPinsNothingWhenThePlainDocumentFailsToo(t *testing.T) {
	s, ops := regServer2091(t, map[string]string{
		"RegisterHostWithRefused": skew2091,
		"RegisterHost":            skew2091,
	})
	for i := 0; i < 2; i++ {
		if _, err := s.Register(context.Background(), hostpoller.Registration{Label: "box1", PollIntervalSeconds: 30}); err == nil {
			t.Fatal("both refused: an error")
		}
	}
	if strings.Join(ops(), ",") != "RegisterHostWithRefused,RegisterHost,RegisterHostWithRefused,RegisterHost" {
		t.Fatalf("calls = %v", ops())
	}
}

// cli#2100: the pin is not forever. A host pinned to the plain registration
// asks the extended document again after refusedReprobeEvery, so a server
// upgraded while the host runs is noticed without a restart.
func TestRegisterReprobesAPinnedServerAfterTheInterval(t *testing.T) {
	answers := map[string]string{
		"RegisterHostWithRefused": skew2091,
		"RegisterHost":            `{"data":{"registerHost":{` + regAnswer2091 + `}}}`,
	}
	s, ops := regServer2091(t, answers)
	clock := time.Date(2026, 11, 15, 12, 0, 0, 0, time.UTC)
	s.now = func() time.Time { return clock }
	reg := func() hostpoller.RegisteredHost {
		got, err := s.Register(context.Background(), hostpoller.Registration{Label: "box1", PollIntervalSeconds: 30})
		if err != nil {
			t.Fatal(err)
		}
		return got
	}
	reg() // skew, plain, pinned
	clock = clock.Add(refusedReprobeEvery - time.Second)
	if got := reg(); got.RefusedKnown { // still pinned: plain only
		t.Fatalf("inside the interval nothing is re-asked: %+v", got)
	}
	clock = clock.Add(2 * time.Second)
	reg() // interval over: asks again, still old, plain again, re-pinned
	want := "RegisterHostWithRefused,RegisterHost,RegisterHost,RegisterHostWithRefused,RegisterHost"
	if strings.Join(ops(), ",") != want {
		t.Fatalf("calls = %v, want %s", ops(), want)
	}
	// The server is upgraded; the next re-probe finds the field and unpins.
	answers["RegisterHostWithRefused"] = `{"data":{"registerHost":{` + regAnswer2091 + `,"refusedWorkers":[{"worker":"Synthetic","code":"HOST_WORKER_NOT_ASSIGNED"}]}}}`
	clock = clock.Add(refusedReprobeEvery)
	got := reg()
	if !got.RefusedKnown || len(got.Refused) != 1 {
		t.Fatalf("an upgraded server's refusals are read again: %+v", got)
	}
	reg()
	if tail := ops()[len(ops())-2:]; strings.Join(tail, ",") != "RegisterHostWithRefused,RegisterHostWithRefused" {
		t.Fatalf("once found, the extended document is the one used: %v", ops())
	}
}

// Ordinary failures never trigger a second document, and a fallback that did
// not succeed pins nothing (Jamir, #2093).
func TestRegisterFallbackEdgeCases(t *testing.T) {
	t.Run("null registration answer is an error with no second document", func(t *testing.T) {
		s, ops := regServer2091(t, map[string]string{"RegisterHostWithRefused": `{"data":{"registerHost":null}}`})
		if _, err := s.Register(context.Background(), hostpoller.Registration{Label: "box1", PollIntervalSeconds: 30}); err == nil {
			t.Fatal("a null registration is an error")
		}
		if strings.Join(ops(), ",") != "RegisterHostWithRefused" {
			t.Fatalf("calls = %v", ops())
		}
	})
	t.Run("transport failure is an error with no second document", func(t *testing.T) {
		s, ops := regServer2091(t, map[string]string{})
		s.client = graphql.NewClient("http://127.0.0.1:1/graphql", http.DefaultClient)
		if _, err := s.Register(context.Background(), hostpoller.Registration{Label: "box1", PollIntervalSeconds: 30}); err == nil {
			t.Fatal("an unreachable server is an error")
		}
		if len(ops()) != 0 {
			t.Fatalf("the failing server saw %v", ops())
		}
	})
	t.Run("plain failing after skew pins nothing, then recovery pins", func(t *testing.T) {
		answers := map[string]string{
			"RegisterHostWithRefused": skew2091,
			"RegisterHost":            `{"errors":[{"message":"boom","extensions":{"code":"INTERNAL_SERVER_ERROR"}}]}`,
		}
		s, ops := regServer2091(t, answers)
		if _, err := s.Register(context.Background(), hostpoller.Registration{Label: "box1", PollIntervalSeconds: 30}); err == nil {
			t.Fatal("the plain registration failed: an error")
		}
		answers["RegisterHost"] = `{"data":{"registerHost":{` + regAnswer2091 + `}}}`
		for i := 0; i < 2; i++ {
			if _, err := s.Register(context.Background(), hostpoller.Registration{Label: "box1", PollIntervalSeconds: 30}); err != nil {
				t.Fatal(err)
			}
		}
		want := "RegisterHostWithRefused,RegisterHost,RegisterHostWithRefused,RegisterHost,RegisterHost"
		if strings.Join(ops(), ",") != want {
			t.Fatalf("calls = %v, want %s (nothing pinned by the failed fallback; pinned after the successful one)", ops(), want)
		}
	})
}
