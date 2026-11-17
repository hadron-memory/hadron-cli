package cmd

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vektah/gqlparser/v2/gqlerror"

	"github.com/hadron-memory/hadron-cli/internal/api"
	"github.com/hadron-memory/hadron-cli/internal/cmd/team"
)

type supersededResult1845 struct {
	stdout, stderr string
	exit           int
	calls          []string
}

func runSuperseded1845(t *testing.T, status int, ext map[string]any, jsonMode bool) supersededResult1845 {
	t.Helper()
	message := "Session superseded."
	if ext["code"] == "SESSION_ENDED" {
		message = "session ended"
	}
	if ext["code"] == "SESSION_EXPIRED" {
		message = "session expired"
	}
	body, _ := json.Marshal(map[string]any{"errors": []any{map[string]any{"message": message, "extensions": ext}}})
	return runSupersededBody1845(t, status, string(body), jsonMode)
}

func runSupersededBody1845(t *testing.T, status int, body string, jsonMode bool) supersededResult1845 {
	t.Helper()
	writeTeamBinding(t)
	calls := []string{}
	srv := statusGraphQLServer(t, map[string]statusGraphQLResponse{"GetWorker": {status, body}}, func(op string) { calls = append(calls, op) })
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
		expected := "session ended"
		if tc.code == "SESSION_EXPIRED" {
			expected = "session expired"
		}
		human := runSuperseded1845(t, 409, map[string]any{"code": tc.code, "sessionId": "session-C", "startedAt": "2026-11-12T16:37:52Z"}, false)
		if human.exit != tc.exit || human.stderr != "hadron: "+expected+"\n" {
			t.Fatalf("ordinary human answer changed: %+v", human)
		}

	}
}

func TestSupersededEscapesAndRawEvidence1845(t *testing.T) {
	const id = "session-C\n\x1b[31m\u2028tail"
	const stamp = "2026-11-12T16:37:52Z"
	for _, status := range []int{200, 409} {
		machine := runSuperseded1845(t, status, map[string]any{"code": "SESSION_SUPERSEDED", "sessionId": id, "startedAt": stamp}, true)
		var doc struct {
			Error map[string]any `json:"error"`
		}
		if err := json.Unmarshal([]byte(machine.stdout), &doc); err != nil {
			t.Fatal(err)
		}
		if doc.Error["sessionId"] != id || doc.Error["startedAt"] != stamp {
			t.Fatalf("JSON escaped pair not lossless: %s", machine.stdout)
		}
		human := runSuperseded1845(t, status, map[string]any{"code": "SESSION_SUPERSEDED", "sessionId": id, "startedAt": stamp}, false)
		if strings.Count(human.stderr, "\n") != 1 || strings.ContainsAny(human.stderr, "\x1b\u2028") || !strings.Contains(human.stderr, "No retry or rebind was attempted.") {
			t.Fatalf("unsafe human hint: %q", human.stderr)
		}
	}
}

func TestSupersededFaultCodePrecedence1845(t *testing.T) {
	for _, status := range []int{502, 503} {
		for _, reverse := range []bool{false, true} {
			t.Run(jsonInt(status)+map[bool]string{false: "/superseded-first", true: "/fault-first"}[reverse], func(t *testing.T) {
				superseded := map[string]any{"message": "Session superseded.", "extensions": map[string]any{"code": "SESSION_SUPERSEDED", "sessionId": "C", "startedAt": "2026-11-12T16:37:52Z"}}
				fault := map[string]any{"message": "private fault detail", "extensions": map[string]any{"code": "INTERNAL_SERVER_ERROR", "requestId": "req-1845"}}
				errors := []any{superseded, fault}
				if reverse {
					errors = []any{fault, superseded}
				}
				body, _ := json.Marshal(map[string]any{"errors": errors})
				r := runSupersededBody1845(t, status, string(body), true)
				var doc struct {
					Error map[string]any `json:"error"`
				}
				if err := json.Unmarshal([]byte(r.stdout), &doc); err != nil {
					t.Fatal(err)
				}
				if r.exit != 1 || doc.Error["serverCode"] != "SESSION_SUPERSEDED" || doc.Error["requestId"] != "req-1845" || doc.Error["message"] != api.InternalFaultMessage("req-1845") {
					t.Fatalf("specific marker must win, fault message/id/exit stay: %+v", r)
				}
				for _, key := range []string{"sessionId", "startedAt"} {
					if _, ok := doc.Error[key]; ok {
						t.Fatalf("mixed 5xx projected evidence: %s", r.stdout)
					}
				}
				human := runSupersededBody1845(t, status, string(body), false)
				if human.stderr != "hadron: "+api.InternalFaultMessage("req-1845")+"\n" || human.exit != 1 {
					t.Fatalf("mixed 5xx hint changed: %+v", human)
				}
			})
		}
	}
	// A single typed gateway error also retains the marker deterministically.
	for _, status := range []int{502, 503} {
		r := runSuperseded1845(t, status, map[string]any{"code": "SESSION_SUPERSEDED"}, true)
		var doc struct {
			Error map[string]any `json:"error"`
		}
		if err := json.Unmarshal([]byte(r.stdout), &doc); err != nil {
			t.Fatal(err)
		}
		if doc.Error["serverCode"] != "SESSION_SUPERSEDED" {
			t.Fatalf("typed gateway marker missing: %s", r.stdout)
		}
	}
}

// Clone details are computed first, then replaced by the definitive superseded
// hint. Pin the rendering order without changing the wrapped JSON evidence.
func TestSupersededCloneWrapperPrecedence2251(t *testing.T) {
	for _, jsonMode := range []bool{false, true} {
		f, out := testFactory(t)
		var stderr bytes.Buffer
		f.IOStreams.ErrOut = &stderr
		f.JSON = jsonMode
		err := api.MapCloneAgentError(&gqlerror.Error{Message: "Session superseded.", Extensions: map[string]any{"code": "SESSION_SUPERSEDED", "sessionId": "A", "startedAt": "2026-11-12T16:37:52Z", "reason": "CLONE_DETAIL"}})
		if api.CloneAgentDetailText(err) == "" {
			t.Fatal("fixture must actually carry clone detail text")
		}
		if code := renderError(f, err); code != 1 {
			t.Fatalf("exit = %d, want 1", code)
		}
		if jsonMode {
			var doc struct {
				Error map[string]any `json:"error"`
			}
			if err := json.Unmarshal([]byte(out.String()), &doc); err != nil {
				t.Fatal(err)
			}
			if doc.Error["serverCode"] != "SESSION_SUPERSEDED" || doc.Error["sessionId"] != "A" || doc.Error["startedAt"] != "2026-11-12T16:37:52Z" {
				t.Fatalf("JSON evidence changed: %s", out.String())
			}
		} else if want := "hadron: Session superseded. Current open session: A (started 2026-11-12T16:37:52Z). No retry or rebind was attempted.\n"; stderr.String() != want {
			t.Fatalf("human answer = %q, want %q", stderr.String(), want)
		}
	}
}

// Stable envelopes cannot activate both human overrides: authored text needs
// an authoring code, while SupersededSessionEvidence requires only superseded
// codes. Reordering the overrides is equivalent for these inputs (Wolfgang
// ruling #113282). This mixed-code control pins the actual boundary instead.
func TestSupersededMixedAuthoredControl2251(t *testing.T) {
	for _, reverse := range []bool{false, true} {
		for _, jsonMode := range []bool{false, true} {
			list := gqlerror.List{
				&gqlerror.Error{Message: "Session superseded.", Extensions: map[string]any{"code": "SESSION_SUPERSEDED", "sessionId": "A", "startedAt": "2026-11-12T16:37:52Z"}},
				&gqlerror.Error{Message: "private task detail", Extensions: map[string]any{"code": "AUTHORING_ATTESTATION_MISSING", "taskAccess": "UNAVAILABLE"}},
			}
			if reverse {
				list[0], list[1] = list[1], list[0]
			}
			err := api.MapError(list)
			if definitive, _, _ := api.SupersededSessionEvidence(err); definitive {
				t.Fatal("mixed codes cannot be a definitive superseded refusal")
			}
			if _, ok := authoredRefusalText(err); !ok {
				t.Fatal("fixture must activate authored text")
			}
			f, out := testFactory(t)
			var stderr bytes.Buffer
			f.IOStreams.ErrOut = &stderr
			f.JSON = jsonMode
			if code := renderError(f, err); code != 1 {
				t.Fatalf("exit = %d, want 1", code)
			}
			const message = "the authoring task is unreadable or unusable; an owner must fix access or the rule"
			if jsonMode {
				var doc struct {
					Error map[string]any `json:"error"`
				}
				if err := json.Unmarshal([]byte(out.String()), &doc); err != nil {
					t.Fatal(err)
				}
				if doc.Error["message"] != message || doc.Error["serverCode"] != "SESSION_SUPERSEDED" {
					t.Fatalf("JSON answer changed: %s", out.String())
				}
				if _, ok := doc.Error["sessionId"]; ok {
					t.Fatalf("mixed envelope projected pair: %s", out.String())
				}
			} else if stderr.String() != "hadron: "+message+"\n" {
				t.Fatalf("human answer = %q", stderr.String())
			}
		}
	}
}
