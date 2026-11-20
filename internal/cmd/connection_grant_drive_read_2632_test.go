package cmd

import (
	"encoding/json"
	"strings"
	"testing"
)

// cli#2632 item 2: the server's createConnectionGrant accepts drive.read, so
// the client-side scope check must too, and send it.
func TestConnectionGrantCreateAcceptsDriveRead2632(t *testing.T) {
	gql, captured := captureGraphQL(t, map[string]string{
		"CreateConnectionGrant": `{"data":{"createConnectionGrant":` + connGrantJSON + `}}`,
	})
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"connection", "grant", "create",
		"--connection", "conn_123", "--app", "capp100000000000000000000", "--scopes", "drive.read", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("drive.read must pass the client-side check: %v", err)
	}
	var vars struct {
		Scopes []string `json:"scopes"`
	}
	if err := json.Unmarshal(captured["CreateConnectionGrant"], &vars); err != nil {
		t.Fatal(err)
	}
	if strings.Join(vars.Scopes, ",") != "drive.read" {
		t.Errorf("the scope must be sent, got %v", vars.Scopes)
	}
}

// A scope outside the set is still refused, and the refusal lists drive.read.
func TestConnectionGrantCreateRefusalListsDriveRead2632(t *testing.T) {
	gql, captured := captureGraphQL(t, map[string]string{
		"CreateConnectionGrant": `{"data":{"createConnectionGrant":` + connGrantJSON + `}}`,
	})
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"connection", "grant", "create",
		"--connection", "conn_123", "--app", "capp100000000000000000000", "--scopes", "drive.write", "--server", gql.URL})
	err := root.Execute()
	if err == nil {
		t.Fatal("drive.write is not a scope and must be refused")
	}
	if !strings.Contains(err.Error(), "drive.read") {
		t.Errorf("the refusal must list drive.read: %v", err)
	}
	if _, called := captured["CreateConnectionGrant"]; called {
		t.Error("an invalid scope must not reach the server")
	}
}

// cli#2632 items 1 and 2: the help names the flow command as the way back to
// the connection ID, and says what drive.read allows and does not.
func TestConnectionGrantCreateHelpDriveReadAndFlow2632(t *testing.T) {
	help := flatHelp1053S3(t, "connection", "grant", "create")
	for _, want := range []string{
		"hadron connection flow <flow-id>",
		"drive.read",
		"read files in the connected Drive",
		"list, search, get, export and download",
		"does not create or change anything",
		"nothing on mail or calendar",
	} {
		if !strings.Contains(help, want) {
			t.Errorf("connection grant create help lacks %q", want)
		}
	}
	for _, banned := range []string{"drive.write", "view files", "browse"} {
		if strings.Contains(help, banned) {
			t.Errorf("connection grant create help carries %q", banned)
		}
	}
	group := flatHelp1053S3(t, "connection", "grant")
	if !strings.Contains(group, "drive.read") {
		t.Errorf("connection grant help must list drive.read")
	}
}
