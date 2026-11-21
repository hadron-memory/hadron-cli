package cmd

import (
	"encoding/json"
	"fmt"
	"github.com/hadron-memory/hadron-cli/internal/api/gen"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/hadron-memory/hadron-cli/internal/exitcode"
)

// cli#2565 (the 25 unread group-1051 rows): every example on those pages is run
// through the real command tree against a local fixture server, per the #1053
// gate amendment r2 — none of these commands has a dry-run flag, so the fixture
// arm carries every example a fixture can carry, destructive and outward-facing
// ones included. The fixture answers every GraphQL operation with an empty data
// object, so this verifies that each example PARSES and is WIRED: the flags
// exist, the arguments are accepted, and the command either reaches the server
// (the operation it sent is recorded) or refuses for a reason the help
// documents. It does not assert output shape. The mode/outcome table the PR
// body cites is written by HELP_EXAMPLES_REPORT_2565=<path>.

// pages2565 is the row list of the issue: every unread group:1051 page at main
// 3b858969. A page is named by its command path under `hadron`.
var pages2565 = []string{
	"comment add",
	"question",
	"question amend",
	"question answer",
	"question ask",
	"question get",
	"question list",
	"question log",
	"question log create",
	"question log get",
	"question log list",
	"question log set-deciders",
	"question purge",
	"question retract",
	"question verify",
	"question withdraw",
	"edge get",
	"find",
	"memory loc-segments",
	"memory merge",
	"memory sync",
	"memory sync pull",
	"memory verify",
	"node set-properties",
	"node sign",
	"node signing-request get",
	"node signing-request list",
	"node signing-request withdraw",
}

// min2565Examples is a floor, not a target: it guards against a page list or a
// parser that silently finds almost nothing. Only examples the harness
// actually executes count toward it — "not run" entries (shell constructs,
// prose placeholders) do not.
const min2565Examples = 20

// localOnly2565 are commands that legitimately make no request.
var localOnly2565 = []string{}

// expectedRefusals2565 are examples whose documented outcome IS a refusal before
// any request (an example that shows a refusal on purpose). Empty unless the
// help documents one; a broken example cannot hide behind the allowlist.
var expectedRefusals2565 = map[string]string{}

// knownPanics2565 lists the commands (by path) that panic on a reply with no
// data, each tracked by a follow-up issue (a panic is a defect of the command,
// not of the help).
var knownPanics2565 = map[string]knownPanic{}

// fileArgFlags2565 are the file-valued flags these pages' examples use. The
// harness provides the file each names, so the example reads what a reader
// following it would already have.
var fileArgFlags2565 = map[string]bool{
	"--body-file":              true,
	"--input-file":             true,
	"--override-file":          true,
	"--patch-file":             true,
	"--payload-file":           true,
	"--policy-file":            true,
	"--unlock-passphrase-file": true,
}

// exampleFileBody2565 is the content a provided file gets, by flag: a JSON
// object where the command parses one (an object, not bare text, where an
// empty one is refused), text where it reads text.
func exampleFileBody2565(flag string) string {
	switch flag {
	case "--input-file":
		// decision amend refuses an empty input locally ("nothing to amend"),
		// so the provided file carries one field both ask and amend accept.
		return `{"title":"Pick a database"}`
	case "--patch-file":
		return `{"tier":"enterprise"}`
	case "--body-file":
		return "Notes for the reviewer."
	case "--unlock-passphrase-file":
		return "correct horse battery staple"
	default:
		return `{"allow":[]}`
	}
}

func collect2565Pages(t *testing.T, root *cobra.Command) []*cobra.Command {
	t.Helper()
	var pages []*cobra.Command
	for _, path := range pages2565 {
		c, _, err := root.Find(strings.Split(path, " "))
		if err != nil || c == nil || c.CommandPath() != "hadron "+path {
			t.Fatalf("page not found in the command tree: %s", path)
		}
		pages = append(pages, c)
	}
	return pages
}

// heredocFiles2565 extracts the input files an Example block writes with a
// heredoc: a line `cat > NAME <<'TAG'` opens one, and the TAG line alone
// closes it. The copyable input files the pages show (ask.json, amend.json)
// are exactly these, so the harness feeds each command the body its page
// actually documents — a canned body would prove nothing about that body
// (Bob's BLIND P2-2 on #2584), and this way the example's own file reaches
// the command. Structural validation of these bodies lives in
// Test2565HeredocInputsAreValidDecisionInput.
func heredocFiles2565(block string) map[string]string {
	files := map[string]string{}
	lines := strings.Split(block, "\n")
	for i := 0; i < len(lines); i++ {
		fields := strings.Fields(strings.TrimSpace(lines[i]))
		if len(fields) != 4 || fields[0] != "cat" || fields[1] != ">" || !strings.HasPrefix(fields[3], "<<") {
			continue
		}
		name, tag := fields[2], strings.Trim(fields[3], "<\"'")
		var body []string
		for i++; i < len(lines); i++ {
			if strings.TrimSpace(lines[i]) == tag {
				files[name] = strings.Join(body, "\n")
				break
			}
			body = append(body, lines[i])
		}
	}
	return files
}

// pipeIntoHadronLines2565 returns example lines that do not themselves start
// with "hadron" but pipe into one (echo "..." | hadron comment add ... --body
// -): the hadron segment is run, the way a reader's shell would run it, with
// the pipe feeding --body -.
func pipeIntoHadronLines2565(block string) []string {
	var cmds []string
	for _, raw := range strings.Split(block, "\n") {
		line := strings.TrimSpace(raw)
		if strings.HasPrefix(line, "hadron ") || !strings.Contains(line, " | hadron ") {
			continue
		}
		cmds = append(cmds, line)
	}
	return cmds
}

func Test2565HelpExamplesAreWired(t *testing.T) {
	fx := newWalkFixture(t)

	f0, _ := testFactory(t)
	pages := collect2565Pages(t, NewRootCmd(f0))
	if len(pages) != len(pages2565) {
		t.Fatalf("found %d of %d pages; the walk is wrong", len(pages), len(pages2565))
	}

	var results []exampleResult
	for _, page := range pages {
		path := strings.TrimPrefix(page.CommandPath(), "hadron ")
		heredocs := heredocFiles2565(page.Example)
		lines := append(exampleLines(page.Example), pipeIntoHadronLines2565(page.Example)...)
		for _, line := range lines {
			// A pipeline is run one hadron command at a time (the other tools, such as
			// jq or echo, are not ours); a trailing "> file" redirect is the shell's,
			// not the command's, so it is dropped and the command run as it would be.
			var segments []string
			hadPipe := len(strings.Split(line, " | ")) > 1
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
				if hadPipe {
					res.Mode = "fixture (the hadron command of a pipeline; the pipe feeds its stdin)"
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
				// Every <...> span, short or prose, is lifted out before the rest of the
				// line is judged, so a placeholder is never mistaken for a redirect.
				spansReplaced := ex
				for {
					i := strings.Index(spansReplaced, "<")
					j := strings.Index(spansReplaced, ">")
					if i < 0 || j < i {
						break
					}
					spansReplaced = spansReplaced[:i] + "x" + spansReplaced[j+1:]
				}
				if strings.ContainsAny(spansReplaced, "|;&><") || strings.Contains(ex, "$(") {
					res.Mode, res.Outcome = "not run", "a shell construct (substitution, redirect or list) the harness does not run"
					results = append(results, res)
					continue
				}
				if strings.ContainsAny(noPlaceholders, "<>") {
					res.Mode, res.Outcome = "not run", "a prose placeholder the harness cannot invent (a value an earlier step prints); the page's other examples exercise the wiring"
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
						w = filepath.Base(w) // redirected into the test directory: the command runs with cwd there, so the bare name lands inside it (and the file-providing loop below writes dir/NAME without doubling the prefix)
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
				// A path the harness does not redirect must not run: an absolute path outside
				// the test directory, a home path or a parent path, wherever it sits — a
				// bare word, a --flag=value, a -f=value or an @file.
				if unsafe := unsafeExamplePath1053(args, dir); unsafe != "" {
					t.Errorf("example %q names the path %q, which the harness does not redirect, so it was not run", ex, unsafe)
					results = append(results, exampleResult{Page: path, Example: ex, Mode: "not run", Outcome: "BROKEN: names a path the harness does not redirect: " + unsafe, Broken: true})
					continue
				}
				for _, a := range args {
					if strings.HasPrefix(a, "@") && len(a) > 1 {
						if err := os.MkdirAll(filepath.Dir(filepath.Join(dir, a[1:])), 0o755); err != nil {
							t.Fatal(err)
						}
						if err := os.WriteFile(filepath.Join(dir, a[1:]), []byte(`{"team":{"x":1}}`), 0o600); err != nil {
							t.Fatal(err)
						}
					}
				}
				for i, a := range args {
					file, flag := "", ""
					switch {
					case i > 0 && fileArgFlags2565[args[i-1]]:
						file, flag = a, args[i-1]
					case strings.HasPrefix(a, "--") && strings.Contains(a, "=") && fileArgFlags2565[a[:strings.Index(a, "=")]]:
						flag = a[:strings.Index(a, "=")]
						file = a[strings.Index(a, "=")+1:]
					}
					if file != "" {
						// The file the page itself writes with a heredoc, when it
						// wrote one, is the body a reader following the page would
						// send; the canned body stands in only for a file the page
						// never shows.
						body, ok := heredocs[filepath.Base(file)]
						if !ok {
							body = exampleFileBody2565(flag)
						}
						if err := os.MkdirAll(filepath.Dir(filepath.Join(dir, file)), 0o755); err != nil {
							t.Fatal(err)
						}
						if err := os.WriteFile(filepath.Join(dir, file), []byte(body), 0o600); err != nil {
							t.Fatal(err)
						}
					}
				}
				t.Run(path+" | "+ex, func(t *testing.T) {
					t.Chdir(dir)
					f, _ := testFactory(t)
					setActiveApp(t, "hrn:app:acme.com:eng-team")
					f.IOStreams.In = strings.NewReader(`{"team":{"x":1}}`) // an example that reads "-" gets a JSON document
					before := fx.mark()
					root := NewRootCmd(f)
					root.SetArgs(append(args, "--server", fx.srv.URL))
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
					sent := fx.since(before)
					if panicked != "" {
						// A command that panics on a reply the server never sends (the fixture
						// answers with an empty data object) is a defect of the command, not of
						// its example: recorded as a finding, tracked by knownPanics2565.
						res.Outcome = "PANIC on an empty fixture reply: " + panicked
						known := false
						if _, ok := matchKnownPanic(knownPanics2565, resolvedPath(root, args), panicked); ok {
							known = true
						}
						if !known {
							res.Broken = true
							t.Errorf("%s panicked: %s", ex, panicked)
						}
						results = append(results, res)
						return
					}
					isLocalOnly := false
					for _, prefix := range localOnly2565 {
						isLocalOnly = isLocalOnly || strings.HasPrefix(ex+" ", prefix)
					}
					switch {
					case err == nil && len(sent) == 0 && isLocalOnly:
						res.Outcome = "ran, no request needed"
					case err == nil && len(sent) == 0:
						res.Broken = true
						res.Outcome = "BROKEN: ran without sending any request, and it is not an allowlisted local command"
					case err == nil:
						res.Outcome = "ran, sent " + strings.Join(sent, ", ")
					default:
						msg := strings.SplitN(err.Error(), "\n", 2)[0]
						for _, m := range brokenMarkers {
							if strings.Contains(msg, m) {
								res.Broken = true
							}
						}
						_, expected := expectedRefusals2565[ex]
						switch {
						case res.Broken:
							res.Outcome = "BROKEN: " + msg
						case len(sent) > 0 && exitCodeFor(err) == exitcode.Usage:
							res.Broken = true
							res.Outcome = "BROKEN: a usage error after a request was sent: " + msg
						case len(sent) > 0:
							// The fixture's empty reply makes the command fail AFTER it reached the
							// server; that is the wiring being verified.
							res.Outcome = fmt.Sprintf("reached the server (%s); then: %s", strings.Join(sent, ", "), msg)
						case isConfirmRefusal(err):
							res.Outcome = "asks for confirmation (refused without a terminal, as documented)"
						case expected:
							res.Outcome = "refused before any request, as the example documents: " + msg
						default:
							res.Broken = true
							res.Outcome = "BROKEN: refused locally before any request: " + msg + " (allowlist it in expectedRefusals2565 only if the help documents it)"
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

	if path := os.Getenv("HELP_EXAMPLES_REPORT_2565"); path != "" {
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
	executed := 0
	for _, r := range results {
		if strings.HasPrefix(r.Mode, "fixture") {
			executed++
		}
	}
	if executed < min2565Examples {
		t.Fatalf("only %d of %d examples executed across the 25 pages; the walk is wrong", executed, len(results))
	}
}

// Test2565HeredocInputsAreValidQuestionInput pins the copyable input files the
// question pages show (cli#2565 F5/F11, cli#2465). The fixture harness proves the
// commands are wired, but the fixture answers every mutation with empty data, so
// server-side validation never runs there: this test decodes the bodies strictly
// into the generated input structs (an unknown key, such as an old `parts`, fails).
func Test2565HeredocInputsAreValidQuestionInput(t *testing.T) {
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	for _, path := range []string{"question ask", "question amend"} {
		c, _, err := root.Find(strings.Split(path, " "))
		if err != nil || c == nil || c.CommandPath() != "hadron "+path {
			t.Fatalf("page not found in the command tree: %s", path)
		}
		files := heredocFiles2565(c.Example)
		if len(files) == 0 {
			t.Fatalf("%s shows no heredoc input file; F5/F11 require a copyable one", path)
		}
		for name, body := range files {
			dec := json.NewDecoder(strings.NewReader(body))
			dec.DisallowUnknownFields()
			if path == "question ask" {
				var in gen.AskQuestionInput
				if err := dec.Decode(&in); err != nil {
					t.Errorf("%s: heredoc %s is not a valid AskQuestionInput: %v (body %q)", path, name, err, body)
				} else if in.Title == "" {
					t.Errorf("%s: heredoc %s lacks the required title", path, name)
				}
				continue
			}
			var in gen.AmendQuestionInput
			if err := dec.Decode(&in); err != nil {
				t.Errorf("%s: heredoc %s is not a valid AmendQuestionInput: %v (body %q)", path, name, err, body)
			}
		}
	}
}
