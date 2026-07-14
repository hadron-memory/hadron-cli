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

func TestUserMerge(t *testing.T) {
	gql, captured := captureGraphQL(t, map[string]string{
		"MergeUsers": `{"data":{"mergeUsers":` + uUserJSON + `}}`,
	})
	f, out := testFactory(t)
	root := NewRootCmd(f)
	// Refs pass through verbatim: a bare handle source, a URN target.
	root.SetArgs([]string{"user", "merge", "dup", "--into", "hrn:user:alice", "--yes", "--json", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	var vars map[string]any
	_ = json.Unmarshal(captured["MergeUsers"], &vars)
	if vars["source"] != "dup" || vars["target"] != "hrn:user:alice" {
		t.Errorf("source/target vars = %v/%v, want dup / hrn:user:alice", vars["source"], vars["target"])
	}
	// JSON output is the surviving target user.
	var dto struct {
		ID     string `json:"id"`
		Handle string `json:"handle"`
	}
	if err := json.Unmarshal([]byte(out.String()), &dto); err != nil {
		t.Fatalf("output not JSON: %v\n%s", err, out.String())
	}
	if dto.ID != "usr1" || dto.Handle != "alice" {
		t.Errorf("survivor dto = %+v, want id usr1 / handle alice", dto)
	}
}

func TestUserMergeHumanOutput(t *testing.T) {
	gql, _ := captureGraphQL(t, map[string]string{
		"MergeUsers": `{"data":{"mergeUsers":` + uUserJSON + `}}`,
	})
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"user", "merge", "dup", "--into", "alice", "--yes", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	// Human output names both the source ref and the surviving user id.
	if got := out.String(); !strings.Contains(got, "dup") || !strings.Contains(got, "usr1") {
		t.Errorf("human output should name source and survivor: %q", got)
	}
}

func TestUserMergeRequiresInto(t *testing.T) {
	// A missing/empty --into is a usage error before any GraphQL request.
	for _, args := range [][]string{
		{"user", "merge", "dup", "--yes", "--server", "http://127.0.0.1:1"},
		{"user", "merge", "dup", "--into", "  ", "--yes", "--server", "http://127.0.0.1:1"},
		{"user", "merge", "  ", "--into", "alice", "--yes", "--server", "http://127.0.0.1:1"},
	} {
		f, _ := testFactory(t)
		root := NewRootCmd(f)
		root.SetArgs(args)
		if err := root.Execute(); err == nil {
			t.Errorf("expected a usage error for args %v, got nil", args)
		}
	}
}

// Without --yes and no interactive terminal (the test IOStreams), the
// confirmation gate refuses — user merge is a destructive global operation —
// and MergeUsers must never be reached (cancellation performs no request).
func TestUserMergeRequiresYesNonInteractive(t *testing.T) {
	gql, captured := captureGraphQL(t, map[string]string{
		"MergeUsers": `{"data":{"mergeUsers":` + uUserJSON + `}}`,
	})
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"user", "merge", "dup", "--into", "alice", "--server", gql.URL})
	if err := root.Execute(); err == nil {
		t.Fatal("expected a refusal without --yes in non-interactive mode")
	}
	if _, ok := captured["MergeUsers"]; ok {
		t.Error("MergeUsers must not be called when the confirmation gate refuses")
	}
}

// A misbehaving server returning null for the non-null mergeUsers field must
// yield an error, not a nil-pointer panic.
func TestUserMergeNullResult(t *testing.T) {
	gql, _ := captureGraphQL(t, map[string]string{
		"MergeUsers": `{"data":{"mergeUsers":null}}`,
	})
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"user", "merge", "dup", "--into", "alice", "--yes", "--server", gql.URL})
	if err := root.Execute(); err == nil {
		t.Fatal("expected an error when the server returns a null user")
	}
}

// Server-side failures (forbidden, same-user merge) propagate through the CLI's
// API error mapping rather than being duplicated locally.
func TestUserMergeServerErrorPropagates(t *testing.T) {
	for _, msg := range []string{
		`{"errors":[{"message":"forbidden","extensions":{"code":"FORBIDDEN"}}]}`,
		`{"errors":[{"message":"cannot merge a user into itself","extensions":{"code":"BAD_USER_INPUT"}}]}`,
	} {
		gql, _ := captureGraphQL(t, map[string]string{"MergeUsers": msg})
		f, _ := testFactory(t)
		root := NewRootCmd(f)
		root.SetArgs([]string{"user", "merge", "dup", "--into", "alice", "--yes", "--server", gql.URL})
		if err := root.Execute(); err == nil {
			t.Errorf("expected a server error to propagate for %q", msg)
		}
	}
}

func TestUserSearchRejectsBadArgs(t *testing.T) {
	cases := [][]string{
		{"user", "search", "  ", "--server", "http://127.0.0.1:1"},
		{"user", "search", "alice", "--limit", "-1", "--server", "http://127.0.0.1:1"},
		{"user", "search", "alice", "--offset", "-5", "--server", "http://127.0.0.1:1"},
	}
	for _, args := range cases {
		f, _ := testFactory(t)
		root := NewRootCmd(f)
		root.SetArgs(args)
		if err := root.Execute(); err == nil {
			t.Errorf("expected a usage error for args %v, got nil", args)
		}
	}
}
