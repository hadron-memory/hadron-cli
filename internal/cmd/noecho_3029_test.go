package cmd

import (
	"fmt"
	"strings"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/exitcode"
)

// cli#3029: the #2898 no-echo policy (#2902 applied it to the first sites) at every remaining refusal that
// printed back a positional argument. Each refusal still says what is wrong and that the value is not
// shown, keeps its exit code, and nothing of the value reaches the error text, stderr, stdout or the
// --json envelope.

// tok3029 is token-shaped but lowercase, so it survives the slug/URN validators that sit in front of
// most of these refusals and reaches the refusal itself. Assembled at runtime: a token-shaped literal in
// the source trips push protection.
func tok3029() string { return "xo" + "xb-1234567890-0987654321-abcdefghijklmnopqrstuvwx" }

type noEcho3029Case struct {
	name      string
	responses map[string]string
	args      []string
	code      int
	says      string
}

// teamBound3029 names the worker-bound commands: they get a team binding pointing at the fake server.
var teamBound3029 = map[string]bool{"team grant get": true, "team grant action": true, "team role-lists set": true}

// runNoEcho3029 is runNoEcho2902 with an optional team binding: render the refusal in text and in
// --json, and return everything the operator could see.
func runNoEcho3029(t *testing.T, tc noEcho3029Case) (everything string, err error) {
	t.Helper()
	gql, _ := captureGraphQL(t, tc.responses)
	if teamBound3029[tc.name] {
		writeTeamBinding(t)
		setTeamBindingServer(t, gql.URL)
	}
	f, out := testFactory(t)
	var stderr strings.Builder
	f.IOStreams.ErrOut = &stderr
	root := NewRootCmd(f)
	root.SetArgs(append(tc.args, "--server", gql.URL))
	err = root.Execute()
	if err == nil {
		t.Fatalf("%v: want a refusal", tc.args)
	}
	renderError(f, err)
	f.JSON = true
	renderError(f, err)
	return err.Error() + out.String() + stderr.String(), err
}

func checkNoEcho3029(t *testing.T, tc noEcho3029Case) {
	t.Helper()
	p := tok3029()
	everything, err := runNoEcho3029(t, tc)
	if exitCodeFor(err) != tc.code {
		t.Errorf("exit = %d, want %d: %v", exitCodeFor(err), tc.code, err)
	}
	for _, leak := range []string{p, p[:8], p[len(p)-8:]} {
		if strings.Contains(everything, leak) {
			t.Errorf("the refusal echoes %q of the value: %q", leak, everything)
		}
	}
	if !strings.Contains(err.Error(), tc.says) || !strings.Contains(err.Error(), "not shown") {
		t.Errorf("the refusal says what is wrong (%q) and that the value is not shown: %v", tc.says, err)
	}
}

const (
	memoryResolves3029 = `{"data":{"memory":{"id":"m1","urn":"hrn:mem:acme.com:dev","name":"dev"}}}`
	scopeResolves3029  = `{"data":{"scopeExplain":{"resolvedVia":"NAME","droppedCount":0,"scope":{"id":"s1"},"memories":[],"winner":null}}}`
)

const resolvesToNode3029 = `{"data":{"resolveUrn":{"id":"n1","kind":"node","memoryId":"m1"}}}`

func TestRefusalsNeverEchoThePositionalArg3029(t *testing.T) {
	p := tok3029()
	node := "hrn:node:acme.com:dev:" + p
	null := func(op, field string) map[string]string {
		return map[string]string{op: `{"data":{"` + field + `":null}}`}
	}
	for _, tc := range []noEcho3029Case{
		{"admin impersonation get", null("ImpersonationSession", "impersonationSession"), []string{"admin", "impersonation", "get", p}, exitcode.NotFound, "no impersonation session is readable here"},
		{"agent get", null("GetAgent", "agent"), []string{"agent", "get", p}, exitcode.NotFound, "agent not found"},
		{"agent rm", map[string]string{"DeleteAgent": `{"data":{"deleteAgent":false}}`}, []string{"agent", "rm", p, "--yes"}, exitcode.Error, "the agent was not deleted"},
		{"agent session end", map[string]string{"EndAgentSession": `{"data":{"endAgentSession":false}}`}, []string{"agent", "session", "end", p}, exitcode.Error, "the session was not ended"},
		{"app properties get", null("AppPropertiesGet", "app"), []string{"app", "properties", "get", "acme.com:" + p}, exitcode.NotFound, "no App found"},
		{"app get", null("GetAppMetadata", "app"), []string{"app", "get", "acme.com:" + p}, exitcode.NotFound, "no App found"},
		{"asset get", null("AssetDownloadUrl", "assetDownloadUrl"), []string{"asset", "get", p}, exitcode.NotFound, "no asset found"},
		{"asset public-link", null("SetAssetPublicLink", "setAssetPublicLink"), []string{"asset", "public-link", p, "--on", "--yes"}, exitcode.NotFound, "no asset found"},
		{"asset rm", null("SoftDeleteAsset", "softDeleteAsset"), []string{"asset", "rm", p, "--yes"}, exitcode.NotFound, "no asset found"},
		{"asset restore", null("RestoreAsset", "restoreAsset"), []string{"asset", "restore", p}, exitcode.NotFound, "no asset found"},
		{"billing prices get", null("AiModelPrice", "aiModelPrice"), []string{"billing", "prices", "get", p}, exitcode.NotFound, "rate row not found"},
		{"billing balance", null("CreditBalance", "creditBalance"), []string{"billing", "balance", p}, exitcode.Error, "the server returned no balance"},
		{"channel create", null("CreateChannel", "createChannel"), []string{"channel", "create", p, "-m", "acme.com::dev", "--loc", "chats:x"}, exitcode.Unavailable, "the server returned no Channel"},
		{"channel get", null("GetChannel", "channel"), []string{"channel", "get", p}, exitcode.NotFound, "no Channel is readable here"},
		{"channel read", null("ChannelMessages", "channelMessages"), []string{"channel", "read", p}, exitcode.NotFound, "no Channel is readable here"},
		{"channel register rm", map[string]string{"GetRegisterEntry": `{"data":{"registerEntry":null}}`, "DeleteRegisterEntry": `{"data":{"deleteRegisterEntry":false}}`}, []string{"channel", "register", "rm", p, "--yes"}, exitcode.NotFound, "no register entry was removed"},
		{"channel rm", map[string]string{"DeleteChannel": `{"data":{"deleteChannel":false}}`}, []string{"channel", "rm", p, "--yes"}, exitcode.NotFound, "no Channel was deleted"},
		{"channel update", null("UpdateChannel", "updateChannel"), []string{"channel", "update", p, "--name", "x"}, exitcode.Unavailable, "the server returned no Channel"},
		{"connection grant get", null("ConnectionGrant", "connectionGrant"), []string{"connection", "grant", "get", p}, exitcode.NotFound, "connection grant not found"},
		{"connection grant revoke", map[string]string{"RevokeConnectionGrant": `{"data":{"revokeConnectionGrant":false}}`}, []string{"connection", "grant", "revoke", p, "--yes"}, exitcode.NotFound, "connection grant not found"},
		{"edge get", null("GetEdge", "edge"), []string{"edge", "get", p}, exitcode.NotFound, "edge not found"},
		{"grant get", null("PrincipalGrant", "principalGrant"), []string{"grant", "get", p}, exitcode.NotFound, "grant not found"},
		{"grant revoke", map[string]string{"RevokePrincipalGrant": `{"data":{"revokePrincipalGrant":false}}`}, []string{"grant", "revoke", p, "--yes"}, exitcode.NotFound, "grant not found"},
		{"ha get", null("HomeAssistantInstance", "homeAssistantInstance"), []string{"ha", "get", p}, exitcode.NotFound, "Home Assistant instance not found"},
		{"ha ops", null("HomeAssistantInstanceOps", "homeAssistantInstanceOps"), []string{"ha", "ops", p}, exitcode.NotFound, "Home Assistant instance not found"},
		{"mcp-server delete", map[string]string{"DeleteMcpServer": `{"data":{"deleteMcpServer":false}}`}, []string{"mcp-server", "delete", p, "--yes"}, exitcode.NotFound, "MCP server not found"},
		{"mcp-server get", null("McpServer", "mcpServer"), []string{"mcp-server", "get", p}, exitcode.NotFound, "MCP server not found"},
		{"mcp-server tools", null("McpServerTools", "mcpServerTools"), []string{"mcp-server", "tools", p}, exitcode.NotFound, "MCP server not found"},
		{"mcp-server update", null("UpdateMcpServer", "updateMcpServer"), []string{"mcp-server", "update", p, "--name", "x"}, exitcode.NotFound, "MCP server not found"},
		{"memory member list", map[string]string{"GetMemory": memoryResolves3029, "MemoryMembers": `{"data":{"memory":null}}`}, []string{"memory", "member", "list", "acme.com::" + p}, exitcode.NotFound, "memory not found"},
		{"memory share list", map[string]string{"GetMemory": memoryResolves3029, "MemoryShares": `{"data":{"memory":null}}`}, []string{"memory", "share", "list", "acme.com::" + p}, exitcode.NotFound, "memory not found"},
		{"memory subscription list", map[string]string{"GetMemory": memoryResolves3029, "MemorySubscriptions": `{"data":{"memory":null}}`}, []string{"memory", "subscription", "list", "acme.com::" + p}, exitcode.NotFound, "memory not found"},
		{"node signatures", map[string]string{"ResolveUrn": resolvesToNode3029, "NodeSignatureStatus": `{"data":{"node":null}}`}, []string{"node", "signatures", node}, exitcode.NotFound, "node not found or not readable by you"},
		{"node signing-request get", null("GetSigningRequest", "signingRequest"), []string{"node", "signing-request", "get", p}, exitcode.NotFound, "signing request not found"},
		{"object delete", map[string]string{"DeleteObject": `{"data":{"deleteObject":false}}`}, []string{"object", "delete", p, "--yes"}, exitcode.NotFound, "object not found or already deleted"},
		{"object get", null("GetObject", "object"), []string{"object", "get", p}, exitcode.NotFound, "object not found"},
		{"org invite show", null("GetInvitation", "invitation"), []string{"org", "invite", "show", p}, exitcode.NotFound, "invitation not found"},
		{"org invite accept", map[string]string{"AcceptInvitation": `{"data":{"acceptInvitation":false}}`}, []string{"org", "invite", "accept", p}, exitcode.Error, "the invitation was not accepted"},
		{"run get", null("AppRun", "appRun"), []string{"run", "get", p}, exitcode.NotFound, "run not found"},
		{"schedule rm", map[string]string{"DeleteAgentSchedule": `{"data":{"deleteAgentSchedule":false}}`}, []string{"schedule", "rm", p, "--yes"}, exitcode.NotFound, "schedule not found"},
		{"scope create", null("CreateScope", "createScope"), []string{"scope", "create", p, "--owner-org", "acme.com", "-m", "acme.com::dev"}, exitcode.Unavailable, "the server returned no scope"},
		{"scope explain", null("ScopeExplain", "scopeExplain"), []string{"scope", "explain", p, "--app", "acme.com:dev"}, exitcode.NotFound, "no scope is readable here"},
		{"scope get", map[string]string{"ScopeExplain": scopeResolves3029, "GetScope": `{"data":{"scope":null}}`}, []string{"scope", "get", p, "--app", "acme.com:dev"}, exitcode.NotFound, "no scope is readable here"},
		{"scope update", map[string]string{"ScopeExplain": scopeResolves3029, "UpdateScope": `{"data":{"updateScope":null}}`}, []string{"scope", "update", p, "--description", "x", "--app", "acme.com:dev"}, exitcode.Unavailable, "the server returned no scope"},
		{"secret get", null("Secret", "secret"), []string{"secret", "get", p}, exitcode.NotFound, "secret not found"},
		{"secret versions", null("SecretVersions", "secret"), []string{"secret", "versions", p}, exitcode.NotFound, "secret not found"},
		{"secret versions: no versions page", map[string]string{"SecretVersions": `{"data":{"secret":{"id":"s1","name":"n","currentVersion":1,"protected":false,"versions":null}}}`}, []string{"secret", "versions", p}, exitcode.Error, "server returned no versions page for the secret"},
		{"secret exec", map[string]string{}, []string{"secret", "exec", "--client", "c1", "--env", "TOKEN", p, "--", "true"}, exitcode.Usage, "unexpected argument before --"},
		{"spec extract", map[string]string{}, []string{"spec", "extract", p, "--title", "x", "--to-feature", "001", "-m", "acme.com::specs"}, exitcode.Usage, "is not one"},
		{"spec new --new-path", map[string]string{}, []string{"spec", "new", p, "--new-path", "--title", "x", "-m", "acme.com::specs"}, exitcode.Usage, "is not one"},
		{"team grant get", null("TeamGrant", "grant"), []string{"team", "grant", "get", p}, exitcode.NotFound, "grant not found"},
		{"team grant action", null("GrantAction", "action"), []string{"team", "grant", "action", p, "--app", "hrn:app:acme.com:" + p}, exitcode.NotFound, "grant action not found in this App"},
		{"team role-lists set", map[string]string{}, []string{"team", "role-lists", "set", p, "--clear"}, exitcode.Usage, "unknown purpose"},
		{"team session outcome", map[string]string{}, []string{"team", "session", "outcome", "s1", p}, exitcode.Usage, "the outcome must be a number from 0 to 1"},
		{"team task list stray arg", map[string]string{}, []string{"team", "task", "list", "--team", p}, exitcode.Usage, "unexpected argument"},
		{"coding preflight route: the router itself", map[string]string{"GetNodeRaw": codingRouterWithBody(flatRouterBody), "ResolveUrn": `{"data":{"resolveUrn":{"id":"root","kind":"node","memoryId":"mem1"}}}`}, []string{"coding", "preflight", "route", node, "-m", codingMem, "--route", "x"}, exitcode.Usage, "is the router itself"},
		{"coding preflight route: target unreadable", map[string]string{"GetNodeRaw": codingRouterWithBody(flatRouterBody), "ResolveUrn": resolvesToNode3029, "GetNode": `{"data":{"node":null}}`}, []string{"coding", "preflight", "route", node, "-m", codingMem, "--route", "x"}, exitcode.NotFound, "the node could not be read"},
		{"edge list", map[string]string{"ResolveUrn": resolvesToNode3029, "NodeEdgesAttributed": `{"data":{"node":null}}`}, []string{"edge", "list", node}, exitcode.NotFound, "node not found"},
		{"memory config rule add", null("CreateNodeRoleRule", "createNodeRoleRule"), []string{"memory", "config", "rule", "add", "acme.com::dev", p}, exitcode.Error, "the server returned no rule for the role"},
		{"memory reembed-failed", map[string]string{"GetMemory": memoryResolves3029, "RetryFailedEmbeddings": `{"data":{"retryFailedEmbeddings":null}}`}, []string{"memory", "reembed-failed", "acme.com::" + p}, exitcode.Unavailable, "reembed-failed outcome unknown"},
		{"node export", map[string]string{"ResolveUrn": resolvesToNode3029, "NodeExport": `{"data":{"nodeExport":null}}`}, []string{"node", "export", node}, exitcode.NotFound, "the node is not readable"},
		{"node signing-request list", map[string]string{"ResolveUrn": resolvesToNode3029, "NodeSigningRequests": `{"data":{"node":null}}`}, []string{"node", "signing-request", "list", node}, exitcode.NotFound, "node not found or not readable by you"},
		{"node update --dry-run", map[string]string{"ResolveUrn": resolvesToNode3029, "GetNodeRaw": `{"data":{"node":null}}`}, []string{"node", "update", node, "--content", "x", "--dry-run"}, exitcode.NotFound, "node not found, or not readable by you"},
		{"node update --role \"\"", map[string]string{"ResolveUrn": resolvesToNode3029, "GetNode": `{"data":{"node":null}}`}, []string{"node", "update", node, "--role", ""}, exitcode.NotFound, "node not found, or not readable by you"},
		{"secret update --protected answered with the secret", map[string]string{"UpdateSecretProtected": `{"data":{"updateSecret":` + secretJSON + `}}`}, []string{"secret", "update", p, "--protected"}, exitcode.Unavailable, "the secret ref given is not shown"},
		{"team role get", map[string]string{"TeamRoles": teamRolesJSON, "TeamAppIdentity": teamAppIdentityJSON}, []string{"team", "role", "get", p, "--app", "acme.com:eng-team"}, exitcode.NotFound, "no such role in this App"},
		{"webhook rm", map[string]string{"DeleteAgentWebhook": `{"data":{"deleteAgentWebhook":false}}`}, []string{"webhook", "rm", p, "--yes"}, exitcode.NotFound, "webhook not found"},
	} {
		t.Run(tc.name, func(t *testing.T) { checkNoEcho3029(t, tc) })
	}
}

// `spec edit --expected-node-id` refuses a citation that now names another node (exit 5). The refusal
// names both node ids (server values) but not the citation the operator typed.
func TestSpecEditReplacedNodeRefusalNeverEchoesTheCitation3029(t *testing.T) {
	p := tok3029()
	cite := "custom:" + p
	hash := previewProposalHash(t, "# new body\n")
	m := guardMocks(guardWriteOK)
	m["GetSpecNodeForEdit"][0] = strings.Replace(m["GetSpecNodeForEdit"][0], `"id":"sp1"`, `"id":"sp2"`, 1)
	gql, captured := queueGraphQL(t, m)
	f, out := testFactory(t)
	var stderr strings.Builder
	f.IOStreams.ErrOut = &stderr
	f.IOStreams.In = strings.NewReader("# new body\n")
	root := NewRootCmd(f)
	root.SetArgs([]string{"spec", "edit", cite, "-m", specMem, "--server", gql.URL, "--content", "-",
		"--expected-revision", "7", "--expected-node-id", "sp1", "--expected-proposal-hash", hash})
	err := root.Execute()
	if exitCodeFor(err) != exitcode.Conflict || !strings.Contains(fmt.Sprint(err), "not the previewed node sp1") {
		t.Fatalf("this must reach the replaced-node refusal (exit 5): %d %v", exitCodeFor(err), err)
	}
	if _, wrote := captured["UpdateSpecNode"]; wrote {
		t.Error("nothing is written")
	}
	renderError(f, err)
	f.JSON = true
	renderError(f, err)
	everything := err.Error() + out.String() + stderr.String()
	for _, leak := range []string{p, p[:8], p[len(p)-8:]} {
		if strings.Contains(everything, leak) {
			t.Errorf("the refusal echoes %q of the citation: %q", leak, everything)
		}
	}
	if !strings.Contains(err.Error(), "not shown") {
		t.Errorf("says the citation is not shown: %v", err)
	}
}
