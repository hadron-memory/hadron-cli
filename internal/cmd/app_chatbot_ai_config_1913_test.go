package cmd

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/api/gen"
	"github.com/hadron-memory/hadron-cli/internal/exitcode"
)

// cli#1913 (hadron-server#3957, cor:dmo:050:10 / cor:dmo:050:12): show, set and
// clear the App's chatbot AI config, and list what it may be chosen from. The
// client pins here are CLIENT behaviour: authority, refusals and the choices
// list are the server's, and a stub cannot prove them.

const (
	cb1913App    = "hrn:app:acme.com:support"
	cb1913Pin    = "cfg-own-1"
	cb1913Meta   = `{"data":{"app":{"id":"app1","urn":"hrn:app:acme.com:support","name":"Support","appType":"TEAM","description":null}}}`
	cb1913Choice = `{"value":"cfg-own-1","name":"Support bot","isDefault":false,"isAppChatbotConfig":true,"provider":"anthropic","model":"claude-sonnet-5-5"}`
	cb1913Other  = `{"value":"cfg-own-2","name":"Cheap bot","isDefault":false,"isAppChatbotConfig":false,"provider":"openai","model":"gpt-6-luna"}`
	cb1913Dflt   = `{"value":"default","name":"default","isDefault":true,"isAppChatbotConfig":false,"provider":null,"model":null}`
)

func cb1913Choices(rows ...string) string {
	return `{"data":{"chatAiConfigChoices":[` + strings.Join(rows, ",") + `]}}`
}

func cb1913Pinned(id string) string {
	if id == "" {
		return `{"data":{"app":{"id":"app1","chatbotAiConfigId":null}}}`
	}
	return `{"data":{"app":{"id":"app1","chatbotAiConfigId":"` + id + `"}}}`
}

func cb1913Set(id string) string {
	pin := "null"
	if id != "" {
		pin = `"` + id + `"`
	}
	return `{"data":{"setAppChatbotAiConfig":{"id":"app1","urn":"hrn:app:acme.com:support","chatbotAiConfigId":` + pin + `}}}`
}

func cb1913Run(t *testing.T, responses map[string]string, args ...string) (string, map[string]json.RawMessage, func(string) int, error) {
	t.Helper()
	url, captured, count := mgCountingGraphQL(t, responses)
	out, err := mgRun(t, url, args...)
	return out, captured, count, err
}

// Show: the id and the server-returned name, JSON on the provider's field.
// The name comes from the narrow choices list, never the inherited walk.
func TestAppGetShowsChatbotAiConfigIdAndName1913(t *testing.T) {
	resp := map[string]string{
		"GetAppMetadata":        cb1913Meta,
		"GetAppChatbotAiConfig": cb1913Pinned(cb1913Pin),
		"ChatAiConfigChoices":   cb1913Choices(cb1913Choice, cb1913Other, cb1913Dflt),
	}
	out, _, count, err := cb1913Run(t, resp, "app", "get", cb1913App)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "chatbot AI config: Support bot (cfg-own-1)\n") {
		t.Fatalf("name and id missing:\n%s", out)
	}
	for _, op := range []string{"ResolveAIConfig", "AIConfigs", "ListAIConfigs"} {
		if count(op) != 0 {
			t.Errorf("%s was sent: the name must not come from the inherited walk", op)
		}
	}
	jout, _, _, err := cb1913Run(t, resp, "app", "get", cb1913App, "--json")
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.Unmarshal([]byte(jout), &got); err != nil {
		t.Fatal(err)
	}
	if got["chatbotAiConfigId"] != cb1913Pin || got["chatbotAiConfigName"] != "Support bot" || got["chatbotAiConfigListed"] != true {
		t.Fatalf("json = %v", got)
	}
	if got["id"] != "app1" || got["name"] != "Support" {
		t.Fatalf("unrelated fields must be preserved: %v", got)
	}
}

// A stored pin the App's enabled choices do not list (a disabled config is
// stored and skipped until enabled) is shown by id and never as the one in use.
func TestAppGetUnlistedPinIsNotLabelledInUse1913(t *testing.T) {
	resp := map[string]string{
		"GetAppMetadata":        cb1913Meta,
		"GetAppChatbotAiConfig": cb1913Pinned("cfg-disabled"),
		"ChatAiConfigChoices":   cb1913Choices(cb1913Other, cb1913Dflt),
	}
	out, _, _, err := cb1913Run(t, resp, "app", "get", cb1913App)
	if err != nil {
		t.Fatal(err)
	}
	want := "chatbot AI config: cfg-disabled (not among the App's enabled choices; a disabled config is skipped until enabled)\n"
	if !strings.Contains(out, want) {
		t.Fatalf("want %q in:\n%s", want, out)
	}
	if strings.Contains(out, "in use") {
		t.Fatalf("a stored pin the list does not carry must not be called in use:\n%s", out)
	}
	jout, _, _, _ := cb1913Run(t, resp, "app", "get", cb1913App, "--json")
	var got map[string]any
	_ = json.Unmarshal([]byte(jout), &got)
	if got["chatbotAiConfigId"] != "cfg-disabled" || got["chatbotAiConfigName"] != nil || got["chatbotAiConfigListed"] != false {
		t.Fatalf("json = %v", got)
	}
}

// No pin: a null id, a dash in text, and no choices read at all.
func TestAppGetNoChatbotPin1913(t *testing.T) {
	resp := map[string]string{"GetAppMetadata": cb1913Meta, "GetAppChatbotAiConfig": cb1913Pinned("")}
	out, _, count, err := cb1913Run(t, resp, "app", "get", cb1913App)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "chatbot AI config: -\n") {
		t.Fatalf("an unset pin shows a dash:\n%s", out)
	}
	if count("ChatAiConfigChoices") != 0 {
		t.Fatal("no pin, no choices read")
	}
	jout, _, _, _ := cb1913Run(t, resp, "app", "get", cb1913App, "--json")
	var got map[string]any
	_ = json.Unmarshal([]byte(jout), &got)
	if v, has := got["chatbotAiConfigId"]; !has || v != nil {
		t.Fatalf("chatbotAiConfigId must be an explicit null: %v", got)
	}
}

// A server that predates #3957 still serves app get: capability absence only.
func TestAppGetOldServerWithoutChatbotField1913(t *testing.T) {
	old := `{"errors":[{"message":"Cannot query field \"chatbotAiConfigId\" on type \"App\".","extensions":{"code":"GRAPHQL_VALIDATION_FAILED"}}]}`
	resp := map[string]string{"GetAppMetadata": cb1913Meta, "GetAppChatbotAiConfig": old}
	out, _, _, err := cb1913Run(t, resp, "app", "get", cb1913App, "--json")
	if err != nil {
		t.Fatalf("an old server must still serve app get: %v", err)
	}
	var got map[string]any
	_ = json.Unmarshal([]byte(out), &got)
	if v, has := got["chatbotAiConfigId"]; !has || v != nil {
		t.Fatalf("chatbotAiConfigId = %v", got)
	}
	boom := `{"errors":[{"message":"boom","extensions":{"code":"INTERNAL_SERVER_ERROR"}}]}`
	if _, _, _, err := cb1913Run(t, map[string]string{"GetAppMetadata": cb1913Meta, "GetAppChatbotAiConfig": boom}, "app", "get", cb1913App); err == nil {
		t.Fatal("a non-capability error must fail the command")
	}
}

// List: the narrow chatbot query only, the default entry is a marker with no
// invented config id, and nothing key-shaped is requested or printed.
func TestAppChatbotAiConfigListProjection1913(t *testing.T) {
	resp := map[string]string{"ChatAiConfigChoices": cb1913Choices(cb1913Choice, cb1913Other, cb1913Dflt)}
	out, captured, count, err := cb1913Run(t, resp, "app", "chatbot-ai-config", "list", cb1913App, "--json")
	if err != nil {
		t.Fatal(err)
	}
	if string(mgVars(t, captured["ChatAiConfigChoices"])["appRef"]) != `"`+cb1913App+`"` {
		t.Fatalf("appRef = %s", captured["ChatAiConfigChoices"])
	}
	for _, op := range []string{"AIConfigs", "ListAIConfigs", "ResolveAIConfig"} {
		if count(op) != 0 {
			t.Errorf("%s was sent: the inherited ai-config list must not back this list", op)
		}
	}
	var rows []map[string]any
	if err := json.Unmarshal([]byte(out), &rows); err != nil || len(rows) != 3 {
		t.Fatalf("rows = %v err %v\n%s", rows, err, out)
	}
	for _, r := range rows {
		for k := range r {
			switch k {
			case "value", "name", "isDefault", "isAppChatbotConfig", "provider", "model":
			default:
				t.Errorf("unexpected field %q in a chatbot choice row", k)
			}
		}
	}
	if rows[2]["isDefault"] != true || rows[2]["value"] != "default" || rows[2]["provider"] != nil {
		t.Fatalf("the default entry is a marker: %v", rows[2])
	}
	empty, _, _, err := cb1913Run(t, map[string]string{"ChatAiConfigChoices": cb1913Choices()}, "app", "chatbot-ai-config", "list", cb1913App, "--json")
	if err != nil || strings.TrimSpace(empty) != "[]" {
		t.Fatalf("empty list = %q err %v", empty, err)
	}
	text, _, _, _ := cb1913Run(t, resp, "app", "chatbot-ai-config", "list", cb1913App)
	if !strings.Contains(text, "Support bot") || !strings.Contains(text, "default") {
		t.Fatalf("text list:\n%s", text)
	}
}

// Set resolves a name or id among the App's OWN choices only and sends the id.
func TestAppChatbotAiConfigSetResolvesAmongOwnChoices1913(t *testing.T) {
	resp := map[string]string{
		"ChatAiConfigChoices":   cb1913Choices(cb1913Choice, cb1913Other, cb1913Dflt),
		"SetAppChatbotAiConfig": cb1913Set(cb1913Pin),
	}
	for _, arg := range []string{"Support bot", cb1913Pin} {
		_, captured, count, err := cb1913Run(t, resp, "app", "chatbot-ai-config", "set", cb1913App, arg)
		if err != nil {
			t.Fatalf("%s: %v", arg, err)
		}
		vars := mgVars(t, captured["SetAppChatbotAiConfig"])
		if string(vars["configId"]) != `"`+cb1913Pin+`"` || string(vars["appRef"]) != `"`+cb1913App+`"` {
			t.Fatalf("%s: vars = %v", arg, vars)
		}
		if count("SetAppChatbotAiConfig") != 1 {
			t.Fatalf("%s: exactly one mutation, got %d", arg, count("SetAppChatbotAiConfig"))
		}
	}
	out, _, _, _ := cb1913Run(t, resp, "app", "chatbot-ai-config", "set", cb1913App, "Support bot", "--json")
	var got map[string]any
	if err := json.Unmarshal([]byte(out), &got); err != nil || got["chatbotAiConfigId"] != cb1913Pin {
		t.Fatalf("json = %v err %v\n%s", got, err, out)
	}
}

// A ref the own-choices list does not carry is sent verbatim: the server alone
// answers a missing, foreign or deleted config (one refusal, no oracle), and a
// stored-disabled own config may still be set by id.
func TestAppChatbotAiConfigSetUnmatchedIsTheServersToRefuse1913(t *testing.T) {
	notFound := `{"errors":[{"message":"AI config not found","extensions":{"code":"AiServiceConfigNotFoundError"}}]}`
	resp := map[string]string{
		"ChatAiConfigChoices":   cb1913Choices(cb1913Choice, cb1913Dflt),
		"SetAppChatbotAiConfig": notFound,
	}
	_, captured, count, err := cb1913Run(t, resp, "app", "chatbot-ai-config", "set", cb1913App, "someone-elses")
	if err == nil {
		t.Fatal("the server's refusal must surface")
	}
	if got := exitCodeFor(err); got != exitcode.NotFound {
		t.Fatalf("exit = %d, want NotFound (%d): a missing, foreign and deleted config share one refusal", got, exitcode.NotFound)
	}
	if string(mgVars(t, captured["SetAppChatbotAiConfig"])["configId"]) != `"someone-elses"` || count("SetAppChatbotAiConfig") != 1 {
		t.Fatalf("an unmatched ref goes to the server once, as given: %s", captured["SetAppChatbotAiConfig"])
	}
}

// `default` is the walk marker, not a config: refused before any write, with
// the way to follow the default pointed at (clear). An empty value is a usage
// error with no request at all.
func TestAppChatbotAiConfigSetRefusesDefaultAndEmptyBeforeAnyRequest1913(t *testing.T) {
	resp := map[string]string{
		"ChatAiConfigChoices":   cb1913Choices(cb1913Choice, cb1913Dflt),
		"SetAppChatbotAiConfig": cb1913Set(""),
	}
	for _, arg := range []string{"default", "", "  "} {
		_, _, count, err := cb1913Run(t, resp, "app", "chatbot-ai-config", "set", cb1913App, arg)
		if err == nil || exitCodeFor(err) != exitcode.Usage {
			t.Fatalf("%q: want a usage error, got %v", arg, err)
		}
		if count("SetAppChatbotAiConfig") != 0 {
			t.Fatalf("%q: no mutation may be sent", arg)
		}
		if arg != "default" && count("ChatAiConfigChoices") != 0 {
			t.Fatalf("%q: an empty value is refused before any request", arg)
		}
	}
}

// Clear sends an explicit null, once, and removes only the selection.
func TestAppChatbotAiConfigClearSendsExplicitNull1913(t *testing.T) {
	resp := map[string]string{"SetAppChatbotAiConfig": cb1913Set("")}
	out, captured, count, err := cb1913Run(t, resp, "app", "chatbot-ai-config", "clear", cb1913App, "--json")
	if err != nil {
		t.Fatal(err)
	}
	vars := mgVars(t, captured["SetAppChatbotAiConfig"])
	if v, has := vars["configId"]; !has || string(v) != "null" {
		t.Fatalf("clear must send an explicit null configId, got %v", vars)
	}
	if count("SetAppChatbotAiConfig") != 1 {
		t.Fatalf("one mutation, got %d", count("SetAppChatbotAiConfig"))
	}
	for _, op := range []string{"DeleteAIConfig", "DeleteAiConfig"} {
		if count(op) != 0 {
			t.Fatalf("%s sent: clear removes only the selection", op)
		}
	}
	var got map[string]any
	if err := json.Unmarshal([]byte(out), &got); err != nil || got["chatbotAiConfigId"] != nil {
		t.Fatalf("json = %v err %v", got, err)
	}
}

// The server's refusal of a non-manager is shown, never pre-judged locally.
func TestAppChatbotAiConfigClearShowsTheServersRefusal1913(t *testing.T) {
	forbidden := `{"errors":[{"message":"not allowed","extensions":{"code":"FORBIDDEN"}}]}`
	_, _, count, err := cb1913Run(t, map[string]string{"SetAppChatbotAiConfig": forbidden}, "app", "chatbot-ai-config", "clear", cb1913App)
	if err == nil || exitCodeFor(err) == exitcode.OK {
		t.Fatalf("err = %v", err)
	}
	if count("SetAppChatbotAiConfig") != 1 {
		t.Fatal("the mutation is sent once and never retried")
	}
}

// The documents select what the commands render and nothing key-shaped
// (review:assert-the-query-not-the-capture): no key, preview, params, endpoint,
// and the pin read is its own operation (old-server compatibility).
func TestChatbotAiConfigOperationsSelectNoKeyMaterial1913(t *testing.T) {
	docs := map[string]string{
		"GetAppChatbotAiConfig": gen.GetAppChatbotAiConfig_Operation,
		"ChatAiConfigChoices":   gen.ChatAiConfigChoices_Operation,
		"SetAppChatbotAiConfig": gen.SetAppChatbotAiConfig_Operation,
	}
	for name, doc := range docs {
		lower := strings.ToLower(doc)
		for _, bad := range []string{"key", "preview", "params", "endpoint", "secret", "baseurl"} {
			if strings.Contains(lower, bad) {
				t.Errorf("%s selects %q:\n%s", name, bad, doc)
			}
		}
	}
	for _, f := range []string{"value", "name", "isDefault", "isAppChatbotConfig", "provider", "model"} {
		if !strings.Contains(gen.ChatAiConfigChoices_Operation, f) {
			t.Errorf("ChatAiConfigChoices does not select %q", f)
		}
	}
	if strings.Contains(gen.GetAppMetadata_Operation, "chatbotAiConfigId") {
		t.Error("the shared GetAppMetadata selects chatbotAiConfigId: an older server would stop serving app get")
	}
}
