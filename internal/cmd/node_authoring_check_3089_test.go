package cmd

import (
	"encoding/json"
	"strings"
	"testing"
)

// cli#3089 — `node authoring-check` reaches Query.nodeAuthoringCheck
// (cor:dmo:060:17): a read-only preview of one proposed write's authoring
// attestation check. One request, nothing written.

const authoringCheckRefused3089 = `{"data":{"nodeAuthoringCheck":{"state":"REFUSED","code":"AUTHORING_ATTESTATION_STALE","message":"the claim names an old revision","role":"spec","recovery":{"taskAccess":"READABLE","task":"hrn:node:acme.com:kb:tasks:author-spec","taskId":"t1","taskRevision":7,"load":{"cli":"hadron node get hrn:node:acme.com:kb:tasks:author-spec","graphql":"query { node }","mcp":null}}}}}`

func runAuthoringCheck3089(t *testing.T, response string, args ...string) (map[string]json.RawMessage, string, error) {
	t.Helper()
	srv, captured := captureGraphQL(t, map[string]string{"NodeAuthoringCheck": response})
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs(append(append([]string{"node", "authoring-check"}, args...), "--json", "--server", srv.URL))
	err := root.Execute()
	return captured, out.String(), err
}

// The request variables and the --json shape are pinned: a refusal's recovery
// passes through verbatim, and a bare loc with -m goes as memoryRef+loc so a
// not-yet-existing node can be checked as a create.
func TestNodeAuthoringCheckRequestAndJSONShape3089(t *testing.T) {
	captured, out, err := runAuthoringCheck3089(t, authoringCheckRefused3089,
		"specs/new", "-m", "acme.com::kb", "--role", "spec", "--runnable=false",
		"--authored-with", "task1", "--authored-revision", "3")
	if err != nil {
		t.Fatal(err)
	}
	var vars struct {
		Input map[string]json.RawMessage `json:"input"`
	}
	if err := json.Unmarshal(captured["NodeAuthoringCheck"], &vars); err != nil {
		t.Fatal(err)
	}
	want := map[string]string{
		"memoryRef":    `"hrn:mem:acme.com:kb"`,
		"loc":          `"specs/new"`,
		"role":         `"spec"`,
		"isRunnable":   `false`,
		"authoredWith": `{"task":"task1","revision":3}`,
	}
	for k, v := range want {
		if got := string(vars.Input[k]); got != v {
			t.Errorf("input.%s = %s, want %s (all: %v)", k, got, v, vars.Input)
		}
	}
	for _, absent := range []string{"ref", "changesAuthoredContent"} {
		if _, ok := vars.Input[absent]; ok {
			t.Errorf("input.%s sent but not asked for: %v", absent, vars.Input)
		}
	}
	var dto struct {
		State    string  `json:"state"`
		Code     *string `json:"code"`
		Message  *string `json:"message"`
		Role     *string `json:"role"`
		Recovery *struct{ TaskAccess, Task string }
	}
	if err := json.Unmarshal([]byte(out), &dto); err != nil {
		t.Fatalf("not JSON: %v\n%s", err, out)
	}
	if dto.State != "REFUSED" || dto.Code == nil || *dto.Code != "AUTHORING_ATTESTATION_STALE" || dto.Recovery == nil || dto.Recovery.Task != "hrn:node:acme.com:kb:tasks:author-spec" {
		t.Fatalf("dto = %s", out)
	}
	if !strings.Contains(out, `"taskRevision": 7`) && !strings.Contains(out, `"taskRevision":7`) {
		t.Fatalf("recovery revision not passed through: %s", out)
	}
}

// A qualified node ref goes as `ref`; --role "" (as node update) is an explicit null; a
// no-content-change check says so.
func TestNodeAuthoringCheckRefAndTriStates3089(t *testing.T) {
	captured, _, err := runAuthoringCheck3089(t, `{"data":{"nodeAuthoringCheck":{"state":"PASS","code":null,"message":null,"role":null,"recovery":null}}}`,
		"hrn:node:acme.com:kb:specs:x", "--role", "", "--no-content-change")
	if err != nil {
		t.Fatal(err)
	}
	var vars struct {
		Input map[string]json.RawMessage `json:"input"`
	}
	_ = json.Unmarshal(captured["NodeAuthoringCheck"], &vars)
	if string(vars.Input["ref"]) != `"hrn:node:acme.com:kb:specs:x"` || string(vars.Input["role"]) != `null` || string(vars.Input["changesAuthoredContent"]) != `false` {
		t.Fatalf("input = %v", vars.Input)
	}
	if _, ok := vars.Input["memoryRef"]; ok {
		t.Fatalf("memoryRef sent with ref: %v", vars.Input)
	}
}

// UNAVAILABLE is an answer: exit 0, its own state, never a pass or not-found.
func TestNodeAuthoringCheckUnavailableIsAnAnswer3089(t *testing.T) {
	_, out, err := runAuthoringCheck3089(t, `{"data":{"nodeAuthoringCheck":{"state":"UNAVAILABLE","code":null,"message":null,"role":null,"recovery":null}}}`,
		"hrn:node:acme.com:kb:specs:x")
	if err != nil {
		t.Fatalf("UNAVAILABLE must not be an error: %v", err)
	}
	if !strings.Contains(out, `"UNAVAILABLE"`) || strings.Contains(out, `"PASS"`) {
		t.Fatalf("out = %s", out)
	}
}

// Human output: a pass is a preview, never a promise; UNAVAILABLE is not a pass.
func TestNodeAuthoringCheckHumanWording3089(t *testing.T) {
	for state, response := range map[string]string{
		"PASS":        `{"data":{"nodeAuthoringCheck":{"state":"PASS","code":null,"message":null,"role":null,"recovery":null}}}`,
		"UNAVAILABLE": `{"data":{"nodeAuthoringCheck":{"state":"UNAVAILABLE","code":null,"message":null,"role":null,"recovery":null}}}`,
		"REFUSED":     authoringCheckRefused3089,
	} {
		srv, _ := captureGraphQL(t, map[string]string{"NodeAuthoringCheck": response})
		f, out := testFactory(t)
		root := NewRootCmd(f)
		root.SetArgs([]string{"node", "authoring-check", "hrn:node:acme.com:kb:specs:x", "--server", srv.URL})
		if err := root.Execute(); err != nil {
			t.Fatal(err)
		}
		text := out.String()
		if !strings.Contains(text, state) {
			t.Errorf("%s: state not shown: %q", state, text)
		}
		low := strings.ToLower(text)
		if strings.Contains(low, "will succeed") || strings.Contains(low, "proof") && !strings.Contains(low, "not proof") {
			t.Errorf("%s: implies a guarantee: %q", state, text)
		}
		if state == "UNAVAILABLE" && strings.Contains(text, "PASS") {
			t.Errorf("UNAVAILABLE shown as a pass: %q", text)
		}
		if state == "REFUSED" && !strings.Contains(text, "hrn:node:acme.com:kb:tasks:author-spec") {
			t.Errorf("recovery task missing: %q", text)
		}
	}
}

// A caller who cannot read the node or memory gets the mapped not-found exit.
func TestNodeAuthoringCheckNotFoundExit3089(t *testing.T) {
	for _, code := range []string{"NODE_NOT_FOUND", "MEMORY_NOT_FOUND"} {
		_, _, err := runAuthoringCheck3089(t, `{"errors":[{"message":"not found","extensions":{"code":"`+code+`"}}]}`,
			"hrn:node:acme.com:kb:specs:x")
		if err == nil || exitCodeFor(err) != 4 {
			t.Fatalf("%s: err=%v exit=%d, want exit 4", code, err, exitCodeFor(err))
		}
	}
}

// Local refusals send nothing.
func TestNodeAuthoringCheckLocalRefusals3089(t *testing.T) {
	for _, args := range [][]string{
		{"specs/x"}, // bare loc with no -m
		{"hrn:node:acme.com:kb:x", "--authored-with", "t1"},
		{"hrn:node:acme.com:kb:x", "--authored-revision", "2"},
		{"hrn:node:acme.com:kb:x", "--authored-with", "t1", "--authored-revision", "2", "--memory", " "},
	} {
		captured, _, err := runAuthoringCheck3089(t, authoringCheckRefused3089, args...)
		if err == nil || exitCodeFor(err) != 2 {
			t.Errorf("%v: err=%v exit=%d, want usage (2)", args, err, exitCodeFor(err))
		}
		if _, sent := captured["NodeAuthoringCheck"]; sent {
			t.Errorf("%v: a request was sent", args)
		}
	}
}

// Omission is a state: with no flag, input carries NO role, isRunnable,
// changesAuthoredContent or authoredWith key (keep-own, never set-empty), and a
// whitespace-only --role clears as node update does.
func TestNodeAuthoringCheckOmissionAndBlankRole3089(t *testing.T) {
	pass := `{"data":{"nodeAuthoringCheck":{"state":"PASS","code":null,"message":null,"role":null,"recovery":null}}}`
	inputOf := func(args ...string) map[string]json.RawMessage {
		captured, _, err := runAuthoringCheck3089(t, pass, args...)
		if err != nil {
			t.Fatal(err)
		}
		var vars struct {
			Input map[string]json.RawMessage `json:"input"`
		}
		_ = json.Unmarshal(captured["NodeAuthoringCheck"], &vars)
		return vars.Input
	}
	in := inputOf("hrn:node:acme.com:kb:specs:x")
	for _, k := range []string{"role", "isRunnable", "changesAuthoredContent", "authoredWith", "memoryRef", "loc"} {
		if _, ok := in[k]; ok {
			t.Errorf("input.%s sent although not asked for: %v", k, in)
		}
	}
	if string(inputOf("hrn:node:acme.com:kb:specs:x", "--role", "  ")["role"]) != "null" {
		t.Errorf("whitespace-only --role must clear (null), as node update does")
	}
	if string(inputOf("hrn:node:acme.com:kb:specs:x", "--runnable")["isRunnable"]) != "true" {
		t.Errorf("--runnable must send true")
	}
}

// An id and an action URN go as ref; an out-of-range revision is refused
// locally; the unreadable-task recovery and an unknown state render honestly.
func TestNodeAuthoringCheckRoutesAndRendersEdges3089(t *testing.T) {
	id := strings.Repeat("a", 32)
	for _, ref := range []string{id, "hrn:action:acme.com:agent:code"} {
		captured, _, err := runAuthoringCheck3089(t, `{"data":{"nodeAuthoringCheck":{"state":"PASS","code":null,"message":null,"role":null,"recovery":null}}}`, ref)
		if err != nil {
			t.Fatal(err)
		}
		var vars struct {
			Input map[string]json.RawMessage `json:"input"`
		}
		_ = json.Unmarshal(captured["NodeAuthoringCheck"], &vars)
		if string(vars.Input["ref"]) != `"`+ref+`"` {
			t.Errorf("%s: input = %v", ref, vars.Input)
		}
	}
	captured, _, err := runAuthoringCheck3089(t, authoringCheckRefused3089, "hrn:node:acme.com:kb:x", "--authored-with", "t1", "--authored-revision", "2147483648")
	if err == nil || exitCodeFor(err) != 2 {
		t.Errorf("out-of-range revision: err=%v", err)
	}
	if _, sent := captured["NodeAuthoringCheck"]; sent {
		t.Error("out-of-range revision sent a request")
	}
	for name, resp := range map[string]string{
		"unreadable task": `{"data":{"nodeAuthoringCheck":{"state":"REFUSED","code":"AUTHOR_TASK_BROKEN","message":"m","role":"spec","recovery":{"taskAccess":"UNAVAILABLE","task":null,"taskId":null,"taskRevision":null,"load":null}}}}`,
		"unknown state":   `{"data":{"nodeAuthoringCheck":{"state":"FUTURE","code":null,"message":null,"role":null,"recovery":null}}}`,
	} {
		srv, _ := captureGraphQL(t, map[string]string{"NodeAuthoringCheck": resp})
		f, out := testFactory(t)
		root := NewRootCmd(f)
		root.SetArgs([]string{"node", "authoring-check", "hrn:node:acme.com:kb:specs:x", "--server", srv.URL})
		if err := root.Execute(); err != nil {
			t.Fatal(err)
		}
		text := out.String()
		if name == "unreadable task" && !strings.Contains(text, "unavailable to you") {
			t.Errorf("%s: %q", name, text)
		}
		if name == "unknown state" && strings.TrimSpace(text) != "FUTURE" {
			t.Errorf("%s: %q", name, text)
		}
	}
}
