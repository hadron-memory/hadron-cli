package cmd

import (
	"encoding/json"
	"strings"
	"testing"
)

const connGrantJSON = `{"id":"cg_1","connectionId":"conn_123","granteeAppId":"app_1",
	"granteeAppName":"Inbox Bot","granteeAppUrn":"hrn:app:acme.com::inbox-bot",
	"scopes":["mail.read","mail.send"],"expiresAt":null,"createdAt":"2026-07-01T00:00:00Z"}`

// create forwards connection/app/scopes; --scopes is deduped + comma-split, and
// an omitted --expires-at is left off the wire (perpetual).
func TestConnectionGrantCreate(t *testing.T) {
	gql, captured := captureGraphQL(t, map[string]string{
		"CreateConnectionGrant": `{"data":{"createConnectionGrant":` + connGrantJSON + `}}`,
	})
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"connection", "grant", "create",
		"--connection", "conn_123", "--app", "acme.com:inbox-bot",
		"--scopes", "mail.read,mail.read,mail.send", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if !strings.Contains(out.String(), "cg_1") || !strings.Contains(out.String(), "hrn:app:acme.com::inbox-bot") {
		t.Errorf("unexpected output: %s", out.String())
	}
	var vars struct {
		ConnectionRef string   `json:"connectionRef"`
		AppRef        string   `json:"appRef"`
		Scopes        []string `json:"scopes"`
		ExpiresAt     *string  `json:"expiresAt"`
	}
	_ = json.Unmarshal(captured["CreateConnectionGrant"], &vars)
	if vars.ConnectionRef != "conn_123" || vars.AppRef != "acme.com:inbox-bot" {
		t.Errorf("unexpected create vars: %+v", vars)
	}
	if strings.Join(vars.Scopes, ",") != "mail.read,mail.send" {
		t.Errorf("scopes should be deduped/split to [mail.read mail.send], got %v", vars.Scopes)
	}
	if vars.ExpiresAt != nil {
		t.Errorf("unset --expires-at must be omitted, got %v", *vars.ExpiresAt)
	}
}

func TestConnectionGrantCreateForwardsExpiry(t *testing.T) {
	gql, captured := captureGraphQL(t, map[string]string{
		"CreateConnectionGrant": `{"data":{"createConnectionGrant":` + connGrantJSON + `}}`,
	})
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"connection", "grant", "create",
		"--connection", "conn_123", "--app", "app_1", "--scopes", "mail.read",
		"--expires-at", "2027-01-01T00:00:00Z", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	var vars struct {
		ExpiresAt *string `json:"expiresAt"`
	}
	_ = json.Unmarshal(captured["CreateConnectionGrant"], &vars)
	if vars.ExpiresAt == nil || *vars.ExpiresAt != "2027-01-01T00:00:00Z" {
		t.Errorf("--expires-at should forward, got %v", vars.ExpiresAt)
	}
}

// --scopes is required.
func TestConnectionGrantCreateRequiresScopes(t *testing.T) {
	gql, captured := captureGraphQL(t, map[string]string{
		"CreateConnectionGrant": `{"data":{"createConnectionGrant":` + connGrantJSON + `}}`,
	})
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"connection", "grant", "create", "--connection", "conn_123", "--app", "app_1", "--server", gql.URL})
	if err := root.Execute(); err == nil {
		t.Fatal("expected an error when --scopes is omitted")
	}
	if _, called := captured["CreateConnectionGrant"]; called {
		t.Error("mutation must not be sent when a required flag is missing")
	}
}

// An unknown scope is rejected client-side (fixed enum) before any server call.
func TestConnectionGrantCreateRejectsUnknownScope(t *testing.T) {
	gql, captured := captureGraphQL(t, map[string]string{
		"CreateConnectionGrant": `{"data":{"createConnectionGrant":` + connGrantJSON + `}}`,
	})
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"connection", "grant", "create",
		"--connection", "conn_123", "--app", "app_1", "--scopes", "mail.read,mail.reed", "--server", gql.URL})
	if err := root.Execute(); err == nil {
		t.Fatal("expected a usage error for an unknown scope")
	}
	if _, called := captured["CreateConnectionGrant"]; called {
		t.Error("an invalid scope must not reach the server")
	}
}

func TestConnectionGrantLs(t *testing.T) {
	gql, captured := captureGraphQL(t, map[string]string{
		"ConnectionGrants": `{"data":{"connectionGrants":{"items":[` + connGrantJSON + `],"total":1}}}`,
	})
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"connection", "grant", "ls", "--connection", "conn_123", "--json", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	var got []map[string]any
	if err := json.Unmarshal([]byte(out.String()), &got); err != nil {
		t.Fatalf("--json not valid: %v\n%s", err, out.String())
	}
	if len(got) != 1 || got[0]["id"] != "cg_1" {
		t.Errorf("unexpected ls output: %s", out.String())
	}
	var vars struct {
		ConnectionRef *string `json:"connectionRef"`
	}
	_ = json.Unmarshal(captured["ConnectionGrants"], &vars)
	if vars.ConnectionRef == nil || *vars.ConnectionRef != "conn_123" {
		t.Errorf("--connection should forward as connectionRef, got %v", vars.ConnectionRef)
	}
}

func TestConnectionGrantRevoke(t *testing.T) {
	gql, captured := captureGraphQL(t, map[string]string{
		"RevokeConnectionGrant": `{"data":{"revokeConnectionGrant":true}}`,
	})
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"connection", "grant", "revoke", "cg_1", "--yes", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if !strings.Contains(out.String(), "revoked connection grant cg_1") {
		t.Errorf("unexpected output: %s", out.String())
	}
	var vars struct {
		Ref string `json:"ref"`
	}
	_ = json.Unmarshal(captured["RevokeConnectionGrant"], &vars)
	if vars.Ref != "cg_1" {
		t.Errorf("ref should be cg_1, got %q", vars.Ref)
	}
}

func TestConnectionGrantRevokeRequiresYes(t *testing.T) {
	gql, captured := captureGraphQL(t, map[string]string{
		"RevokeConnectionGrant": `{"data":{"revokeConnectionGrant":true}}`,
	})
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"connection", "grant", "revoke", "cg_1", "--server", gql.URL})
	if err := root.Execute(); err == nil {
		t.Fatal("expected refusal without --yes")
	}
	if _, called := captured["RevokeConnectionGrant"]; called {
		t.Error("mutation must not be sent when confirmation is refused")
	}
}

// A false return (should be NOT_FOUND per contract) is surfaced as an error.
func TestConnectionGrantRevokeFalseIsNotFound(t *testing.T) {
	gql, _ := captureGraphQL(t, map[string]string{
		"RevokeConnectionGrant": `{"data":{"revokeConnectionGrant":false}}`,
	})
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"connection", "grant", "revoke", "gone", "--yes", "--server", gql.URL})
	if err := root.Execute(); err == nil {
		t.Fatal("expected a not-found error when the server returns false")
	}
}
