package cmd

import (
	"strings"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/exitcode"
)

// cli#3114 slice 1: values typed as flags, and values that reach a refusal through a helper, are named
// by kind and never shown. The flow guard (noecho_flow_3114_test.go) covers positionals everywhere;
// these pins render each shape through the root like the binary does, in text and --json.
func TestSlice1RefusalsNeverShowTheTypedValue3114(t *testing.T) {
	p := pastedUpper3029()
	low := strings.ToLower(p)
	for _, tc := range []struct {
		name      string
		responses map[string]string
		argv      []string
		says      string
		exit      int
	}{
		{"credential-capable header", nil, []string{"mcp-server", "create", "--org", "acme.com", "--slug", "x", "--name", "x", "--url", "https://example.com", "--header", p}, "malformed --header", exitcode.Usage},
		{"api -F", nil, []string{"api", "query{viewer{id}}", "-F", p}, "invalid -F", exitcode.Usage},
		{"task run --arg", nil, []string{"task", "run", "hrn:node:acme.com:kb:t", "--arg", p}, "--arg must be", exitcode.Usage},
		{"access shorthand", nil, []string{"access", "check", "acme.com::" + low, "acme.com::" + low}, "not a fully-qualified URN", exitcode.Usage},
		{"asset ref", nil, []string{"asset", "get", "acme.com:kb:" + p}, "not an asset id or URN", exitcode.Usage},
		{"unlock twice", nil, []string{"memory", "list", "--unlock", "acme.com::" + low, "--unlock", "acme.com::" + low, "--unlock-passphrase-stdin"}, "same memory twice", exitcode.Usage},
		{"delete without --yes", nil, []string{"ha", "rm", p}, "refusing to delete", exitcode.Usage},
		{"connect provider", nil, []string{"connection", "connect", p, "--org", "acme.com"}, "unknown provider", exitcode.Usage},
		{"skill lint --node", nil, []string{"skill", "lint", "--node", "acme.com:" + low}, "--node", exitcode.Usage},
		{"ai config not found", map[string]string{"AiServiceConfigWithUrn": `{"data":{"aiServiceConfig":null}}`}, []string{"ai-config", "get", p}, "AI service config not found", exitcode.NotFound},
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

// The gate's second half: a typed enum or number drops the echo and names the accepted form.
func TestSlice1EnumRefusalsNameTheFormNotTheValue3114(t *testing.T) {
	low := strings.ToLower(pastedUpper3029())
	for _, tc := range []struct {
		argv []string
		form string
	}{
		{[]string{"run", "list", "--app", "hrn:app:acme.com:ops", "--status"}, "one of"},
		{[]string{"run", "list", "--app", "hrn:app:acme.com:ops", "--statuses"}, "each one of"},
		{[]string{"search", "q", "--mode"}, "hybrid, keyword, vector, or regex"},
		{[]string{"search", "q", "--field"}, "want one of"},
		{[]string{"agent", "list", "--type"}, "ASSISTANT or CHATBOT"},
	} {
		t.Run(strings.Join(tc.argv, " "), func(t *testing.T) {
			seen, code := renderedNoEcho3029(t, nil, append(append([]string{}, tc.argv...), low)...)
			if code != exitcode.Usage || !strings.Contains(seen, tc.form) {
				t.Errorf("exit %d, want the accepted form %q: %s", code, tc.form, seen)
			}
			if strings.Contains(strings.ToLower(seen), low) {
				t.Errorf("shows the value: %s", seen)
			}
		})
	}
}
