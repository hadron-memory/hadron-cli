package cmd

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/cmd/team"
)

type supersededResult1845 struct {
	stdout, stderr string
	exit           int
	calls          []string
}

func runSuperseded1845(t *testing.T, status int, ext map[string]any, jsonMode bool) supersededResult1845 {
	t.Helper()
	writeTeamBinding(t)
	body, _ := json.Marshal(map[string]any{"errors": []any{map[string]any{"message": "Session superseded.", "extensions": ext}}})
	calls := []string{}
	srv := statusServer1495(t, map[string]resp1495{"GetWorker": {status, string(body)}}, func(op string) { calls = append(calls, op) })
	setTeamBindingServer(t, srv.URL)
	path := filepath.Join(os.Getenv(team.GitDirEnv), "hadron-team-session.json")
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	f, out := testFactory(t)
	var stderr bytes.Buffer
	f.IOStreams.ErrOut = &stderr
	root := NewRootCmd(f)
	args := []string{"team", "worker", "get", "hrn:worker:acme.com:eng-team:iris", "--server", srv.URL}
	if jsonMode {
		args = append(args, "--json")
	}
	root.SetArgs(args)
	err = root.Execute()
	if err == nil {
		t.Fatal("refusal became success")
	}
	code := renderFailure(f, args, err)
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) {
		t.Fatal("binding mutated")
	}
	if len(calls) != 1 || calls[0] != "GetWorker" {
		t.Fatalf("retry/successor fetch: %v", calls)
	}
	return supersededResult1845{out.String(), stderr.String(), code, calls}
}

func TestSupersededCurrentEvidence1845(t *testing.T) {
	const stamp = "2026-11-12T16:37:52.123+01:00"
	for _, tc := range []struct {
		name  string
		ext   map[string]any
		valid bool
	}{
		{"readable-current-C", map[string]any{"sessionId": "session-C", "startedAt": stamp, "supersededBySessionId": "historical-B", "secret": "not-allowed"}, true},
		{"omitted", map[string]any{}, false},
		{"no-open-session", map[string]any{}, false},
		{"partial-id", map[string]any{"sessionId": "session-C"}, false},
		{"partial-time", map[string]any{"startedAt": stamp}, false},
		{"wrong-id-type", map[string]any{"sessionId": 123, "startedAt": stamp}, false},
		{"wrong-time-type", map[string]any{"sessionId": "session-C", "startedAt": 123}, false},
		{"bad-time", map[string]any{"sessionId": "session-C", "startedAt": "not a timestamp"}, false},
		{"empty-id", map[string]any{"sessionId": "", "startedAt": stamp}, false},
	} {
		for _, jsonMode := range []bool{false, true} {
			t.Run(tc.name+map[bool]string{false: "/text", true: "/json"}[jsonMode], func(t *testing.T) {
				ext := map[string]any{"code": "SESSION_SUPERSEDED"}
				for k, v := range tc.ext {
					ext[k] = v
				}
				r := runSuperseded1845(t, 409, ext, jsonMode)
				if r.exit != 1 {
					t.Fatalf("existing unknown-code numeric exit changed: %+v", r)
				}
				if jsonMode {
					var doc struct {
						Error map[string]any `json:"error"`
					}
					if err := json.Unmarshal([]byte(r.stdout), &doc); err != nil {
						t.Fatal(err)
					}
					if doc.Error["serverCode"] != "SESSION_SUPERSEDED" || doc.Error["message"] != "Session superseded." {
						t.Fatalf("existing envelope not retained: %s", r.stdout)
					}
					_, id := doc.Error["sessionId"]
					_, started := doc.Error["startedAt"]
					if id != tc.valid || started != tc.valid {
						t.Fatalf("partial/wrong projection: %s", r.stdout)
					}
					if tc.valid && (doc.Error["sessionId"] != "session-C" || doc.Error["startedAt"] != stamp) {
						t.Fatalf("raw pair changed: %s", r.stdout)
					}
					for _, key := range []string{"secret", "supersededBySessionId", "extensions"} {
						if _, ok := doc.Error[key]; ok {
							t.Fatalf("extension leaked: %s", r.stdout)
						}
					}
				} else {
					want := "hadron: Session superseded. No current-session details are available. No retry or rebind was attempted.\n"
					if tc.valid {
						want = "hadron: Session superseded. Current open session: session-C (started " + stamp + "). No retry or rebind was attempted.\n"
					}
					if r.stderr != want {
						t.Fatalf("hint got %q want %q", r.stderr, want)
					}
				}
			})
		}
	}
}

func TestSupersededUncertainAndOtherCodes1845(t *testing.T) {
	for _, status := range []int{502, 503} {
		for _, jsonMode := range []bool{false, true} {
			r := runSuperseded1845(t, status, map[string]any{"code": "SESSION_SUPERSEDED", "sessionId": "session-C", "startedAt": "2026-11-12T16:37:52Z"}, jsonMode)
			all := r.stdout + r.stderr
			if r.exit != 1 || strings.Contains(all, "session-C") || strings.Contains(all, "No retry or rebind") || strings.Contains(all, "Current open session:") || strings.Contains(all, "\"startedAt\"") {
				t.Fatalf("5xx became definitive: %+v", r)
			}
		}
	}
	for _, tc := range []struct {
		code string
		exit int
	}{{"SESSION_ENDED", 5}, {"SESSION_EXPIRED", 3}} {
		r := runSuperseded1845(t, 409, map[string]any{"code": tc.code, "sessionId": "session-C", "startedAt": "2026-11-12T16:37:52Z"}, true)
		if r.exit != tc.exit || strings.Contains(r.stdout, "session-C") || strings.Contains(r.stdout, "\"startedAt\"") {
			t.Fatalf("ordinary ended/expired changed: %+v", r)
		}
	}
}
