package cmd

import (
	"strings"
	"testing"
)

// cli#2558 (help slice-4 polish, deferred P3s from #2553): each acceptance
// item is pinned by a literal string in the live --help, exactly as the issue
// table checks them with grep -F. "gone" strings are the wording the item
// replaces; a page that regresses to the old phrasing fails here. Item 5 is a
// count: "exits 0" is stated once, in the chat-probe paragraph whose exact
// sentence TestAiConfig1959HelpStatesOneExitContract pins.
func TestHelpSlice4Polish2558(t *testing.T) {
	cases := []struct {
		name    string
		args    []string
		present []string
		gone    []string
	}{
		{
			name:    "1 ai-config rm json shape names the id",
			args:    []string{"ai-config", "rm"},
			present: []string{`{"id": <id>, "status": "deleted"}`},
			gone:    []string{`{"id", "status"`},
		},
		{
			name:    "2 ai-config rm outer wording points at precedence",
			args:    []string{"ai-config", "rm"},
			present: []string{"at an outer level"},
			gone:    []string{"at an outer owner"},
		},
		{
			name:    "3 ai-config test provider-unavailable retry and closing line",
			args:    []string{"ai-config", "test"},
			present: []string{"retry after it recovers", "other than FAIL_PROVIDER_UNAVAILABLE"},
			gone:    []string{"retry or fix ok"},
		},
		{
			name:    "4 ai-config test override stdin refusal wording",
			args:    []string{"ai-config", "test"},
			present: []string{"never waits for typed input"},
			gone:    []string{"a piped document is refused"},
		},
		{
			name:    "6 ai-config update enabled flag says when it changes",
			args:    []string{"ai-config", "update"},
			present: []string{"changes it only when passed"},
		},
		{
			name:    "7 connection grant create connection placeholder and flag",
			args:    []string{"connection", "grant", "create"},
			present: []string{"--connection <id>", "ID or ref of the connection"},
			gone:    []string{"--connection <ref>"},
		},
		{
			name:    "8 connection grant create scopes plural",
			args:    []string{"connection", "grant", "create"},
			present: []string{"scope(s) to grant"},
		},
		{
			name:    "9 connection grant create long opening",
			args:    []string{"connection", "grant", "create"},
			present: []string{"Give an App limited access"},
			gone:    []string{"Grant an App scoped access"},
		},
		{
			name:    "10 mcp-server policy wording, wrap and absent layer",
			args:    []string{"mcp-server"},
			present: []string{"fails to allow it", "an absent layer"},
			gone:    []string{"which layer denies"},
		},
		{
			// The leaf Shorts never render on the leaves' own help pages (each
			// has a Long); their first line is the row the group page lists.
			name: "11 mcp-server leaf shorts say ADMIN or OWNER",
			args: []string{"mcp-server"},
			present: []string{
				"Register an external MCP server (org ADMIN or OWNER)",
				"Update a registered MCP server (org ADMIN or OWNER)",
				"Delete a registered MCP server (org ADMIN or OWNER)",
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			text := renderHelp2558(t, tc.args)
			for _, p := range tc.present {
				if !strings.Contains(text, p) {
					t.Errorf("%s --help must contain %q", strings.Join(tc.args, " "), p)
				}
			}
			for _, g := range tc.gone {
				if strings.Contains(text, g) {
					t.Errorf("%s --help must no longer contain %q", strings.Join(tc.args, " "), g)
				}
			}
		})
	}
}

// Item 5: the first exit-0 sentence keeps only its "read ok and error.code"
// half; the one remaining "exits 0" is the chat-probe paragraph's pinned
// sentence (kept verbatim for TestAiConfig1959HelpStatesOneExitContract).
func TestHelpSlice4ExitsZeroStatedOnce2558(t *testing.T) {
	text := renderHelp2558(t, []string{"ai-config", "test", "--help"})
	if n := strings.Count(text, "exits 0"); n != 1 {
		t.Fatalf("ai-config test --help must state \"exits 0\" exactly once, has %d", n)
	}
	if !strings.Contains(text, "exits 0 whatever ok and the chat status say") {
		t.Fatal("the pinned chat-probe sentence must stay verbatim")
	}
	if !strings.Contains(text, "read ok and error.code in --json") {
		t.Fatal("the first sentence keeps its \"read ok and error.code\" half")
	}
}

func renderHelp2558(t *testing.T, args []string) string {
	t.Helper()
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs(append(args, "--help"))
	root.SetOut(out)
	root.SetErr(out)
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	return out.String()
}
