package cmd

import (
	"encoding/json"
	"strings"
	"testing"
)

const uUserJSON = `{"id":"usr1","name":"Alice","email":"alice@acme.com","handle":"alice",
	"githubUsername":null,"roles":["CONTRIBUTOR"]}`

func TestUserSearch(t *testing.T) {
	gql, captured := captureGraphQL(t, map[string]string{
		"SearchUsers": `{"data":{"users":{"total":1,"items":[` + uUserJSON + `]}}}`,
	})
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"user", "search", "alice", "--json", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	var vars map[string]any
	_ = json.Unmarshal(captured["SearchUsers"], &vars)
	if vars["query"] != "alice" {
		t.Errorf("search query var: %v", vars)
	}
	var users []struct {
		ID     string `json:"id"`
		Handle string `json:"handle"`
	}
	if err := json.Unmarshal([]byte(out.String()), &users); err != nil {
		t.Fatalf("not a JSON array: %v\n%s", err, out.String())
	}
	if len(users) != 1 || users[0].Handle != "alice" {
		t.Errorf("users: %+v", users)
	}
}

func TestProfileSet(t *testing.T) {
	gql, captured := captureGraphQL(t, map[string]string{
		"UpdateMyProfile": `{"data":{"updateMyProfile":` + uUserJSON + `}}`,
	})
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"profile", "set", "--name", "Alice A", "--handle", "alice", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	var vars map[string]any
	_ = json.Unmarshal(captured["UpdateMyProfile"], &vars)
	if vars["name"] != "Alice A" || vars["handle"] != "alice" {
		t.Errorf("profile vars: %v", vars)
	}
	// --email was not passed, so it must be omitted (leave the field unchanged).
	if _, present := vars["email"]; present {
		t.Errorf("unset --email must be omitted, got %v", vars["email"])
	}
}

func TestProfileSetNothingIsUsageError(t *testing.T) {
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"profile", "set", "--server", "http://127.0.0.1:1"})
	err := root.Execute()
	if err == nil || !strings.Contains(err.Error(), "nothing to update") {
		t.Fatalf("expected nothing-to-update usage error, got %v", err)
	}
}
