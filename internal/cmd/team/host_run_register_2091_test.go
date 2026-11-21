package team

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Khan/genqlient/graphql"

	"github.com/hadron-memory/hadron-cli/internal/api"
	"github.com/hadron-memory/hadron-cli/internal/exitcode"
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
		// "STATUS <code> <body>" answers with that HTTP status and a plain body
		// (a gateway/server failure, not a GraphQL answer).
		if strings.HasPrefix(resp, "STATUS ") {
			parts := strings.SplitN(resp, " ", 3)
			code, _ := strconv.Atoi(parts[1])
			w.WriteHeader(code)
			if len(parts) == 3 {
				_, _ = w.Write([]byte(parts[2]))
			}
			return
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
	// Each reg() call and the document(s) it sends, in order:
	steps := []struct {
		call string
		docs []string
	}{
		{"1: first call, no pin yet: skew, then the plain document, pinned", []string{"RegisterHostWithRefused", "RegisterHost"}},
		{"2: inside the interval: pinned, plain only", []string{"RegisterHost"}},
		{"3: interval over: asks the extended again, still old, plain again, re-pinned", []string{"RegisterHostWithRefused", "RegisterHost"}},
		{"4: same instant: the renewed pin suppresses another extended request", []string{"RegisterHost"}},
	}
	var want []string
	for _, st := range steps {
		want = append(want, st.docs...)
	}
	reg() // 1
	clock = clock.Add(refusedReprobeEvery - time.Second)
	if got := reg(); got.RefusedKnown { // 2
		t.Fatalf("inside the interval nothing is re-asked: %+v", got)
	}
	clock = clock.Add(2 * time.Second)
	reg() // 3
	reg() // 4
	if strings.Join(ops(), ",") != strings.Join(want, ",") {
		t.Fatalf("calls = %v, want %v (steps %+v)", ops(), want, steps)
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
		// Deliberately not regServer2091: the failure must come from the
		// transport itself, so the client is isolated (an unresolvable host and
		// an injected RoundTripper, no listening server and no DNS or socket)
		// and the App is explicit.
		transport := &failingRegistrationTransport2091{}
		s := &hostServer{
			client: graphql.NewClient("http://registration.invalid/graphql", &http.Client{Transport: transport}),
			app:    "hrn:app:acme.com:eng-team",
		}
		_, err := s.Register(context.Background(), hostpoller.Registration{Label: "box1", PollIntervalSeconds: 30})
		// The returned failure must be THIS transport's. The CLI maps a
		// transport error to its own diagnostic and does not wrap the cause (a
		// production choice this test does not change), so the cause is pinned
		// in two places: the mapped answer is exactly what api.MapError gives
		// for this transport error, and the transport itself returned the
		// sentinel. An unrelated later error (a "no registration", a server
		// refusal) matches neither.
		want := api.MapError(&url.Error{Op: "Post", URL: "http://registration.invalid/graphql", Err: errSyntheticTransport2091})
		if err == nil || err.Error() != want.Error() || exitcode.FromError(err) != exitcode.FromError(want) {
			t.Fatalf("the failure must be the mapped transport failure:\n got %v\nwant %v", err, want)
		}
		if !strings.Contains(err.Error(), "could not reach the server") {
			t.Fatalf("not a transport diagnostic: %v", err)
		}
		if got := transport.returned(); len(got) != 1 || !errors.Is(got[0], errSyntheticTransport2091) {
			t.Fatalf("the transport did not return the sentinel: %v", got)
		}
		if got := transport.attempts.Load(); got != 1 {
			t.Fatalf("transport attempts = %d, want 1 (no retry or fallback)", got)
		}
		// The one document sent is the extended one, never the plain one.
		sent := transport.sentDocuments()
		if len(sent) != 1 || !strings.Contains(sent[0], `"operationName":"RegisterHostWithRefused"`) || !strings.Contains(sent[0], "refusedWorkers") {
			t.Fatalf("sent documents = %q, want exactly the extended registration", sent)
		}
		// A transport failure is never a refusal and never proves the field
		// missing: nothing is pinned (cor:agt:070:01: a refusal is the
		// server's per-worker answer).
		if s.noRefusedAt.Load() != 0 {
			t.Fatalf("a transport failure pinned the plain document: noRefusedAt=%d", s.noRefusedAt.Load())
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

// cli#2104 P3 3: a transport or server failure is not schema skew. It is the
// registration's own failure: no fallback document, nothing pinned.
func TestRegisterDoesNotFallBackOnATransportOrServerFailure(t *testing.T) {
	for name, answer := range map[string]string{
		"gateway 502":     "STATUS 502 Bad Gateway",
		"server 500":      "STATUS 500 internal error",
		"unavailable 503": "STATUS 503 ",
		"non-JSON 200":    "<html>not graphql</html>",
	} {
		s, ops := regServer2091(t, map[string]string{"RegisterHostWithRefused": answer})
		if _, err := s.Register(context.Background(), hostpoller.Registration{Label: "box1", PollIntervalSeconds: 30}); err == nil {
			t.Errorf("%s: a failed registration is an error", name)
		}
		if strings.Join(ops(), ",") != "RegisterHostWithRefused" {
			t.Errorf("%s: calls = %v, want only the extended document", name, ops())
		}
		if s.noRefusedAt.Load() != 0 {
			t.Errorf("%s: a transport failure must pin nothing", name)
		}
	}
}

// Skew that names another field still takes the plain document, and a
// registration failure on THAT document is returned and pins nothing.
func TestRegisterPinsNothingWhenThePlainDocumentFailsWithAServerError(t *testing.T) {
	s, ops := regServer2091(t, map[string]string{
		"RegisterHostWithRefused": skew2091,
		"RegisterHost":            "STATUS 502 Bad Gateway",
	})
	if _, err := s.Register(context.Background(), hostpoller.Registration{Label: "box1", PollIntervalSeconds: 30}); err == nil {
		t.Fatal("a failed plain registration is an error")
	}
	if strings.Join(ops(), ",") != "RegisterHostWithRefused,RegisterHost" || s.noRefusedAt.Load() != 0 {
		t.Fatalf("calls = %v pinned=%d", ops(), s.noRefusedAt.Load())
	}
}

// errSyntheticTransport2091 is the failing transport's own cause, so a test
// can tell that error from any other.
var errSyntheticTransport2091 = errors.New("synthetic registration transport failure")

// failingRegistrationTransport2091 counts attempts and records the document of
// each at the failing boundary itself; no DNS or socket is used.
type failingRegistrationTransport2091 struct {
	attempts   atomic.Int32
	mu         sync.Mutex
	sent       []string
	failures   []error
	readErrors []error
}

func (t *failingRegistrationTransport2091) bodyReadErrors() []error {
	t.mu.Lock()
	defer t.mu.Unlock()
	return append([]error{}, t.readErrors...)
}

func (t *failingRegistrationTransport2091) returned() []error {
	t.mu.Lock()
	defer t.mu.Unlock()
	return append([]error{}, t.failures...)
}

func (t *failingRegistrationTransport2091) sentDocuments() []string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return append([]string{}, t.sent...)
}

func (t *failingRegistrationTransport2091) RoundTrip(req *http.Request) (*http.Response, error) {
	t.attempts.Add(1)
	var body []byte
	if req.Body != nil {
		body, _ = io.ReadAll(req.Body)
		// A RoundTripper must close the request body, even on failure.
		_ = req.Body.Close()
	}
	t.mu.Lock()
	t.sent = append(t.sent, string(body))
	t.failures = append(t.failures, errSyntheticTransport2091)
	t.mu.Unlock()
	return nil, errSyntheticTransport2091
}

// cli#2401: a failure to inspect the request must remain visible to the test,
// even though this synthetic transport always returns its own transport error.
func TestRegistrationTransportRecordsBodyReadError2401(t *testing.T) {
	body := &failingRegistrationBody2401{}
	transport := &failingRegistrationTransport2091{}
	req, err := http.NewRequest(http.MethodPost, "http://registration.invalid/graphql", body)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = transport.RoundTrip(req)
	reads := transport.bodyReadErrors()
	if len(reads) != 1 || !errors.Is(reads[0], errSyntheticBodyRead2401) {
		t.Fatalf("request-body read errors = %v, want the injected read failure", reads)
	}
	if !body.closed {
		t.Fatal("the failing request body was not closed")
	}
}

var errSyntheticBodyRead2401 = errors.New("synthetic request body read failure")

type failingRegistrationBody2401 struct{ closed bool }

func (*failingRegistrationBody2401) Read([]byte) (int, error) { return 0, errSyntheticBodyRead2401 }
func (b *failingRegistrationBody2401) Close() error           { b.closed = true; return nil }
