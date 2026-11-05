package cmd

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

// cli#1495: pointer-refusal classification and the lost-record answer note
// (follow-ups to #1493). A statusServer1495 answers each operation with its own
// HTTP status and body, which the plain captureGraphQL cannot (it is always 200).
func statusServer1495(t *testing.T, resp map[string]resp1495, seen func(op string)) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && r.URL.Path == "/capabilities" {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"capabilities":[]}`))
			return
		}
		var req struct {
			OperationName string `json:"operationName"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		seen(req.OperationName)
		got, ok := resp[req.OperationName]
		if !ok {
			t.Errorf("unexpected operation %q", req.OperationName)
			got.status, got.body = 200, `{"errors":[{"message":"unexpected operation"}]}`
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(got.status)
		_, _ = w.Write([]byte(got.body))
	}))
	t.Cleanup(srv.Close)
	return srv
}

type resp1495 = struct {
	status int
	body   string
}

const recorded1495 = `{"data":{"recordTeamWork":{"nodeId":"w1","sessionId":"s-new","workerId":"wkr1","workerName":"Iris",
	"tool":"github","kind":"pr","ref":"hadron-memory/hadron-cli#371","action":"worked-on",
	"at":"2026-08-13T10:00:00Z","detail":null,"model":null}}}`

func runLog1495(t *testing.T, responses map[string]resp1495) (called map[string]bool, errOut string, err error) {
	t.Helper()
	bindWorklogModelTest(t)
	called = map[string]bool{}
	var mu sync.Mutex
	srv := statusServer1495(t, responses, func(op string) {
		mu.Lock()
		called[op] = true
		mu.Unlock()
	})
	f, _, eo := testFactoryTTY(t, "")
	root := NewRootCmd(f)
	root.SetArgs([]string{"team", "session", "log", "--pr", "371", "--server", srv.URL})
	err = root.Execute()
	return called, eo.String(), err
}

// Item 1: graphQLErrors reads the envelope inside a non-200 HTTPError, so a
// JSON-bodied gateway 5xx carrying an `errors` list used to pass for a
// definite refusal and print "NOT updated". Exit codes stay as they were.
func TestSessionLogPointerClassification1495(t *testing.T) {
	for _, tc := range []struct {
		name     string
		status   int
		body     string
		wantExit int
		definite bool
	}{
		{"502 with a typed NOT_FOUND envelope", 502, `{"data":null,"errors":[{"message":"upstream said no","extensions":{"code":"NOT_FOUND"}}]}`, 4, false},
		{"504 with a typed BAD_USER_INPUT envelope", 504, `{"data":null,"errors":[{"message":"gateway timeout","extensions":{"code":"BAD_USER_INPUT"}}]}`, 2, false},
		{"429 with an untyped envelope", 429, `{"data":null,"errors":[{"message":"slow down"}]}`, 1, false},
		{"200 with an untyped envelope", 200, `{"data":null,"errors":[{"message":"weird"}]}`, 1, false},
		// Controls: a refusal the server really issued is still definite.
		{"200 with a typed refusal", 200, `{"data":null,"errors":[{"message":"session is ended","extensions":{"code":"BAD_USER_INPUT"}}]}`, 2, true},
		{"400 with a typed refusal", 400, `{"data":null,"errors":[{"message":"bad input","extensions":{"code":"BAD_USER_INPUT"}}]}`, 2, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, errOut, err := runLog1495(t, map[string]resp1495{
				"UpdateTeamSession": {tc.status, tc.body},
				"RecordTeamWork":    {200, recorded1495},
			})
			if got := exitCodeFor(err); got != tc.wantExit {
				t.Fatalf("exit = %d, want %d (err %v)", got, tc.wantExit, err)
			}
			if !strings.Contains(errOut, "worklog row WAS recorded") {
				t.Fatalf("stderr does not say the row was recorded: %q", errOut)
			}
			if tc.definite {
				if !strings.Contains(errOut, "was NOT updated") {
					t.Errorf("a definite refusal must still say NOT updated: %q", errOut)
				}
				return
			}
			if strings.Contains(errOut, "was NOT updated") || !strings.Contains(errOut, "could NOT be confirmed") {
				t.Errorf("not a proven refusal, so the note must state the uncertainty: %q", errOut)
			}
		})
	}
}

// Item 4: when the RECORD call's own answer is lost, the row may exist, so the
// note must warn against a re-run (it would duplicate the row). A refused
// record (proven: nothing written) gets no such note.
func TestSessionLogLostRecordAnswer1495(t *testing.T) {
	for _, tc := range []struct {
		name     string
		status   int
		body     string
		wantExit int
	}{
		{"truncated body", 200, `{"data":`, 7},
		{"undecodable body", 200, `not-json`, 1},
		{"empty body", 200, ``, 7},
		{"internal fault", 200, `{"data":null,"errors":[{"message":"boom","extensions":{"code":"INTERNAL_SERVER_ERROR","requestId":"req-1"}}]}`, 1},
		{"gateway 502 with a typed envelope", 502, `{"data":null,"errors":[{"message":"x","extensions":{"code":"NOT_FOUND"}}]}`, 4},
	} {
		t.Run(tc.name, func(t *testing.T) {
			called, errOut, err := runLog1495(t, map[string]resp1495{
				"UpdateTeamSession": {200, modelLogSessionJSON},
				"RecordTeamWork":    {tc.status, tc.body},
			})
			if got := exitCodeFor(err); got != tc.wantExit {
				t.Fatalf("exit = %d, want %d (err %v)", got, tc.wantExit, err)
			}
			if called["UpdateTeamSession"] {
				t.Errorf("the pointer moved although the record was not confirmed")
			}
			if !strings.Contains(errOut, "may or may not have been recorded") || !strings.Contains(errOut, "may duplicate the row") {
				t.Errorf("a lost record answer needs the do-not-rerun note: %q", errOut)
			}
		})
	}

	t.Run("a refused record carries no such note", func(t *testing.T) {
		_, errOut, err := runLog1495(t, map[string]resp1495{
			"UpdateTeamSession": {200, modelLogSessionJSON},
			"RecordTeamWork":    {200, `{"data":null,"errors":[{"message":"Team Agent cannot be inferred","extensions":{"code":"NOT_FOUND"}}]}`},
		})
		if exitCodeFor(err) != 4 {
			t.Fatalf("want exit 4, got %v", err)
		}
		if strings.Contains(errOut, "may or may not have been recorded") {
			t.Errorf("a proven refusal wrote nothing; the duplicate warning is false: %q", errOut)
		}
	})
}

// The legacy (old-server) record call is a second RECORD path: a lost answer
// there needs the same do-not-rerun note, and a refusal there still does not.
func TestSessionLogLostLegacyRecordAnswer1495(t *testing.T) {
	skew := resp1495{200, `{"errors":[{"message":"Cannot query field \"model\" on type \"TeamWorkItem\".","extensions":{"code":"GRAPHQL_VALIDATION_FAILED"}}]}`}
	for _, tc := range []struct {
		name string
		resp resp1495
		note bool
	}{
		{"truncated legacy answer", resp1495{200, `{"data":`}, true},
		{"refused legacy record", resp1495{200, `{"data":null,"errors":[{"message":"nope","extensions":{"code":"NOT_FOUND"}}]}`}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			called, errOut, err := runLog1495(t, map[string]resp1495{
				"UpdateTeamSession":    {200, modelLogSessionJSON},
				"RecordTeamWork":       skew,
				"RecordTeamWorkLegacy": tc.resp,
			})
			if err == nil || !called["RecordTeamWorkLegacy"] {
				t.Fatalf("legacy path not reached: err=%v called=%v", err, called)
			}
			if called["UpdateTeamSession"] {
				t.Errorf("the pointer moved although the record was not confirmed")
			}
			if got := strings.Contains(errOut, "may or may not have been recorded"); got != tc.note {
				t.Errorf("note present = %v, want %v: %q", got, tc.note, errOut)
			}
		})
	}
}
