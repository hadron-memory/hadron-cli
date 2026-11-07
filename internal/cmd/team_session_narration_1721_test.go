package cmd

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/exitcode"
)

const takeover1721 = "the old session is ended without a handoff from this command; any handoff it already saved is kept"

// Synthetic requests only. Counts are synchronized and assert actual operation
// invocations, including absence of writes after a failed driver read.
func narrationServer1721(t *testing.T, responses map[string]string) (*httptest.Server, func(string) int) {
	t.Helper()
	var mu sync.Mutex
	counts := map[string]int{}
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			OperationName string `json:"operationName"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Errorf("decode request: %v", err)
			http.Error(w, "bad fixture", 400)
			return
		}
		mu.Lock()
		counts[req.OperationName]++
		mu.Unlock()
		body, ok := responses[req.OperationName]
		if !ok {
			body, ok = unstubbedDefault(req.OperationName)
		}
		if !ok {
			t.Errorf("unexpected operation %q", req.OperationName)
			body = `{"errors":[{"message":"unexpected operation"}]}`
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(s.Close)
	return s, func(op string) int { mu.Lock(); defer mu.Unlock(); return counts[op] }
}

func TestSessionNarration1721UserAndLifecycle(t *testing.T) {
	ended := strings.Replace(activeSessionJSON, `"endedAt":null`, `"endedAt":"2026-11-07T09:00:00Z"`, 1)
	for _, tc := range []struct {
		name, session, live, user, want string
		force, refuse                   bool
	}{
		{"ended", ended, "false", `{"data":{"user":{"id":"u-holger","urn":"hrn:user:fixture"}}}`, "ended 2026-11-07T09:00:00Z", false, false},
		{"live-refusal", activeSessionJSON, "true", `{"data":{"user":{"id":"u-holger","urn":"hrn:user:fixture"}}}`, "since ", false, true},
		{"live-takeover", activeSessionJSON, "true", `{"data":{"user":{"id":"u-holger","urn":"hrn:user:fixture"}}}`, "taking over worker", true, false},
		{"lookup-error", ended, "false", `{"errors":[{"message":"not readable","extensions":{"code":"FORBIDDEN"}}]}`, "user u-holger", false, false},
		{"lookup-null", ended, "false", `{"data":{"user":null}}`, "user u-holger", false, false},
		{"lookup-null-urn", ended, "false", `{"data":{"user":{"id":"u-holger","urn":null}}}`, "user u-holger", false, false},
		{"lookup-empty-urn", ended, "false", `{"data":{"user":{"id":"u-holger","urn":""}}}`, "user u-holger", false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			teamGitDir(t)
			srv, count := narrationServer1721(t, map[string]string{
				"GetWorker":        `{"data":{"worker":` + withLiveness(irisWorkerJSON, tc.live) + `}}`,
				"TeamSessionsPage": `{"data":{"sessionsPage":{"total":1,"hasMore":false,"items":[` + tc.session + `]}}}`,
				"GetUser":          tc.user, "StartTeamSession": `{"data":{"startSession":` + startedSessionJSON + `}}`,
			})
			f, _ := testFactory(t)
			stderr := captureErrOut(f)
			root := NewRootCmd(f)
			args := []string{"team", "session", "start", "--as", "wkr1", "--server", srv.URL}
			if tc.force {
				args = append(args, "--force")
			}
			root.SetArgs(args)
			err := root.Execute()
			text := stderr()
			if err != nil {
				text += "\n" + err.Error()
			}
			if tc.refuse {
				if exitCodeFor(err) != exitcode.Conflict {
					t.Fatalf("want conflict, got %v", err)
				}
			} else if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(text, tc.want) {
				t.Errorf("missing %q: %s", tc.want, text)
			}
			if strings.Contains(tc.user, "hrn:user:fixture") && !strings.Contains(text, "user hrn:user:fixture") {
				t.Errorf("readable user URN missing: %s", text)
			}
			if count("GetUser") != 1 {
				t.Errorf("user lookup count=%d, want1", count("GetUser"))
			}
			for _, bad := range []string{"someone you are not permitted to see", "could not read who is driving"} {
				if strings.Contains(text, bad) {
					t.Errorf("user decoration changed driver-read meaning: %s", text)
				}
			}
			if strings.Count(text, takeover1721) != 0 {
				t.Errorf("no explicit end was sent; no end-receipt line expected: %s", text)
			}
		})
	}
}

func TestSessionNarration1721ForceEndNeutral(t *testing.T) {
	for _, tc := range []struct {
		name, end string
		ok        bool
	}{
		{"new-end", `{"data":{"endSession":` + strings.Replace(startedSessionJSON, `"endedAt":null`, `"endedAt":"2026-11-07T09:00:00Z"`, 1) + `}}`, true},
		{"previous-end", `{"data":{"endSession":` + strings.Replace(startedSessionJSON, `"endedAt":null`, `"endedAt":"2026-11-06T09:00:00Z"`, 1) + `}}`, true},
		{"failed-end", `{"errors":[{"message":"refused","extensions":{"code":"FORBIDDEN"}}]}`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := teamGitDir(t)
			if err := os.WriteFile(filepath.Join(dir, "hadron-team-session.json"), []byte(bindingFixture), 0o600); err != nil {
				t.Fatal(err)
			}
			srv, count := narrationServer1721(t, map[string]string{
				"EndTeamSession": tc.end, "GetWorker": `{"data":{"worker":` + withLiveness(irisWorkerJSON, "false") + `}}`,
				"TeamSessionsPage": `{"data":{"sessionsPage":{"total":0,"hasMore":false,"items":[]}}}`,
				"StartTeamSession": `{"data":{"startSession":` + startedSessionJSON + `}}`,
			})
			f, _ := testFactory(t)
			stderr := captureErrOut(f)
			root := NewRootCmd(f)
			root.SetArgs([]string{"team", "session", "start", "--as", "wkr1", "--force", "--server", srv.URL})
			if err := root.Execute(); err != nil {
				t.Fatal(err)
			}
			got := stderr()
			wantCount := 0
			if tc.ok {
				wantCount = 1
			}
			for _, line := range []string{"previously bound session s-new is ended", takeover1721} {
				if strings.Count(got, line+"\n") != wantCount {
					t.Errorf("want %d exact line %q: %s", wantCount, line, got)
				}
			}
			for _, bad := range []string{"ended previously", "already ended"} {
				if strings.Contains(got, bad) {
					t.Errorf("unsupported per-call outcome %q: %s", bad, got)
				}
			}
			if count("EndTeamSession") != 1 || count("StartTeamSession") != 1 {
				t.Errorf("end/start invocation counts=%d/%d", count("EndTeamSession"), count("StartTeamSession"))
			}
		})
	}
}

func TestSessionNarration1721DriverReadCases(t *testing.T) {
	for _, tc := range []struct {
		name, live, page, want string
		refuse                 bool
	}{
		{"failed", "true", `{"errors":[{"message":"refused","extensions":{"code":"FORBIDDEN"}}]}`, "could not read who is driving", true},
		{"masked-empty", "true", `{"data":{"sessionsPage":{"total":0,"hasMore":false,"items":[]}}}`, "someone you are not permitted to see", false},
		{"ordinary-empty", "false", `{"data":{"sessionsPage":{"total":0,"hasMore":false,"items":[]}}}`, "", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			teamGitDir(t)
			srv, count := narrationServer1721(t, map[string]string{
				"GetWorker": `{"data":{"worker":` + withLiveness(irisWorkerJSON, tc.live) + `}}`, "TeamSessionsPage": tc.page,
				"StartTeamSession": `{"data":{"startSession":` + startedSessionJSON + `}}`,
			})
			f, _ := testFactory(t)
			stderr := captureErrOut(f)
			root := NewRootCmd(f)
			root.SetArgs([]string{"team", "session", "start", "--as", "wkr1", "--force", "--server", srv.URL})
			err := root.Execute()
			got := stderr()
			if err != nil {
				got += "\n" + err.Error()
			}
			if tc.refuse {
				if err == nil || count("StartTeamSession") != 0 {
					t.Fatalf("failed driver read must fail closed: %v", err)
				}
			} else if err != nil {
				t.Fatal(err)
			}
			if tc.want != "" && !strings.Contains(got, tc.want) {
				t.Errorf("missing %q: %s", tc.want, got)
			}
			if tc.name == "ordinary-empty" && (strings.Contains(got, "not permitted") || strings.Contains(got, "could not read")) {
				t.Errorf("ordinary empty was mislabeled: %s", got)
			}
			if strings.Contains(got, takeover1721) {
				t.Errorf("no successful end call, no end receipt: %s", got)
			}
			if count("GetUser") != 0 {
				t.Errorf("no driver user to decorate: %d lookups", count("GetUser"))
			}
		})
	}
}
