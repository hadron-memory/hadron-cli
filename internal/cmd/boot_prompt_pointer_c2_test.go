package cmd

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/exitcode"
)

// cli#916 C2 — the boot-prompt pointers of #1772 S8 (hadron-server#2219,
// cor:agt:040). Three setters, GraphQL only, each ONE mutation whose nodeRef
// is always on the wire: the node ref sets, an explicit null clears (an
// omitted nodeRef is the server's BAD_USER_INPUT, never a clear). The manager
// acts as themselves (R23), so none of them carries the bound worktree's
// session. Reads add bootPromptRef/bootPromptTargetDeleted, both null when
// unset OR withheld.

const c2NodeURN = "hrn:node:acme.com:eng-team-install:boot:compiled"

const c2AppAgentJSON = `{"agent":{"id":"agt1","urn":"hrn:agent:acme.com:support-bot","name":"Support Bot"},
	"app":{"id":"capp100000000000000000000","urn":"hrn:app:acme.com:eng-team","name":"Eng Team"},
	"bootPromptRef":"` + c2NodeURN + `","bootPromptTargetDeleted":false}`

func c2AgentWith(ref, deleted string) string {
	return strings.Replace(agentJSON, `"createdAt"`, `"bootPromptRef":`+ref+`,"bootPromptTargetDeleted":`+deleted+`,"createdAt"`, 1)
}

func c2WorkerWith(ref, deleted string) string {
	return strings.Replace(irisWorkerJSON, `"createdAt"`, `"bootPromptRef":`+ref+`,"bootPromptTargetDeleted":`+deleted+`,"createdAt"`, 1)
}

func c2Responses() map[string]string {
	return map[string]string{
		"SetAgentBootPrompt":    `{"data":{"setAgentBootPrompt":` + c2AgentWith(`"`+c2NodeURN+`"`, "false") + `}}`,
		"SetAppAgentBootPrompt": `{"data":{"setAppAgentBootPrompt":` + c2AppAgentJSON + `}}`,
		"SetWorkerBootPrompt":   `{"data":{"setWorkerBootPrompt":` + c2WorkerWith(`"`+c2NodeURN+`"`, "false") + `}}`,
		"GetWorker":             `{"data":{"worker":` + irisWorkerJSON + `}}`,
	}
}

var c2Setters = []struct {
	name string
	op   string
	argv []string // up to, not including, the node ref / --clear
}{
	{"agent", "SetAgentBootPrompt", []string{"agent", "set-boot-prompt", "hrn:agent:acme.com:support-bot"}},
	{"app agent", "SetAppAgentBootPrompt", []string{"app", "agent", "set-boot-prompt", "hrn:app:acme.com:eng-team", "hrn:agent:acme.com:support-bot"}},
	{"team worker", "SetWorkerBootPrompt", []string{"team", "worker", "set-boot-prompt", "hrn:worker:acme.com:eng-team:iris"}},
}

// c2Mutation returns the one call of op, failing unless there is exactly one.
func c2Mutation(t *testing.T, calls *[]attnCall, op string) attnCall {
	t.Helper()
	var found []attnCall
	for _, c := range *calls {
		if c.Op == op {
			found = append(found, c)
		}
	}
	if len(found) != 1 {
		t.Fatalf("%s must be sent exactly once, got %d", op, len(found))
	}
	return found[0]
}

func TestC2SetSendsTheNodeRefOnce(t *testing.T) {
	for _, s := range c2Setters {
		t.Run(s.name, func(t *testing.T) {
			calls, _, err := runTeamTask(t, c2Responses(), append(s.argv, c2NodeURN)...)
			if err != nil {
				t.Fatal(err)
			}
			c := c2Mutation(t, calls, s.op)
			if !strings.Contains(string(c.RawVars), `"nodeRef":"`+c2NodeURN+`"`) {
				t.Errorf("set must send the node ref as given, wire variables %s", c.RawVars)
			}
		})
	}
}

func TestC2ClearSerializesExplicitNull(t *testing.T) {
	for _, s := range c2Setters {
		t.Run(s.name, func(t *testing.T) {
			calls, _, err := runTeamTask(t, c2Responses(), append(s.argv, "--clear")...)
			if err != nil {
				t.Fatal(err)
			}
			c := c2Mutation(t, calls, s.op)
			if !strings.Contains(string(c.RawVars), `"nodeRef":null`) {
				t.Errorf("clear must send an explicit nodeRef:null (omitted is refused, not a clear), wire variables %s", c.RawVars)
			}
		})
	}
}

// -m composes a bare loc into the node URN locally, as elsewhere; no lookup.
func TestC2MemoryFlagComposesBareLoc(t *testing.T) {
	for _, s := range c2Setters {
		t.Run(s.name, func(t *testing.T) {
			calls, _, err := runTeamTask(t, c2Responses(), append(s.argv, "boot:compiled", "-m", "hrn:mem:acme.com:eng-team-install")...)
			if err != nil {
				t.Fatal(err)
			}
			c := c2Mutation(t, calls, s.op)
			if !strings.Contains(string(c.RawVars), `"nodeRef":"`+c2NodeURN+`"`) {
				t.Errorf("-m + bare loc must compose %s, wire variables %s", c2NodeURN, c.RawVars)
			}
			for _, other := range *calls {
				if other.Op == "ResolveUrn" {
					t.Errorf("the node ref is the server's to resolve; no ResolveUrn prelookup")
				}
			}
		})
	}
}

// R23: the manager acts as themselves. On a BOUND worktree none of the three
// carries X-Hadron-Session. The positive control proves the harness attaches
// one to an authored write, so the absence is not vacuous.
func TestC2SettersCarryNoWorkerSession(t *testing.T) {
	control, _, err := runTeamTask(t, map[string]string{
		"GetWorker":          `{"data":{"worker":` + irisWorkerJSON + `}}`,
		"SetWorkerReportsTo": `{"data":{"setWorkerReportsTo":` + irisWorkerJSON + `}}`,
	}, "team", "worker", "set-reports-to", "hrn:worker:acme.com:eng-team:iris", "--clear")
	if err != nil {
		t.Fatal(err)
	}
	if c2Mutation(t, control, "SetWorkerReportsTo").Session == "" {
		t.Fatal("control: an authored write on this bound worktree carries no session, so the absence below proves nothing")
	}
	for _, s := range c2Setters {
		for _, tail := range [][]string{{c2NodeURN}, {"--clear"}} {
			t.Run(s.name+" "+tail[0], func(t *testing.T) {
				calls, _, err := runTeamTask(t, c2Responses(), append(s.argv, tail...)...)
				if err != nil {
					t.Fatal(err)
				}
				if got := c2Mutation(t, calls, s.op).Session; got != "" {
					t.Errorf("%s carried worker session %q; the server refuses it BOOT_PROMPT_POINTER_SESSION_REFUSED (R23)", s.op, got)
				}
			})
		}
	}
}

func TestC2BothOrNeitherIsUsageWithNoCall(t *testing.T) {
	for _, s := range c2Setters {
		for name, tail := range map[string][]string{
			"both":    {c2NodeURN, "--clear"},
			"neither": {},
		} {
			t.Run(s.name+" "+name, func(t *testing.T) {
				calls, _, err := runTeamTask(t, c2Responses(), append(s.argv, tail...)...)
				if got := exitCodeFor(err); got != exitcode.Usage {
					t.Fatalf("want exit %d, got %d (%v)", exitcode.Usage, got, err)
				}
				for _, c := range *calls {
					if c.Op == s.op {
						t.Errorf("a refused argument list must make no %s call", s.op)
					}
				}
			})
		}
	}
}

func TestC2RefusalExits(t *testing.T) {
	for code, want := range map[string]int{
		"BOOT_PROMPT_POINTER_NOT_FOUND":       exitcode.NotFound,
		"BOOT_PROMPT_POINTER_NOT_COMPILED":    exitcode.Usage,
		"BOOT_PROMPT_POINTER_WRONG_ROLE":      exitcode.Usage,
		"BOOT_PROMPT_POINTER_WRONG_MEMORY":    exitcode.Usage,
		"BOOT_PROMPT_POINTER_SESSION_REFUSED": exitcode.Forbidden,
		"FORBIDDEN":                           exitcode.Forbidden,
	} {
		t.Run(code, func(t *testing.T) {
			msg := "refused: " + code
			r := c2Responses()
			r["SetAgentBootPrompt"] = `{"errors":[{"message":"` + msg + `","extensions":{"code":"` + code + `"}}]}`
			calls, _, err := runTeamTask(t, r, "agent", "set-boot-prompt", "hrn:agent:acme.com:support-bot", c2NodeURN)
			if got := exitCodeFor(err); got != want {
				t.Fatalf("%s: want exit %d, got %d (%v)", code, want, got, err)
			}
			if !strings.Contains(err.Error(), msg) {
				t.Errorf("the server's sentence must survive: %v", err)
			}
			c2Mutation(t, calls, "SetAgentBootPrompt")
		})
	}
}

func TestC2SetterJSON(t *testing.T) {
	_, out, err := runTeamTask(t, c2Responses(), "app", "agent", "set-boot-prompt", "hrn:app:acme.com:eng-team", "hrn:agent:acme.com:support-bot", c2NodeURN, "--json")
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("not JSON: %v\n%s", err, out)
	}
	if got["bootPromptRef"] != c2NodeURN || got["bootPromptTargetDeleted"] != false || got["agentUrn"] != "hrn:agent:acme.com:support-bot" || got["appUrn"] != "hrn:app:acme.com:eng-team" {
		t.Errorf("app agent set-boot-prompt --json = %v", got)
	}
}

// ---- reads ------------------------------------------------------------------

func TestC2ReadsCarryThePointerKeys(t *testing.T) {
	cases := []struct {
		name string
		argv []string
		resp map[string]string
	}{
		{"agent get", []string{"agent", "get", "agt1"}, map[string]string{"GetAgent": `{"data":{"agent":%s}}`}},
		{"team worker get", []string{"team", "worker", "get", "hrn:worker:acme.com:eng-team:iris"}, map[string]string{"GetWorker": `{"data":{"worker":%s}}`}},
	}
	for _, tc := range cases {
		for _, v := range []struct {
			ref, deleted string
			wantRef      any
			wantDeleted  any
		}{
			{"null", "null", nil, nil},
			{`"` + c2NodeURN + `"`, "true", c2NodeURN, true},
		} {
			t.Run(tc.name+" "+v.deleted, func(t *testing.T) {
				r := map[string]string{}
				for op, tmpl := range tc.resp {
					body := c2AgentWith(v.ref, v.deleted)
					if op == "GetWorker" {
						body = c2WorkerWith(v.ref, v.deleted)
					}
					r[op] = strings.Replace(tmpl, "%s", body, 1)
				}
				_, out, err := runTeamTask(t, r, append(tc.argv, "--json")...)
				if err != nil {
					t.Fatal(err)
				}
				var got map[string]any
				if err := json.Unmarshal([]byte(out), &got); err != nil {
					t.Fatalf("not JSON: %v\n%s", err, out)
				}
				for key, want := range map[string]any{"bootPromptRef": v.wantRef, "bootPromptTargetDeleted": v.wantDeleted} {
					val, present := got[key]
					if !present {
						t.Errorf("--json must carry %q (null stays null)", key)
					} else if val != want {
						t.Errorf("%s = %v, want %v", key, val, want)
					}
				}
			})
		}
	}
}

func TestC2HumanRendering(t *testing.T) {
	for _, v := range []struct {
		ref, deleted, want string
	}{
		{"null", "null", "boot prompt: — (unset or not visible to you)"},
		{`"` + c2NodeURN + `"`, "false", "boot prompt: " + c2NodeURN + "\n"},
		{`"` + c2NodeURN + `"`, "true", "boot prompt: " + c2NodeURN + " (deleted: clear or repoint)"},
	} {
		_, out, err := runTeamTask(t, map[string]string{"GetAgent": `{"data":{"agent":` + c2AgentWith(v.ref, v.deleted) + `}}`}, "agent", "get", "agt1")
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(out, v.want) {
			t.Errorf("agent get must show %q:\n%s", v.want, out)
		}
		_, out, err = runTeamTask(t, map[string]string{"GetWorker": `{"data":{"worker":` + c2WorkerWith(v.ref, v.deleted) + `}}`}, "team", "worker", "get", "hrn:worker:acme.com:eng-team:iris")
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(out, v.want) {
			t.Errorf("team worker get must show %q:\n%s", v.want, out)
		}
	}
}

// app agent list joins App.appAgents onto the installed Agents by agent id.
// An install with no readable join row keeps both keys null.
func TestC2AppAgentListCarriesTheInstallPointer(t *testing.T) {
	resp := `{"data":{"app":{"id":"capp100000000000000000000","urn":"hrn:app:acme.com:eng-team","name":"Eng Team",
	"agents":[{"id":"agt1","urn":"hrn:agent:acme.com:support-bot","name":"Support Bot","description":null,"visibility":"ORGANIZATION","organizationId":"o1","personaRole":null,"createdAt":"2026-08-11T00:00:00Z"},
	          {"id":"agt2","urn":"hrn:agent:acme.com:other","name":"Other","description":null,"visibility":"ORGANIZATION","organizationId":"o1","personaRole":null,"createdAt":"2026-08-11T00:00:00Z"}],
	"appAgents":[{"agent":{"id":"agt1"},"bootPromptRef":"` + c2NodeURN + `","bootPromptTargetDeleted":true},
	             {"agent":null,"bootPromptRef":"hrn:node:acme.com:x:y","bootPromptTargetDeleted":false}]}}}`
	_, out, err := runTeamTask(t, map[string]string{"AppAgentRoster": resp}, "app", "agent", "list", "hrn:app:acme.com:eng-team", "--json")
	if err != nil {
		t.Fatal(err)
	}
	var got []map[string]any
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("not JSON: %v\n%s", err, out)
	}
	if len(got) != 2 {
		t.Fatalf("want 2 members, got %d", len(got))
	}
	if got[0]["bootPromptRef"] != c2NodeURN || got[0]["bootPromptTargetDeleted"] != true {
		t.Errorf("agt1 must carry its install pointer: %v", got[0])
	}
	for _, key := range []string{"bootPromptRef", "bootPromptTargetDeleted"} {
		if v, present := got[1][key]; !present || v != nil {
			t.Errorf("agt2 has no readable join row, so %s must be null, got %v (present %t)", key, v, present)
		}
	}
	_, out, err = runTeamTask(t, map[string]string{"AppAgentRoster": resp}, "app", "agent", "list", "hrn:app:acme.com:eng-team")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, c2NodeURN+" (deleted: clear or repoint)") || !strings.Contains(out, "— (unset or not visible to you)") {
		t.Errorf("app agent list must render the pointer column:\n%s", out)
	}
}
