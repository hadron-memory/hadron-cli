package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/hadron-memory/hadron-cli/internal/exitcode"
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

// minSlice1Pages and minSlice1Examples are floors, not targets: they only guard
// against a walk or a parser that silently finds almost nothing.
const (
	minSlice1Pages    = 80
	minSlice1Examples = 100
)

// localOnly1053 are commands that legitimately make no request.
var localOnly1053 = []string{"hadron app set-active "}

// expectedRefusals1053 are examples whose documented outcome IS a refusal before
// any request (an example that shows a refusal on purpose). Empty: every other
// local refusal is a failure, so a broken example cannot hide behind one.
var expectedRefusals1053 = map[string]string{}

// knownPanics1053 lists the commands (by path) that panic on a reply with no data,
// each tracked by a follow-up issue (a panic is a defect of the command, not of
// the help).
var knownPanics1053 = map[string]knownPanic{}

type exampleResult struct {
	Page, Example, Mode, Outcome string
	Broken                       bool
}

// unsafeExamplePath1053 returns the first argument whose value names a path
// the harness does not redirect into the test directory: absolute and outside
// it (a prefix match on the directory itself does not count — a SIBLING
// directory shares the prefix), a home path, or a parent path. Values are
// read from the argument shapes examplePathCandidates1053 names (a bare word, after
// --flag= or -f=, a glued short-flag value or cluster, after an @file's @, in
// key=value at any depth, in a comma list) and cleaned first, so "a/.." and "./.." are judged as the
// parent paths they resolve to, not as strings unlike "../".
//
// The check is LEXICAL, and that is enough (cli#2813, decided): each example runs in its own
// empty throwaway directory that nothing seeds with a symlink, so no path under it can leave
// it by a link. TestNoProductionCodeCreatesASymlink2813 pins the one premise a change could
// break (a command that creates symlinks) and says what a resolving guard would need.
func unsafeExamplePath1053(args []string, dir string) string {
	dir = filepath.Clean(dir)
	for _, a := range args {
		for _, v := range examplePathCandidates1053(a) {
			p := filepath.Clean(v)
			if strings.HasPrefix(v, "~") ||
				(filepath.IsAbs(p) && p != dir && !strings.HasPrefix(p, dir+string(filepath.Separator))) ||
				p == ".." || strings.HasPrefix(p, ".."+string(filepath.Separator)) {
				return v
			}
		}
	}
	return ""
}

// examplePathCandidates1053 lists the values an example argument could make a command read
// or write, for the argument shapes it understands (cli#2525 F6, cli#2849 F18): the argument
// itself, the part after a long flag's "=", the glued value of a short flag or of a cluster
// of them ("-o/etc/x", "-vo/etc/x": the value may start where a path does, so the text from
// the first "/", "~" or "." after the flag letters is a candidate too), each item of a comma list, and the part after
// EVERY "=" inside an item ("key=/etc/x", "a=b=/etc/x"), each without a leading "@". It is
// a lexical reading of the shapes the examples use, not a parse of any command's flags: a
// value in a shape this does not read (say a path inside a quoted JSON string) is not listed.
func examplePathCandidates1053(a string) []string {
	vals := []string{a}
	if strings.HasPrefix(a, "--") {
		if i := strings.Index(a, "="); i >= 0 {
			vals = []string{a[i+1:]}
		}
	} else if strings.HasPrefix(a, "-") && len(a) > 2 {
		// -o=value, -ovalue, or a cluster such as -vovalue: the text after the first flag
		// letter, and, where a path starts later in the cluster (the first "/", "~" or "."
		// after the flag letters: "-vo/etc/x" is -v -o /etc/x), the text from there. An "="
		// that joins the flag to its value is dropped.
		vals = []string{strings.TrimPrefix(a[2:], "=")}
		if k := strings.IndexAny(a[2:], "/~."); k > 0 {
			vals = append(vals, a[2+k:])
		}
	} else if strings.HasPrefix(a, "-") {
		return nil
	}
	var out []string
	for _, v := range vals {
		for _, item := range strings.Split(v, ",") {
			item = strings.TrimPrefix(item, "@")
			out = append(out, item)
			// key=value inside an item, at every "=": a=b=/etc/x offers b=/etc/x and /etc/x.
			for j := 0; j < len(item); j++ {
				if item[j] == '=' {
					out = append(out, strings.TrimPrefix(item[j+1:], "@"))
				}
			}
		}
	}
	return out
}

// The guard itself is pinned (Jonas's BLIND delta N4 on #2499): @values,
// -o=, a sibling directory that shares the test directory's prefix, and a
// trailing /.. all escaped the string matching it replaced.
func TestUnsafeExamplePathGuard1053(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "run")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	sibling := dir + "-sibling"
	safe := []string{"notes.txt", "a/b.json", "a/..", "@data.json", "--file=spec.json", "-o=out.json", dir + "/x", dir,
		"-oout.json", "-n5", "--set=key=value", "a.json,b.json", "--files=a.json,b/c.json", "k=v", dir + "/../" + filepath.Base(dir) + "/x",
		"-vn5", "-vvoout.json", "a=b=c", "--set=a=b=c", "-vo" + dir + "/x"}
	for _, a := range safe {
		if got := unsafeExamplePath1053([]string{"hadron", "app", "x", a}, dir); got != "" {
			t.Errorf("%q judged unsafe, want safe (reported %q)", a, got)
		}
	}
	unsafe := []string{
		"/etc/passwd", "~/notes", "~root/x", "..", "../sibling", "./..", "a/../..",
		"@/etc/passwd", "@/~/x",
		"--payload-file=/etc/secret", "-o=/home/me/keys",
		sibling + "/f",
		// cli#2525 F6: a short-flag value glued to its flag, a key=path inside a value,
		// and a comma list that carries a path.
		"-o/etc/x", "-o../x", "-o~/x", "--set=key=/etc/x", "--meta=k=../x", "a.json,/etc/x", "--files=a.json,../b.json",
		// cli#2849 F18: short flags clustered before a glued value (-v -o/etc/x as -vo/etc/x),
		// and a value nested in more than one key=value (a=b=/etc/x).
		"/tmp/x.json", "--input-file=/tmp/x.json", "-vo/etc/x", "-vo../x", "-vo~/x", "-vvo/etc/x", "-vo=/etc/x", "a=b=/etc/x", "--set=a=b=/etc/x", "--set=a=b=../x", "x,a=b=/etc/x",
	}
	for _, a := range unsafe {
		if got := unsafeExamplePath1053([]string{"hadron", "app", "x", a}, dir); got == "" {
			t.Errorf("%q judged safe, want unsafe", a)
		}
	}
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
	fx := newWalkFixture(t)

	f0, _ := testFactory(t)
	pages := collectSlice1Pages(NewRootCmd(f0))
	if len(pages) < minSlice1Pages {
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
						w = filepath.Join(dir, filepath.Base(w)) // redirected into the test directory
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
				// bare word, a --flag=value, a -f=value or an @file. The example fails
				// here instead of touching the filesystem.
				if unsafe := unsafeExamplePath1053(args, dir); unsafe != "" {
					t.Errorf("example %q names the path %q, which the harness does not redirect, so it was not run", ex, unsafe)
					results = append(results, exampleResult{Page: path, Example: ex, Mode: "not run", Outcome: "BROKEN: names a path the harness does not redirect: " + unsafe, Broken: true})
					continue
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
					file := ""
					switch {
					case i > 0 && (args[i-1] == "--policy-file" || args[i-1] == "--payload-file" || args[i-1] == "--override-file" || args[i-1] == "--body-file"):
						file = a
					case strings.HasPrefix(a, "--policy-file=") || strings.HasPrefix(a, "--payload-file=") || strings.HasPrefix(a, "--override-file=") || strings.HasPrefix(a, "--body-file="):
						file = a[strings.Index(a, "=")+1:]
					}
					if file != "" {
						if err := os.WriteFile(filepath.Join(dir, file), []byte(`{"allow":[]}`), 0o600); err != nil {
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
						// its example: recorded as a finding, tracked by knownPanics1053.
						res.Outcome = "PANIC on an empty fixture reply: " + panicked
						known := false
						if _, ok := matchKnownPanic(knownPanics1053, resolvedPath(root, args), panicked); ok {
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
					for _, prefix := range localOnly1053 {
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
						_, expected := expectedRefusals1053[ex]
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
							res.Outcome = "BROKEN: refused locally before any request: " + msg + " (allowlist it in expectedRefusals1053 only if the help documents it)"
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
	if len(results) < minSlice1Examples {
		t.Fatalf("only %d examples found across the app and agent pages; expected the ~119 the inventory implies", len(results))
	}
}
