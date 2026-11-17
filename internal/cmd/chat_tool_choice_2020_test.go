package cmd

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/exitcode"
)

// cli#2020 (hadron-server#4042/#4151, cor:dmo:050:10 Proposed, cor:acl:030:01):
// the two decision-only tool-choice reads. The CLI reads the server's decision
// and never makes one: inputs go through verbatim, the answer is printed as
// returned, and a chat the caller may not read answers as a missing one.

func runToolChoice2020(t *testing.T, responses map[string]string, args ...string) (map[string]json.RawMessage, string, error) {
	t.Helper()
	gql, captured := captureGraphQL(t, responses)
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs(append(args, "--server", gql.URL))
	err := root.Execute()
	return captured, out.String(), err
}

func TestChatToolChoiceReadsTheDecision2020(t *testing.T) {
	for _, tc := range []struct {
		name, resp, wantText, wantJSON string
	}{
		{"auto", `{"data":{"chatEffectiveToolChoice":"AUTO"}}`, "AUTO", `"toolChoice": "AUTO"`},
		{"forced", `{"data":{"chatEffectiveToolChoice":"FORCED"}}`, "FORCED", `"toolChoice": "FORCED"`},
		// Null means nothing resolved: a clean result, not an error and not a
		// not-found, with no reason invented for it.
		{"null", `{"data":{"chatEffectiveToolChoice":null}}`, "not resolved", `"toolChoice": null`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			captured, out, err := runToolChoice2020(t, map[string]string{"ChatEffectiveToolChoice": tc.resp}, "chat", "tool-choice", "chat_1")
			if err != nil || !strings.Contains(out, tc.wantText) {
				t.Fatalf("text: %v\n%s", err, out)
			}
			if v := unmarshalVars(t, captured["ChatEffectiveToolChoice"]); v["chatId"] != "chat_1" {
				t.Errorf("the chat id goes through verbatim: %v", v)
			}
			_, out, err = runToolChoice2020(t, map[string]string{"ChatEffectiveToolChoice": tc.resp}, "chat", "tool-choice", "chat_1", "--json")
			if err != nil || !strings.Contains(out, tc.wantJSON) || !strings.Contains(out, `"chatId": "chat_1"`) {
				t.Fatalf("json: %v\n%s", err, out)
			}
			// The answer is the decision only: nothing joined to it.
			for _, banned := range []string{"provider", "model", "apiKey", "configId", "name"} {
				if strings.Contains(out, `"`+banned+`"`) {
					t.Errorf("the output must carry the decision only, found %q:\n%s", banned, out)
				}
			}
		})
	}
}

// A chat the caller may not read is answered exactly as a missing one: the CLI
// maps both to the same exit and the same message and adds no hint.
func TestChatToolChoiceUnreadableChatIsAMissingChat2020(t *testing.T) {
	refusal := `{"errors":[{"message":"Chat not found.","extensions":{"code":"NOT_FOUND"}}],"data":{"chatEffectiveToolChoice":null}}`
	var msgs []string
	for _, id := range []string{"chat_missing", "chat_not_readable"} {
		_, out, err := runToolChoice2020(t, map[string]string{"ChatEffectiveToolChoice": refusal}, "chat", "tool-choice", id)
		if exitCodeFor(err) != exitcode.NotFound || out != "" {
			t.Fatalf("%s: exit %d out %q err %v", id, exitCodeFor(err), out, err)
		}
		msgs = append(msgs, err.Error())
		for _, hint := range []string{"forbidden", "not authorized", "exists", "permission"} {
			if strings.Contains(strings.ToLower(err.Error()), hint) {
				t.Errorf("no hint of which chats exist: %q", err)
			}
		}
	}
	if msgs[0] != msgs[1] {
		t.Errorf("the two refusals must read the same: %q vs %q", msgs[0], msgs[1])
	}
}

func TestAiConfigToolChoiceForAPair2020(t *testing.T) {
	resp := map[string]string{"ChatToolChoiceFor": `{"data":{"chatToolChoiceFor":"FORCED"}}`}
	// Verbatim, whatever the spelling: no trimming, no case change, no list.
	captured, out, err := runToolChoice2020(t, resp, "ai-config", "tool-choice", "OpenAI", " gpt-X ", "--json")
	if err != nil {
		t.Fatal(err)
	}
	v := unmarshalVars(t, captured["ChatToolChoiceFor"])
	if v["provider"] != "OpenAI" || v["model"] != " gpt-X " {
		t.Errorf("provider and model go through verbatim: %v", v)
	}
	if !strings.Contains(out, `"toolChoice": "FORCED"`) || !strings.Contains(out, `"provider": "OpenAI"`) {
		t.Errorf("json: %s", out)
	}
	// No owner context is asked for, or sent.
	for _, banned := range []string{"appRef", "orgRef", "ownerId"} {
		if _, ok := v[banned]; ok {
			t.Errorf("an owner argument %q must not be sent", banned)
		}
	}
	// An unknown pair answering FORCED is a normal result: no warning.
	_, text, err := runToolChoice2020(t, resp, "ai-config", "tool-choice", "nobody", "nothing")
	if err != nil || !strings.Contains(text, "FORCED") || strings.Contains(strings.ToLower(text), "warn") {
		t.Fatalf("%v\n%s", err, text)
	}
}

// An enum member the CLI does not know fails clearly: never mapped to a default.
func TestToolChoiceUnknownMemberFailsClearly2020(t *testing.T) {
	for _, args := range [][]string{{"chat", "tool-choice", "chat_1"}, {"ai-config", "tool-choice", "p", "m"}} {
		resp := map[string]string{
			"ChatEffectiveToolChoice": `{"data":{"chatEffectiveToolChoice":"SOMETIMES"}}`,
			"ChatToolChoiceFor":       `{"data":{"chatToolChoiceFor":"SOMETIMES"}}`,
		}
		_, out, err := runToolChoice2020(t, resp, args...)
		if exitCodeFor(err) != exitcode.Error || out != "" || err == nil || !strings.Contains(err.Error(), "SOMETIMES") {
			t.Errorf("%v: exit %d out %q err %v", args, exitCodeFor(err), out, err)
		}
	}
}

func TestToolChoiceArgumentsAreRequired2020(t *testing.T) {
	for _, args := range [][]string{{"chat", "tool-choice"}, {"ai-config", "tool-choice", "only-provider"}} {
		captured, _, err := runToolChoice2020(t, map[string]string{}, args...)
		if exitCodeFor(err) != exitcode.Usage || len(captured) != 0 {
			t.Errorf("%v: exit %d, %d calls", args, exitCodeFor(err), len(captured))
		}
	}
}
