package cmd

// hadron-cli#1520 — typed task references (server#2649, cor:agt:050:01/:09).
// Red first: the --ref / --clear-refs / --ref-kind flags and the DTO field.

import (
	"encoding/json"
	"strings"
	"testing"
)

const refsApp1520 = "capp100000000000000000000"

func sentOp1520(t *testing.T, calls *[]attnCall, op string) attnCall {
	t.Helper()
	var got []attnCall
	for _, c := range *calls {
		if c.Op == op {
			got = append(got, c)
		}
	}
	if len(got) != 1 {
		t.Fatalf("%s requests = %d, want 1", op, len(got))
	}
	return got[0]
}

func TestTaskCreateRefsInOrder1520(t *testing.T) {
	calls, _, err := runTeamTask(t, taskResponses(), "team", "task", "create", "T",
		"--ref", "issue=https://github.com/o/r/issues/1", "--ref", "NODE=hrn:node:x:y:z|spec", "--ref", "pull-request=https://github.com/o/r/pull/2", "--ref", "pull_request=https://github.com/o/r/pull/3", "--ref", "task=hrn:agenttask:a:AT1", "--ref", "url=https://example.com")
	if err != nil {
		t.Fatal(err)
	}
	in := sentOp1520(t, calls, "AgentTaskCreate").Vars["input"].(map[string]any)
	b, _ := json.Marshal(in["references"])
	want := `[{"kind":"ISSUE","target":"https://github.com/o/r/issues/1"},{"kind":"NODE","label":"spec","target":"hrn:node:x:y:z"},{"kind":"PULL_REQUEST","target":"https://github.com/o/r/pull/2"},{"kind":"PULL_REQUEST","target":"https://github.com/o/r/pull/3"},{"kind":"TASK","target":"hrn:agenttask:a:AT1"},{"kind":"URL","target":"https://example.com"}]`
	if string(b) != want {
		t.Fatalf("references = %s\nwant %s", b, want)
	}
	// No --ref: the key is absent.
	calls, _, err = runTeamTask(t, taskResponses(), "team", "task", "create", "T")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(sentOp1520(t, calls, "AgentTaskCreate").RawVars), "references") {
		t.Fatal("create without --ref sent a references key")
	}
}

func TestTaskUpdateRefsPatch1520(t *testing.T) {
	in, _ := updateInput1137(t, "AT1.2", "--ref", "url=https://example.com")
	if compact1520(string(in["references"])) != `[{"kind":"URL","target":"https://example.com"}]` {
		t.Fatalf("--ref alone: %s", in["references"])
	}
	in, _ = updateInput1137(t, "AT1.2", "--clear-refs")
	if string(in["references"]) != `[]` && string(in["references"]) != `null` {
		t.Fatalf("--clear-refs: %s", in["references"])
	}
	if string(in["references"]) != `[]` {
		t.Fatalf("--clear-refs must send [] (the spec's explicit clear): %s", in["references"])
	}
	in, _ = updateInput1137(t, "AT1.2", "--title", "x")
	if _, ok := in["references"]; ok {
		t.Fatal("an update without either flag sent references")
	}
	calls, _, err := runTeamTask(t, taskUpdateResponses1137(), "team", "task", "update", "AT1.2", "--ref", "url=https://example.com", "--clear-refs")
	if err == nil || exitCodeFor(err) != 2 || !strings.Contains(err.Error(), "--clear-refs") {
		t.Fatalf("--ref with --clear-refs: want usage refusal, got %v", err)
	}
	for _, c := range *calls {
		if c.Op == "AgentTaskUpdate" {
			t.Fatal("a request was sent")
		}
	}
}

func TestTaskRefMalformedRefusedBeforeAnyCall1520(t *testing.T) {
	for _, v := range []string{"no-equals", "bogus=https://x", "=https://x", "issue=", "issue=|label"} {
		for _, argv := range [][]string{{"team", "task", "create", "T", "--ref", v}, {"team", "task", "update", "AT1.2", "--ref", v}} {
			calls, _, err := runTeamTask(t, taskUpdateResponses1137(), argv...)
			if err == nil || exitCodeFor(err) != 2 {
				t.Fatalf("%v: want usage exit, got %v", argv, err)
			}
			if !strings.Contains(err.Error(), "pull-request") {
				t.Fatalf("%v: the refusal must list the kinds: %v", argv, err)
			}
			for _, c := range *calls {
				if strings.HasPrefix(c.Op, "AgentTaskCreate") || c.Op == "AgentTaskUpdate" {
					t.Fatalf("%v: a write was sent", argv)
				}
			}
		}
	}
}

func refsTaskResponse1520(refs string) map[string]string {
	r := taskResponses()
	body := strings.Replace(taskFieldsJSON, `"tags":["ci"],`, `"tags":["ci"],"references":`+refs+`,`, 1)
	r["AgentTaskGet"] = `{"data":{"agentTask":` + body + `}}`
	return r
}

func TestTaskGetJSONReferences1520(t *testing.T) {
	_, out, err := runTeamTask(t, taskResponses(), "team", "task", "get", "AT1.2", "--json")
	if err != nil {
		t.Fatal(err)
	}
	var dto struct {
		References *[]json.RawMessage `json:"references"`
	}
	if err := json.Unmarshal([]byte(out), &dto); err != nil {
		t.Fatal(err)
	}
	if dto.References == nil || len(*dto.References) != 0 || !strings.Contains(out, `"references": []`) {
		t.Fatalf("no references must render []: %s", out)
	}
	_, out, err = runTeamTask(t, refsTaskResponse1520(`[{"kind":"ISSUE","target":"https://github.com/o/r/issues/1","label":null},{"kind":"NODE","target":"hrn:node:x:y:z","label":"spec"}]`), "team", "task", "get", "AT1.2", "--json")
	if err != nil {
		t.Fatal(err)
	}
	var ordered struct {
		References []struct {
			Kind, Target string
			Label        *string
		} `json:"references"`
	}
	if err := json.Unmarshal([]byte(out), &ordered); err != nil {
		t.Fatal(err)
	}
	r := ordered.References
	if len(r) != 2 || r[0].Kind != "ISSUE" || r[0].Label != nil || r[1].Kind != "NODE" || r[1].Target != "hrn:node:x:y:z" || r[1].Label == nil || *r[1].Label != "spec" {
		t.Fatalf("references in order, null label when absent: %s", out)
	}
	if !strings.Contains(out, `"label": null`) {
		t.Fatalf("an absent label must be an explicit null in --json: %s", out)
	}
	_, out, err = runTeamTask(t, refsTaskResponse1520(`[{"kind":"ISSUE","target":"https://github.com/o/r/issues/1","label":null},{"kind":"NODE","target":"hrn:node:x:y:z","label":"spec"}]`), "team", "task", "get", "AT1.2")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"ISSUE https://github.com/o/r/issues/1\n", "NODE hrn:node:x:y:z (spec)\n"} {
		if !strings.Contains(out, want) {
			t.Fatalf("human view lacks %q:\n%s", want, out)
		}
	}
}

func compact1520(s string) string {
	var b strings.Builder
	var buf json.RawMessage = []byte(s)
	var v any
	if json.Unmarshal(buf, &v) != nil {
		return s
	}
	out, _ := json.Marshal(v)
	b.Write(out)
	return b.String()
}

func TestTaskRenderingHostileTargetsUnchanged1520(t *testing.T) {
	bidi := "https://example.com/\u202eevil"
	userinfo := "https://a@b/x"
	refs := `[{"kind":"URL","target":` + mustJSON1250(bidi) + `,"label":null},{"kind":"URL","target":` + mustJSON1250(userinfo) + `,"label":null}]`
	_, out, err := runTeamTask(t, refsTaskResponse1520(refs), "team", "task", "get", "AT1.2", "--json")
	if err != nil {
		t.Fatal(err)
	}
	var dto struct {
		References []struct{ Target string } `json:"references"`
	}
	if err := json.Unmarshal([]byte(out), &dto); err != nil {
		t.Fatal(err)
	}
	if len(dto.References) != 2 || dto.References[0].Target != bidi || dto.References[1].Target != userinfo {
		t.Fatalf("--json must carry the targets verbatim: %+v", dto.References)
	}
	_, human, err := runTeamTask(t, refsTaskResponse1520(refs), "team", "task", "get", "AT1.2")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(human, userinfo+"\n") || strings.Contains(human, "\x1b") {
		t.Fatalf("human view must print the userinfo target unchanged and add no colour:\n%q", human)
	}
}

func TestTaskListRefFilter1520(t *testing.T) {
	vars, ok, err := filters1250Run(t, "AgentTaskListFiltered", "--ref", "https://github.com/o/r/pull/9", "--ref-kind", "pull-request")
	if err != nil || !ok {
		t.Fatalf("ok=%v err=%v", ok, err)
	}
	b, _ := json.Marshal(vars["reference"])
	if string(b) != `{"kind":"PULL_REQUEST","target":"https://github.com/o/r/pull/9"}` {
		t.Fatalf("reference = %s", b)
	}
	vars, ok, err = filters1250Run(t, "AgentTaskListFiltered", "--ref", "hrn:node:x:y:z")
	if err != nil || !ok {
		t.Fatalf("ok=%v err=%v", ok, err)
	}
	b, _ = json.Marshal(vars["reference"])
	if string(b) != `{"target":"hrn:node:x:y:z"}` {
		t.Fatalf("kind must be omitted when not given: %s", b)
	}
}

func TestTaskListRefRefusals1520(t *testing.T) {
	for _, args := range [][]string{{"--ref-kind", "issue"}, {"--ref", "x", "--ref-kind", "bogus"}, {"--team", "--ref", "x"}, {"--team", "--ref-kind", "issue"}} {
		srv, captured := captureGraphQL(t, map[string]string{})
		f, _ := testFactory(t)
		r := NewRootCmd(f)
		r.SetArgs(append([]string{"team", "task", "list", "--app", refsApp1520, "--server", srv.URL}, args...))
		err := r.Execute()
		if err == nil || exitCodeFor(err) != 2 {
			t.Fatalf("%v: want usage refusal, got %v", args, err)
		}
		if args[0] == "--team" && !strings.Contains(err.Error(), "does not combine with --team") {
			t.Fatalf("%v: the team view must refuse the server filter by name, got %v", args, err)
		}
		if len(captured) != 0 {
			t.Fatalf("%v: request sent: %v", args, captured)
		}
	}
}

func TestTaskRefServerRefusalMapsExit1520(t *testing.T) {
	r := taskResponses()
	r["AgentTaskCreate"] = memberGQLError("references[0]: target is not a valid issue URL", "BAD_USER_INPUT")
	_, _, err := runTeamTask(t, r, "team", "task", "create", "T", "--ref", "issue=not-a-url")
	if err == nil || exitCodeFor(err) != 2 || !strings.Contains(err.Error(), "BAD_USER_INPUT") {
		t.Fatalf("want exit 2 with the code, got %v", err)
	}
	_, out, err := runTeamTask(t, r, "team", "task", "create", "T", "--ref", "issue=not-a-url", "--json")
	if err == nil || !strings.Contains(out+err.Error(), "BAD_USER_INPUT") {
		t.Fatalf("json envelope: out=%q err=%v", out, err)
	}
}
