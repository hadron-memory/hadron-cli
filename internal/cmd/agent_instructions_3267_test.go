package cmd

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/exitcode"
)

// ---- cli#3267: `hadron agent instructions` (hadron-server#5284) -----------
//
// A thin client of agentInstructions: it sends what the caller gave and shows
// the whole answer. It decides nothing (no ranking, no floor, no class check).

const instrTaskA = `{"node":` + listedNodeJSON + `,"trigger":"tasks:agent","matchedBy":"VECTOR","score":0.82}`
const instrTaskB = `{"node":` + listedOwnNodeJSON + `,"trigger":null,"matchedBy":"VECTOR","score":0.51}`
const instrTaskNoScore = `{"node":` + listedOwnNodeJSON + `,"trigger":null,"matchedBy":"KEYWORD","score":null}`

const instrMatched = `{"data":{"agentInstructions":{"state":"MATCHED","route":"SEARCH",
	"instructions":"Step 1: do the thing.\nStep 2: stop.",
	"candidates":[` + instrTaskA + `,` + instrTaskB + `],"nearest":[],"fallback":null,"register":null}}}`

const instrNoMatch = `{"data":{"agentInstructions":{"state":"NO_MATCH","route":null,
	"instructions":null,"candidates":[],
	"nearest":[` + instrTaskNoScore + `],
	"fallback":{"node":{"id":"n9","urn":"hrn:node:acme.com:support-tasks:fallback","name":"Fallback","inheritedFrom":null},"trigger":null,"matchedBy":null,"score":null},
	"register":{"code":"build.failed","state":"UNMAPPED","taskLoc":null,"taskUrn":null}}}}`

// runInstructions runs the command with the given configured App ("" for
// none) and returns stdout, stderr, the recorded variables and the error.
func runInstructions(t *testing.T, resp string, args ...string) (string, string, map[string]json.RawMessage, error) {
	return runInstructionsApp(t, "", resp, args...)
}

func runInstructionsApp(t *testing.T, configuredApp, resp string, args ...string) (string, string, map[string]json.RawMessage, error) {
	t.Helper()
	gql, captured := captureGraphQL(t, map[string]string{"AgentInstructions": resp})
	f, out := testFactory(t)
	if configuredApp != "" {
		cfg, err := f.Config()
		if err != nil {
			t.Fatalf("config: %v", err)
		}
		if err := cfg.Set("app", configuredApp); err != nil {
			t.Fatalf("set app: %v", err)
		}
	}
	root := NewRootCmd(f)
	root.SetArgs(append([]string{"agent", "instructions", "--server", gql.URL}, args...))
	err := root.Execute()
	errOut := f.IOStreams.ErrOut.(*strings.Builder).String()
	return out.String(), errOut, captured, err
}

func instrVars(t *testing.T, captured map[string]json.RawMessage) map[string]any {
	t.Helper()
	raw, ok := captured["AgentInstructions"]
	if !ok {
		t.Fatalf("no AgentInstructions request was sent")
	}
	var vars map[string]any
	if err := json.Unmarshal(raw, &vars); err != nil {
		t.Fatalf("variables: %v (%s)", err, raw)
	}
	return vars
}

func TestAgentInstructionsMatchedPrintsTaskRouteScoreAndBody(t *testing.T) {
	out, _, captured, err := runInstructions(t, instrMatched, "the build is red")
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	for _, want := range []string{"Onboarding", "hrn:node:acme.com:base-mem:tasks:onboarding", "SEARCH", "0.82", "Step 1: do the thing.", "Step 2: stop."} {
		if !strings.Contains(out, want) {
			t.Errorf("output should carry %q:\n%s", want, out)
		}
	}
	// The runner-up is named, never given a body.
	if !strings.Contains(out, "Escalate") || strings.Count(out, "Step 1") != 1 {
		t.Errorf("runner-up must be named without a body:\n%s", out)
	}
	if vars := instrVars(t, captured); vars["phrase"] != "the build is red" {
		t.Errorf("phrase must ride the request: %v", vars)
	}
}

func TestAgentInstructionsMatchedJSONCarriesTheWholeAnswer(t *testing.T) {
	out, _, _, err := runInstructions(t, instrMatched, "x", "--json")
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	var got struct {
		State        string  `json:"state"`
		Route        *string `json:"route"`
		Instructions *string `json:"instructions"`
		Candidates   []struct {
			Node struct {
				URN string `json:"urn"`
			} `json:"node"`
			Score *float64 `json:"score"`
		} `json:"candidates"`
		Nearest  []json.RawMessage `json:"nearest"`
		Fallback json.RawMessage   `json:"fallback"`
		Register json.RawMessage   `json:"register"`
	}
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("not JSON: %v\n%s", err, out)
	}
	if got.State != "MATCHED" || got.Route == nil || *got.Route != "SEARCH" || got.Instructions == nil || len(got.Candidates) != 2 {
		t.Errorf("answer = %+v", got)
	}
	if got.Candidates[0].Score == nil || *got.Candidates[0].Score != 0.82 {
		t.Errorf("candidate score = %v", got.Candidates[0].Score)
	}
	// Every key is present, nulls and empty lists included.
	for _, key := range []string{`"nearest":[]`, `"fallback":null`, `"register":null`} {
		if !strings.Contains(strings.Join(strings.Fields(out), ""), key) {
			t.Errorf("--json must carry %s:\n%s", key, out)
		}
	}
}

func TestAgentInstructionsNoMatchNeverPrintsABody(t *testing.T) {
	out, _, _, err := runInstructions(t, instrNoMatch, "x", "--event-code", "build.failed", "--app", "hrn:app:acme.com:team")
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	for _, want := range []string{"No task fits", "not instructions", "Escalate", "Fallback", "hrn:node:acme.com:support-tasks:fallback", "build.failed", "UNMAPPED", "no score"} {
		if !strings.Contains(out, want) {
			t.Errorf("output should carry %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "0.00") {
		t.Errorf("a missing score must never print as 0:\n%s", out)
	}
}

// A server that answers NO_MATCH with a body anyway (it must not) gets none
// printed: the CLI never promotes a no-match to instructions.
func TestAgentInstructionsNoMatchIgnoresAStrayBody(t *testing.T) {
	resp := strings.Replace(instrNoMatch, `"instructions":null`, `"instructions":"SHOULD NOT PRINT"`, 1)
	out, _, _, err := runInstructions(t, resp, "x")
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if strings.Contains(out, "SHOULD NOT PRINT") {
		t.Errorf("a NO_MATCH never prints a body:\n%s", out)
	}
}

func TestAgentInstructionsNoMatchJSONKeepsNullScoreAndRegister(t *testing.T) {
	out, _, _, err := runInstructions(t, instrNoMatch, "x", "--json")
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	var got struct {
		State    string `json:"state"`
		Register *struct {
			Code  string `json:"code"`
			State string `json:"state"`
		} `json:"register"`
		Nearest []map[string]any `json:"nearest"`
	}
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("not JSON: %v\n%s", err, out)
	}
	if got.State != "NO_MATCH" || got.Register == nil || got.Register.Code != "build.failed" || got.Register.State != "UNMAPPED" {
		t.Errorf("answer = %+v", got)
	}
	if v, present := got.Nearest[0]["score"]; !present || v != nil {
		t.Errorf("nearest score must be present and null: %v (present=%v)", v, present)
	}
}

func TestAgentInstructionsSendsNoAgentRefWhenNotGiven(t *testing.T) {
	_, _, captured, err := runInstructions(t, instrMatched, "x")
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	vars := instrVars(t, captured)
	if _, ok := vars["agentRef"]; ok {
		t.Errorf("no --agent means no agentRef on the wire (the server uses the session's Agent): %v", vars)
	}
	_, _, captured, _ = runInstructions(t, instrMatched, "x", "--agent", "hrn:agent:acme.com:support")
	if vars := instrVars(t, captured); vars["agentRef"] != "hrn:agent:acme.com:support" {
		t.Errorf("--agent rides as typed: %v", vars)
	}
}

// S8: a phrase and an Agent are enough; the configured App is not sent.
func TestAgentInstructionsAppIsSentOnlyWhenItMustBe(t *testing.T) {
	const configured = "hrn:app:acme.com:configured"
	// Configured App, no --app, no --event-code: App-less.
	_, _, captured, err := runInstructionsApp(t, configured, instrMatched, "x", "--agent", "hrn:agent:acme.com:support")
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if _, ok := instrVars(t, captured)["appRef"]; ok {
		t.Errorf("a phrase-only call must be App-less: %s", captured["AgentInstructions"])
	}
	// --app given: sent.
	_, _, captured, _ = runInstructionsApp(t, configured, instrMatched, "x", "--app", "hrn:app:acme.com:team")
	if v := instrVars(t, captured)["appRef"]; v != "hrn:app:acme.com:team" {
		t.Errorf("--app must be sent: %v", v)
	}
	// --event-code with the configured App: sent (the register needs an App).
	_, _, captured, _ = runInstructionsApp(t, configured, instrMatched, "x", "--event-code", "build.failed")
	vars := instrVars(t, captured)
	if vars["appRef"] != configured || vars["eventCode"] != "build.failed" {
		t.Errorf("--event-code sends the configured App: %v", vars)
	}
	// The ambient App is reported with its source, so a reader can see which
	// App answered (review:ambient-scope-must-report-its-source).
	out, _, _, _ := runInstructionsApp(t, configured, instrMatched, "x", "--event-code", "build.failed")
	if !strings.Contains(out, configured) || !strings.Contains(out, "configured App") {
		t.Errorf("the human render must name the configured App and its source:\n%s", out)
	}
	out, _, _, _ = runInstructionsApp(t, configured, instrMatched, "x", "--agent", "hrn:agent:acme.com:support")
	if strings.Contains(out, "App:") {
		t.Errorf("an App-less call names no App:\n%s", out)
	}
}

// An event code with no App is the server's refusal: the CLI sends it as is.
func TestAgentInstructionsEventCodeWithoutAppIsNotCheckedLocally(t *testing.T) {
	out, _, captured, err := runInstructions(t, memberGQLError("eventCode needs appRef", "BAD_USER_INPUT"), "--event-code", "build.failed")
	if _, ok := captured["AgentInstructions"]; !ok {
		t.Fatalf("the call must reach the server")
	}
	if got := exitCodeFor(err); got != exitcode.Usage || out != "" {
		t.Errorf("want the server's refusal as exit %d with empty stdout, got %d (%v) out=%q", exitcode.Usage, got, err, out)
	}
	if _, ok := instrVars(t, captured)["appRef"]; ok {
		t.Errorf("no App to send: %s", captured["AgentInstructions"])
	}
}

func TestAgentInstructionsPhraseFlagAndPositional(t *testing.T) {
	_, _, captured, err := runInstructions(t, instrMatched, "--phrase", "from the flag")
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if v := instrVars(t, captured)["phrase"]; v != "from the flag" {
		t.Errorf("--phrase: %v", v)
	}
	// Both at once is ambiguous: refused before any request.
	_, _, captured, err = runInstructions(t, instrMatched, "a", "--phrase", "b")
	if exitCodeFor(err) != exitcode.Usage || len(captured) != 0 {
		t.Errorf("both forms must be a usage error with no request: %v %v", err, captured)
	}
	// Neither a phrase nor an event code: nothing to ask.
	_, _, captured, err = runInstructions(t, instrMatched)
	if exitCodeFor(err) != exitcode.Usage || len(captured) != 0 {
		t.Errorf("no input must be a usage error with no request: %v %v", err, captured)
	}
}

// An empty phrase is sent, not dropped: the server owns the 1 to 500 rule.
func TestAgentInstructionsEmptyPhraseIsSentNotDropped(t *testing.T) {
	_, _, captured, _ := runInstructions(t, memberGQLError("phrase must be 1 to 500 characters", "BAD_USER_INPUT"), "--phrase", "")
	if v, ok := instrVars(t, captured)["phrase"]; !ok || v != "" {
		t.Errorf("empty phrase must be sent: %s", captured["AgentInstructions"])
	}
}

func TestAgentInstructionsRefusalMapsToItsExitCode(t *testing.T) {
	for code, want := range map[string]int{"BAD_USER_INPUT": exitcode.Usage} {
		_, _, _, err := runInstructions(t, memberGQLError("nope", code), "x")
		if got := exitCodeFor(err); got != want {
			t.Errorf("%s: exit %d, want %d (%v)", code, got, want, err)
		}
	}
	for _, code := range []string{"APP_NOT_FOUND", "AGENT_NOT_FOUND"} {
		_, _, _, err := runInstructions(t, memberGQLError("not found", code), "x", "--app", "hrn:app:acme.com:team")
		if exitCodeFor(err) != exitcode.NotFound {
			t.Errorf("%s: exit %d, want %d (%v)", code, exitCodeFor(err), exitcode.NotFound, err)
		}
	}
}

// A MATCHED answer with no candidate is malformed: refused, nothing printed.
func TestAgentInstructionsMalformedMatchIsRefused(t *testing.T) {
	for name, resp := range map[string]string{
		"no candidate": `{"data":{"agentInstructions":{"state":"MATCHED","route":"SEARCH","instructions":"body","candidates":[],"nearest":[],"fallback":null,"register":null}}}`,
		"no route":     strings.Replace(instrMatched, `"route":"SEARCH"`, `"route":null`, 1),
	} {
		out, _, _, err := runInstructions(t, resp, "x")
		if exitCodeFor(err) != exitcode.Error || out != "" {
			t.Errorf("%s: want exit %d and empty stdout, got %d (%v) out=%q", name, exitcode.Error, exitCodeFor(err), err, out)
		}
	}
}

// Nothing is kept: the phrase and event code reach the server and the
// caller's stdout, and appear on no stderr line and in no file the CLI writes.
func TestAgentInstructionsKeepsNothing(t *testing.T) {
	const phrase, code = "PHRASE-SECRET-3267", "EVENT-SECRET-3267"
	gql, _ := captureGraphQL(t, map[string]string{"AgentInstructions": instrMatched})
	f, _ := testFactory(t)
	// The config tree the factory resolved (testFactory points it at a temp
	// dir BEFORE any command runs), plus a file that must exist so the walk
	// below provably looks at a real directory.
	cfgDir := os.Getenv("XDG_CONFIG_HOME")
	if cfgDir == "" {
		t.Fatal("testFactory must pin XDG_CONFIG_HOME")
	}
	marker := filepath.Join(cfgDir, "marker")
	if err := os.WriteFile(marker, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	walked := 0
	root := NewRootCmd(f)
	root.SetArgs([]string{"agent", "instructions", phrase, "--event-code", code, "--app", "hrn:app:acme.com:team", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if errOut := f.IOStreams.ErrOut.(*strings.Builder).String(); strings.Contains(errOut, phrase) || strings.Contains(errOut, code) {
		t.Errorf("stderr must not echo the phrase or event code:\n%s", errOut)
	}
	_ = filepath.WalkDir(cfgDir, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		walked++
		if b, _ := os.ReadFile(path); strings.Contains(string(b), phrase) || strings.Contains(string(b), code) {
			t.Errorf("%s holds the phrase or event code", path)
		}
		return nil
	})
	if walked == 0 {
		t.Error("the walk saw no files: the test would prove nothing")
	}
}

// A blank --agent or --app is refused locally, before any request.
func TestAgentInstructionsBlankAgentOrAppIsAUsageError(t *testing.T) {
	for _, args := range [][]string{{"x", "--agent", "  "}, {"x", "--app", "  "}} {
		_, _, captured, err := runInstructions(t, instrMatched, args...)
		if exitCodeFor(err) != exitcode.Usage || len(captured) != 0 {
			t.Errorf("%v: want a usage error and no request, got %v %v", args, err, captured)
		}
	}
}

// The register naming a mapped task is printed with its task, and a MATCHED
// answer carries the register through to --json.
func TestAgentInstructionsMatchedShowsTheRegisterTask(t *testing.T) {
	resp := strings.Replace(instrMatched, `"register":null`, `"register":{"code":"build.failed","state":"MAPPED","taskLoc":"tasks:fix","taskUrn":"hrn:node:acme.com:support-tasks:fix"}`, 1)
	resp = strings.Replace(resp, `"route":"SEARCH"`, `"route":"REGISTER"`, 1)
	out, _, _, err := runInstructions(t, resp, "x", "--event-code", "build.failed", "--app", "hrn:app:acme.com:team")
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	for _, want := range []string{"REGISTER", "build.failed", "MAPPED", "-> hrn:node:acme.com:support-tasks:fix"} {
		if !strings.Contains(out, want) {
			t.Errorf("output should carry %q:\n%s", want, out)
		}
	}
	jsonOut, _, _, _ := runInstructions(t, resp, "x", "--event-code", "build.failed", "--app", "hrn:app:acme.com:team", "--json")
	if !strings.Contains(jsonOut, "support-tasks:fix") {
		t.Errorf("--json must carry the register's task:\n%s", jsonOut)
	}
}

func TestAgentInstructionsHelpWording(t *testing.T) {
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"agent", "instructions", "--help"})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	help := strings.Join(strings.Fields(out.String()), " ")
	for _, want := range []string{"--event-code", "--agent", "--app", "no score", "not instructions", "App-less"} {
		if !strings.Contains(help, want) {
			t.Errorf("help should say %q:\n%s", want, out.String())
		}
	}
}
