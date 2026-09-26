package cmd

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/exitcode"
)

const modelLogSessionJSON = `{"data":{"updateSession":{"id":"s-new","agentId":"agt1","workerId":"wkr1","userId":"u1",
	"type":"DEVELOPER","repo":null,"branch":null,"prNumber":371,
	"startedAt":"2026-08-11T10:00:00Z","endedAt":null,"host":null,"tool":null,
	"transcriptPath":null,"llmModel":"initial-model"}}}`

func bindWorklogModelTest(t *testing.T) {
	t.Helper()
	path := filepath.Join(teamGitDir(t), "hadron-team-session.json")
	// Deliberately stale: a model switch must never be inferred from local
	// binding metadata or sent as an explicit override on an omitted --model.
	b := strings.Replace(bindingWithTeamFixture, `"tool":"claude-code"`, `"tool":"claude-code","model":"initial-model"`, 1)
	if err := os.WriteFile(path, []byte(b), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestSessionLogReportsServerStoredMilestoneModel(t *testing.T) {
	for _, tc := range []struct {
		name, modelFlag, stored string
	}{
		{"explicit switch", "switched-model", "switched-model"},
		{"omitted uses server fallback", "", "initial-model"},
		{"unknown remains unknown", "", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			bindWorklogModelTest(t)
			stored := "null"
			if tc.stored != "" {
				b, _ := json.Marshal(tc.stored)
				stored = string(b)
			}
			gql, captured := captureGraphQL(t, map[string]string{
				"UpdateTeamSession": modelLogSessionJSON,
				"RecordTeamWork": `{"data":{"recordTeamWork":{"nodeId":"w1","sessionId":"s-new","workerId":"wkr1","workerName":"Iris",
					"tool":"github","kind":"pr","ref":"hadron-memory/hadron-cli#371","action":"worked-on",
					"at":"2026-08-13T10:00:00Z","detail":null,"model":` + stored + `}}}`,
			})
			args := []string{"team", "session", "log", "--pr", "371", "--json", "--server", gql.URL}
			if tc.modelFlag != "" {
				args = append(args, "--model", tc.modelFlag)
			}
			f, out := testFactory(t)
			root := NewRootCmd(f)
			root.SetArgs(args)
			if err := root.Execute(); err != nil {
				t.Fatal(err)
			}
			var vars map[string]any
			if err := json.Unmarshal(captured["RecordTeamWork"], &vars); err != nil {
				t.Fatal(err)
			}
			if tc.modelFlag == "" {
				if _, present := vars["model"]; present {
					t.Errorf("omitted --model must leave fallback to the server, got %v", vars["model"])
				}
			} else if vars["model"] != tc.modelFlag {
				t.Errorf("model argument = %v, want %q", vars["model"], tc.modelFlag)
			}
			var dto struct {
				Model *string `json:"model"`
			}
			if err := json.Unmarshal([]byte(out.String()), &dto); err != nil {
				t.Fatal(err)
			}
			if tc.stored == "" && dto.Model != nil || tc.stored != "" && (dto.Model == nil || *dto.Model != tc.stored) {
				t.Errorf("receipt model = %v, want %q", dto.Model, tc.stored)
			}
			f, out = testFactory(t)
			root = NewRootCmd(f)
			humanArgs := []string{"team", "session", "log", "--pr", "371", "--server", gql.URL}
			if tc.modelFlag != "" {
				humanArgs = append(humanArgs, "--model", tc.modelFlag)
			}
			root.SetArgs(humanArgs)
			if err := root.Execute(); err != nil {
				t.Fatal(err)
			}
			wantLabel := worklogModelLabelForTest(tc.stored)
			if !strings.Contains(out.String(), "reported model: "+wantLabel) {
				t.Errorf("human receipt must show server-stored model %q: %s", wantLabel, out.String())
			}
		})
	}
}

func worklogModelLabelForTest(model string) string {
	if model == "" {
		return "unknown"
	}
	return model
}

func TestSessionLogModelOldServerCompatibility(t *testing.T) {
	bindWorklogModelTest(t)
	gql, captured := captureGraphQL(t, map[string]string{
		"UpdateTeamSession": modelLogSessionJSON,
		"RecordTeamWork":    `{"errors":[{"message":"Cannot query field \"model\" on type \"TeamWorkItem\".","extensions":{"code":"GRAPHQL_VALIDATION_FAILED"}}]}`,
		"RecordTeamWorkLegacy": `{"data":{"recordTeamWork":{"nodeId":"w1","sessionId":"s-new","workerId":"wkr1","workerName":"Iris",
			"tool":"github","kind":"pr","ref":"hadron-memory/hadron-cli#371","action":"worked-on",
			"at":"2026-08-13T10:00:00Z","detail":null}}}`,
	})
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"team", "session", "log", "--pr", "371", "--json", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("ordinary logging must work on an old server: %v", err)
	}
	if _, called := captured["RecordTeamWorkLegacy"]; !called || !strings.Contains(out.String(), `"model": null`) {
		t.Errorf("legacy worklog write and unknown model receipt: %s; calls %v", out.String(), captured)
	}
	delete(captured, "RecordTeamWorkLegacy")
	f, _ = testFactory(t)
	root = NewRootCmd(f)
	root.SetArgs([]string{"team", "session", "log", "--pr", "371", "--model", "switched-model", "--server", gql.URL})
	err := root.Execute()
	if got := exitCodeFor(err); got != exitcode.Usage {
		t.Errorf("explicit model on old server: exit = %d, want 2 (%v)", got, err)
	}
	if _, called := captured["RecordTeamWorkLegacy"]; called {
		t.Error("an explicit model must never fall back to an unattributed write")
	}
}

func TestSessionLogRejectsBlankExplicitModelBeforeAnyRequest(t *testing.T) {
	bindWorklogModelTest(t)
	gql, captured := captureGraphQL(t, nil)
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"team", "session", "log", "--pr", "371", "--model", "  ", "--server", gql.URL})
	if got := exitCodeFor(root.Execute()); got != exitcode.Usage {
		t.Errorf("blank model: exit = %d, want 2", got)
	}
	if len(captured) != 0 {
		t.Errorf("blank explicit model must refuse before any request: %v", captured)
	}
}

func TestSessionListProvenanceKeepsEachMilestoneModel(t *testing.T) {
	teamGitDir(t)
	items := `{"data":{"teamWorkItems":{"total":3,"items":[
		{"nodeId":"w1","sessionId":"s-done","workerId":"wkr1","workerName":"Iris","tool":"github","kind":"pr","ref":"hadron-memory/hadron-cli#371","action":"opened","at":"2026-08-13T10:00:00Z","detail":null,"model":"initial-model"},
		{"nodeId":"w2","sessionId":"s-done","workerId":"wkr1","workerName":"Iris","tool":"github","kind":"pr","ref":"hadron-memory/hadron-cli#371","action":"pushed","at":"2026-08-14T10:00:00Z","detail":null,"model":"switched-model"},
		{"nodeId":"w3","sessionId":"s-done","workerId":"wkr1","workerName":"Iris","tool":"github","kind":"pr","ref":"hadron-memory/hadron-cli#371","action":"reviewed","at":"2026-08-15T10:00:00Z","detail":null,"model":null}]}}}`
	started := strings.Replace(endedSessionJSON, `"llmModel":null`, `"llmModel":"initial-model"`, 1)
	gql, _ := captureGraphQL(t, sessionRenderStubs(map[string]string{
		"TeamMemoryApp":  `{"data":{"memory":{"id":"m1","appId":"capp100000000000000000000"}}}`,
		"TeamWorkItems":  items,
		"GetTeamSession": `{"data":{"session":` + started + `}}`,
	}))
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"team", "session", "list", "--pr", "hadron-memory/hadron-cli#371", "-m", "acme.com::eng-team", "--json", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	var rows []struct {
		LLMModel *string `json:"llmModel"`
		Worklog  []struct {
			Action string  `json:"action"`
			Model  *string `json:"model"`
		} `json:"worklog"`
	}
	if err := json.Unmarshal([]byte(out.String()), &rows); err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].LLMModel == nil || *rows[0].LLMModel != "initial-model" || len(rows[0].Worklog) != 3 ||
		rows[0].Worklog[0].Model == nil || *rows[0].Worklog[0].Model != "initial-model" ||
		rows[0].Worklog[1].Model == nil || *rows[0].Worklog[1].Model != "switched-model" || rows[0].Worklog[2].Model != nil {
		t.Errorf("event-time models must stay distinct from the initial session model: %s", out.String())
	}
	f, out = testFactory(t)
	root = NewRootCmd(f)
	root.SetArgs([]string{"team", "session", "list", "--pr", "hadron-memory/hadron-cli#371", "-m", "acme.com::eng-team", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"SESSION MODEL", "REPORTED MODEL", "initial-model", "switched-model", "unknown"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("human provenance must show %q: %s", want, out.String())
		}
	}
}

func TestSessionListProvenanceReadsOldServerWithoutModel(t *testing.T) {
	teamGitDir(t)
	gql, captured := captureGraphQL(t, map[string]string{
		"TeamMemoryApp": `{"data":{"memory":{"id":"m1","appId":"capp100000000000000000000"}}}`,
		"TeamWorkItems": `{"errors":[{"message":"Unknown field \"model\" on type \"TeamWorkItem\".","extensions":{"code":"GRAPHQL_VALIDATION_FAILED"}}]}`,
		"TeamWorkItemsLegacy": `{"data":{"teamWorkItems":{"total":1,"items":[
			{"nodeId":"w1","sessionId":"s-done","workerId":"wkr1","workerName":"Iris","tool":"github","kind":"pr",
			"ref":"hadron-memory/hadron-cli#371","action":"opened","at":"2026-08-13T10:00:00Z","detail":null}]}}}`,
		"GetTeamSession": `{"data":{"session":` + endedSessionJSON + `}}`,
	})
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"team", "session", "list", "--pr", "hadron-memory/hadron-cli#371", "-m", "acme.com::eng-team", "--json", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("legacy provenance read: %v", err)
	}
	if _, called := captured["TeamWorkItemsLegacy"]; !called || !strings.Contains(out.String(), `"model": null`) {
		t.Errorf("legacy read must preserve unknown model: %s; calls %v", out.String(), captured)
	}
}
