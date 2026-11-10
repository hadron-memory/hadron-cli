package cmd

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/exitcode"
)

// cli#1853: `agent boot-prompt` is one agentServedBootPrompt call; the text is
// printed byte for byte and each typed refusal has an explicit exit code.

const servedJSON = `{"data":{"agentServedBootPrompt":{"decision":{"compiledContentHash":"h1","compiledNodeId":"n1","pointerLevel":"AGENT","provenanceRevSeq":2,"servedRevSeq":3},"text":"  You are Bot.\n\ttrailing  \n"}}}`

func TestAgentBootPromptSendsOnlyWhatWasAsked(t *testing.T) {
	calls, out, err := runAgentSub(t, map[string]string{"AgentServedBootPrompt": servedJSON}, "boot-prompt", "hrn:agent:acme.com:bot")
	if err != nil {
		t.Fatal(err)
	}
	if len(calls.ops) != 1 || calls.ops[0] != "AgentServedBootPrompt" {
		t.Fatalf("ops=%v", calls.ops)
	}
	v := calls.vars["AgentServedBootPrompt"]
	if v["agentRef"] != "hrn:agent:acme.com:bot" {
		t.Fatalf("vars=%v", v)
	}
	// genqlient sends an unset optional as null, which the server reads as absent.
	if v["appRef"] != nil {
		t.Fatalf("appRef must be unset without --app (the ambient App is never used): %v", v)
	}
	if v["llmModel"] != nil {
		t.Fatalf("llmModel must be unset without --model: %v", v)
	}
	if out != "  You are Bot.\n\ttrailing  \n" {
		t.Fatalf("text must be byte for byte: %q", out)
	}
}

func TestAgentBootPromptForwardsAppAndModel(t *testing.T) {
	calls, _, err := runAgentSub(t, map[string]string{"AgentServedBootPrompt": servedJSON},
		"boot-prompt", "agt1", "--app", "hrn:app:acme.com:team", "--model", "claude-opus-5-5")
	if err != nil {
		t.Fatal(err)
	}
	v := calls.vars["AgentServedBootPrompt"]
	if v["appRef"] != "hrn:app:acme.com:team" || v["llmModel"] != "claude-opus-5-5" {
		t.Fatalf("vars=%v", v)
	}
}

func TestAgentBootPromptBlankFlagsRefuseBeforeAnyCall(t *testing.T) {
	for _, args := range [][]string{{"boot-prompt", "agt1", "--model", " "}, {"boot-prompt", "agt1", "--app", " "}, {"boot-prompt", " "}} {
		calls, _, err := runAgentSub(t, map[string]string{"AgentServedBootPrompt": servedJSON}, args...)
		if err == nil || exitCodeFor(err) != exitcode.Usage || len(calls.ops) != 0 {
			t.Fatalf("%v: err=%v ops=%v", args, err, calls.ops)
		}
	}
}

func TestAgentBootPromptJSONShapeIsPinned(t *testing.T) {
	_, out, err := runAgentSub(t, map[string]string{"AgentServedBootPrompt": servedJSON}, "boot-prompt", "agt1", "--json")
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("not JSON: %v\n%s", err, out)
	}
	if len(got) != 2 || got["text"] != "  You are Bot.\n\ttrailing  \n" {
		t.Fatalf("top level must be exactly {decision,text}: %v", got)
	}
	d, _ := got["decision"].(map[string]any)
	want := map[string]any{"pointerLevel": "AGENT", "compiledNodeId": "n1", "compiledContentHash": "h1", "provenanceRevSeq": float64(2), "servedRevSeq": float64(3)}
	if len(d) != len(want) {
		t.Fatalf("decision keys: %v", d)
	}
	for k, w := range want {
		if d[k] != w {
			t.Fatalf("decision.%s=%v want %v", k, d[k], w)
		}
	}
}

func TestAgentBootPromptRefusalsMapAndPrintNoPrompt(t *testing.T) {
	for code, want := range map[string]int{
		"AGENT_NOT_FOUND":            exitcode.NotFound,
		"BOOT_PROMPT_NOT_SET":        exitcode.NotFound,
		"BOOT_PROMPT_TARGET_DELETED": exitcode.Conflict,
		"BOOT_PROMPT_NOT_APPROVED":   exitcode.Conflict,
		"BOOT_PROMPT_NOT_SIGNED":     exitcode.Conflict,
	} {
		t.Run(code, func(t *testing.T) {
			calls, out, err := runAgentSub(t, map[string]string{
				"AgentServedBootPrompt": `{"errors":[{"message":"` + code + `: nope","extensions":{"code":"` + code + `"}}]}`,
			}, "boot-prompt", "agt1")
			if err == nil || exitCodeFor(err) != want {
				t.Fatalf("err=%v want exit %d", err, want)
			}
			if out != "" || len(calls.ops) != 1 {
				t.Fatalf("nothing printed, no follow-up: out=%q ops=%v", out, calls.ops)
			}
			if !strings.Contains(err.Error(), code) {
				t.Fatalf("message must carry the code: %v", err)
			}
		})
	}
}

func TestAgentBootPromptNeverPrintsEmptyPromptAsSuccess(t *testing.T) {
	empty := strings.Replace(servedJSON, `"text":"  You are Bot.\n\ttrailing  \n"`, `"text":""`, 1)
	_, out, err := runAgentSub(t, map[string]string{"AgentServedBootPrompt": empty}, "boot-prompt", "agt1")
	if err == nil || exitCodeFor(err) != exitcode.Error || out != "" {
		t.Fatalf("err=%v out=%q", err, out)
	}
}
