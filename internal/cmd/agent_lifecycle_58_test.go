package cmd

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/exitcode"
)

// cli#58 (four-command cut): `agent ai-config clear`, `agent import publish|rm`,
// `agent repair-system-memory`. Each is one mutation with refs as typed; the
// server owns authority.

func runAgent58(t *testing.T, responses map[string]string, args ...string) (*memCall, string, error) {
	t.Helper()
	calls := &memCall{vars: map[string]map[string]any{}}
	gql, captured := captureGraphQLFunc(t, func(op string) string {
		calls.ops = append(calls.ops, op)
		return responses[op]
	})
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs(append(append([]string{"agent"}, args...), "--server", gql.URL))
	err := root.Execute()
	for op, raw := range captured {
		v := map[string]any{}
		_ = json.Unmarshal(raw, &v)
		calls.vars[op] = v
	}
	return calls, out.String(), err
}

const importJSON58 = `{"parentAgentId":"p1","importedAgentId":"i1","position":3,"required":false,"createdAt":"2026-11-20T00:00:00Z"}`

// verb58 describes one of the four commands for the table tests.
type verb58 struct {
	name  string
	path  []string // after "agent"
	refs  []string // the reference arguments
	extra []string // flags that make the call valid without a terminal
	op    string
	field string
	okRes string // a valid result for the field
	vars  []string
}

func verbs58() []verb58 {
	ag := `{"data":{"%s":` + agentJSON + `}}`
	return []verb58{
		{"ai-config clear", []string{"ai-config", "clear"}, []string{"agt1"}, []string{"--yes"}, "ClearAgentAIConfig", "clearAgentAIConfig",
			strings.Replace(ag, "%s", "clearAgentAIConfig", 1), []string{"agentRef"}},
		{"import publish", []string{"import", "publish"}, []string{"par1", "imp1"}, []string{"--position", "3"}, "PublishAgentImport", "publishAgentImport",
			`{"data":{"publishAgentImport":` + importJSON58 + `}}`, []string{"parentAgentRef", "importedAgentRef"}},
		{"import rm", []string{"import", "rm"}, []string{"par1", "imp1"}, []string{"--yes"}, "RemoveAgentImport", "removeAgentImport",
			`{"data":{"removeAgentImport":true}}`, []string{"parentAgentRef", "importedAgentRef"}},
		{"repair-system-memory", []string{"repair-system-memory"}, []string{"agt1"}, nil, "RepairAgentSystemMemory", "repairAgentSystemMemory",
			strings.Replace(ag, "%s", "repairAgentSystemMemory", 1), []string{"agentRef"}},
	}
}

func (v verb58) args(refs ...string) []string {
	if refs == nil {
		refs = v.refs
	}
	return append(append(append([]string{}, v.path...), refs...), v.extra...)
}

// Each command sends exactly one mutation, with every ref exactly as typed
// (padding included: no trim, no pre-resolve), and no follow-on call.
func TestAgent58SendsOneCallWithRefsVerbatim(t *testing.T) {
	for _, v := range verbs58() {
		t.Run(v.name, func(t *testing.T) {
			padded := make([]string, len(v.refs))
			for i, r := range v.refs {
				padded[i] = " hrn:agent:acme.com:" + r + " "
			}
			calls, _, err := runAgent58(t, map[string]string{v.op: v.okRes}, v.args(padded...)...)
			if err != nil {
				t.Fatal(err)
			}
			if len(calls.ops) != 1 || calls.ops[0] != v.op {
				t.Fatalf("want exactly one %s, got %v", v.op, calls.ops)
			}
			for i, name := range v.vars {
				if got := calls.vars[v.op][name]; got != padded[i] {
					t.Errorf("%s = %#v, want %q verbatim", name, got, padded[i])
				}
			}
		})
	}
}

// A blank ref is a usage error with zero requests.
func TestAgent58BlankRefIsUsageWithNoCall(t *testing.T) {
	for _, v := range verbs58() {
		for i := range v.refs {
			refs := append([]string{}, v.refs...)
			refs[i] = "  "
			calls, out, err := runAgent58(t, nil, v.args(refs...)...)
			if err == nil || exitCodeFor(err) != exitcode.Usage || len(calls.ops) != 0 || out != "" {
				t.Errorf("%s blank ref %d: err=%v ops=%v out=%q", v.name, i, err, calls.ops, out)
			}
		}
	}
}

// The two destructive commands refuse without --yes and a terminal: exit 2,
// zero requests. (A mutant that drops the confirmation turns this red.)
func TestAgent58DestructiveCommandsNeedYes(t *testing.T) {
	for _, v := range verbs58() {
		if v.name != "ai-config clear" && v.name != "import rm" {
			continue
		}
		t.Run(v.name, func(t *testing.T) {
			args := append(append([]string{}, v.path...), v.refs...)
			calls, out, err := runAgent58(t, map[string]string{v.op: v.okRes}, args...)
			if err == nil || exitCodeFor(err) != exitcode.Usage || !strings.Contains(err.Error(), "--yes") || len(calls.ops) != 0 || out != "" {
				t.Fatalf("err=%v ops=%v out=%q", err, calls.ops, out)
			}
		})
	}
}

// repair-system-memory is idempotent and asks no confirmation: it runs without --yes.
func TestAgent58RepairNeedsNoConfirmation(t *testing.T) {
	v := verbs58()[3]
	calls, _, err := runAgent58(t, map[string]string{v.op: v.okRes}, "repair-system-memory", "agt1")
	if err != nil || len(calls.ops) != 1 {
		t.Fatalf("err=%v ops=%v", err, calls.ops)
	}
}

// import publish: --position is required (zero requests without it);
// --required is sent only when set, and an explicit false is sent as false.
func TestAgent58ImportPublishPositionAndRequired(t *testing.T) {
	res := map[string]string{"PublishAgentImport": `{"data":{"publishAgentImport":` + importJSON58 + `}}`}
	calls, out, err := runAgent58(t, res, "import", "publish", "par1", "imp1")
	if err == nil || exitCodeFor(err) != exitcode.Usage || !strings.Contains(err.Error(), "--position") || len(calls.ops) != 0 || out != "" {
		t.Fatalf("missing --position: err=%v ops=%v", err, calls.ops)
	}
	calls, _, err = runAgent58(t, res, "import", "publish", "par1", "imp1", "--position", "0")
	if err != nil {
		t.Fatal(err)
	}
	v := calls.vars["PublishAgentImport"]
	if got, ok := v["position"]; !ok || got != float64(0) {
		t.Errorf("an explicit --position 0 must be sent as 0: %v", v)
	}
	if _, present := v["required"]; present {
		t.Errorf("an unset --required must be omitted (server default true): %v", v)
	}
	for flag, want := range map[string]bool{"--required": true, "--required=false": false} {
		calls, _, err = runAgent58(t, res, "import", "publish", "par1", "imp1", "--position", "1", flag)
		if err != nil {
			t.Fatal(err)
		}
		if got, ok := calls.vars["PublishAgentImport"]["required"]; !ok || got != want {
			t.Errorf("%s must send required=%v, got %v (present=%v)", flag, want, got, ok)
		}
	}
}

// A null or missing result is a controlled error, never a panic or a silent
// no-op: one call, no output. For import rm that means a missing or null Boolean
// is an unknown outcome (exit 1), while an explicit false stays a successful
// no-op (TestAgent58OutputShapes).
func TestAgent58NullResultIsControlledError(t *testing.T) {
	for _, v := range verbs58() {
		for name, body := range map[string]string{"null": `{"data":{"` + v.field + `":null}}`, "missing": `{"data":{}}`} {
			t.Run(v.name+"/"+name, func(t *testing.T) {
				calls, out, err := runAgent58(t, map[string]string{v.op: body}, v.args()...)
				if err == nil || exitCodeFor(err) != exitcode.Error || len(calls.ops) != 1 || out != "" {
					t.Fatalf("err=%v ops=%v out=%q", err, calls.ops, out)
				}
			})
		}
	}
}

// Refusals map through MapError on every command: one call, nothing written.
func TestAgent58RefusalMapsOnEveryCommand(t *testing.T) {
	for _, v := range verbs58() {
		for _, tc := range []struct {
			code string
			want int
		}{{"FORBIDDEN", exitcode.Forbidden}, {"AGENT_NOT_FOUND", exitcode.NotFound}, {"BAD_USER_INPUT", exitcode.Usage}} {
			t.Run(v.name+"/"+tc.code, func(t *testing.T) {
				calls, out, err := runAgent58(t, map[string]string{
					v.op: `{"errors":[{"message":"refused","extensions":{"code":"` + tc.code + `"}}]}`,
				}, v.args()...)
				if err == nil || exitCodeFor(err) != tc.want || len(calls.ops) != 1 || out != "" {
					t.Fatalf("err=%v ops=%v out=%q", err, calls.ops, out)
				}
			})
		}
	}
}

// repair-system-memory's three documented refusals reach the user with their text.
func TestAgent58RepairRefusalsKeepTheirMessage(t *testing.T) {
	for _, code := range []string{"FORBIDDEN", "AGENT_HAS_NO_SYSTEM_MEMORY", "SYSTEM_MEMORY_NOT_FOUND"} {
		_, _, err := runAgent58(t, map[string]string{
			"RepairAgentSystemMemory": `{"errors":[{"message":"server says no: ` + code + `","extensions":{"code":"` + code + `"}}]}`,
		}, "repair-system-memory", "agt1")
		if err == nil || !strings.Contains(err.Error(), "server says no: "+code) {
			t.Errorf("%s: the server's message must survive: %v", code, err)
		}
	}
}

// --json: the Agent DTO has [] not null and carries no key material; the import
// DTO is the server's row; import rm prints the server's Boolean, not an assumption.
func TestAgent58OutputShapes(t *testing.T) {
	_, out, err := runAgent58(t, map[string]string{"ClearAgentAIConfig": `{"data":{"clearAgentAIConfig":` + agentJSON + `}}`},
		"ai-config", "clear", "agt1", "--yes", "--json")
	if err != nil {
		t.Fatal(err)
	}
	var dto map[string]any
	if err := json.Unmarshal([]byte(out), &dto); err != nil {
		t.Fatalf("not JSON: %v\n%s", err, out)
	}
	for _, k := range []string{"surfaces", "memberRoles"} {
		if arr, ok := dto[k].([]any); !ok || len(arr) != 0 {
			t.Errorf("%s must be [], got %#v", k, dto[k])
		}
	}
	for k := range dto {
		if strings.Contains(strings.ToLower(k), "apikey") && k != "hasAiApiKey" {
			t.Errorf("clear must not project key material: field %q", k)
		}
	}

	_, out, err = runAgent58(t, map[string]string{"PublishAgentImport": `{"data":{"publishAgentImport":` + importJSON58 + `}}`},
		"import", "publish", "par1", "imp1", "--position", "3", "--json")
	if err != nil {
		t.Fatal(err)
	}
	var imp map[string]any
	if err := json.Unmarshal([]byte(out), &imp); err != nil {
		t.Fatalf("import receipt is not JSON: %v\n%s", err, out)
	}
	if imp["parentAgentId"] != "p1" || imp["importedAgentId"] != "i1" || imp["position"] != float64(3) || imp["required"] != false || imp["createdAt"] != "2026-11-20T00:00:00Z" {
		t.Errorf("import dto: %s", out)
	}

	for body, want := range map[string]string{`true`: `"removed":true`, `false`: `"removed":false`} {
		_, out, err = runAgent58(t, map[string]string{"RemoveAgentImport": `{"data":{"removeAgentImport":` + body + `}}`},
			"import", "rm", "par1", "imp1", "--yes", "--json")
		var rm map[string]any
		if err != nil || json.Unmarshal([]byte(out), &rm) != nil || rm["removed"] != (body == "true") || len(rm) != 1 {
			t.Errorf("rm result %s: err=%v out=%q (want only removed=%s)", body, err, out, body)
		}
		_ = want
	}
}

// Text output is one control-free line even when the server's name carries escapes.
func TestAgent58TextIsControlFree(t *testing.T) {
	evil := strings.Replace(agentJSON, `"Support Bot"`, `"Bad\u001b[31mBot\nX"`, 1)
	_, out, err := runAgent58(t, map[string]string{"RepairAgentSystemMemory": `{"data":{"repairAgentSystemMemory":` + evil + `}}`},
		"repair-system-memory", "agt1")
	if err != nil {
		t.Fatal(err)
	}
	if strings.ContainsAny(out, "\x1b") || strings.Count(out, "\n") != 1 {
		t.Fatalf("one control-free line expected: %q", out)
	}
}

// The help states what the gate required it to say.
func TestAgent58HelpStatesTheGateConstraints(t *testing.T) {
	f, _ := testFactory(t)
	for path, wants := range map[string][]string{
		"import rm":            {"grants", "does not revive", "idempotent", "ADMINs and OWNERs"},
		"ai-config clear":      {"ADMINs and OWNERs", "never reads or prints a key", `only the one named "default"`},
		"import publish":       {"ADMINs and OWNERs", "--position", "one level"},
		"repair-system-memory": {"platform admins", "idempotent"},
	} {
		c, _, err := NewRootCmd(f).Find(append([]string{"agent"}, strings.Fields(path)...))
		if err != nil {
			t.Fatal(err)
		}
		flat := strings.Join(strings.Fields(c.Long), " ")
		for _, w := range wants {
			if !strings.Contains(flat, w) {
				t.Errorf("agent %s help lacks %q", path, w)
			}
		}
	}
}
