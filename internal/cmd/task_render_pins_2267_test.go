package cmd

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/exitcode"
)

// cli#2267: the optional task-render pins after #2266.

func flatHelp2267(t *testing.T, args ...string) string {
	t.Helper()
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs(append(args, "--help"))
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	return strings.Join(strings.Fields(out.String()), " ")
}

// F2: a qualified reference supplies its own memory. The request carries the
// URN exactly as typed and a conflicting -m has no effect. This pins
// composition, NOT a refusal of the pair: no spec defines one, and adding it
// would be a new client admission rule (Victor's FIT, cor:urn:010:02,
// cor:api:140:00).
func TestTaskRenderQualifiedRefSuppliesItsOwnMemory2267(t *testing.T) {
	for _, tc := range []struct{ name, ref, memory string }{
		{"v2 URN, other memory", renderNodeRef1010, "other.com:elsewhere"},
		{"v2 URN, memory in v2 form", renderNodeRef1010, "hrn:mem:other.com:elsewhere"},
		{"legacy qualified spelling, other memory", "acme.com::kb::tasks:brief", "other.com::elsewhere"},
		// cli#2417 F7: a node ID is its own address too.
		{"node ID, other memory", "0123456789abcdef0123456789abcdef", "other.com:elsewhere"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server, requests := renderFixture1010(t, `{"data":{"renderRunNode":"ok"}}`)
			f, _ := testFactory(t)
			root := NewRootCmd(f)
			args := []string{"task", "render", tc.ref, "-m", tc.memory, "--server", server.URL}
			root.SetArgs(args)
			if err := root.Execute(); err != nil {
				t.Fatalf("a qualified ref with -m must compose, not refuse: args %q: %T: %v", args, err, err)
			}
			vars := requestVariables2417(t, requests, "with -m")
			// Without -m the same ref composes to this; -m must not change it.
			wantServer, wantRequests := renderFixture1010(t, `{"data":{"renderRunNode":"ok"}}`)
			f2, _ := testFactory(t)
			root2 := NewRootCmd(f2)
			root2.SetArgs([]string{"task", "render", tc.ref, "--server", wantServer.URL})
			if err := root2.Execute(); err != nil {
				t.Fatal(err)
			}
			want := requestVariables2417(t, wantRequests, "without -m")
			if vars["nodeRef"] != want["nodeRef"] {
				t.Fatalf("-m changed a qualified ref: with -m %v, without %v", vars["nodeRef"], want["nodeRef"])
			}
		})
	}
}

// requestVariables2417 returns the variables of the one request the fixture
// captured. It fails on a wrong request count, an undecodable "variables" or a
// missing nodeRef, so a comparison of two absent values can never pass
// vacuously (cli#2417 F2/F3).
func requestVariables2417(t *testing.T, c *renderCapture1010, which string) map[string]any {
	t.Helper()
	reqs := c.snapshot()
	if len(reqs) != 1 {
		t.Fatalf("%s: requests=%d, want exactly 1", which, len(reqs))
	}
	var vars map[string]any
	if err := json.Unmarshal(reqs[0]["variables"], &vars); err != nil {
		t.Fatalf("%s: the request carries no decodable variables: %v", which, err)
	}
	if vars["nodeRef"] == nil {
		t.Fatalf("%s: the request variables carry no nodeRef: %v", which, vars)
	}
	return vars
}

// F1/F9/F10 and F5: the help says, in complete sentences, what render does and
// how it compares with run, and the parent names both verbs. The recording
// sentences stay within cor:api:060:05: no model, no content written, the read
// still recorded.
func TestTaskRenderHelpSentences2267(t *testing.T) {
	render := flatHelp2267(t, "task", "render")
	for _, want := range []string{
		"task render calls only the renderRunNode Query.",
		"It runs no model, creates no run row and records no action-run, but the server still records the content reads, including actually inlined dependencies.",
		"Compare task run: without --app it calls the runTask mutation and also records an action-run; with --app it executes.",
		"A qualified reference supplies its own memory; -m composes only a bare loc.",
	} {
		if !strings.Contains(render, want) {
			t.Errorf("task render help lacks the sentence %q", want)
		}
	}
	run := flatHelp2267(t, "task", "run")
	want := "To only read the prompt, use 'hadron task render'. Both record content read usage; task run's render mode also records an action-run, while task render records no action-run and never executes."
	if !strings.Contains(run, want) {
		t.Errorf("task run help lacks the complete comparison sentence %q", want)
	}
	parent := flatHelp2267(t, "task")
	if !strings.Contains(parent, "Nothing executes unless you pass --app") {
		t.Errorf("the task group must say that run only prints without --app:\n%s", parent)
	}
}

func TestAgenticUsageTaskRenderMemoryComposition2267(t *testing.T) {
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"agentic-usage"})
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	doc := strings.Join(strings.Fields(out.String()), " ")
	if !strings.Contains(doc, "A qualified reference supplies its own memory; `-m` composes only a bare loc") {
		t.Fatal("agentic-usage lacks the qualified-reference/-m sentence for task render")
	}
}

// F6: the mapped exit categories for broken and refused replies, in text mode
// (a human refusal through the root path) and --json. A body that is not JSON
// and a SERVICE_UNAVAILABLE code are both generic failures (exitcode.Error), a
// FORBIDDEN is exitcode.Forbidden; none retries, none prints a render, and the refusal text
// reaches the error, not stdout.
func TestTaskRenderMappedExitCategories2267(t *testing.T) {
	for _, tc := range []struct {
		name, reply string
		exit        int
		message     string
	}{
		// No message pin for the undecodable body: its text is the decoder's
		// own (Go's stdlib wording), which is not this CLI's contract
		// (cli#2417 F5); the exit category and the no-retry checks carry it.
		{"body is not JSON", "not JSON", exitcode.Error, ""},
		{"service unavailable code", `{"errors":[{"message":"unavailable","extensions":{"code":"SERVICE_UNAVAILABLE"}}]}`, exitcode.Error, "unavailable"},
		{"forbidden", `{"errors":[{"message":"refused","extensions":{"code":"FORBIDDEN","requestId":"r-2267"}}]}`, exitcode.Forbidden, "refused"},
	} {
		for _, asJSON := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/json=%t", tc.name, asJSON), func(t *testing.T) {
				server, requests := renderFixture1010(t, tc.reply)
				f, out := testFactory(t)
				args := []string{"task", "render", renderNodeRef1010, "--server", server.URL}
				if asJSON {
					args = append(args, "--json")
				}
				root := NewRootCmd(f)
				root.SetArgs(args)
				err := root.Execute()
				if err == nil || exitCodeFor(err) != tc.exit || renderError(f, err) != tc.exit {
					t.Fatalf("exit=%d rendered=%d, want %d (err=%v)", exitCodeFor(err), renderError(f, err), tc.exit, err)
				}
				if len(requests.snapshot()) != 1 {
					t.Fatalf("no retry: requests=%d", len(requests.snapshot()))
				}
				if !asJSON && out.Len() != 0 {
					t.Fatalf("a human refusal prints nothing on stdout, got %q", out.String())
				}
				if strings.Contains(out.String(), `"rendered"`) {
					t.Fatalf("a refusal must not render: %s", out.String())
				}
				if err.Error() == "" || !strings.Contains(err.Error(), tc.message) {
					t.Fatalf("error %q lacks %q", err, tc.message)
				}
			})
		}
	}
}

// cli#2417 F8: the one-line summary says what render does not do (no
// execution, no run row), and does not claim "read-only": the server records
// the content reads (cor:api:060:05).
func TestTaskRenderShortWording2417(t *testing.T) {
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	cmd, _, err := root.Find([]string{"task", "render"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(cmd.Short, "no execution, no run row") || strings.Contains(strings.ToLower(cmd.Short), "read-only") {
		t.Fatalf("task render Short = %q", cmd.Short)
	}
}
