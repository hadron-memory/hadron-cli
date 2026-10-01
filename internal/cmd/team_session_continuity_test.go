package cmd

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/api/gen"
)

const currentHandoffResponse = `{"data":{"worker":{"continuity":{"status":"CURRENT",` +
	`"handoff":{"urn":"hrn:node:acme.com:iris:handoffs:2026-10-01-0115",` +
	`"loc":"handoffs:2026-10-01-0115","content":"PR #42 awaits review. Do not rerun migration.",` +
	`"createdAt":"2026-10-01T01:15:00Z"},` +
	`"previousSession":{"id":"s-prev","endedAt":"2026-10-01T01:16:00Z","autoExpiredAt":null}}}}}`

const missingHandoffResponse = `{"data":{"worker":{"continuity":{"status":"MISSING",` +
	`"handoff":{"urn":"hrn:node:acme.com:iris:handoffs:older","loc":"handoffs:older",` +
	`"content":"Historical work only.","createdAt":"2026-09-30T00:00:00Z"},` +
	`"previousSession":{"id":"s-prev","endedAt":"2026-10-01T01:16:00Z",` +
	`"autoExpiredAt":"2026-10-01T01:16:00Z"}}}}}`

func TestTeamSessionStartDeliversCurrentHandoffInTextAndJSON(t *testing.T) {
	for _, jsonOutput := range []bool{false, true} {
		t.Run(map[bool]string{false: "text", true: "json"}[jsonOutput], func(t *testing.T) {
			teamGitDir(t)
			gql, captured := captureGraphQL(t, map[string]string{
				"GetWorker":           `{"data":{"worker":` + irisWorkerJSON + `}}`,
				"TeamSessions":        `{"data":{"sessions":[]}}`,
				"StartTeamSession":    `{"data":{"startSession":` + startedSessionJSON + `}}`,
				"GetWorkerContinuity": currentHandoffResponse,
			})
			f, out := testFactory(t)
			root := NewRootCmd(f)
			args := []string{"team", "session", "start", "--as", "wkr1", "--server", gql.URL}
			if jsonOutput {
				args = append(args, "--json")
			}
			root.SetArgs(args)
			if err := root.Execute(); err != nil {
				t.Fatal(err)
			}
			var vars struct {
				Ref string `json:"ref"`
			}
			if err := json.Unmarshal(captured["GetWorkerContinuity"], &vars); err != nil || vars.Ref != "wkr1" {
				t.Fatalf("continuity must be read for the bound worker id: vars=%s err=%v", captured["GetWorkerContinuity"], err)
			}
			if jsonOutput {
				var result struct {
					Continuity struct {
						Status  string `json:"status"`
						Handoff struct {
							URN     string `json:"urn"`
							Content string `json:"content"`
						} `json:"handoff"`
					} `json:"continuity"`
				}
				if err := json.Unmarshal([]byte(out.String()), &result); err != nil {
					t.Fatalf("JSON output: %v: %s", err, out.String())
				}
				if result.Continuity.Status != "CURRENT" || !strings.Contains(result.Continuity.Handoff.URN, "handoffs:") ||
					result.Continuity.Handoff.Content != "PR #42 awaits review. Do not rerun migration." {
					t.Errorf("the bind must deliver the current handoff: %s", out.String())
				}
			} else {
				for _, want := range []string{"CURRENT", "hrn:node:acme.com:iris:handoffs:2026-10-01-0115", "PR #42 awaits review. Do not rerun migration.", "You are Iris."} {
					if !strings.Contains(out.String(), want) {
						t.Errorf("bind output missing %q: %s", want, out.String())
					}
				}
				if strings.Index(out.String(), "PR #42") > strings.Index(out.String(), "You are Iris.") {
					t.Errorf("handoff must precede the boot briefing: %s", out.String())
				}
			}
		})
	}
}

func TestTeamSessionStartDistinguishesFirstStintAndMissingHandoff(t *testing.T) {
	for _, tc := range []struct {
		name, response string
		want           []string
		dontWant       []string
	}{
		{"first stint", `{"data":{"worker":{"continuity":{"status":"FIRST_STINT","handoff":null,"previousSession":null}}}}`,
			[]string{"handoff: none (first stint)"}, []string{"Older handoff", "No current handoff"}},
		{"missing with older history", missingHandoffResponse,
			[]string{"No current handoff", "s-prev was auto-expired", "Older handoff (history, not the current state)", "Historical work only."}, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			teamGitDir(t)
			gql, _ := captureGraphQL(t, map[string]string{
				"GetWorker":           `{"data":{"worker":` + irisWorkerJSON + `}}`,
				"TeamSessions":        `{"data":{"sessions":[]}}`,
				"StartTeamSession":    `{"data":{"startSession":` + startedSessionJSON + `}}`,
				"GetWorkerContinuity": tc.response,
			})
			f, out := testFactory(t)
			root := NewRootCmd(f)
			root.SetArgs([]string{"team", "session", "start", "--as", "wkr1", "--server", gql.URL})
			if err := root.Execute(); err != nil {
				t.Fatal(err)
			}
			for _, want := range tc.want {
				if !strings.Contains(out.String(), want) {
					t.Errorf("missing %q: %s", want, out.String())
				}
			}
			for _, unwanted := range tc.dontWant {
				if strings.Contains(out.String(), unwanted) {
					t.Errorf("unexpected %q: %s", unwanted, out.String())
				}
			}
			if i := strings.Index(out.String(), "No current handoff"); i >= 0 &&
				strings.Index(out.String(), "Historical work only.") < i {
				t.Errorf("the missing-current warning must precede the older content: %s", out.String())
			}
		})
	}
}

func TestTeamSessionStartJSONDistinguishesMissingFromUnavailable(t *testing.T) {
	for _, tc := range []struct {
		name, response string
		wantStatus     string
	}{
		{"missing with older history", missingHandoffResponse, "MISSING"},
		{"masked continuity", `{"data":{"worker":{"continuity":null}}}`, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			teamGitDir(t)
			gql, _ := captureGraphQL(t, map[string]string{
				"GetWorker":           `{"data":{"worker":` + irisWorkerJSON + `}}`,
				"TeamSessions":        `{"data":{"sessions":[]}}`,
				"StartTeamSession":    `{"data":{"startSession":` + startedSessionJSON + `}}`,
				"GetWorkerContinuity": tc.response,
			})
			f, out := testFactory(t)
			root := NewRootCmd(f)
			root.SetArgs([]string{"team", "session", "start", "--as", "wkr1", "--json", "--server", gql.URL})
			if err := root.Execute(); err != nil {
				t.Fatal(err)
			}
			var result struct {
				Continuity *struct {
					Status  string `json:"status"`
					Handoff *struct {
						URN     string `json:"urn"`
						Content string `json:"content"`
					} `json:"handoff"`
				} `json:"continuity"`
			}
			if err := json.Unmarshal([]byte(out.String()), &result); err != nil {
				t.Fatalf("JSON output: %v: %s", err, out.String())
			}
			if tc.wantStatus == "" {
				if result.Continuity != nil {
					t.Errorf("masked continuity must be null: %s", out.String())
				}
				return
			}
			if result.Continuity == nil || result.Continuity.Status != tc.wantStatus ||
				result.Continuity.Handoff == nil || result.Continuity.Handoff.Content != "Historical work only." {
				t.Errorf("JSON must retain missing status with older historical handoff: %s", out.String())
			}
		})
	}
}

func TestTeamSessionStartForceReadsContinuityAfterEndingOldStint(t *testing.T) {
	dir := teamGitDir(t)
	prev := strings.Replace(bindingFixture, "s-new", "s-prev", 1)
	if err := os.WriteFile(filepath.Join(dir, "hadron-team-session.json"), []byte(prev), 0o600); err != nil {
		t.Fatal(err)
	}
	var ops []string
	responses := map[string]string{
		"EndTeamSession":   `{"data":{"endSession":` + startedSessionJSON + `}}`,
		"GetWorker":        `{"data":{"worker":` + irisWorkerJSON + `}}`,
		"TeamSessions":     `{"data":{"sessions":[]}}`,
		"StartTeamSession": `{"data":{"startSession":` + startedSessionJSON + `}}`,
		"GetWorkerContinuity": strings.Replace(missingHandoffResponse,
			`"autoExpiredAt":"2026-10-01T01:16:00Z"`, `"autoExpiredAt":null`, 1),
	}
	gql, _ := captureGraphQLFunc(t, func(op string) string {
		ops = append(ops, op)
		return responses[op]
	})
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"team", "session", "start", "--as", "wkr1", "--force", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	index := func(op string) int {
		for i, got := range ops {
			if got == op {
				return i
			}
		}
		return -1
	}
	if index("EndTeamSession") < 0 || index("StartTeamSession") <= index("EndTeamSession") ||
		index("GetWorkerContinuity") <= index("StartTeamSession") {
		t.Errorf("continuity must reflect the completed force refresh: %v", ops)
	}
	if !strings.Contains(out.String(), "No current handoff") || !strings.Contains(out.String(), "Older handoff") {
		t.Errorf("force refresh must disclose that the closed stint wrote no handoff: %s", out.String())
	}
}

func TestTeamSessionStartOldServerKeepsBindAndJSONValid(t *testing.T) {
	teamGitDir(t)
	gql, captured := captureGraphQL(t, map[string]string{
		"GetWorker":        `{"data":{"worker":` + irisWorkerJSON + `}}`,
		"TeamSessions":     `{"data":{"sessions":[]}}`,
		"StartTeamSession": `{"data":{"startSession":` + startedSessionJSON + `}}`,
		// GetWorkerContinuity receives the old-server validation refusal
		// from unstubbedDefault.
	})
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"team", "session", "start", "--as", "wkr1", "--json", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("older server must still bind: %v", err)
	}
	var result struct {
		Session struct {
			ID string `json:"id"`
		} `json:"session"`
		Continuity *json.RawMessage `json:"continuity"`
	}
	if err := json.Unmarshal([]byte(out.String()), &result); err != nil || result.Session.ID != "s-new" || result.Continuity != nil {
		t.Errorf("older server result should keep the bind and report continuity unavailable: %s, %v", out.String(), err)
	}
	if _, ok := captured["GetWorkerContinuity"]; !ok {
		t.Error("compatibility test did not exercise the optional continuity read")
	}
	if stderr := f.IOStreams.ErrOut.(*strings.Builder).String(); strings.Contains(stderr, "continuity") {
		t.Errorf("an older server should degrade quietly: %s", stderr)
	}
	selectsContinuity := func(operation string) bool {
		for _, line := range strings.Split(operation, "\n") {
			if strings.TrimSpace(line) == "continuity {" {
				return true
			}
		}
		return false
	}
	if selectsContinuity(gen.GetWorker_Operation) || selectsContinuity(gen.Workers_Operation) ||
		!selectsContinuity(gen.GetWorkerContinuity_Operation) {
		t.Error("only the optional continuity operation may select Worker.continuity")
	}
}
