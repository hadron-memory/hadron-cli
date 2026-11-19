package cmd

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/spf13/cobra"

	"github.com/hadron-memory/hadron-cli/internal/exitcode"
)

// cli#2579 (the group 1184 residual rows: automation grant/team-snapshot-grant/preview/re-adopt, billing grant and prices, marketplace, quota, usage): every example in the help of the replace
// and comment pages is run through the real command tree against a local fixture
// server, the same mode as the app/agent walk (#1053 slice 1): the fixture
// answers every GraphQL operation with an empty data object, so this verifies
// that each example PARSES and is WIRED (the flags exist, the arguments are
// accepted, and the command reaches the server or refuses for a reason the help
// documents). It does not assert output shape, and it is not usability evidence:
// a row reaches `pass` only through a named evaluator who did not write the text.
// The money-moving and destructive examples in this set (billing grant, billing prices set, quota set, usage record, automation grant/team-snapshot-grant set|revoke, re-adopt) run ONLY against this local fixture, never a real memory, App, ledger or server (the #1053 gate amendment r2 vehicle).

// knownPanics2579 lists the commands (by path) that panic on a reply with no data,
// each tracked by a follow-up issue: a panic is a defect of the command, not of
// its example, so the walk records it here instead of failing the help check.
var knownPanics2579 = map[string]string{}

func collectResidualPages2579(root *cobra.Command) []*cobra.Command {
	var pages []*cobra.Command
	add := func(path ...string) {
		if c, _, err := root.Find(path); err == nil && c != root && c.Name() == path[len(path)-1] {
			pages = append(pages, c)
		}
	}
	for _, p := range [][]string{
		{"automation", "grant"}, {"automation", "grant", "list"}, {"automation", "grant", "revoke"}, {"automation", "grant", "set"},
		{"automation", "preview"}, {"automation", "re-adopt"},
		{"automation", "team-snapshot-grant"}, {"automation", "team-snapshot-grant", "list"}, {"automation", "team-snapshot-grant", "revoke"}, {"automation", "team-snapshot-grant", "set"},
		{"billing", "grant"}, {"billing", "prices", "get"}, {"billing", "prices", "set"},
		{"marketplace"}, {"marketplace", "agents"}, {"marketplace", "memories"}, {"marketplace", "orgs"},
		{"quota"}, {"quota", "list"}, {"quota", "set"},
		{"usage"}, {"usage", "activity"}, {"usage", "events"}, {"usage", "record"},
	} {
		add(p...)
	}
	return pages
}

func TestResidualHelpExamplesAreWired2579(t *testing.T) {
	var mu sync.Mutex
	var ops []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			OperationName string `json:"operationName"`
		}
		b, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(b, &body)
		mu.Lock()
		ops = append(ops, body.OperationName)
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":{}}`))
	}))
	t.Cleanup(srv.Close)

	f0, _ := testFactory(t)
	pages := collectResidualPages2579(NewRootCmd(f0))
	// Exactly the 24 rows of the issue: a floor against a walk that silently finds
	// almost nothing, and a tripwire if a row is renamed.
	if len(pages) != 24 {
		t.Fatalf("found %d of the 24 residual pages; the walk is wrong", len(pages))
	}
	examples := 0
	for _, page := range pages {
		path := strings.TrimPrefix(page.CommandPath(), "hadron ")
		for _, line := range exampleLines(page.Example) {
			segments := []string{}
			for _, seg := range strings.Split(line, " | ") {
				seg = strings.TrimSpace(seg)
				if strings.HasPrefix(seg, "hadron ") {
					segments = append(segments, seg)
				}
			}
			for _, ex := range segments {
				examples++
				words, err := splitShell(ex)
				if err != nil || len(words) < 2 {
					t.Errorf("%s: example %q does not split into words: %v", path, ex, err)
					continue
				}
				dir := t.TempDir()
				args := make([]string, 0, len(words)+2)
				for _, w := range words[1:] {
					if strings.HasPrefix(w, "<") && strings.HasSuffix(w, ">") {
						w = "fixture-" + strings.Trim(w, "<>") // a placeholder the reader fills in
					}
					args = append(args, w)
				}
				if unsafe := unsafeExamplePath1053(args, dir); unsafe != "" {
					t.Errorf("%s: example %q names the path %q, which the harness does not redirect", path, ex, unsafe)
					continue
				}
				for i, a := range args {
					if i > 0 && args[i-1] == "--body-file" { // a user who follows the example has the file
						if err := os.WriteFile(filepath.Join(dir, a), []byte("a note"), 0o600); err != nil {
							t.Fatal(err)
						}
					}
				}
				t.Run(path+" | "+ex, func(t *testing.T) {
					t.Chdir(dir)
					f, _ := testFactory(t)
					f.IOStreams.In = strings.NewReader("piped text") // an example that reads "-"
					mu.Lock()
					before := len(ops)
					mu.Unlock()
					root := NewRootCmd(f)
					root.SetArgs(append(args, "--server", srv.URL))
					var err error
					panicked := false
					func() {
						defer func() {
							if r := recover(); r != nil {
								for cmdPath, issue := range knownPanics2579 {
									if ex == "hadron "+cmdPath || strings.HasPrefix(ex, "hadron "+cmdPath+" ") {
										t.Logf("known panic on an empty fixture reply (%s): %v", issue, r)
										panicked = true
									}
								}
								if !panicked {
									t.Errorf("panicked on an empty fixture reply: %v", r)
								}
							}
						}()
						err = root.Execute()
					}()
					if panicked {
						return
					}
					mu.Lock()
					sent := append([]string(nil), ops[before:]...)
					mu.Unlock()
					msg := ""
					if err != nil {
						msg = strings.SplitN(err.Error(), "\n", 2)[0]
					}
					for _, m := range brokenMarkers {
						if strings.Contains(msg, m) {
							t.Errorf("the example is broken: %s", msg)
						}
					}
					switch {
					case err == nil && len(sent) == 0:
						t.Errorf("ran without sending any request")
					case err != nil && len(sent) > 0 && exitCodeFor(err) == exitcode.Usage:
						t.Errorf("a usage error after a request was sent: %s", msg)
					case err != nil && len(sent) == 0:
						// The only local refusal the help documents is the missing --yes.
						if !strings.Contains(msg, "without --yes") && !strings.Contains(msg, "--yes") {
							t.Errorf("refused locally before any request: %s", msg)
						}
					}
				})
			}
		}
	}
	if examples < 10 {
		t.Fatalf("only %d examples found across the residual pages", examples)
	}
}
