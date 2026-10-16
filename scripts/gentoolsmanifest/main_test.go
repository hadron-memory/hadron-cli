// Package gentoolsmanifest holds no code — only the test that drives
// scripts/gen-tools-manifest.sh against a fixture server tree (hadron-cli#901).
//
// The generator once read only src/mcp/server.ts, so every tool registered in a
// split module (src/mcp/agentDefinitionTools.ts, signingTools.ts, …) was
// missing from the embedded manifest and `spec check-tools` reported the specs
// that cite them as drift. The fixture reproduces that layout, so a module the
// generator does not read fails here and not on the next spec that cites it.
package gentoolsmanifest

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func write(t *testing.T, root, rel, body string) {
	t.Helper()
	p := filepath.Join(root, rel)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// generate runs the script against a fixture and returns the tool names (the
// non-comment lines), in the order printed.
func generate(t *testing.T, root string) []string {
	t.Helper()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("bash", filepath.Join(wd, "..", "gen-tools-manifest.sh"))
	cmd.Env = append(os.Environ(), "HADRON_SERVER_DIR="+root)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("generator failed: %v\n%s", err, out)
	}
	var names []string
	for _, line := range strings.Split(string(out), "\n") {
		if line = strings.TrimSpace(line); line != "" && !strings.HasPrefix(line, "#") {
			names = append(names, line)
		}
	}
	return names
}

func fixture(t *testing.T) string {
	root := t.TempDir()
	write(t, root, "src/mcp/server.ts", "server.tool(\n  'hadron_get_node',\n  'desc',\n  {},\n  h);\n")
	// A split module, multi-line registration, name on the following line.
	write(t, root, "src/mcp/agentDefinitionTools.ts", "export function register(server) {\n  server.tool(\n    'hadron_agent_tasks',\n    'List tasks',\n    {},\n    h);\n}\n")
	// A split module, single-line registration.
	write(t, root, "src/mcp/automationPinTools.ts", "  server.tool('hadron_preview_automation', 'Review', schema, h);\n")
	// Registered but gated behind a feature: still a registered name.
	write(t, root, "src/mcp/signingTools.ts", "export function register(server, enabled) {\n  if (enabled) {\n    server.tool(\n      \"hadron_request_signature\",\n      'Ask',\n      {},\n      h);\n  }\n}\n")
	// A nested module is a module too.
	write(t, root, "src/mcp/extra/nestedTools.ts", "server.tool('hadron_nested_tool', 'x', {}, h);\n")
	// Test files register fixtures, not tools.
	write(t, root, "src/mcp/server.fixture.test.ts", "server.tool('hadron_only_in_a_test', 'x', {}, h);\n")
	// Runner tools stay in, and a duplicate of an MCP name appears once.
	write(t, root, "src/lib/runner/tools/read.ts", "export const t = { name: 'hadron_read_node' };\nexport const u = { name: 'hadron_get_node' };\n")
	return root
}

func TestGeneratorScansEverySplitMCPModule(t *testing.T) {
	got := generate(t, fixture(t))
	set := map[string]int{}
	for _, n := range got {
		set[n]++
	}
	for _, want := range []string{
		"hadron_get_node",           // server.ts
		"hadron_agent_tasks",        // split module, multi-line
		"hadron_preview_automation", // split module, single-line
		"hadron_request_signature",  // registered but gated
		"hadron_nested_tool",        // nested module
		"hadron_read_node",          // runner tool
	} {
		if set[want] != 1 {
			t.Errorf("%s appears %d times, want exactly once\nall: %v", want, set[want], got)
		}
	}
	if set["hadron_only_in_a_test"] != 0 {
		t.Errorf("a *.test.ts registration must not count: %v", got)
	}
}

func TestGeneratorOutputIsSorted(t *testing.T) {
	got := generate(t, fixture(t))
	for i := 1; i < len(got); i++ {
		if got[i-1] >= got[i] {
			t.Fatalf("not sorted/unique at %d: %v", i, got)
		}
	}
}
