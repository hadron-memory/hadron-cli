package cmd

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/exitcode"
)

const agentJSON = `{"id":"agt1","urn":"acme.com::support-bot","name":"Support Bot","description":null,
	"type":"CHATBOT","visibility":"ORGANIZATION","organizationId":"acme.com","surfaces":[],
	"systemMemoryId":null,"systemPrompt":null,"aiProvider":null,"aiModel":null,"hasAiApiKey":false,
	"createdAt":"2026-06-19T00:00:00Z"}`

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
	for _, k := range []string{"description", "systemPrompt", "systemMemoryId", "surfaces", "personaRole", "personaPrompt"} {
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

// #428 (PR #431 review): the persona dressing rides create too, so the team
// bootstrap's step 1 is one command — a role agent is born dressed.
func TestAgentCreateWithPersonaDressing(t *testing.T) {
	gql, captured := captureGraphQL(t, map[string]string{
		"CreateAgent": `{"data":{"createAgent":` + agentJSON + `}}`,
	})
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"agent", "create", "--org", "acme.com", "--name", "backend-engineer",
		"--persona-role", "backend-engineer",
		"--persona-prompt", "You are {{name}}, a backend engineer.", "--json", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	var vars map[string]any
	_ = json.Unmarshal(captured["CreateAgent"], &vars)
	if vars["personaRole"] != "backend-engineer" ||
		vars["personaPrompt"] != "You are {{name}}, a backend engineer." {
		t.Errorf("dressing must ride the create: %v", vars)
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
	gql, captured := captureGraphQL(t, map[string]string{
		"GetAgent": `{"data":{"agent":` + agentJSON + `}}`,
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
	if !strings.Contains(out.String(), "agt1") {
		t.Errorf("output: %s", out.String())
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

// #541: the persona prompt is the longest text this CLI takes and it is dense
// with backticks, {{braces}} and newlines — the characters a shell argument
// mangles. --persona-prompt-file carries it verbatim, which is the whole point:
// a prompt documenting CLI usage is full of backticks BY NATURE, and inline
// they are command substitution the shell runs before hadron sees them.
func TestAgentCreatePersonaPromptFileCarriesHostileCharsVerbatim(t *testing.T) {
	// A template that would be mauled inline: backtick command spans, {{name}}
	// placeholders, a $VAR, quotes, an em-dash, and blank-line paragraph breaks.
	prompt := "You are {{name}}, the {{role}}.\n\n" +
		"Use `spec new` to allocate a citation and `hadron team worker cast` to staff.\n" +
		"Never run $(rm -rf /) — obviously. Prices are in \"USD\".\n"
	dir := t.TempDir()
	path := filepath.Join(dir, "persona.md")
	if err := os.WriteFile(path, []byte(prompt), 0o600); err != nil {
		t.Fatal(err)
	}
	gql, captured := captureGraphQL(t, map[string]string{
		"CreateAgent": `{"data":{"createAgent":` + agentJSON + `}}`,
	})
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"agent", "create", "--org", "acme.com", "--name", "Specs Engineer",
		"--persona-role", "specs-engineer", "--persona-prompt-file", path, "--json", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	var vars map[string]any
	_ = json.Unmarshal(captured["CreateAgent"], &vars)
	if vars["personaPrompt"] != prompt {
		t.Errorf("the file must reach the server byte-for-byte:\n got %q\nwant %q", vars["personaPrompt"], prompt)
	}
}

// --persona-prompt - reads the template from stdin, so it can be piped or
// heredoc'd rather than quoted.
func TestAgentCreatePersonaPromptFromStdin(t *testing.T) {
	prompt := "You are {{name}}. Use `spec new`.\n"
	gql, captured := captureGraphQL(t, map[string]string{
		"CreateAgent": `{"data":{"createAgent":` + agentJSON + `}}`,
	})
	f, _ := testFactory(t)
	f.IOStreams.In = strings.NewReader(prompt)
	root := NewRootCmd(f)
	root.SetArgs([]string{"agent", "create", "--org", "acme.com", "--name", "Specs Engineer",
		"--persona-prompt", "-", "--json", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	var vars map[string]any
	_ = json.Unmarshal(captured["CreateAgent"], &vars)
	if vars["personaPrompt"] != prompt {
		t.Errorf("stdin must reach the server verbatim: got %q", vars["personaPrompt"])
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

// Inline and file for the same field are mutually exclusive, refused by cobra
// before any request.
func TestAgentPersonaPromptInlineAndFileConflict(t *testing.T) {
	gql, captured := captureGraphQL(t, map[string]string{})
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"agent", "create", "--org", "acme.com", "--name", "X",
		"--persona-prompt", "inline", "--persona-prompt-file", "/tmp/whatever", "--server", gql.URL})
	err := root.Execute()
	if code := exitCodeFor(err); code != exitcode.Usage {
		t.Fatalf("exit = %d, want Usage; err %v", code, err)
	}
	if len(captured) != 0 {
		t.Errorf("a usage error must cost no round trip; sent %v", captured)
	}
	if err == nil || !strings.Contains(err.Error(), "persona-prompt") {
		t.Errorf("message should name the conflicting flag: %v", err)
	}
}

// Stdin is one stream: two fields both asking for it is refused before either
// read, rather than silently leaving the second empty.
func TestAgentTwoPromptsFromStdinRefused(t *testing.T) {
	gql, captured := captureGraphQL(t, map[string]string{})
	f, _ := testFactory(t)
	f.IOStreams.In = strings.NewReader("only one stream here\n")
	root := NewRootCmd(f)
	root.SetArgs([]string{"agent", "create", "--org", "acme.com", "--name", "X",
		"--persona-prompt", "-", "--system-prompt", "-", "--server", gql.URL})
	err := root.Execute()
	if code := exitCodeFor(err); code != exitcode.Usage {
		t.Fatalf("exit = %d, want Usage; err %v", code, err)
	}
	if len(captured) != 0 {
		t.Errorf("must refuse before any request; sent %v", captured)
	}
	if err == nil || !strings.Contains(err.Error(), "stdin") {
		t.Errorf("message should name the stdin clash: %v", err)
	}
}

// An unreadable --persona-prompt-file is a usage error naming the flag, before
// any request — not a raw os error, and not a round trip.
func TestAgentPersonaPromptFileMissing(t *testing.T) {
	gql, captured := captureGraphQL(t, map[string]string{})
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"agent", "create", "--org", "acme.com", "--name", "X",
		"--persona-prompt-file", filepath.Join(t.TempDir(), "nope.md"), "--server", gql.URL})
	err := root.Execute()
	if code := exitCodeFor(err); code != exitcode.Usage {
		t.Fatalf("exit = %d, want Usage; err %v", code, err)
	}
	if len(captured) != 0 {
		t.Errorf("must refuse before any request; sent %v", captured)
	}
	if err == nil || !strings.Contains(err.Error(), "persona-prompt-file") {
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
	for _, k := range []string{"systemPrompt", "personaPrompt"} {
		if _, present := vars[k]; present {
			t.Errorf("unpassed %q must be omitted, got %v", k, vars[k])
		}
	}
}

// #541 codex P1: --persona-prompt-file "" (an unset shell variable) is
// Changed-but-empty. Without a guard, ResolveTextInput reads the empty path as
// an empty value and `update` silently CLEARS the prompt. It must be a usage
// error, before any request, on both update and create.
func TestAgentPersonaPromptFileEmptyPathRefused(t *testing.T) {
	for _, tc := range []struct {
		name string
		args []string
	}{
		{"update", []string{"agent", "update", "agt1", "--persona-prompt-file", ""}},
		{"create", []string{"agent", "create", "--org", "acme.com", "--name", "X", "--persona-prompt-file", ""}},
		{"update system", []string{"agent", "update", "agt1", "--system-prompt-file", "   "}},
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
