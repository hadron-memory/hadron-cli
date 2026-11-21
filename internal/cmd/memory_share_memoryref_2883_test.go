package cmd

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/api/gen"
)

// cli#2883 (server #4137): createMemoryShare and updateMemoryShareRole take
// memoryRef, not memoryId. The wire must carry memoryRef and never memoryId.
func TestMemoryShareWritesSendMemoryRefNotMemoryID2883(t *testing.T) {
	const mem = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	user := `{"data":{"user":{"id":"usr1","name":"Alice","email":"alice@acme.com","handle":"alice","githubUsername":null,"roles":[]}}}`
	share := `{"memoryShare":{"role":"reader","grantee":` + memUserJSON + `}}`
	for _, tc := range []struct{ op, verb string }{
		{"CreateMemoryShare", "create"},
		{"UpdateMemoryShareRole", "set-role"},
	} {
		t.Run(tc.op, func(t *testing.T) {
			field := strings.ToLower(tc.op[:1]) + tc.op[1:]
			gql, captured := captureGraphQL(t, map[string]string{
				"GetUser": user,
				tc.op:     `{"data":{"` + field + `":` + share + `}}`,
			})
			f, _ := testFactory(t)
			root := NewRootCmd(f)
			root.SetArgs([]string{"memory", "share", tc.verb, mem, "--grantee", "usr1", "--role", "reader", "--server", gql.URL})
			if err := root.Execute(); err != nil {
				t.Fatalf("execute: %v", err)
			}
			var vars map[string]any
			_ = json.Unmarshal(captured[tc.op], &vars)
			if vars["memoryRef"] != mem {
				t.Errorf("%s must send memoryRef=%s, got %v", tc.op, mem, vars)
			}
			for _, gone := range []string{"memoryId", "granteeId", "userId"} {
				if _, ok := vars[gone]; ok {
					t.Errorf("%s must not send the removed arg %s: %v", tc.op, gone, vars)
				}
			}
		})
	}
}

// The server now declares these args as ID!, so a String! variable is refused
// at validation. Pin each of the six operations' declared variable types.
func TestMemoryShareMemberOperationsDeclareRefVariablesAsID2883(t *testing.T) {
	for name, tc := range map[string]struct {
		doc  string
		want []string
		not  []string
	}{
		"CreateMemoryShare":      {gen.CreateMemoryShare_Operation, []string{"$memoryRef: ID!", "$granteeRef: ID!"}, []string{"memoryId"}},
		"UpdateMemoryShareRole":  {gen.UpdateMemoryShareRole_Operation, []string{"$memoryRef: ID!", "$granteeRef: ID!"}, []string{"memoryId"}},
		"AddMemoryMember":        {gen.AddMemoryMember_Operation, []string{"$memoryRef: ID!", "$userRef: ID!"}, []string{"memoryId", "userId"}},
		"UpdateMemoryMemberRole": {gen.UpdateMemoryMemberRole_Operation, []string{"$memoryRef: ID!", "$userRef: ID!"}, []string{"memoryId", "userId"}},
		"RemoveMemoryMember":     {gen.RemoveMemoryMember_Operation, []string{"$memoryRef: ID!", "$userRef: ID!"}, []string{"memoryId:", "userId:"}},
		"DeleteMemoryShare":      {gen.DeleteMemoryShare_Operation, []string{"$memoryRef: ID!", "$granteeRef: String"}, []string{"$granteeId"}},
	} {
		for _, w := range tc.want {
			if !strings.Contains(tc.doc, w) {
				t.Errorf("%s must declare %q:\n%s", name, w, tc.doc)
			}
		}
		for _, n := range tc.not {
			if strings.Contains(tc.doc, "$"+n) || strings.Contains(tc.doc, "("+n+":") || strings.Contains(tc.doc, " "+n+": $") {
				t.Errorf("%s must not use the removed arg %q", name, n)
			}
		}
	}
}
