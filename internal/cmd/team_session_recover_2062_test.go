package cmd

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/exitcode"
)

// hadron-cli#2062 (C7): `team session recover` is one STRICTLY READ-ONLY call.
// The pins that matter: no request carries X-Hadron-Session (so nothing can
// heartbeat or acknowledge), no mutation is issued, an absent state note is
// null (not an error), and an unreadable section is loud (exit 7), never empty.

const recoverNote2062 = "# State\nwork in flight: #2062\n\n## Standing directions\nNever merge.\nAsk Wolfgang first.\n\n## Waiting on\nreview seats\n"

func recoverResponses2062() map[string]string {
	return map[string]string{
		"GetTeamSession":       `{"data":{"session":` + startedSessionJSON + `}}`,
		"RecoverWorker":        `{"data":{"worker":{"id":"wkr1","urn":"hrn:worker:acme.com:eng-team:iris","name":"Iris","role":"backend-engineer","appId":"capp100000000000000000000","memoryId":"mw1"}}}`,
		"GetMemory":            `{"data":{"memory":{"id":"mw1","urn":"hrn:mem:acme.com:iris-work","name":"Iris work"}}}`,
		"AgentTaskList":        `{"data":{"agentTasks":[` + startedTaskFieldsJSON + `]}}`,
		"GetNode":              `{"data":{"node":{"id":"n1","urn":"hrn:node:acme.com:iris-work:state","memoryId":"mw1","loc":"state","name":"State","nodeType":"note","tags":[],"content":` + jsonStr1525(recoverNote2062) + `,"updatedAt":"2026-11-15T09:00:00Z"}}}`,
		"TeamChatReadMetadata": `{"data":{"app":{"id":"capp100000000000000000000","defaultChannel":{"id":"ch1","lastSeq":9}}}}`,
		"ChannelReadState":     `{"data":{"channelReadState":{"channelId":"ch1","lastSeenSeq":4,"updatedAt":"2026-11-15T08:00:00Z"}}}`,
		"TeamChatMessages":     teamChatPage(2, 5, 7),
	}
}

func runRecover2062(t *testing.T, responses map[string]string, args ...string) (string, *[]attnCall, error) {
	t.Helper()
	writeTeamBinding(t)
	srv, calls := attnServer(t, responses)
	setTeamBindingServer(t, srv.URL)
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs(append([]string{"team", "session", "recover"}, append(args, "--server", srv.URL)...))
	err := root.Execute()
	return out.String(), calls, err
}

func TestSessionRecoverIsReadOnly2062(t *testing.T) {
	_, calls, err := runRecover2062(t, recoverResponses2062(), "--json")
	if err != nil {
		t.Fatal(err)
	}
	if len(*calls) == 0 {
		t.Fatal("no requests recorded")
	}
	for _, c := range *calls {
		if c.Session != "" {
			t.Errorf("%s carried X-Hadron-Session %q: recover must be header-free so it cannot heartbeat or acknowledge", c.Op, c.Session)
		}
		if strings.Contains(c.Query, "mutation") {
			t.Errorf("%s is a mutation; recover is read-only", c.Op)
		}
		switch c.Op {
		case "MarkOwnTeamChatRead", "AdvanceChannelReadState", "StartTeamSession", "EndTeamSession":
			t.Errorf("recover issued %s", c.Op)
		}
	}
}

func TestSessionRecoverJSONShape2062(t *testing.T) {
	out, calls, err := runRecover2062(t, recoverResponses2062(), "--json")
	if err != nil {
		t.Fatal(err)
	}
	var dto map[string]any
	if err := json.Unmarshal([]byte(out), &dto); err != nil {
		t.Fatalf("not JSON: %v\n%s", err, out)
	}
	for _, k := range []string{"worker", "session", "tasks", "tasksMore", "stateNote", "standingDirections", "mentions", "unavailable"} {
		if _, ok := dto[k]; !ok {
			t.Errorf("key %q missing: %s", k, out)
		}
	}
	if got := dto["standingDirections"]; got != "Never merge.\nAsk Wolfgang first." {
		t.Errorf("standingDirections = %q, want the section verbatim and no neighbours", got)
	}
	if u, _ := dto["unavailable"].([]any); u == nil || len(u) != 0 {
		t.Errorf("unavailable = %v, want []", dto["unavailable"])
	}
	mentions := dto["mentions"].(map[string]any)
	if mentions["watermark"] != float64(4) || mentions["head"] != float64(9) {
		t.Errorf("watermark/head = %v/%v, want 4/9", mentions["watermark"], mentions["head"])
	}
	if items, _ := mentions["items"].([]any); len(items) != 2 {
		t.Errorf("mentions items = %v", mentions["items"])
	}
	if tasks, _ := dto["tasks"].([]any); len(tasks) != 1 {
		t.Errorf("tasks = %v", dto["tasks"])
	}
	// The mentions read must start AFTER the watermark and be filtered to this worker.
	for _, c := range *calls {
		if c.Op == "TeamChatMessages" {
			if c.Vars["sinceSeq"] != float64(4) || c.Vars["mentionsRef"] != "wkr1" {
				t.Errorf("mentions read vars = %v, want sinceSeq 4 and mentions wkr1", c.Vars)
			}
		}
		if c.Op == "GetNode" && c.Vars["ref"] != "hrn:node:acme.com:iris-work:state" {
			t.Errorf("state note ref = %v", c.Vars["ref"])
		}
	}
}

func TestSessionRecoverAbsentNoteAndEmptyListsAre2062(t *testing.T) {
	r := recoverResponses2062()
	r["GetNode"] = `{"data":{"node":null}}`
	r["AgentTaskList"] = `{"data":{"agentTasks":[]}}`
	r["TeamChatMessages"] = teamChatPage(0)
	out, _, err := runRecover2062(t, r, "--json")
	if err != nil {
		t.Fatalf("an absent note is normal, got %v", err)
	}
	var dto map[string]any
	_ = json.Unmarshal([]byte(out), &dto)
	if dto["stateNote"] != nil || dto["standingDirections"] != nil {
		t.Errorf("absent note must be null: %v / %v", dto["stateNote"], dto["standingDirections"])
	}
	if tasks, ok := dto["tasks"].([]any); !ok || len(tasks) != 0 {
		t.Errorf("tasks = %#v, want []", dto["tasks"])
	}
	if items, ok := dto["mentions"].(map[string]any)["items"].([]any); !ok || len(items) != 0 {
		t.Errorf("mention items = %#v, want []", dto["mentions"])
	}
}

func TestSessionRecoverUnreadableSectionIsLoud2062(t *testing.T) {
	r := recoverResponses2062()
	r["AgentTaskList"] = `{"errors":[{"message":"boom","extensions":{"code":"INTERNAL_SERVER_ERROR"}}]}`
	out, _, err := runRecover2062(t, r, "--json")
	if got := exitCodeFor(err); got != exitcode.Unavailable {
		t.Fatalf("exit = %d (%v), want %d unavailable", got, err, exitcode.Unavailable)
	}
	var dto struct {
		Unavailable []struct{ Section string } `json:"unavailable"`
		Worker      struct{ Name string }      `json:"worker"`
	}
	if jerr := json.Unmarshal([]byte(out), &dto); jerr != nil {
		t.Fatalf("the rest must still print as JSON: %v\n%s", jerr, out)
	}
	if len(dto.Unavailable) != 1 || dto.Unavailable[0].Section != "tasks" || dto.Worker.Name != "Iris" {
		t.Errorf("unavailable = %+v worker = %+v", dto.Unavailable, dto.Worker)
	}
}

func TestSessionRecoverNoWatermarkIsNotZero2062(t *testing.T) {
	r := recoverResponses2062()
	delete(r, "ChannelReadState") // the harness default answers a null read state
	out, calls, err := runRecover2062(t, r, "--json")
	if exitCodeFor(err) != exitcode.Unavailable {
		t.Fatalf("exit = %v, want unavailable: no watermark means \"after the watermark\" is unanswerable", err)
	}
	var dto struct {
		Mentions struct {
			Watermark *int `json:"watermark"`
		} `json:"mentions"`
	}
	_ = json.Unmarshal([]byte(out), &dto)
	if dto.Mentions.Watermark != nil {
		t.Errorf("watermark = %d, want null, never a forged 0", *dto.Mentions.Watermark)
	}
	for _, c := range *calls {
		if c.Op == "TeamChatMessages" {
			t.Error("a mentions page was read without a watermark; that would be the newest page, not 'since the watermark'")
		}
	}
}

func TestSessionRecoverUsesTheWorkersMemoryID2062(t *testing.T) {
	r := recoverResponses2062()
	r["RecoverWorker"] = strings.Replace(r["RecoverWorker"], `"memoryId":"mw1"`, `"memoryId":null`, 1)
	out, calls, err := runRecover2062(t, r, "--json")
	if err != nil {
		t.Fatalf("no working memory reads as no note, got %v", err)
	}
	var dto map[string]any
	_ = json.Unmarshal([]byte(out), &dto)
	if dto["stateNote"] != nil {
		t.Errorf("stateNote = %v, want null", dto["stateNote"])
	}
	for _, c := range *calls {
		if c.Op == "GetNode" || c.Op == "GetMemory" {
			t.Errorf("%s was read although the worker has no memoryId; the address must come from memoryId, never a slug guess", c.Op)
		}
	}
}

func TestSessionRecoverNullMentionsPageIsLoud2062(t *testing.T) {
	r := recoverResponses2062()
	r["TeamChatMessages"] = `{"data":{"teamChatMessages":null}}`
	out, _, err := runRecover2062(t, r, "--json")
	if exitCodeFor(err) != exitcode.Unavailable {
		t.Fatalf("exit = %d (%v), want unavailable: a null page must not read as no mentions", exitCodeFor(err), err)
	}
	var dto struct {
		Unavailable []struct{ Section string } `json:"unavailable"`
	}
	_ = json.Unmarshal([]byte(out), &dto)
	if len(dto.Unavailable) != 1 || dto.Unavailable[0].Section != "mentions" {
		t.Errorf("unavailable = %+v, want mentions", dto.Unavailable)
	}
}

func TestSessionRecoverNeedsABinding2062(t *testing.T) {
	teamGitDir(t)
	srv, calls := attnServer(t, map[string]string{})
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"team", "session", "recover", "--server", srv.URL})
	err := root.Execute()
	if got := exitCodeFor(err); got != exitcode.Usage {
		t.Fatalf("exit = %d (%v), want usage", got, err)
	}
	if !strings.Contains(err.Error(), "session start") {
		t.Errorf("must name the remedy: %v", err)
	}
	if len(*calls) != 0 {
		t.Errorf("an unbound recover must make no request: %v", opsOf(*calls))
	}
}

func TestSessionRecoverTextOutput2062(t *testing.T) {
	out, _, err := runRecover2062(t, recoverResponses2062())
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"worker: Iris", "open tasks: 1", "Fix the flaky CI build", "state note:", "standing directions (verbatim):\nNever merge.", "mentions after watermark #4: 2"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q:\n%s", want, out)
		}
	}
}

func TestSessionRecoverStateNoteFailureIsLoud2062(t *testing.T) {
	for _, op := range []string{"GetMemory", "GetNode"} {
		r := recoverResponses2062()
		r[op] = `{"errors":[{"message":"boom","extensions":{"code":"INTERNAL_SERVER_ERROR"}}]}`
		out, _, err := runRecover2062(t, r, "--json")
		if exitCodeFor(err) != exitcode.Unavailable {
			t.Fatalf("%s failing: exit = %d (%v), want unavailable", op, exitCodeFor(err), err)
		}
		var dto struct {
			Unavailable []struct{ Section string } `json:"unavailable"`
		}
		_ = json.Unmarshal([]byte(out), &dto)
		if len(dto.Unavailable) != 1 || dto.Unavailable[0].Section != "stateNote" {
			t.Errorf("%s failing: unavailable = %+v, want stateNote", op, dto.Unavailable)
		}
	}
}
