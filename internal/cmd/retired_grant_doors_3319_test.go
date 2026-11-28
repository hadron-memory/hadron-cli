package cmd

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/exitcode"
)

const doorRetired3319 = `{"errors":[{"message":"setAutomationReadGrant is retired: give a signed memory.read grant with createGrant instead.","extensions":{"code":"DOOR_RETIRED","replacement":"memory.read"}}],"data":null}`

// cli#3319 (hadron-server#5404): the per-automation read-grant and App write-grant doors answer DOOR_RETIRED. Each old
// command says on stderr that it no longer works and names the signed-grant replacement, then surfaces the server's refusal
// as a usage error (exit 2) without the "outcome unknown, list before retrying" advice that fits a lost answer.
func TestRetiredGrantDoorsSayWhatReplacesThem3319(t *testing.T) {
	cases := []struct {
		name        string
		args        []string
		replacement string
	}{
		{"automation grant set", []string{"automation", "grant", "set", "acme.com:ops", "--memory", "acme.com:notes", "--trigger-kind", "SCHEDULE", "--trigger-id", "s1", "--yes"}, "memory.read"},
		{"automation grant list", []string{"automation", "grant", "list", "--app", "acme.com:ops"}, "memory.read"},
		{"automation grant revoke", []string{"automation", "grant", "revoke", "g1", "--yes"}, "memory.read"},
		{"app memory-grant add", []string{"app", "memory-grant", "add", "acme.com:ops", "--memory", "acme.com:notes", "--prefix", "docs", "--yes"}, "memory.write"},
		{"app memory-grant list", []string{"app", "memory-grant", "list", "acme.com:ops"}, "memory.write"},
		{"app memory-grant rm", []string{"app", "memory-grant", "rm", "g1", "--yes"}, "memory.write"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			gql, _ := captureGraphQLFunc(t, func(string) string { return doorRetired3319 })
			f, out := testFactory(t)
			var stderr strings.Builder
			f.IOStreams.ErrOut = &stderr
			root := NewRootCmd(f)
			args := append([]string{"--server", gql.URL, "--json"}, tc.args...)
			root.SetArgs(args)
			err := root.Execute()
			if err == nil {
				t.Fatal("want the server's refusal")
			}
			if code := renderFailure(f, args, err); code != exitcode.Usage {
				t.Errorf("exit %d, want 2", code)
			}
			all := stderr.String() + out.String()
			for _, want := range []string{"no longer works", tc.replacement, "hadron team grant create --holder app", "DOOR_RETIRED"} {
				if !strings.Contains(all, want) {
					t.Errorf("missing %q:\n%s", want, all)
				}
			}
			if strings.Contains(all, "outcome unknown") {
				t.Errorf("a retired door is not a lost answer:\n%s", all)
			}
			if !strings.HasPrefix(stderr.String(), "warning: ") {
				t.Errorf("the notice goes to stderr first:\n%s", stderr.String())
			}
			var env map[string]any
			if jerr := json.Unmarshal([]byte(strings.TrimSpace(out.String())), &env); jerr != nil && strings.TrimSpace(out.String()) != "" {
				t.Errorf("stdout is not one JSON document (the notice must not land there): %v\n%s", jerr, out.String())
			}
		})
	}
}

func TestRetiredGrantDoorsAreMarkedInHelp3319(t *testing.T) {
	for _, args := range [][]string{{"automation", "grant", "--help"}, {"app", "memory-grant", "--help"}, {"automation", "grant", "set", "--help"}} {
		f, out := testFactory(t)
		root := NewRootCmd(f)
		root.SetArgs(args)
		if err := root.Execute(); err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(out.String(), "DEPRECATED") || !strings.Contains(out.String(), "hadron team grant create --holder app") {
			t.Errorf("%v help lacks the deprecation and the replacement:\n%s", args, out.String())
		}
	}
}

// --holder app is how the App holds a memory.read grant: the server wants the App's id as holderRef, so a root:slug is read once.
func TestTeamGrantCreateHolderApp3319(t *testing.T) {
	grant := strings.Replace(managerGrant1846, `"holderKind":"WORKER"`, `"holderKind":"APP"`, 1)
	resp := map[string]string{
		"TeamAppIdentity": `{"data":{"app":{"id":"capp1","urn":"hrn:app:acme.com:eng","name":"Eng"}}}`,
		"TeamCreateGrant": `{"data":{"createGrant":` + grant + `}}`,
	}
	args := []string{"team", "grant", "create", "--app", "acme.com:eng", "--action", "memory.read", "--values", `{"memory":["m1"]}`, "--reason", "r", "--holder", "app"}
	calls, _, _, err := runManage1846(t, resp, args...)
	if err != nil {
		t.Fatal(err)
	}
	if len(*calls) != 2 || (*calls)[0].Op != "TeamAppIdentity" || (*calls)[1].Op != "TeamCreateGrant" {
		t.Fatalf("calls %+v", *calls)
	}
	var vars struct {
		Input map[string]json.RawMessage `json:"input"`
	}
	_ = json.Unmarshal((*calls)[1].RawVars, &vars)
	if string(vars.Input["holderKind"]) != `"APP"` || string(vars.Input["holderRef"]) != `"capp1"` || string(vars.Input["appRef"]) != `"acme.com:eng"` {
		t.Errorf("input %v", vars.Input)
	}

	// An App id needs no lookup, and a given --holder-id is sent as is.
	calls, _, _, err = runManage1846(t, resp, "team", "grant", "create", "--app", "capp100000000000000000000", "--action", "memory.read", "--values", `{"memory":["m1"]}`, "--reason", "r", "--holder", "app")
	if err != nil {
		t.Fatal(err)
	}
	if len(*calls) != 1 || (*calls)[0].Op != "TeamCreateGrant" {
		t.Errorf("an App id is not looked up: %+v", *calls)
	}
}

func TestTeamGrantCreateHolderAppUnknownApp3319(t *testing.T) {
	calls, _, _, err := runManage1846(t, map[string]string{"TeamAppIdentity": `{"data":{"app":null}}`},
		"team", "grant", "create", "--app", "acme.com:none", "--action", "memory.read", "--values", `{"memory":["m1"]}`, "--reason", "r", "--holder", "app")
	if err == nil || exitCodeFor(err) != exitcode.NotFound {
		t.Fatalf("want exit 4, got %v", err)
	}
	for _, c := range *calls {
		if c.Op == "TeamCreateGrant" {
			t.Error("nothing is created for an App that is not found")
		}
	}
}

func TestTeamGrantCreateWorkerStillNeedsHolderID3319(t *testing.T) {
	calls, _, _, err := runManage1846(t, nil, "team", "grant", "create", "--app", "acme.com:eng", "--action", "node.read", "--values", `{"a":1}`, "--reason", "r", "--holder", "agent")
	if err == nil || exitCodeFor(err) != exitcode.Usage || len(*calls) != 0 {
		t.Fatalf("want a usage refusal before any request: %v %+v", err, *calls)
	}
}
