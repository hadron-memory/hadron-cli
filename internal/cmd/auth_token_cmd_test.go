package cmd

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/exitcode"
)

const apiKeyJSON = `{"id":"uak_1","label":"ci-deploy","keyPreview":"hdrk_ab1","issuedVia":"cli","createdAt":"2026-06-19T00:00:00Z","lastUsedAt":null,"revokedAt":null}`

func TestAuthTokenCreate(t *testing.T) {
	gql, captured := captureGraphQL(t, map[string]string{
		"CreateUserApiKey": `{"data":{"createUserApiKey":{"rawKey":"hdr_user_secret123","userApiKey":` + apiKeyJSON + `}}}`,
	})
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"auth", "token", "create", "--label", "ci-deploy", "--json", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	var dto struct {
		ID      string `json:"id"`
		RawKey  string `json:"rawKey"`
		Revoked bool   `json:"revoked"`
	}
	if err := json.Unmarshal([]byte(out.String()), &dto); err != nil {
		t.Fatalf("not JSON: %v\n%s", err, out.String())
	}
	if dto.RawKey != "hdr_user_secret123" || dto.ID != "uak_1" || dto.Revoked {
		t.Errorf("dto = %+v", dto)
	}
	var vars struct {
		Label string `json:"label"`
	}
	_ = json.Unmarshal(captured["CreateUserApiKey"], &vars)
	if vars.Label != "ci-deploy" {
		t.Errorf("--label should be sent, got %s", captured["CreateUserApiKey"])
	}
}

// The human path prints the raw key once with a "store it now" warning.
func TestAuthTokenCreateShowsKeyOnce(t *testing.T) {
	gql, captured := captureGraphQL(t, map[string]string{
		"CreateUserApiKey": `{"data":{"createUserApiKey":{"rawKey":"hdr_user_secret123","userApiKey":` + apiKeyJSON + `}}}`,
	})
	f, out := testFactory(t)
	root := NewRootCmd(f)
	// no --label: the variable must be omitted (server applies its default)
	root.SetArgs([]string{"auth", "token", "create", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	got := out.String()
	if !strings.Contains(got, "hdr_user_secret123") || !strings.Contains(got, "only time") {
		t.Errorf("create output must show the key once with a warning:\n%s", got)
	}
	var vars map[string]any
	_ = json.Unmarshal(captured["CreateUserApiKey"], &vars)
	if _, present := vars["label"]; present {
		t.Errorf("unset --label must be omitted, got %v", vars["label"])
	}
}

func TestAuthTokenLs(t *testing.T) {
	const revoked = `{"id":"uak_2","label":null,"keyPreview":"hdrk_ab2","issuedVia":"oauth","createdAt":"2026-06-18T00:00:00Z","lastUsedAt":null,"revokedAt":"2026-06-19T00:00:00Z"}`
	gql, _ := captureGraphQL(t, map[string]string{
		"MyUserApiKeys": `{"data":{"myUserApiKeys":[` + apiKeyJSON + `,` + revoked + `]}}`,
	})
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"auth", "token", "ls", "--json", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	var tokens []struct {
		ID      string `json:"id"`
		Revoked bool   `json:"revoked"`
	}
	if err := json.Unmarshal([]byte(out.String()), &tokens); err != nil {
		t.Fatalf("not a JSON array: %v\n%s", err, out.String())
	}
	if len(tokens) != 2 || tokens[0].Revoked || !tokens[1].Revoked {
		t.Errorf("revoked derivation wrong: %+v", tokens)
	}
}

func TestAuthTokenLsHuman(t *testing.T) {
	gql, _ := captureGraphQL(t, map[string]string{
		"MyUserApiKeys": `{"data":{"myUserApiKeys":[` + apiKeyJSON + `]}}`,
	})
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"auth", "token", "ls", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	got := out.String()
	for _, want := range []string{"uak_1", "ci-deploy", "hdrk_ab1", "active", "never"} {
		if !strings.Contains(got, want) {
			t.Errorf("ls table missing %q:\n%s", want, got)
		}
	}
}

// A token the server accepts prints valid:true with the owning user and exits 0.
func TestAuthTokenValidateValid(t *testing.T) {
	gql := fakeGraphQL(t, map[string]string{
		"Me": `{"data":{"me":{"id":"u1","name":"Holger","email":"h@example.com","handle":null,"githubUsername":null,"roles":["USER"]}}}`,
	})
	f, out := testFactory(t)
	f.IOStreams.In = strings.NewReader("hdr_user_secret123\n")
	root := NewRootCmd(f)
	root.SetArgs([]string{"auth", "token", "validate", "--json", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	var dto struct {
		Valid  bool     `json:"valid"`
		UserID string   `json:"userId"`
		Email  string   `json:"email"`
		Roles  []string `json:"roles"`
	}
	if err := json.Unmarshal([]byte(out.String()), &dto); err != nil {
		t.Fatalf("not JSON: %v\n%s", err, out.String())
	}
	if !dto.Valid || dto.UserID != "u1" || dto.Email != "h@example.com" || len(dto.Roles) != 1 {
		t.Errorf("dto = %+v", dto)
	}
}

// A rejected token is a definitive answer, not a CLI failure: valid:false and exit 3.
func TestAuthTokenValidateInvalid(t *testing.T) {
	gql := fakeGraphQL(t, map[string]string{
		"Me": `{"data":{"me":null},"errors":[{"message":"not signed in","extensions":{"code":"UNAUTHENTICATED"}}]}`,
	})
	f, out := testFactory(t)
	f.IOStreams.In = strings.NewReader("hdr_user_bad\n")
	root := NewRootCmd(f)
	root.SetArgs([]string{"auth", "token", "validate", "--json", "--server", gql.URL})
	err := root.Execute()
	if code := exitcode.FromError(err); code != exitcode.AuthRequired {
		t.Fatalf("expected exit %d, got %d (err=%v)", exitcode.AuthRequired, code, err)
	}
	var dto struct {
		Valid bool `json:"valid"`
	}
	if jsonErr := json.Unmarshal([]byte(out.String()), &dto); jsonErr != nil {
		t.Fatalf("not JSON: %v\n%s", jsonErr, out.String())
	}
	if dto.Valid {
		t.Errorf("expected valid:false, got %s", out.String())
	}
}

// An empty stdin is a usage error — there is no token to check.
func TestAuthTokenValidateEmptyStdin(t *testing.T) {
	f, _ := testFactory(t)
	f.IOStreams.In = strings.NewReader("")
	root := NewRootCmd(f)
	root.SetArgs([]string{"auth", "token", "validate", "--server", "http://127.0.0.1:1"})
	err := root.Execute()
	if code := exitcode.FromError(err); code != exitcode.Usage {
		t.Fatalf("expected a usage error, got code %d (err=%v)", code, err)
	}
}

func TestAuthTokenRevokeRequiresYes(t *testing.T) {
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"auth", "token", "revoke", "uak_1", "--server", "http://127.0.0.1:1"})
	err := root.Execute()
	if err == nil || !strings.Contains(err.Error(), "--yes") {
		t.Fatalf("expected a --yes refusal, got %v", err)
	}
}

func TestAuthTokenRevokeWithYes(t *testing.T) {
	const revoked = `{"id":"uak_1","label":"ci-deploy","keyPreview":"hdrk_ab1","issuedVia":"cli","createdAt":"2026-06-19T00:00:00Z","lastUsedAt":null,"revokedAt":"2026-06-19T02:00:00Z"}`
	gql, captured := captureGraphQL(t, map[string]string{
		"RevokeUserApiKey": `{"data":{"revokeUserApiKey":` + revoked + `}}`,
	})
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"auth", "token", "revoke", "uak_1", "--yes", "--json", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	var vars struct {
		ID string `json:"id"`
	}
	_ = json.Unmarshal(captured["RevokeUserApiKey"], &vars)
	if vars.ID != "uak_1" {
		t.Errorf("id should be sent, got %s", captured["RevokeUserApiKey"])
	}
	var dto struct {
		Revoked bool `json:"revoked"`
	}
	_ = json.Unmarshal([]byte(out.String()), &dto)
	if !dto.Revoked {
		t.Errorf("revoked should be true after revoke: %s", out.String())
	}
}
