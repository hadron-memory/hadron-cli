package cmd

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"

	"github.com/spf13/cobra"
)

// cli#1053 slice 1 (app and agent): every example in the help is run through the
// real command tree against a local fixture server (gate amendment r2: a fixture
// is how destructive and outward-facing examples are verified). The fixture
// answers every GraphQL operation with an empty data object, so this verifies
// that each example PARSES and is WIRED: the flags exist, the arguments are
// accepted, and the command either reaches the server (the operation it sent is
// recorded) or refuses for a reason the help documents (a confirmation, a
// local usage check). It does not assert output shape. Examples that rely on a
// real file, key or worktree get a temporary directory; absolute paths are
// redirected into it, so nothing is written outside the test.

// splitShell splits one example line the way a shell would for plain quoting:
// single quotes, double quotes and backslash escapes. Anything fancier is
// reported by the caller as not run.
func splitShell(line string) ([]string, error) {
	var out []string
	var cur strings.Builder
	in, quote := false, rune(0)
	flush := func() {
		if in {
			out = append(out, cur.String())
			cur.Reset()
			in = false
		}
	}
	rs := []rune(line)
	for i := 0; i < len(rs); i++ {
		r := rs[i]
		switch {
		case quote == '\'':
			if r == '\'' {
				quote = 0
			} else {
				cur.WriteRune(r)
			}
		case quote == '"':
			switch {
			case r == '"':
				quote = 0
			case r == '\\' && i+1 < len(rs) && strings.ContainsRune("\"\\$`", rs[i+1]):
				i++
				cur.WriteRune(rs[i])
			default:
				cur.WriteRune(r)
			}
		case r == '\'' || r == '"':
			quote, in = r, true
		case r == '\\' && i+1 < len(rs):
			i++
			cur.WriteRune(rs[i])
			in = true
		case r == ' ' || r == '\t':
			flush()
		default:
			cur.WriteRune(r)
			in = true
		}
	}
	if quote != 0 {
		return nil, fmt.Errorf("unterminated quote")
	}
	flush()
	return out, nil
}

// exampleLines returns the runnable example commands of one Example block: lines
// starting with "hadron", with backslash continuations joined, comments dropped.
func exampleLines(block string) []string {
	var cmds []string
	var cur string
	for _, raw := range strings.Split(block, "\n") {
		line := strings.TrimSpace(raw)
		if cur == "" && !strings.HasPrefix(line, "hadron ") {
			continue
		}
		if strings.HasSuffix(line, "\\") {
			cur += strings.TrimSuffix(line, "\\") + " "
			continue
		}
		cur += line
		cmds = append(cmds, strings.TrimSpace(cur))
		cur = ""
	}
	return cmds
}

// knownPanics1053 lists the commands (by path) that panic on a reply with no data,
// each tracked by a follow-up issue (a panic is a defect of the command, not of
// the help).
var knownPanics1053 = map[string]string{
	"app key scope grant": "#2492",
}

type exampleResult struct {
	Page, Example, Mode, Outcome string
	Broken                       bool
}

// brokenMarkers are the cobra and flag-parsing refusals that mean the EXAMPLE is
// wrong: it names a flag or command that does not exist, or the wrong arguments.
var brokenMarkers = []string{
	"unknown flag", "unknown shorthand flag", "unknown command", "flag needs an argument",
	"required flag(s)", "accepts ", "invalid argument", "if any flags in the group", "unknown subcommand",
}

func collectSlice1Pages(root *cobra.Command) []*cobra.Command {
	var pages []*cobra.Command
	var walk func(c *cobra.Command)
	walk = func(c *cobra.Command) {
		if c.Hidden {
			return
		}
		if c != root {
			pages = append(pages, c)
		}
		for _, k := range c.Commands() {
			walk(k)
		}
	}
	for _, name := range []string{"app", "agent"} {
		if c, _, err := root.Find([]string{name}); err == nil && c != root {
			walk(c)
		}
	}
	return pages
}

func TestSlice1HelpExamplesAreWired1053(t *testing.T) {
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
	pages := collectSlice1Pages(NewRootCmd(f0))
	if len(pages) < 80 {
		t.Fatalf("found only %d app/agent pages; the walk is wrong", len(pages))
	}

	var results []exampleResult
	for _, page := range pages {
		path := strings.TrimPrefix(page.CommandPath(), "hadron ")
		for _, line := range exampleLines(page.Example) {
			// A pipeline is run one hadron command at a time (the other tools, such as
			// jq, are not ours); a trailing "> file" redirect is the shell's, not the
			// command's, so it is dropped and the command run as it would be.
			var segments []string
			for _, seg := range strings.Split(line, " | ") {
				seg = strings.TrimSpace(seg)
				if i := strings.LastIndex(seg, " > "); i > 0 {
					seg = strings.TrimSpace(seg[:i])
				}
				if strings.HasPrefix(seg, "hadron ") {
					segments = append(segments, seg)
				}
			}
			for _, ex := range segments {
				res := exampleResult{Page: path, Example: ex, Mode: "fixture"}
				if len(segments) > 1 {
					res.Mode = "fixture (one command of a pipeline)"
				}
				noPlaceholders := ex
				for {
					i := strings.Index(noPlaceholders, "<")
					j := strings.Index(noPlaceholders, ">")
					if i < 0 || j < i || strings.ContainsAny(noPlaceholders[i+1:j], " \t\"'") {
						break
					}
					noPlaceholders = noPlaceholders[:i] + "x" + noPlaceholders[j+1:]
				}
				if strings.ContainsAny(noPlaceholders, "|;&><") || strings.Contains(ex, "$(") {
					res.Mode, res.Outcome = "not run", "a shell construct (substitution, redirect or list) the harness does not run"
					results = append(results, res)
					continue
				}
				words, err := splitShell(ex)
				if err != nil || len(words) < 2 {
					res.Broken, res.Outcome = true, fmt.Sprintf("example does not split into words: %v", err)
					results = append(results, res)
					continue
				}
				dir := t.TempDir()
				args := make([]string, 0, len(words)+2)
				for _, w := range words[1:] {
					switch {
					case strings.HasPrefix(w, "/etc/") || strings.HasPrefix(w, "/home/") || strings.HasPrefix(w, "/var/"):
						w = filepath.Join(dir, filepath.Base(w)) // never write outside the test directory
					case strings.HasPrefix(w, "<") && strings.HasSuffix(w, ">"):
						w = "fixture-" + strings.Trim(w, "<>") // a placeholder the reader fills in
					case strings.HasPrefix(w, "$") && len(w) > 1 && strings.Trim(w[1:], "ABCDEFGHIJKLMNOPQRSTUVWXYZ_") == "":
						// A shell variable the reader has set (for example $REVISION, a number).
						if strings.Contains(w, "REVISION") {
							w = "3"
						} else {
							w = "fixture-" + strings.ToLower(w[1:])
						}
					}
					args = append(args, w)
				}
				// A user who follows the example has a file where it names one.
				for _, a := range args {
					if strings.HasPrefix(a, "@") && len(a) > 1 {
						if err := os.WriteFile(filepath.Join(dir, a[1:]), []byte(`{"team":{"x":1}}`), 0o600); err != nil {
							t.Fatal(err)
						}
					}
				}
				for i, a := range args {
					if i > 0 && (args[i-1] == "--policy-file" || args[i-1] == "--payload-file" || args[i-1] == "--override-file") {
						if err := os.WriteFile(filepath.Join(dir, a), []byte(`{"allow":[]}`), 0o600); err != nil {
							t.Fatal(err)
						}
					}
				}
				t.Run(path+" | "+ex, func(t *testing.T) {
					t.Chdir(dir)
					f, out := testFactory(t)
					setActiveApp(t, "hrn:app:acme.com:eng-team")
					f.IOStreams.In = strings.NewReader(`{"team":{"x":1}}`) // an example that reads "-" gets a JSON document
					mu.Lock()
					before := len(ops)
					mu.Unlock()
					root := NewRootCmd(f)
					root.SetArgs(append(args, "--server", srv.URL))
					var err error
					panicked := ""
					func() {
						defer func() {
							if r := recover(); r != nil {
								panicked = fmt.Sprint(r)
							}
						}()
						err = root.Execute()
					}()
					mu.Lock()
					sent := append([]string(nil), ops[before:]...)
					mu.Unlock()
					_ = out
					if panicked != "" {
						// A command that panics on a reply the server never sends (the fixture
						// answers with an empty data object) is a defect of the command, not of
						// its example: recorded as a finding, tracked by knownPanics1053.
						res.Outcome = "PANIC on an empty fixture reply: " + panicked
						known := false
						for cmdPath := range knownPanics1053 {
							known = known || strings.Contains(ex, "hadron "+cmdPath+" ")
						}
						if !known {
							res.Broken = true
							t.Errorf("%s panicked: %s", ex, panicked)
						}
						results = append(results, res)
						return
					}
					switch {
					case err == nil && len(sent) == 0:
						res.Outcome = "ran, no request needed"
					case err == nil:
						res.Outcome = "ran, sent " + strings.Join(sent, ", ")
					default:
						msg := strings.SplitN(err.Error(), "\n", 2)[0]
						for _, m := range brokenMarkers {
							if strings.Contains(msg, m) {
								res.Broken = true
							}
						}
						switch {
						case res.Broken:
							res.Outcome = "BROKEN: " + msg
						case len(sent) > 0:
							res.Outcome = fmt.Sprintf("reached the server (%s); then: %s", strings.Join(sent, ", "), msg)
						case strings.Contains(msg, "without --yes"):
							res.Outcome = "asks for confirmation (refused without a terminal, as documented)"
						default:
							res.Outcome = "refused locally before any request: " + msg
						}
					}
					if res.Broken {
						t.Errorf("%s: %s", ex, res.Outcome)
					}
					results = append(results, res)
				})
			}
		}
	}

	if path := os.Getenv("HELP_EXAMPLES_REPORT"); path != "" {
		sort.SliceStable(results, func(i, j int) bool { return results[i].Page < results[j].Page })
		var b strings.Builder
		b.WriteString("| page | example | mode | outcome |\n|---|---|---|---|\n")
		for _, r := range results {
			fmt.Fprintf(&b, "| `%s` | `%s` | %s | %s |\n", r.Page, strings.ReplaceAll(r.Example, "|", "\\|"), r.Mode, strings.ReplaceAll(r.Outcome, "|", "\\|"))
		}
		if err := os.WriteFile(path, []byte(b.String()), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if len(results) < 100 {
		t.Fatalf("only %d examples found across the app and agent pages; expected the ~119 the inventory implies", len(results))
	}
}
