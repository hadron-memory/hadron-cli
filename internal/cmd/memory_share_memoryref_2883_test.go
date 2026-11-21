package cmd

import (
	"encoding/json"
	"regexp"
	"strings"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/api/gen"
)

// cli#2883 (server #4137): the share and member writes take refs only. The
// removed args (memoryId, userId, granteeId) must never reach the wire, and the
// ref the command sends must be the memory it was given.
func TestShareAndMemberWritesSendRefsNotRemovedIDs2883(t *testing.T) {
	const mem = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	const member = `{"memoryMember":{"role":"reader","user":` + memUserJSON + `}}`
	const share = `{"memoryShare":{"role":"reader","grantee":` + memUserJSON + `}}`
	// GetUser resolves --grantee for the share create/set-role commands.
	const user = `{"data":{"user":{"id":"usr1","name":"Alice","email":"alice@acme.com","handle":"alice","githubUsername":null,"roles":[]}}}`
	for _, tc := range []struct {
		op, field, payload string
		args               []string
		wantRef            map[string]string // variable -> value the command must send
	}{
		{"CreateMemoryShare", "createMemoryShare", share, []string{"share", "create", mem, "--grantee", "usr1", "--role", "reader"}, map[string]string{"memoryRef": mem, "granteeRef": "usr1"}},
		{"UpdateMemoryShareRole", "updateMemoryShareRole", share, []string{"share", "set-role", mem, "--grantee", "usr1", "--role", "reader"}, map[string]string{"memoryRef": mem, "granteeRef": "usr1"}},
		{"DeleteMemoryShare", "deleteMemoryShare", `{"memoryId":"` + mem + `","granteeId":"usr1"}`, []string{"share", "rm", mem, "--grantee", "usr1", "--yes"}, map[string]string{"memoryRef": mem, "granteeRef": "usr1"}},
		{"AddMemoryMember", "addMemoryMember", member, []string{"member", "add", mem, "--user", "usr1", "--role", "reader"}, map[string]string{"memoryRef": mem, "userRef": "usr1"}},
		{"UpdateMemoryMemberRole", "updateMemoryMemberRole", member, []string{"member", "set-role", mem, "--user", "usr1", "--role", "reader"}, map[string]string{"memoryRef": mem, "userRef": "usr1"}},
		{"RemoveMemoryMember", "removeMemoryMember", `{"memoryId":"` + mem + `","userId":"usr1"}`, []string{"member", "rm", mem, "--user", "usr1", "--yes"}, map[string]string{"memoryRef": mem, "userRef": "usr1"}},
	} {
		t.Run(tc.op, func(t *testing.T) {
			gql, captured := captureGraphQL(t, map[string]string{
				"GetUser": user,
				tc.op:     `{"data":{"` + tc.field + `":` + tc.payload + `}}`,
			})
			f, _ := testFactory(t)
			root := NewRootCmd(f)
			root.SetArgs(append(append([]string{"memory"}, tc.args...), "--server", gql.URL))
			if err := root.Execute(); err != nil {
				t.Fatalf("execute: %v", err)
			}
			raw, ok := captured[tc.op]
			if !ok {
				t.Fatalf("%s was never sent", tc.op)
			}
			var vars map[string]any
			if err := json.Unmarshal(raw, &vars); err != nil {
				t.Fatalf("%s variables are not JSON: %v (%s)", tc.op, err, raw)
			}
			for k, want := range tc.wantRef {
				if vars[k] != want {
					t.Errorf("%s must send %s=%q, got %v", tc.op, k, want, vars)
				}
			}
			for _, gone := range []string{"memoryId", "granteeId", "userId"} {
				if _, present := vars[gone]; present {
					t.Errorf("%s must not send the removed arg %s: %v", tc.op, gone, vars)
				}
			}
		})
	}
}

// removedArg matches a removed argument or variable as a whole word, so a
// longer name (memoryIdentifier) is not mistaken for it.
var removedArg = regexp.MustCompile(`\b(memoryId|userId|granteeId)\b\s*:|\$(memoryId|userId|granteeId)\b`)

// The server declares these args ID!, and a String! variable is refused at
// validation (genqlient also refuses it at generate time). Pin each operation's
// declared variable types and that no removed arg is selected as an argument.
// DeleteMemoryShare is a characterization pin: its granteeRef is deliberately
// optional (omitted means the caller's own share), unlike the other writes.
func TestShareAndMemberOperationsDeclareRefVariablesAsID2883(t *testing.T) {
	for name, tc := range map[string]struct {
		doc  string
		want []string
	}{
		"CreateMemoryShare":      {gen.CreateMemoryShare_Operation, []string{"$memoryRef: ID!", "$granteeRef: ID!"}},
		"UpdateMemoryShareRole":  {gen.UpdateMemoryShareRole_Operation, []string{"$memoryRef: ID!", "$granteeRef: ID!"}},
		"AddMemoryMember":        {gen.AddMemoryMember_Operation, []string{"$memoryRef: ID!", "$userRef: ID!"}},
		"UpdateMemoryMemberRole": {gen.UpdateMemoryMemberRole_Operation, []string{"$memoryRef: ID!", "$userRef: ID!"}},
		"RemoveMemoryMember":     {gen.RemoveMemoryMember_Operation, []string{"$memoryRef: ID!", "$userRef: ID!"}},
		"DeleteMemoryShare":      {gen.DeleteMemoryShare_Operation, []string{"$memoryRef: ID!", "$granteeRef: String"}},
	} {
		for _, w := range tc.want {
			if !strings.Contains(tc.doc, w) {
				t.Errorf("%s must declare %q:\n%s", name, w, tc.doc)
			}
		}
		// Output selections (memoryId, userId, granteeId) are not arguments and
		// stay; only an argument or variable use is refused.
		if m := removedArg.FindString(argumentsOf(tc.doc)); m != "" {
			t.Errorf("%s must not use the removed arg %q:\n%s", name, m, tc.doc)
		}
	}
}

// argumentsOf returns the operation header and the mutation call's argument
// list (everything up to the selection set), where a removed arg would appear.
func argumentsOf(doc string) string {
	if i := strings.Index(doc, "{"); i >= 0 {
		if j := strings.Index(doc[i+1:], "{"); j >= 0 {
			return doc[:i+1+j]
		}
	}
	return doc
}
