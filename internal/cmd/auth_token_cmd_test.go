package cmd

import (
	"encoding/json"
	"strings"
	"testing"
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
