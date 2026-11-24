package cmd

import (
	"strings"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/exitcode"
)

// cli#3029 BLIND F1 (Gravitus), ruled blocking by Peter (team chat #145360): the no-echo guarantee is
// proven for each path class a pasted secret actually takes, not only for refusals that reach the
// server answer. The token here is deliberately INVALID for the front-line validators (upper case), so
// it stops at them rather than routing around them.
//
// Classes and where each is pinned:
//   - front-line validators: the URN validators (app, node, org, scope, every URN-typed flag) are pinned
//     by cli#2902 (internal/cmdutil/noecho_2902_test.go, internal/cmd/noecho_2902_test.go); the memory-ref
//     validator and its not-found helper below.
//   - wrapped parse errors: spec extract / spec new --new-path in TestRefusalsNeverEchoThePositionalArg3029
//     (the token fails ParseCitation, whose error echoed and was wrapped with %v), plus below with the
//     upper-case token.
//   - cobra/pflag: unknown root command, unknown group subcommand, unknown long and shorthand flag,
//     invalid flag value, bad flag syntax, below.

func pastedUpper3029() string {
	// Assembled at runtime: a token-shaped literal in the source trips push protection.
	return "xo" + "xb-1234567890-0987654321-AbCdEfGhIjKlMnOpQrStUvWx"
}

// renderedNoEcho3029 runs argv through the root like the binary does (renderFailure after Execute) and
// returns what the operator sees: stdout, stderr and the --json envelope. cobra raises an unknown root
// command inside Find, before any command code, so its raw error carries the value until renderError
// rewrites it; what is checked is therefore the rendered output, which is everything the CLI prints.
func renderedNoEcho3029(t *testing.T, responses map[string]string, argv ...string) (seen string, code int) {
	t.Helper()
	gql, _ := captureGraphQL(t, responses)
	var all strings.Builder
	for _, jsonMode := range []bool{false, true} {
		f, out := testFactory(t)
		var stderr strings.Builder
		f.IOStreams.ErrOut = &stderr
		root := NewRootCmd(f)
		args := append(append([]string{}, argv...), "--server", gql.URL)
		if jsonMode {
			args = append(args, "--json")
		}
		root.SetArgs(args)
		err := root.Execute()
		if err == nil {
			t.Fatalf("%v: want a refusal", argv)
		}
		code = renderFailure(f, args, err)
		all.WriteString(out.String() + stderr.String())
	}
	return all.String(), code
}

func assertNoEcho3029(t *testing.T, seen string, says string) {
	t.Helper()
	p := pastedUpper3029()
	for _, leak := range []string{p, p[:8], p[len(p)-8:], "AbCdEf"} {
		if strings.Contains(seen, leak) {
			t.Errorf("echoes %q of the value: %q", leak, seen)
		}
	}
	if !strings.Contains(seen, says) || !strings.Contains(seen, "not shown") {
		t.Errorf("says what is wrong (%q) and that the value is not shown: %q", says, seen)
	}
}

func TestNoEchoOnEveryPathClass3029(t *testing.T) {
	p := pastedUpper3029()
	for _, tc := range []struct {
		name      string
		responses map[string]string
		argv      []string
		says      string
	}{
		// front-line validator
		{"memory-ref validator", nil, []string{"memory", "member", "list", p}, "invalid memory reference"},
		{"memory-ref validator (memory get)", nil, []string{"memory", "get", p}, "invalid memory reference"},
		{"memory not found helper", map[string]string{"GetMemory": `{"data":{"memory":null}}`}, []string{"memory", "share", "list", "acme.com::" + strings.ToLower(p)}, "no memory found"},
		{"URN validator (app ref)", nil, []string{"app", "get", p}, "not org-qualified"},
		{"URN validator (node ref)", nil, []string{"node", "get", p}, "not a fully-qualified node URN"},
		// wrapped parse error
		{"wrapped ParseCitation (spec extract)", nil, []string{"spec", "extract", p, "--title", "x", "--to-feature", "001", "-m", "acme.com::specs"}, "is not one"},
		{"wrapped ParseCitation (spec new --new-path)", nil, []string{"spec", "new", p, "--new-path", "--title", "x", "-m", "acme.com::specs"}, "is not one"},
		// cobra / pflag
		{"unknown root command", nil, []string{p}, "unknown command"},
		{"unknown long flag", nil, []string{"agent", "get", "x", "--" + p}, "unknown flag"},
		{"unknown shorthand flag", nil, []string{"agent", "get", "x", "-Z" + p}, "unknown flag"},
		{"invalid flag value", nil, []string{"asset", "list", "--limit", p}, "invalid value for --limit"},
		{"bad flag syntax", nil, []string{"agent", "get", "x", "---" + p}, "bad flag syntax"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			seen, code := renderedNoEcho3029(t, tc.responses, tc.argv...)
			if code != exitcode.Usage && code != exitcode.NotFound {
				t.Errorf("exit = %d, want a usage or not-found exit", code)
			}
			assertNoEcho3029(t, seen, tc.says)
		})
	}
}

// The group-level unknown-subcommand probe runs in Execute before cobra (#232), so it is driven the way
// Execute drives it.
func TestUnknownSubcommandProbeNeverEchoesTheName3029(t *testing.T) {
	p := pastedUpper3029()
	for _, jsonMode := range []bool{false, true} {
		f, out := testFactory(t)
		var stderr strings.Builder
		f.IOStreams.ErrOut = &stderr
		_, err := checkUnknownSubcommand([]string{"agent", p})
		if err == nil {
			t.Fatal("want the unknown-command refusal")
		}
		f.JSON = jsonMode
		if code := renderError(f, err); code != exitcode.Usage {
			t.Errorf("exit = %d, want %d", code, exitcode.Usage)
		}
		assertNoEcho3029(t, err.Error()+out.String()+stderr.String(), `unknown command for "hadron agent"`)
	}
}

// A flag type's own reason is kept when it does not carry the value; the value itself never is.
func TestInvalidFlagValueKeepsAReasonWithoutTheValue3029(t *testing.T) {
	seen, code := renderedNoEcho3029(t, nil, "asset", "list", "--limit", "12x")
	if code != exitcode.Usage {
		t.Errorf("exit = %d, want %d", code, exitcode.Usage)
	}
	if strings.Contains(seen, "12x") || !strings.Contains(seen, "expected int") {
		t.Errorf("names the flag and the expected type, not the value: %q", seen)
	}
}
