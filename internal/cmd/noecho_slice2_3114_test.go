package cmd

import (
	"strings"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/exitcode"
)

// cli#3114 slice 2 (spec, node, memory, question, secret, coding): flag values and values reaching a
// refusal through a helper, which the positional flow guard cannot see. Text and --json, whole output.
func TestSlice2RefusalsNeverShowTheTypedValue3114(t *testing.T) {
	p := pastedUpper3029()
	low := strings.ToLower(p)
	q := "acme.com::specs"
	staged := func(op, field string) map[string]string {
		return map[string]string{op: `{"data":null,"errors":[{"message":"m","path":["` + field + `"],"extensions":{"code":"SECRET_PROTECTED_NEEDS_PASSKEY","requestId":"r1","ceremonyUrl":"https://x.test/c"}}]}`}
	}
	router := map[string]string{"GetNodeRaw": codingRouterWithBody(flatRouterBody), "GetMemory": `{"data":{"memory":null}}`}
	for _, tc := range []struct {
		name      string
		responses map[string]string
		argv      []string
		says      string
		exit      int
	}{
		{"node --edge shorthand", nil, []string{"node", "create", "-m", "acme.com:dev", "--loc", "a", "--name", "x", "--edge", p}, "--edge must be <name>=<target>", exitcode.Usage},
		{"node --edge object", nil, []string{"node", "create", "-m", "acme.com:dev", "--loc", "a", "--name", "x", "--edge", `{"` + p + `":1}`}, "not a valid edge object", exitcode.Usage},
		{"node --edge trailing", nil, []string{"node", "create", "-m", "acme.com:dev", "--loc", "a", "--name", "x", "--edge", `{"target":"` + p + `"} x`}, "trailing text", exitcode.Usage},
		{"node --edge no target", nil, []string{"node", "create", "-m", "acme.com:dev", "--loc", "a", "--name", "x", "--edge", `{"name":"` + p + `"}`}, "has no target", exitcode.Usage},
		{"node import glob", nil, []string{"node", "import", "-r", t.TempDir(), "-m", "acme.com::dev", "--include", "[" + p}, "glob pattern", exitcode.Usage},
		{"spec memory flag", nil, []string{"spec", "list", "-m", "acme.com:" + p + ":x:y"}, "memory ref", exitcode.Usage},
		{"spec import memory flag", nil, []string{"spec", "import", "code", "x", "-m", "acme.com:" + p + ":x:y"}, "memory ref", exitcode.Usage},
		{"spec citation segment", nil, []string{"spec", "new", "abc:" + p, "--new-path", "--title", "x", "-m", q}, "not one", exitcode.Usage},
		{"spec --title prefix", nil, []string{"spec", "new", "abc:010", "--new-path", "--title", "abc:010 — " + low, "-m", q}, "--title", exitcode.Usage},
		{"spec grep --regex", nil, []string{"spec", "grep", "(" + p, "--regex", "-m", q}, "--regex pattern", exitcode.Usage},
		{"coding --route stem memory", router, []string{"coding", "preflight", "list", "-m", p}, "-m/--memory", exitcode.Usage},
		{"coding review --link", nil, []string{"coding", "review", "create", "abc", "-m", codingMem, "--trigger", "x", "--description", "x", "--link", "=" + p}, "--link", exitcode.Usage},
		{"question get", map[string]string{"Question": `{"data":{"question":null}}`}, []string{"question", "get", p}, "question not found", exitcode.NotFound},
		{"secret rm", map[string]string{"DeleteSecret": `{"data":{"deleteSecret":false}}`}, []string{"secret", "rm", p, "--yes"}, "secret not found", exitcode.NotFound},
		{"secret exec command", nil, []string{"secret", "exec", "--client", "c1", "--env", "T", "--", p}, "cannot run the command", exitcode.Usage},
		{"secret exec path", nil, []string{"secret", "exec", "--client", "c1", "--env", "T", "--", "./" + p}, "cannot run the command", exitcode.Usage},
		{"secret rm staged", staged("DeleteSecret", "deleteSecret"), []string{"secret", "rm", p, "--yes"}, "staged, not applied", exitcode.Conflict},
		{"secret protect staged", staged("SetSecretProtected", "setSecretProtected"), []string{"secret", "protect", p, "--off"}, "staged, not applied", exitcode.Conflict},
		{"secret change get", map[string]string{"SecretChangeRequest": `{"data":{"secretChangeRequest":null}}`}, []string{"secret", "change", "get", p}, "change request not found", exitcode.NotFound},
		{"secret change withdraw", map[string]string{"WithdrawSecretChangeRequest": `{"data":{"withdrawSecretChangeRequest":false}}`}, []string{"secret", "change", "withdraw", p}, "not open", exitcode.Conflict},
		{"memory template", map[string]string{"MemoryConfigTemplate": `{"data":{"memoryConfigTemplate":null}}`}, []string{"memory", "config", "template", "get", p}, "no template", exitcode.NotFound},
	} {
		t.Run(tc.name, func(t *testing.T) {
			seen, code := renderedNoEcho3029(t, tc.responses, tc.argv...)
			if code != tc.exit {
				t.Errorf("exit = %d, want %d: %s", code, tc.exit, seen)
			}
			if strings.Contains(strings.ToLower(seen), low) {
				t.Errorf("shows the value: %s", seen)
			}
			if !strings.Contains(seen, tc.says) || !strings.Contains(seen, "not shown") {
				t.Errorf("want %q and that the value is not shown: %s", tc.says, seen)
			}
		})
	}
}

func TestSlice2EnumRefusalsNameTheFormNotTheValue3114(t *testing.T) {
	low := strings.ToLower(pastedUpper3029())
	for _, tc := range []struct {
		argv []string
		form string
	}{
		{[]string{"memory", "list", "--visibility"}, "ORGANIZATION, GROUP or PUBLIC"},
		{[]string{"memory", "export", "acme.com::dev", "--format"}, "only markdown"},
		{[]string{"memory", "member", "add", "acme.com::dev", "--user", "u1", "--role"}, "owner, writer, or reader"},
		{[]string{"memory", "validate", "acme.com::dev", "--check"}, "one of"},
		{[]string{"memory", "config", "rule", "add", "acme.com::dev", "r", "--writers"}, "all, admin or owner"},
		{[]string{"memory", "config", "rule", "add", "acme.com::dev", "r", "--validate-by"}, "agent or platform"},
		{[]string{"node", "export", "hrn:node:acme.com:dev:a", "--format"}, "md, json, or pdf"},
		{[]string{"node", "list", "-m", "acme.com::dev", "--memory-class"}, "app, group, knowledge"},
		{[]string{"node", "list", "-m", "acme.com::dev", "--updated-after"}, "RFC 3339"},
		{[]string{"spec", "grep", "x", "-m", "acme.com::specs", "--field"}, "content, abstract"},
		{[]string{"spec", "new", "--title", "x", "-m", "acme.com::specs", "--product"}, "3 lowercase letters"},
		{[]string{"spec", "new", "--title", "x", "-m", "acme.com::specs", "--module"}, "3 lowercase letters"},
		{[]string{"spec", "lint", "-m", "acme.com::specs", "--module"}, "3 lowercase letters"},
		{[]string{"coding", "preflight", "list", "-m", codingMem, "--route-model"}, "leaf or mirrored"},
	} {
		t.Run(strings.Join(tc.argv, " "), func(t *testing.T) {
			seen, code := renderedNoEcho3029(t, map[string]string{"GetMemory": memoryResolves3029}, append(append([]string{}, tc.argv...), low)...)
			if code != exitcode.Usage || !strings.Contains(seen, tc.form) {
				t.Errorf("exit %d, want the accepted form %q: %s", code, tc.form, seen)
			}
			if strings.Contains(strings.ToLower(seen), low) {
				t.Errorf("shows the value: %s", seen)
			}
		})
	}
}
