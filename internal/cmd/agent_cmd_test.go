package cmd

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/vektah/gqlparser/v2"
	"github.com/vektah/gqlparser/v2/ast"
	"github.com/vektah/gqlparser/v2/parser"
	"github.com/vektah/gqlparser/v2/validator"

	"github.com/hadron-memory/hadron-cli/internal/api/gen"
	"github.com/hadron-memory/hadron-cli/internal/exitcode"
)

const agentJSON = `{"id":"agt1","urn":"acme.com::support-bot","name":"Support Bot","description":null,
	"type":"CHATBOT","visibility":"ORGANIZATION","organizationId":"acme.com","surfaces":[],
	"systemMemoryId":null,"systemPrompt":null,"aiProvider":null,"aiModel":null,"hasAiApiKey":false,
	"createdAt":"2026-06-19T00:00:00Z"}`

func TestAgentOperationsValidateAgainstCutoverSchema(t *testing.T) {
	sdl, err := os.ReadFile("../../schema/schema.graphql")
	if err != nil {
		t.Fatal(err)
	}
	schema, err := gqlparser.LoadSchema(&ast.Source{Name: "schema.graphql", Input: string(sdl)})
	if err != nil {
		t.Fatal(err)
	}
	for name, operation := range map[string]string{
		"GetAgent": gen.GetAgent_Operation, "Agents": gen.Agents_Operation,
		"PublicAgents": gen.PublicAgents_Operation, "CreateAgent": gen.CreateAgent_Operation,
		"UpdateAgent": gen.UpdateAgent_Operation,
	} {
		t.Run(name, func(t *testing.T) {
			doc, err := parser.ParseQuery(&ast.Source{Name: name, Input: operation})
			if err != nil {
				t.Fatal(err)
			}
			if errs := validator.Validate(schema, doc); len(errs) > 0 {
				t.Fatalf("generated operation fails cutover schema: %v", errs)
			}
		})
	}
	legacy, err := parser.ParseQuery(&ast.Source{Name: "retired-agent-shape", Input: `
		query RetiredSelection { agent(ref: "x") { personaPrompt } }
		mutation RetiredArgument { updateAgent(ref: "x", personaPrompt: "old") { id } }
	`})
	if err != nil {
		t.Fatal(err)
	}
	if errs := validator.Validate(schema, legacy); len(errs) < 2 {
		t.Fatalf("schema must reject the removed field and argument; got %v", errs)
	}
}

func TestAgentCreate(t *testing.T) {
	gql, captured := captureGraphQL(t, map[string]string{
		"CreateAgent": `{"data":{"createAgent":` + agentJSON + `}}`,
	})
	f, out := testFactory(t)
	root := NewRootCmd(f)
	// lower-case enums normalize; description/system-* unset → omitted.
	root.SetArgs([]string{"agent", "create", "--org", "acme.com", "--name", "Support Bot",
		"--type", "chatbot", "--visibility", "organization", "--json", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	var vars map[string]any
	_ = json.Unmarshal(captured["CreateAgent"], &vars)
	if vars["name"] != "Support Bot" || vars["orgId"] != "acme.com" ||
		vars["agentType"] != "CHATBOT" || vars["visibility"] != "ORGANIZATION" {
		t.Errorf("create vars: %v", vars)
	}
	for _, k := range []string{"description", "systemPrompt", "systemMemoryId", "surfaces", "personaRole"} {
		if _, present := vars[k]; present {
			t.Errorf("unset %q must be omitted, got %v", k, vars[k])
		}
	}
	var dto struct {
		ID string `json:"id"`
	}
	_ = json.Unmarshal([]byte(out.String()), &dto)
	if dto.ID != "agt1" {
		t.Errorf("dto: %s", out.String())
	}
}

// A role agent is born with role metadata and a shared Worker template.
func TestAgentCreateWithSharedTemplate(t *testing.T) {
	gql, captured := captureGraphQL(t, map[string]string{
		"CreateAgent": `{"data":{"createAgent":` + agentJSON + `}}`,
	})
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"agent", "create", "--org", "acme.com", "--name", "backend-engineer",
		"--persona-role", "backend-engineer",
		"--system-prompt", "You are {{name}}, the {{role}}.", "--json", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	var vars map[string]any
	_ = json.Unmarshal(captured["CreateAgent"], &vars)
	if vars["personaRole"] != "backend-engineer" ||
		vars["systemPrompt"] != "You are {{name}}, the {{role}}." {
		t.Errorf("role and shared template must ride the create: %v", vars)
	}
	if _, exists := vars["personaPrompt"]; exists {
		t.Errorf("removed personaPrompt argument reached the wire: %v", vars)
	}
}

func TestAgentCreateUserOwnedOmitsOrg(t *testing.T) {
	gql, captured := captureGraphQL(t, map[string]string{
		"CreateAgent": `{"data":{"createAgent":` + agentJSON + `}}`,
	})
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"agent", "create", "--name", "Personal Bot", "--json", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	var vars map[string]any
	_ = json.Unmarshal(captured["CreateAgent"], &vars)
	if vars["name"] != "Personal Bot" {
		t.Errorf("create name: %v", vars)
	}
	if _, present := vars["orgId"]; present {
		t.Errorf("user-owned create must omit orgId, got %v", vars["orgId"])
	}
}

// #257: --owner-me is the explicit user-owned affordance — it omits orgId (the
// spec-047 personal-create path), same as omitting --org.
func TestAgentCreateOwnerMeOmitsOrg(t *testing.T) {
	gql, captured := captureGraphQL(t, map[string]string{
		"CreateAgent": `{"data":{"createAgent":` + agentJSON + `}}`,
	})
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"agent", "create", "--owner-me", "--name", "Personal Bot", "--json", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	var vars map[string]any
	_ = json.Unmarshal(captured["CreateAgent"], &vars)
	if _, present := vars["orgId"]; present {
		t.Errorf("--owner-me must omit orgId, got %v", vars["orgId"])
	}
}

// --owner-me and --org are mutually exclusive (an agent is owned by exactly one
// of a user or an org) — the guard fires before any network round-trip.
func TestAgentCreateRejectsOwnerMeAndOrg(t *testing.T) {
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"agent", "create", "--owner-me", "--org", "acme.com", "--name", "X", "--server", "http://127.0.0.1:1"})
	err := root.Execute()
	if err == nil || !strings.Contains(err.Error(), "drop --org") {
		t.Fatalf("expected a mutual-exclusion usage error, got %v", err)
	}
}

func TestAgentCreateRejectsBadType(t *testing.T) {
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"agent", "create", "--org", "acme.com", "--name", "X", "--type", "wizard", "--server", "http://127.0.0.1:1"})
	err := root.Execute()
	if err == nil || !strings.Contains(err.Error(), "invalid --type") {
		t.Fatalf("expected invalid-type error, got %v", err)
	}
}

func TestAgentLs(t *testing.T) {
	gql, captured := captureGraphQL(t, map[string]string{
		"Agents": `{"data":{"agents":{"total":1,"items":[` + agentJSON + `]}}}`,
	})
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"agent", "ls", "--org", "acme.com", "--type", "CHATBOT", "--json", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	var vars map[string]any
	_ = json.Unmarshal(captured["Agents"], &vars)
	if vars["orgId"] != "acme.com" {
		t.Errorf("ls orgId: %v", vars)
	}
	var agents []struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal([]byte(out.String()), &agents); err != nil {
		t.Fatalf("not a JSON array: %v\n%s", err, out.String())
	}
	if len(agents) != 1 || agents[0].ID != "agt1" {
		t.Errorf("agents: %+v", agents)
	}
}

// --public hits the cross-org publicAgents slice (not agents()), passes --type
// through as a filter, and sends no orgId.
func TestAgentLsPublic(t *testing.T) {
	gql, captured := captureGraphQL(t, map[string]string{
		"PublicAgents": `{"data":{"publicAgents":{"total":1,"items":[` + agentJSON + `]}}}`,
	})
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"agent", "ls", "--public", "--type", "ASSISTANT", "--json", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	var vars map[string]any
	_ = json.Unmarshal(captured["PublicAgents"], &vars)
	if _, present := vars["orgId"]; present {
		t.Errorf("--public must not send orgId, got %v", vars["orgId"])
	}
	fl, _ := vars["filter"].(map[string]any)
	if fl["type"] != "ASSISTANT" {
		t.Errorf("--type should map to filter.type, got %v", vars["filter"])
	}
	// PublicAgentFilter is a narrower input than agents()' AgentFilter — the
	// server rejects visibility / ownedByMe on this slice, so sending the
	// wider filter here is a schema error, not a silently-ignored field.
	if len(fl) != 1 {
		t.Errorf("public filter must carry only type, got %v", fl)
	}
	var agents []struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal([]byte(out.String()), &agents); err != nil {
		t.Fatalf("not a JSON array: %v\n%s", err, out.String())
	}
	if len(agents) != 1 || agents[0].ID != "agt1" {
		t.Errorf("agents: %+v", agents)
	}
}

// --public is a distinct surface; --org / --visibility don't apply and are a
// usage error rather than a silently-ignored flag.
func TestAgentLsPublicRejectsOrgAndVisibility(t *testing.T) {
	for _, extra := range [][]string{{"--org", "acme.com"}, {"--visibility", "PUBLIC"}} {
		f, _ := testFactory(t)
		root := NewRootCmd(f)
		root.SetArgs(append([]string{"agent", "ls", "--public"}, append(extra, "--server", "http://127.0.0.1:1")...))
		err := root.Execute()
		if err == nil || !strings.Contains(err.Error(), "don't apply") {
			t.Fatalf("expected --public exclusivity error for %v, got %v", extra, err)
		}
	}
}

func TestAgentGet(t *testing.T) {
	withTemplate := strings.Replace(agentJSON, `"systemPrompt":null`, `"systemPrompt":"You are {{name}}, the {{role}}."`, 1)
	gql, captured := captureGraphQL(t, map[string]string{
		"GetAgent": `{"data":{"agent":` + withTemplate + `}}`,
	})
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"agent", "get", "acme.com::support-bot", "--json", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	var vars map[string]any
	_ = json.Unmarshal(captured["GetAgent"], &vars)
	if vars["ref"] != "acme.com::support-bot" {
		t.Errorf("get ref: %v", vars)
	}
	var dto map[string]any
	if err := json.Unmarshal([]byte(out.String()), &dto); err != nil {
		t.Fatalf("agent JSON: %v", err)
	}
	if dto["id"] != "agt1" || dto["systemPrompt"] != "You are {{name}}, the {{role}}." {
		t.Errorf("output: %s", out.String())
	}
	if _, exists := dto["personaPrompt"]; exists {
		t.Errorf("removed server field must not be presented as a JSON value: %s", out.String())
	}
}

// #789 — `agent update` / `agent rm` accept a fully-qualified URN, not just a
// PK. The CLI always passed the user's argument straight through; what changed
// is server-side (updateAgent/deleteAgent did a PK-only findUniqueOrThrow, so a
// URN was rejected). These pin that the URN reaches the wire untouched, so the
// capability can't regress by someone re-adding a client-side pre-resolve.
func TestAgentUpdateAcceptsURN(t *testing.T) {
	gql, captured := captureGraphQL(t, map[string]string{
		"UpdateAgent": `{"data":{"updateAgent":` + agentJSON + `}}`,
	})
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"agent", "update", "acme.com::support-bot", "--name", "Bot v2", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	var vars map[string]any
	_ = json.Unmarshal(captured["UpdateAgent"], &vars)
	if vars["ref"] != "acme.com::support-bot" {
		t.Errorf("update by URN should send the URN as ref, got: %v", vars)
	}
}

func TestAgentRmAcceptsURN(t *testing.T) {
	gql, captured := captureGraphQL(t, map[string]string{
		"DeleteAgent": `{"data":{"deleteAgent":true}}`,
	})
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"agent", "rm", "acme.com::support-bot", "--yes", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	var vars map[string]any
	_ = json.Unmarshal(captured["DeleteAgent"], &vars)
	if vars["ref"] != "acme.com::support-bot" {
		t.Errorf("rm by URN should send the URN as ref, got: %v", vars)
	}
}

func TestAgentUpdate(t *testing.T) {
	gql, captured := captureGraphQL(t, map[string]string{
		"UpdateAgent": `{"data":{"updateAgent":` + agentJSON + `}}`,
	})
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"agent", "update", "agt1", "--name", "Bot v2", "--visibility", "PUBLIC", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	var vars map[string]any
	_ = json.Unmarshal(captured["UpdateAgent"], &vars)
	// #789: the wire arg is `ref` (PK or fully-qualified URN), not `id`.
	if vars["ref"] != "agt1" || vars["name"] != "Bot v2" || vars["visibility"] != "PUBLIC" {
		t.Errorf("update vars: %v", vars)
	}
	// Unset fields must be omitted (preserve), not sent.
	for _, k := range []string{"description", "agentType", "systemPrompt", "systemMemoryId", "surfaces", "urn"} {
		if _, present := vars[k]; present {
			t.Errorf("unset %q must be omitted, got %v", k, vars[k])
		}
	}
}

func TestAgentUpdateAcceptsUserAuthorURN(t *testing.T) {
	gql, captured := captureGraphQL(t, map[string]string{
		"UpdateAgent": `{"data":{"updateAgent":` + agentJSON + `}}`,
	})
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"agent", "update", "agt1", "--urn", "@holger:triage", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	var vars map[string]any
	_ = json.Unmarshal(captured["UpdateAgent"], &vars)
	if vars["urn"] != "@holger:triage" {
		t.Errorf("update urn: %v", vars)
	}
}

func TestAgentUpdateNothingIsUsageError(t *testing.T) {
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"agent", "update", "agt1", "--server", "http://127.0.0.1:1"})
	err := root.Execute()
	if err == nil || !strings.Contains(err.Error(), "nothing to update") {
		t.Fatalf("expected nothing-to-update error, got %v", err)
	}
}

func TestAgentRmRequiresYes(t *testing.T) {
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"agent", "rm", "agt1", "--server", "http://127.0.0.1:1"})
	err := root.Execute()
	if err == nil || !strings.Contains(err.Error(), "--yes") {
		t.Fatalf("expected --yes refusal, got %v", err)
	}
}

func TestAgentRmWithYes(t *testing.T) {
	gql, captured := captureGraphQL(t, map[string]string{
		"DeleteAgent": `{"data":{"deleteAgent":true}}`,
	})
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"agent", "rm", "agt1", "--yes", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	var vars map[string]any
	_ = json.Unmarshal(captured["DeleteAgent"], &vars)
	// #789: `ref`, not `id`.
	if vars["ref"] != "agt1" {
		t.Errorf("rm vars: %v", vars)
	}
}

// #541: the shared prompt is long and dense with backticks, {{braces}} and
// newlines — the characters a shell argument mangles. --system-prompt-file
// carries it verbatim, which is the whole point:
// a prompt documenting CLI usage is full of backticks BY NATURE, and inline
// they are command substitution the shell runs before hadron sees them.
func TestAgentCreateSystemPromptFileCarriesHostileCharsVerbatim(t *testing.T) {
	// A template that would be mauled inline: backtick command spans, {{name}}
	// placeholders, a $VAR, quotes, an em-dash, and blank-line paragraph breaks.
	prompt := "You are {{name}}, the {{role}}.\n\n" +
		"Use `spec new` to allocate a citation and `hadron team worker cast` to staff.\n" +
		"Never run $(rm -rf /) — obviously. Prices are in \"USD\".\n"
	dir := t.TempDir()
	path := filepath.Join(dir, "system.md")
	if err := os.WriteFile(path, []byte(prompt), 0o600); err != nil {
		t.Fatal(err)
	}
	gql, captured := captureGraphQL(t, map[string]string{
		"CreateAgent": `{"data":{"createAgent":` + agentJSON + `}}`,
	})
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"agent", "create", "--org", "acme.com", "--name", "Specs Engineer",
		"--persona-role", "specs-engineer", "--system-prompt-file", path, "--json", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	var vars map[string]any
	_ = json.Unmarshal(captured["CreateAgent"], &vars)
	if vars["systemPrompt"] != prompt {
		t.Errorf("the file must reach the server byte-for-byte:\n got %q\nwant %q", vars["systemPrompt"], prompt)
	}
}

// --system-prompt - reads the template from stdin, so it can be piped or
// heredoc'd rather than quoted.
func TestAgentCreateSystemPromptFromStdin(t *testing.T) {
	prompt := "You are {{name}}. Use `spec new`.\n"
	gql, captured := captureGraphQL(t, map[string]string{
		"CreateAgent": `{"data":{"createAgent":` + agentJSON + `}}`,
	})
	f, _ := testFactory(t)
	f.IOStreams.In = strings.NewReader(prompt)
	root := NewRootCmd(f)
	root.SetArgs([]string{"agent", "create", "--org", "acme.com", "--name", "Specs Engineer",
		"--system-prompt", "-", "--json", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	var vars map[string]any
	_ = json.Unmarshal(captured["CreateAgent"], &vars)
	if vars["systemPrompt"] != prompt {
		t.Errorf("stdin must reach the server verbatim: got %q", vars["systemPrompt"])
	}
}

// --system-prompt-file has the identical shape (issue #541 asked for both).
func TestAgentUpdateSystemPromptFile(t *testing.T) {
	prompt := "System rules.\n\nBe terse.\n"
	dir := t.TempDir()
	path := filepath.Join(dir, "sys.md")
	if err := os.WriteFile(path, []byte(prompt), 0o600); err != nil {
		t.Fatal(err)
	}
	gql, captured := captureGraphQL(t, map[string]string{
		"UpdateAgent": `{"data":{"updateAgent":` + agentJSON + `}}`,
	})
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"agent", "update", "agt1", "--system-prompt-file", path, "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	var vars map[string]any
	_ = json.Unmarshal(captured["UpdateAgent"], &vars)
	if vars["systemPrompt"] != prompt {
		t.Errorf("systemPrompt = %q, want the file contents verbatim", vars["systemPrompt"])
	}
}

func TestAgentUpdateSystemPromptExplicitEmptyClears(t *testing.T) {
	gql, captured := captureGraphQL(t, map[string]string{
		"UpdateAgent": `{"data":{"updateAgent":` + agentJSON + `}}`,
	})
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"agent", "update", "agt1", "--system-prompt", "", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	var vars map[string]any
	_ = json.Unmarshal(captured["UpdateAgent"], &vars)
	if value, present := vars["systemPrompt"]; !present || value != "" {
		t.Fatalf("explicit empty system prompt must reach update (clear), got %v", vars)
	}
}

// Inline and file for the same field are mutually exclusive, refused by cobra
// before any request.
func TestAgentSystemPromptInlineAndFileConflict(t *testing.T) {
	gql, captured := captureGraphQL(t, map[string]string{})
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"agent", "create", "--org", "acme.com", "--name", "X",
		"--system-prompt", "inline", "--system-prompt-file", "/tmp/whatever", "--server", gql.URL})
	err := root.Execute()
	if code := exitCodeFor(err); code != exitcode.Usage {
		t.Fatalf("exit = %d, want Usage; err %v", code, err)
	}
	if len(captured) != 0 {
		t.Errorf("a usage error must cost no round trip; sent %v", captured)
	}
	if err == nil || !strings.Contains(err.Error(), "system-prompt") {
		t.Errorf("message should name the conflicting flag: %v", err)
	}
}

func TestAgentRetiredPersonaPromptFlagsRefuseBeforeRequest(t *testing.T) {
	for _, tc := range []struct {
		name string
		args []string
	}{
		{"create inline", []string{"agent", "create", "--org", "acme.com", "--name", "X", "--persona-prompt", "old"}},
		{"create file", []string{"agent", "create", "--org", "acme.com", "--name", "X", "--persona-prompt-file", "/absent"}},
		{"update inline", []string{"agent", "update", "agt1", "--persona-prompt", "old"}},
		{"update file", []string{"agent", "update", "agt1", "--persona-prompt-file", "/absent"}},
	} {
		for _, signedIn := range []bool{true, false} {
			name := tc.name
			if !signedIn {
				name += " signed out"
			}
			t.Run(name, func(t *testing.T) {
				gql, captured := captureGraphQL(t, map[string]string{})
				f, _ := testFactory(t)
				if !signedIn {
					t.Setenv("HADRON_TOKEN", "")
				}
				root := NewRootCmd(f)
				root.SetArgs(append(tc.args, "--server", gql.URL))
				err := root.Execute()
				if code := exitCodeFor(err); code != exitcode.Usage {
					t.Fatalf("exit = %d, want Usage; err %v", code, err)
				}
				if len(captured) != 0 {
					t.Fatalf("retired flag must refuse before any request; sent %v", captured)
				}
				if err == nil || !strings.Contains(err.Error(), "--system-prompt") {
					t.Fatalf("retired flag error must name replacement: %v", err)
				}
			})
		}
	}
}

// An unreadable --system-prompt-file is a usage error naming the flag, before
// any request — not a raw os error, and not a round trip.
func TestAgentSystemPromptFileMissing(t *testing.T) {
	gql, captured := captureGraphQL(t, map[string]string{})
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"agent", "create", "--org", "acme.com", "--name", "X",
		"--system-prompt-file", filepath.Join(t.TempDir(), "nope.md"), "--server", gql.URL})
	err := root.Execute()
	if code := exitCodeFor(err); code != exitcode.Usage {
		t.Fatalf("exit = %d, want Usage; err %v", code, err)
	}
	if len(captured) != 0 {
		t.Errorf("must refuse before any request; sent %v", captured)
	}
	if err == nil || !strings.Contains(err.Error(), "system-prompt-file") {
		t.Errorf("message should name the flag: %v", err)
	}
}

// The -file flags are optional: not passing them changes nothing, and the
// prompt fields stay omitted (preserve) exactly as before #541.
func TestAgentUpdateWithoutPromptFlagsOmitsThem(t *testing.T) {
	gql, captured := captureGraphQL(t, map[string]string{
		"UpdateAgent": `{"data":{"updateAgent":` + agentJSON + `}}`,
	})
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"agent", "update", "agt1", "--name", "Renamed", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	var vars map[string]any
	_ = json.Unmarshal(captured["UpdateAgent"], &vars)
	for _, k := range []string{"systemPrompt"} {
		if _, present := vars[k]; present {
			t.Errorf("unpassed %q must be omitted, got %v", k, vars[k])
		}
	}
}

// #541 codex P1: --system-prompt-file "" (an unset shell variable) is
// Changed-but-empty. Without a guard, ResolveTextInput reads the empty path as
// an empty value and `update` silently CLEARS the prompt. It must be a usage
// error, before any request, on both update and create.
func TestAgentSystemPromptFileEmptyPathRefused(t *testing.T) {
	for _, tc := range []struct {
		name string
		args []string
	}{
		{"update", []string{"agent", "update", "agt1", "--system-prompt-file", ""}},
		{"create", []string{"agent", "create", "--org", "acme.com", "--name", "X", "--system-prompt-file", ""}},
		{"update whitespace", []string{"agent", "update", "agt1", "--system-prompt-file", "   "}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			gql, captured := captureGraphQL(t, map[string]string{})
			f, _ := testFactory(t)
			root := NewRootCmd(f)
			root.SetArgs(append(tc.args, "--server", gql.URL))
			err := root.Execute()
			if code := exitCodeFor(err); code != exitcode.Usage {
				t.Fatalf("exit = %d, want Usage; err %v", code, err)
			}
			if len(captured) != 0 {
				t.Errorf("must refuse before any request; sent %v", captured)
			}
			if err == nil || !strings.Contains(err.Error(), "empty path") {
				t.Errorf("message should name the empty path: %v", err)
			}
		})
	}
}

// ---- cli#874: the Agent's two index refs (cor:dmo:050:15 / :16) ----------
//
// `agent tasks list` / `agent processes list` read the server's own chain
// walk and dedupe (agentTaskList/agentProcessList); `agent update` sets or
// clears the two refs through setAgentIndexRefs. The server reads an OMITTED
// argument as preserve and an explicit NULL as clear, so each flag is
// three-state: absent, value, or explicit empty (clear).

// listedNodeJSON is one Node as the list rows carry it: own address, and the
// base it was inherited from where that applies.
const listedNodeJSON = `{"id":"n1","urn":"hrn:node:acme.com:base-mem:tasks:onboarding",
	"name":"Onboarding","inheritedFrom":{"agentId":"agt_base","agentUrn":"hrn:agent:acme.com:base"}}`

const listedOwnNodeJSON = `{"id":"n2","urn":"hrn:node:acme.com:support-tasks:escalate",
	"name":"Escalate","inheritedFrom":null}`

func TestAgentTasksList(t *testing.T) {
	gql, captured := captureGraphQL(t, map[string]string{
		"AgentTaskIndexList": `{"data":{"agentTaskList":[
			{"node":` + listedNodeJSON + `,"trigger":"tasks:agent"},
			{"node":` + listedOwnNodeJSON + `,"trigger":null}]}}`,
	})
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"agent", "tasks", "list", "hrn:agent:acme.com:support", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	var vars struct {
		Ref string `json:"ref"`
	}
	_ = json.Unmarshal(captured["AgentTaskIndexList"], &vars)
	if vars.Ref != "hrn:agent:acme.com:support" {
		t.Fatalf("ref = %v", vars.Ref)
	}
	got := out.String()
	for _, want := range []string{"Onboarding", "tasks:agent", "hrn:agent:acme.com:base", "Escalate"} {
		if !strings.Contains(got, want) {
			t.Errorf("rows should carry %q:\n%s", want, got)
		}
	}
	// A task no index edge lists shows a blank trigger, not "null".
	if strings.Contains(got, "null") {
		t.Errorf("a null trigger must render blank, got:\n%s", got)
	}
}

// --json returns the server shape unchanged: a list of {node, trigger}.
func TestAgentTasksListJSON(t *testing.T) {
	gql, _ := captureGraphQL(t, map[string]string{
		"AgentTaskIndexList": `{"data":{"agentTaskList":[{"node":` + listedOwnNodeJSON + `,"trigger":"tasks:agent"}]}}`,
	})
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"agent", "tasks", "list", "agt1", "--json", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	var rows []struct {
		Node struct {
			ID            string `json:"id"`
			URN           string `json:"urn"`
			Name          string `json:"name"`
			InheritedFrom *struct {
				AgentUrn string `json:"agentUrn"`
			} `json:"inheritedFrom"`
		} `json:"node"`
		Trigger *string `json:"trigger"`
	}
	if err := json.Unmarshal([]byte(out.String()), &rows); err != nil {
		t.Fatalf("not JSON: %v\n%s", err, out.String())
	}
	if len(rows) != 1 || rows[0].Node.URN == "" || rows[0].Trigger == nil || *rows[0].Trigger != "tasks:agent" {
		t.Fatalf("server shape must pass through unchanged, got %s", out.String())
	}
	if rows[0].Node.InheritedFrom != nil {
		t.Fatalf("own node must render inheritedFrom:null, got %s", out.String())
	}
}

func TestAgentProcessesList(t *testing.T) {
	gql, captured := captureGraphQL(t, map[string]string{
		"AgentProcessIndexList": `{"data":{"agentProcessList":[
			{"node":` + listedNodeJSON + `,"kind":"STARTS","trigger":"processes:agent"},
			{"node":` + listedOwnNodeJSON + `,"kind":"INVOLVED","trigger":null}]}}`,
	})
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"agent", "processes", "list", "agt1", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	var vars struct {
		Ref string `json:"ref"`
	}
	_ = json.Unmarshal(captured["AgentProcessIndexList"], &vars)
	if vars.Ref != "agt1" {
		t.Fatalf("ref = %v", vars.Ref)
	}
	// kind renders in the readable spelling (starts/involved), trigger null
	// renders blank.
	for _, want := range []string{"starts", "involved", "Onboarding", "hrn:agent:acme.com:base"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("rows should carry %q:\n%s", want, out.String())
		}
	}
}

// A value flag routes through the set document; with no field flags, update
// fires ONLY the index mutation (the server refuses an empty field update).
func TestAgentUpdateSetsIndexRefs(t *testing.T) {
	gql, captured := captureGraphQL(t, map[string]string{
		"SetAgentIndexRefs": `{"data":{"setAgentIndexRefs":` + agentJSON + `}}`,
	})
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"agent", "update", "agt1",
		"--tasks-index", "acme.com::support-tasks::tasks:agent", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	vars := map[string]any{}
	_ = json.Unmarshal(captured["SetAgentIndexRefs"], &vars)
	if vars["ref"] != "agt1" || vars["tasksIndexRef"] != "acme.com::support-tasks::tasks:agent" {
		t.Fatalf("set vars: %v", vars)
	}
	if _, present := vars["processesIndexRef"]; present {
		t.Fatalf("an unchanged ref must be OMITTED (preserve), got %v", vars["processesIndexRef"])
	}
	if _, called := captured["UpdateAgent"]; called {
		t.Fatal("an index-only update must not send a field update")
	}
}

// An explicit empty flag CLEARS: the server reads explicit null as clear and
// an empty string as a bad ref, so the clear goes through its own document
// whose variable marshals nil as null — while the untouched ref is omitted.
func TestAgentUpdateClearsIndexRef(t *testing.T) {
	gql, captured := captureGraphQL(t, map[string]string{
		"ClearAgentTasksIndex": `{"data":{"setAgentIndexRefs":` + agentJSON + `}}`,
	})
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"agent", "update", "agt1", "--tasks-index", "", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	vars := map[string]any{}
	_ = json.Unmarshal(captured["ClearAgentTasksIndex"], &vars)
	value, present := vars["tasksIndexRef"]
	if !present || value != nil {
		t.Fatalf("the clear must send an explicit null tasksIndexRef, got %v (present=%v)", value, present)
	}
	if _, present := vars["processesIndexRef"]; present {
		t.Fatalf("the untouched ref must be omitted, got %v", vars["processesIndexRef"])
	}
	for _, op := range []string{"SetAgentIndexRefs", "UpdateAgent"} {
		if _, called := captured[op]; called {
			t.Fatalf("%s must not be called on a clear", op)
		}
	}
}

// Field flags and index flags compose: one field update, one index set.
func TestAgentUpdateMixedFieldsAndIndexRefs(t *testing.T) {
	gql, captured := captureGraphQL(t, map[string]string{
		"UpdateAgent":              `{"data":{"updateAgent":` + agentJSON + `}}`,
		"SetAgentIndexRefs":        `{"data":{"setAgentIndexRefs":` + agentJSON + `}}`,
		"ClearAgentProcessesIndex": `{"data":{"setAgentIndexRefs":` + agentJSON + `}}`,
	})
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"agent", "update", "agt1", "--name", "Support Bot v2",
		"--tasks-index", "acme.com::kb::tasks:agent", "--processes-index", "", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if _, called := captured["UpdateAgent"]; !called {
		t.Fatal("the field update must still run")
	}
	setVars := map[string]any{}
	_ = json.Unmarshal(captured["SetAgentIndexRefs"], &setVars)
	if setVars["tasksIndexRef"] != "acme.com::kb::tasks:agent" {
		t.Fatalf("set vars: %v", setVars)
	}
	if _, present := setVars["processesIndexRef"]; present {
		t.Fatalf("the cleared ref must not ride the set document, got %v", setVars["processesIndexRef"])
	}
	clearVars := map[string]any{}
	_ = json.Unmarshal(captured["ClearAgentProcessesIndex"], &clearVars)
	if v, present := clearVars["processesIndexRef"]; !present || v != nil {
		t.Fatalf("the clear must send explicit null, got %v (present=%v)", v, present)
	}
}

// agentWithRefsJSON is the agent fixture carrying the two index refs (null
// when unset) — the read-back half of the #874 round trip.
func agentWithRefsJSON(tasks, processes *string) string {
	ts, ps := "null", "null"
	if tasks != nil {
		ts = `"` + *tasks + `"`
	}
	if processes != nil {
		ps = `"` + *processes + `"`
	}
	return fmt.Sprintf(`{"id":"agt1","urn":"acme.com::support-bot","name":"Support Bot","description":null,
		"type":"CHATBOT","visibility":"ORGANIZATION","organizationId":"acme.com","surfaces":[],
		"systemMemoryId":null,"systemPrompt":null,"aiProvider":null,"aiModel":null,"hasAiApiKey":false,
		"tasksIndexRef":%s,"processesIndexRef":%s,
		"createdAt":"2026-06-19T00:00:00Z"}`, ts, ps)
}

// Colin's QA P2 on #884 (#11646): a ref set through `agent update` could not
// be read back — `agent get` exposed neither index ref, in JSON or human
// output. The acceptance is the round trip through the CLI itself: set, get
// (the ref shows), clear, get (null renders as unset).
func TestAgentGetExposesIndexRefsRoundTrip(t *testing.T) {
	tasksRef := "n-tasks-index"
	processesRef := "n-processes-index"
	getCalls := 0
	gql, _ := captureGraphQLFunc(t, func(op string) string {
		switch op {
		case "SetAgentIndexRefs":
			return `{"data":{"setAgentIndexRefs":` + agentWithRefsJSON(&tasksRef, &processesRef) + `}}`
		case "ClearAgentTasksIndex":
			return `{"data":{"setAgentIndexRefs":` + agentWithRefsJSON(nil, &processesRef) + `}}`
		case "GetAgent":
			getCalls++
			if getCalls == 1 {
				return `{"data":{"agent":` + agentWithRefsJSON(&tasksRef, &processesRef) + `}}`
			}
			return `{"data":{"agent":` + agentWithRefsJSON(nil, &processesRef) + `}}`
		}
		return ""
	})

	run := func(jsonOut bool, argv ...string) (string, error) {
		f, out := testFactory(t)
		root := NewRootCmd(f)
		args := append([]string{"--server", gql.URL}, argv...)
		if jsonOut {
			args = append(args, "--json")
		}
		root.SetArgs(args)
		err := root.Execute()
		// Read the builder AFTER Execute: a `return out.String(), root.Execute()`
		// would snapshot the output before the command ran.
		return out.String(), err
	}

	// Set, then read back through agent get --json: the set ref is present,
	// the untouched one is an explicit null (unset), not a missing key.
	if _, err := run(false, "agent", "update", "agt1", "--tasks-index", tasksRef, "--processes-index", processesRef); err != nil {
		t.Fatalf("set: %v", err)
	}
	out, err := run(true, "agent", "get", "agt1")
	if err != nil {
		t.Fatalf("get after set: %v", err)
	}
	var got map[string]any
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("not JSON: %v\n%s", err, out)
	}
	if got["tasksIndexRef"] != tasksRef {
		t.Fatalf("tasksIndexRef = %v, want the set ref", got["tasksIndexRef"])
	}
	if got["processesIndexRef"] != processesRef {
		t.Fatalf("processesIndexRef = %v, want the set ref", got["processesIndexRef"])
	}

	// Clear, then read back: both render unset — the human view shows the
	// dash, and the stale value is gone.
	if _, err := run(false, "agent", "update", "agt1", "--tasks-index", ""); err != nil {
		t.Fatalf("clear: %v", err)
	}
	humanOut, err := run(false, "agent", "get", "agt1")
	if err != nil {
		t.Fatalf("get after clear: %v", err)
	}
	if strings.Contains(humanOut, tasksRef) {
		t.Fatalf("the cleared ref must not render, got:\n%s", humanOut)
	}
	// The untouched processes ref SURVIVES the tasks clear (the clear
	// document omits it): still set, not dashed to unset.
	if !strings.Contains(humanOut, processesRef) {
		t.Fatalf("the processes ref must survive the tasks clear, got:\n%s", humanOut)
	}
	for _, want := range []string{"tasks index", "processes index"} {
		if !strings.Contains(humanOut, want) {
			t.Fatalf("the human view should show %q, got:\n%s", want, humanOut)
		}
	}
}

// Andi's blind P3 (#11639): exact JSON equality pins on both list shapes —
// the --json arrays equal the server's arrays, not merely contain their
// fields.
func TestAgentListShapesExactJSON(t *testing.T) {
	taskFixture := `[{"node":` + listedNodeJSON + `,"trigger":"tasks:agent"},{"node":` + listedOwnNodeJSON + `,"trigger":null}]`
	gql, _ := captureGraphQL(t, map[string]string{
		"AgentTaskIndexList": `{"data":{"agentTaskList":` + taskFixture + `}}`,
	})
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"agent", "tasks", "list", "agt1", "--json", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	var got, want []any
	if err := json.Unmarshal([]byte(out.String()), &got); err != nil {
		t.Fatalf("not JSON: %v\n%s", err, out.String())
	}
	if err := json.Unmarshal([]byte(taskFixture), &want); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("--json must equal the server array exactly:\ngot  %s\nwant %s", out.String(), taskFixture)
	}

	processFixture := `[{"node":` + listedNodeJSON + `,"kind":"STARTS","trigger":"processes:agent"},{"node":` + listedOwnNodeJSON + `,"kind":"INVOLVED","trigger":null}]`
	gql2, _ := captureGraphQL(t, map[string]string{
		"AgentProcessIndexList": `{"data":{"agentProcessList":` + processFixture + `}}`,
	})
	f2, out2 := testFactory(t)
	root2 := NewRootCmd(f2)
	root2.SetArgs([]string{"agent", "processes", "list", "agt1", "--json", "--server", gql2.URL})
	if err := root2.Execute(); err != nil {
		t.Fatal(err)
	}
	var got2, want2 []any
	if err := json.Unmarshal([]byte(out2.String()), &got2); err != nil {
		t.Fatalf("not JSON: %v\n%s", err, out2.String())
	}
	if err := json.Unmarshal([]byte(processFixture), &want2); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got2, want2) {
		t.Fatalf("--json must equal the server array exactly:\ngot  %s", out2.String())
	}
}
