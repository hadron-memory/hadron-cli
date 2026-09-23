package cmd

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/auth"
	"github.com/hadron-memory/hadron-cli/internal/auth/store"
	"github.com/hadron-memory/hadron-cli/internal/exitcode"
)

// #681: a key whose OAuth grant is `mcp` alone.
//
// mcpOnlyRefusalBody is hadron-server's refusal EXACTLY as it reaches the CLI.
// The /graphql context builder throws it (src/server.ts,
// `authContextAllowsOAuthSurface(auth, 'account')`) before any resolver runs.
// Apollo 4 keeps a thrown GraphQLError as is, with no "Context creation
// failed" prefix, and answers HTTP 500, because the error sets no http
// extension. So the fake serves the status too: a 200 would skip the #544
// gateway classifier this body has to survive in production.
const mcpOnlyRefusalBody = `{"errors":[{"message":"This OAuth credential is limited to the MCP surface.","extensions":{"code":"FORBIDDEN"}}]}`

// graphQLAlways answers every operation with one status and body.
func graphQLAlways(t *testing.T, status int, body string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestAuthStatusReportsAnMCPOnlyKeyAsUnusable(t *testing.T) {
	gql := graphQLAlways(t, http.StatusInternalServerError, mcpOnlyRefusalBody)
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"auth", "status", "--json", "--server", gql.URL})
	err := root.Execute()

	// 3, not 8: the remedy is a different credential, not someone granting access.
	if code := exitCodeFor(err); code != exitcode.AuthRequired {
		t.Fatalf("exit code = %d, want %d (AuthRequired): %v", code, exitcode.AuthRequired, err)
	}
	var dto map[string]any
	if jerr := json.Unmarshal([]byte(out.String()), &dto); jerr != nil {
		t.Fatalf("status must still print its report: %v\n%q", jerr, out.String())
	}
	if dto["authenticated"] != false || dto["rejectedReason"] != "mcp-only-scope" || dto["tokenSource"] != "HADRON_TOKEN" {
		t.Errorf("want authenticated:false, rejectedReason:mcp-only-scope, tokenSource:HADRON_TOKEN; got %v", dto)
	}
}

// The remedy follows the key's source. A key in HADRON_TOKEN outranks the store,
// so `auth logout && auth login` would change nothing while it stays set.
func TestAuthStatusMCPOnlyRemedyFollowsTheTokenSource(t *testing.T) {
	gql := graphQLAlways(t, http.StatusInternalServerError, mcpOnlyRefusalBody)

	t.Run("env", func(t *testing.T) {
		f, out := testFactory(t)
		root := NewRootCmd(f)
		root.SetArgs([]string{"auth", "status", "--server", gql.URL})
		_ = root.Execute()
		got := out.String()
		for _, want := range []string{"limited to the MCP surface", "HADRON_TOKEN holds an MCP-only key", "/app/account/api-keys"} {
			if !strings.Contains(got, want) {
				t.Errorf("missing %q:\n%s", want, got)
			}
		}
		if strings.Contains(got, "auth logout") {
			t.Errorf("logout cannot help while HADRON_TOKEN is set:\n%s", got)
		}
	})

	t.Run("store", func(t *testing.T) {
		f, out := testFactory(t)
		t.Setenv("HADRON_TOKEN", "")
		st := memStore{auth.Host(gql.URL): "hdr_user_mcponly"}
		f.TokenStoreFn = func() store.Store { return st }
		root := NewRootCmd(f)
		root.SetArgs([]string{"auth", "status", "--server", gql.URL})
		err := root.Execute()
		if code := exitCodeFor(err); code != exitcode.AuthRequired {
			t.Fatalf("exit code = %d, want %d: %v", code, exitcode.AuthRequired, err)
		}
		got := out.String()
		for _, want := range []string{"limited to the MCP surface", "hadron auth logout && hadron auth login", "--with-token", "/app/account/api-keys"} {
			if !strings.Contains(got, want) {
				t.Errorf("missing %q:\n%s", want, got)
			}
		}
	})
}

// Every other command gets the same exit code and names both recoveries.
func TestMCPOnlyRefusalNamesARecoveryOnAnyCommand(t *testing.T) {
	gql := graphQLAlways(t, http.StatusInternalServerError, mcpOnlyRefusalBody)
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"memory", "list", "--server", gql.URL})
	err := root.Execute()
	if code := exitCodeFor(err); code != exitcode.AuthRequired {
		t.Fatalf("exit code = %d, want %d (AuthRequired): %v", code, exitcode.AuthRequired, err)
	}
	msg := err.Error()
	for _, want := range []string{
		"This OAuth credential is limited to the MCP surface.", // the server's words, kept
		"hadron auth logout && hadron auth login",
		"/app/account/api-keys",
		"--with-token",
	} {
		if !strings.Contains(msg, want) {
			t.Errorf("missing %q: %s", want, msg)
		}
	}
}

// createUserApiKey's resolver refuses the same key with its own sentence, which
// arrives as an HTTP 200 envelope rather than the context gate's 500. The
// /graphql gate normally fires first, so this pins the shared prefix: if the
// gate ever moves, `auth token create` still names the recovery, when today it
// would name a remedy that fails.
func TestAuthTokenCreateMCPOnlyNamesARecovery(t *testing.T) {
	gql := graphQLAlways(t, http.StatusOK,
		`{"data":null,"errors":[{"message":"This OAuth credential is limited to the MCP surface and cannot create API keys.","extensions":{"code":"FORBIDDEN"}}]}`)
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"auth", "token", "create", "--label", "x", "--server", gql.URL})
	err := root.Execute()
	if code := exitCodeFor(err); code != exitcode.AuthRequired {
		t.Fatalf("exit code = %d, want %d: %v", code, exitcode.AuthRequired, err)
	}
	if !strings.Contains(err.Error(), "/app/account/api-keys") {
		t.Errorf("no recovery named: %v", err)
	}
}

// An MCP-only key is not "invalid, revoked, or expired": it works on MCP.
func TestAuthTokenValidateReportsAnMCPOnlyKey(t *testing.T) {
	gql := graphQLAlways(t, http.StatusInternalServerError, mcpOnlyRefusalBody)
	f, out := testFactory(t)
	f.IOStreams.In = strings.NewReader("hdr_user_mcponly\n")
	root := NewRootCmd(f)
	root.SetArgs([]string{"auth", "token", "validate", "--json", "--server", gql.URL})
	err := root.Execute()
	if code := exitCodeFor(err); code != exitcode.AuthRequired {
		t.Fatalf("exit code = %d, want %d: %v", code, exitcode.AuthRequired, err)
	}
	var dto map[string]any
	if jerr := json.Unmarshal([]byte(out.String()), &dto); jerr != nil {
		t.Fatalf("not JSON: %v\n%q", jerr, out.String())
	}
	if dto["valid"] != false || dto["rejectedReason"] != "mcp-only-scope" {
		t.Errorf("want valid:false, rejectedReason:mcp-only-scope; got %v", dto)
	}
}

// Detection matches server PROSE, so it must fail SAFE. The code alone and the
// sentence alone must each fall through to what the error meant before #681:
// a plain FORBIDDEN still exits 8, and nothing gets a remedy it doesn't need.
func TestMCPOnlyDetectionNeedsBothCodeAndMessage(t *testing.T) {
	for _, c := range []struct {
		name, body string
		want       int
	}{
		// The server rewords the refusal: back to the plain FORBIDDEN mapping.
		{"reworded FORBIDDEN", `{"errors":[{"message":"This credential only covers MCP.","extensions":{"code":"FORBIDDEN"}}]}`, exitcode.Forbidden},
		// Any other permission boundary keeps its exit 8.
		{"other FORBIDDEN", `{"errors":[{"message":"You do not have access to this memory.","extensions":{"code":"FORBIDDEN"}}]}`, exitcode.Forbidden},
		// The sentence under a different code does not qualify.
		{"sentence without FORBIDDEN", `{"errors":[{"message":"This OAuth credential is limited to the MCP surface.","extensions":{"code":"INTERNAL_SERVER_ERROR"}}]}`, exitcode.Error},
	} {
		t.Run(c.name, func(t *testing.T) {
			gql := graphQLAlways(t, http.StatusOK, c.body)
			f, _ := testFactory(t)
			root := NewRootCmd(f)
			root.SetArgs([]string{"memory", "list", "--server", gql.URL})
			err := root.Execute()
			if code := exitCodeFor(err); code != c.want {
				t.Errorf("exit code = %d, want %d: %v", code, c.want, err)
			}
			if err != nil && strings.Contains(err.Error(), "/app/account/api-keys") {
				t.Errorf("an unrelated refusal got the MCP-only remedy: %v", err)
			}
		})
	}
}

// install/uninstall rewrites any FORBIDDEN into "you need CONTRIBUTOR+ on the
// org" (#389). For an MCP-only key that is a false remedy, since no role lets
// that key through, so the credential fix must win. The 200-shaped refusal is
// the one that reaches the guidance: the 500 one is not seen by HasErrorCode.
func TestAppAgentAddMCPOnlyGetsTheCredentialFixNotTheRoleRule(t *testing.T) {
	gql := graphQLAlways(t, http.StatusOK,
		`{"data":null,"errors":[{"message":"This OAuth credential is limited to the MCP surface.","extensions":{"code":"FORBIDDEN"}}]}`)
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"app", "agent", "add", "hrn:app:acme.com:eng", "hrn:agent:acme.com:iris", "--server", gql.URL})
	err := root.Execute()
	if code := exitCodeFor(err); code != exitcode.AuthRequired {
		t.Fatalf("exit code = %d, want %d: %v", code, exitcode.AuthRequired, err)
	}
	if strings.Contains(err.Error(), "CONTRIBUTOR") || !strings.Contains(err.Error(), "/app/account/api-keys") {
		t.Errorf("want the credential fix, not the role rule: %v", err)
	}
}

// `hadron api` is the raw path. It reports the server's envelope as is and
// classifies it by extensions.code alone, so this refusal exits 8 there.
// agentic-usage documents that exception, and this test pins it.
func TestRawAPIReportsTheMCPOnlyRefusalUnmapped(t *testing.T) {
	gql := graphQLAlways(t, http.StatusInternalServerError, mcpOnlyRefusalBody)
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"api", "{ __typename }", "--server", gql.URL})
	err := root.Execute()
	if code := exitCodeFor(err); code != exitcode.Forbidden {
		t.Errorf("exit code = %d, want %d (the raw path maps extensions.code only): %v", code, exitcode.Forbidden, err)
	}
}

// `agent create --install-into` (PR #683 review, @copilot): the create has
// succeeded, so the error names the finishing command. For an MCP-only key that
// command fails the same way until the key is replaced, so it has to come second.
func TestAgentCreateInstallIntoMCPOnlyFinishesAfterTheCredentialFix(t *testing.T) {
	gql, _ := captureGraphQL(t, map[string]string{
		"CreateAgent":         `{"data":{"createAgent":` + agentJSON + `}}`,
		"InstallAgentIntoApp": `{"data":null,"errors":[{"message":"This OAuth credential is limited to the MCP surface.","extensions":{"code":"FORBIDDEN"}}]}`,
	})
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"agent", "create", "--org", "acme.com", "--name", "Support Bot",
		"--install-into", "hrn:app:acme.com:eng-team", "--server", gql.URL})
	err := root.Execute()
	if code := exitCodeFor(err); code != exitcode.AuthRequired {
		t.Fatalf("exit code = %d, want %d: %v", code, exitcode.AuthRequired, err)
	}
	msg := err.Error()
	for _, want := range []string{"CREATED but NOT installed", "/app/account/api-keys", "Once the key is replaced, finish with: hadron app agent add"} {
		if !strings.Contains(msg, want) {
			t.Errorf("missing %q: %s", want, msg)
		}
	}
	if strings.Contains(msg, "CONTRIBUTOR") {
		t.Errorf("the role rule is a false remedy for this key: %s", msg)
	}
}

// The human rendering of `auth token validate` (PR #683 review, @copilot).
func TestAuthTokenValidateMCPOnlyHuman(t *testing.T) {
	gql := graphQLAlways(t, http.StatusInternalServerError, mcpOnlyRefusalBody)
	f, out := testFactory(t)
	f.IOStreams.In = strings.NewReader("hdr_user_mcponly\n")
	root := NewRootCmd(f)
	root.SetArgs([]string{"auth", "token", "validate", "--server", gql.URL})
	err := root.Execute()
	if code := exitCodeFor(err); code != exitcode.AuthRequired {
		t.Fatalf("exit code = %d, want %d: %v", code, exitcode.AuthRequired, err)
	}
	got := out.String()
	if !strings.Contains(got, "limited to the MCP surface") || !strings.Contains(got, "/app/account/api-keys") {
		t.Errorf("want the MCP-only explanation and the portal page:\n%s", got)
	}
	if strings.Contains(got, "invalid, revoked, or expired") {
		t.Errorf("an MCP-only key is not invalid:\n%s", got)
	}
}
