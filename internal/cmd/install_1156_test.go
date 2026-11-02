package cmd

import (
	"encoding/json"
	"strings"
	"testing"
)

// Existing fail-closed behavior: these are regression pins, not a missing
// feature. The empty-URN guard mutant must fail all three before any node call.
func TestInstall1156EmptyMemoryURNStopsAtMetadata(t *testing.T) {
	for _, args := range [][]string{{"node", "get", "custom"}, {"node", "list"}, {"node", "update", "custom", "--name", "changed"}} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			calls, _, err := runTeamTask(t, map[string]string{"InstallMemory": `{"data":{"installMemory":{"id":"m1","urn":"","name":"bad"}}}`}, append(args, "--install", c7Install)...)
			if exitCodeFor(err) != 1 || len(*calls) != 1 || (*calls)[0].Op != "InstallMemory" {
				t.Fatalf("empty URN must stop before node request: err=%v calls=%v", err, *calls)
			}
		})
	}
}

func TestInstall1156ReadHelpOmitsWriteOnlyAccess(t *testing.T) {
	for _, verb := range []string{"get", "list"} {
		f, out := testFactory(t)
		root := NewRootCmd(f)
		root.SetArgs([]string{"node", verb, "--help"})
		if err := root.Execute(); err != nil {
			t.Fatal(err)
		}
		if strings.Contains(out.String(), "Only an App manager may write") {
			t.Fatalf("read help repeats writer-only conditions: %s", out.String())
		}
		if !strings.Contains(out.String(), "A read never") {
			t.Fatal("read/concealment guidance lost")
		}
	}
}

func TestInstall1156RefSpellingsPreserveSelectors(t *testing.T) {
	for _, refs := range [][2]string{
		{"hrn:app:acme.com:eng-team", "hrn:agent:acme.com:support-bot"},
		{"urn:app:acme.com::eng-team", "urn:agent:acme.com::support-bot"},
		{"hrn:app:acme.com::eng-team", "hrn:agent:acme.com::support-bot"},
		{"01a1815f9fc375348c31188474140ad5", "01a1815f9fc375348c31188474140ad6"},
	} {
		t.Run(strings.Join(refs[:], "/"), func(t *testing.T) {
			for _, read := range []bool{true, false} {
				responses := map[string]string{"InstallMemory": c7MemoryResponse, "FindNodes": `{"data":{"nodes":[]}}`, "CreateNode": `{"data":{"createNode":` + nodeJSON + `}}`}
				args := []string{"node", "list", "--install", strings.Join(refs[:], "/"), "--json"}
				if !read {
					args = []string{"node", "add", "--install", strings.Join(refs[:], "/"), "--loc", "x", "--name", "X"}
				}
				calls, _, err := runTeamTask(t, responses, args...)
				if err != nil {
					t.Fatal(err)
				}
				op := "InstallMemory"
				if !read {
					op = "CreateNode"
				}
				call := c2Mutation(t, calls, op)
				vars := call.Vars
				if !read {
					vars = vars["input"].(map[string]any)["install"].(map[string]any)
				}
				wantApp := refs[0]
				if strings.Contains(wantApp, "app:") {
					wantApp = "hrn:app:acme.com:eng-team"
				}
				if vars["appRef"] != wantApp || vars["agentRef"] != refs[1] {
					b, _ := json.Marshal(vars)
					t.Fatalf("selector changed: %s", b)
				}
			}
		})
	}
}
