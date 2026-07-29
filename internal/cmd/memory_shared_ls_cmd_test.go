package cmd

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/exitcode"
)

// sharedWithMeJSON is a MemoriesSharedWithMe page: one memory granted to the
// caller, plus one whose myShare came back null (a row must survive that).
const sharedWithMeJSON = `{"data":{"memories":{"total":2,"items":[
	{"id":"m1","urn":"holger::jens","name":"Jens","shortDescription":null,"class":"personal",
	 "visibility":null,"organizationId":null,"isEncrypted":false,"maxRevCount":10,
	 "updatedAt":"2026-07-28T00:00:00Z",
	 "myShare":{"role":"reader","grantor":{"handle":"holger","name":"Holger S"}}},
	{"id":"m2","urn":"acme.com::notes","name":"Notes","shortDescription":null,"class":"personal",
	 "visibility":null,"organizationId":null,"isEncrypted":false,"maxRevCount":10,
	 "updatedAt":"2026-07-28T00:00:00Z","myShare":null}]}}}`

// #316: the shared-with-me slice is unreachable from the default listing, so
// the flag must switch operations — and report the granted role and grantor.
func TestMemoryLsSharedWithMe(t *testing.T) {
	gql, captured := captureGraphQL(t, map[string]string{
		"MemoriesSharedWithMe": sharedWithMeJSON,
	})
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"memory", "ls", "--shared-with-me", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	// The default listing's operation must not be used: it draws the caller's
	// own union, which by definition excludes shared memories.
	if _, ok := captured["Memories"]; ok {
		t.Error("--shared-with-me must not query the own-union listing")
	}
	text := out.String()
	for _, want := range []string{"ROLE", "SHARED BY", "holger::jens", "reader", "holger"} {
		if !strings.Contains(text, want) {
			t.Errorf("missing %q in output:\n%s", want, text)
		}
	}
	if strings.Contains(text, "CLASS") {
		t.Errorf("shared listing should report role/grantor, not class:\n%s", text)
	}
}

func TestMemoryLsSharedWithMeJSON(t *testing.T) {
	gql, _ := captureGraphQL(t, map[string]string{"MemoriesSharedWithMe": sharedWithMeJSON})
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"memory", "ls", "--shared-with-me", "--json", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	var memories []struct {
		URN       string  `json:"urn"`
		ShareRole *string `json:"shareRole"`
		SharedBy  *string `json:"sharedBy"`
	}
	if err := json.Unmarshal([]byte(out.String()), &memories); err != nil {
		t.Fatalf("unmarshal: %v\n%s", err, out.String())
	}
	if len(memories) != 2 {
		t.Fatalf("expected both rows, got %d", len(memories))
	}
	if memories[0].ShareRole == nil || *memories[0].ShareRole != "reader" {
		t.Errorf("shareRole = %v", memories[0].ShareRole)
	}
	if memories[0].SharedBy == nil || *memories[0].SharedBy != "holger" {
		t.Errorf("sharedBy = %v", memories[0].SharedBy)
	}
	// A null myShare must not drop the row — the memory is still shared with us.
	if memories[1].URN != "acme.com::notes" || memories[1].ShareRole != nil {
		t.Errorf("a row with no myShare should survive without a role, got %+v", memories[1])
	}
}

// The default listing's --json shape is untouched: shareRole/sharedBy are
// omitempty, so they don't appear at all outside the shared slice.
func TestMemoryLsDefaultOmitsShareFields(t *testing.T) {
	gql, captured := captureGraphQL(t, map[string]string{
		"Memories": `{"data":{"memories":{"total":1,"items":[
			{"id":"m1","urn":"acme.com::kb","name":"KB","shortDescription":null,"class":"knowledge",
			 "visibility":"ORGANIZATION","organizationId":"o1","isEncrypted":false,"maxRevCount":10,
			 "updatedAt":"2026-06-11T00:00:00Z"}]}}}`,
	})
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"memory", "ls", "--json", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if _, ok := captured["MemoriesSharedWithMe"]; ok {
		t.Error("the default listing must not query the shared slice")
	}
	if strings.Contains(out.String(), "shareRole") || strings.Contains(out.String(), "sharedBy") {
		t.Errorf("share fields must be omitted outside --shared-with-me:\n%s", out.String())
	}
}

// Combining the two is meaningless (shared memories are personal-class), so it
// is rejected rather than silently ignored — and as a flag misuse it must exit
// 2, which is the classification renderError applies (cobra returns a plain
// error, so the code is only assigned there, as in Execute).
func TestMemoryLsSharedWithMeRejectsIncludeAgentSystem(t *testing.T) {
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"memory", "ls", "--shared-with-me", "--include-agent-system", "--server", "http://127.0.0.1:1"})
	err := root.Execute()
	if err == nil {
		t.Fatal("combining --shared-with-me with --include-agent-system should fail")
	}
	if got := renderError(f, err); got != exitcode.Usage {
		t.Fatalf("mutually exclusive flags should exit %d (usage), got %d", exitcode.Usage, got)
	}
}
