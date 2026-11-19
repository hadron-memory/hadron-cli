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

	"github.com/hadron-memory/hadron-cli/internal/exitcode"
)

// cli#1054 slice A (team host, team router, team ticker): every example in the
// help of these 26 pages is run through the real command tree against a local
// fixture server — the slice-1 method (cli#1053, gate amendment r2) carried to
// the host/router/ticker families. The fixture answers every GraphQL operation
// with an empty data object, so this verifies that each example PARSES and is
// WIRED, not what it prints. The dispatch's caution ("team host is destructive
// and outward, so be careful") is honored by two slice-A rules:
//
//   - `sweep install` and `sweep uninstall` change the HOST's service manager
//     (systemctl --user; internal/healthsched/install.go) — they are
//     parse-checked and never executed.
//   - everything else may only reach the loopback fixture (testFactory's
//     client refuses any other destination) and the per-example temporary
//     directory; file-taking flags of this slice get documents that pass the
//     real parsers (hostpoller.LoadConfig's full validation, host_up.go's
//     strict desired-state decoder).

const (
	minSliceAPages    = 26
	minSliceAExamples = 20
)

// minExamplesPerPageSliceA pins the round-1 finding that these pages carried
// no Example at all: an operator's first copy-paste from the page must exist
// and be wired. On main (3b858969) both counts are zero, which is the red
// state this harness is committed against.
var minExamplesPerPageSliceA = map[string]int{
	"team host run":        1,
	"team host sweep-runs": 1,
}

// notRunSliceA are examples the harness must not execute: they mutate the
// host's own service manager. Each is parse-checked instead — the command
// path exists and every flag parses — which is the wiring claim the harness
// can honestly make about them.
var notRunSliceA = map[string]string{
	"hadron team host sweep install ":   "installs a systemd --user timer on this host (healthsched/install.go); parse-checked, not executed",
	"hadron team host sweep uninstall ": "disables and removes a systemd --user timer on this host (healthsched/install.go); parse-checked, not executed",
	"hadron team host run ":             "the poller: launches real worker processes from the host.yaml launch lines; parse-checked, not executed",
}

// localOnlySliceA are commands that legitimately make no request: their work
// is local by design and their help says so (sweep status: "Does not read
// server history"; report-stops posts the stop records it finds under the
// host's state directory, and the harness's empty host has none — "--dry-run
// posts nothing", and with no records neither does a real run).
var localOnlySliceA = []string{
	"hadron team host sweep status ",
	"hadron team host report-stops ",
}

// expectedRefusalsSliceA are examples whose documented outcome IS a refusal
// before any request. Empty unless a run shows one; the reason names where
// the help documents it.
var expectedRefusalsSliceA = map[string]string{}

// knownPanicsSliceA lists commands that panic on a reply with no data, each
// tracked by a follow-up issue (a panic is a defect of the command, not of
// the help): `team host issue` dereferences the returned operation without a
// nil check (host.go, gen.IssueHostOperation), the same class as slice 1's
// `app key scope grant` (#2492).
var knownPanicsSliceA = map[string]string{
	"team host issue": "#2572",
}

// sliceAFileFlags are the file-taking flags of this slice, with the fixture
// document each gets. A host.yaml must pass LoadConfig's full validation
// (app, absolute worktrees_root, one worker with a family and a launch), or
// the example would fail on the fixture, not the wiring; the test directory
// is templated in at write time.
func sliceAFileContent(flag, dir string) string {
	switch flag {
	case "--from", "--config":
		// key_file: availability (and any host-key read) refuses a config
		// without one; the key file itself is written beside it, mode 0600.
		return "app: app1\nkey_file: " + filepath.Join(dir, "host.key") + "\nworktrees_root: " + dir + "\nworkers:\n  iris:\n    family: claude\n    launch: [claude]\n"
	case "--desired-state-file":
		return `{"hostId":"orca1","workers":[{"workerId":"w1","name":"Iris"}]}`
	default:
		return `{"allow":[]}`
	}
}

var sliceAFileFlags = []string{"--from", "--config", "--desired-state-file", "--policy-file", "--payload-file", "--override-file"}

func collectSliceAPages(root *cobra.Command) []*cobra.Command {
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
	for _, name := range []string{"host", "router", "ticker"} {
		if c, _, err := root.Find([]string{"team", name}); err == nil && c != root {
			walk(c)
		}
	}
	return pages
}

// parseCheckSliceA verifies the wiring the harness can honestly check without
// executing: the command path resolves and every flag parses against it.
// Required-flag and positional-argument rules are the command's, not the
// parser's, and are not claimed here.
func parseCheckSliceA(root *cobra.Command, args []string) error {
	cmd, flags, err := root.Find(args[1:])
	if err != nil {
		return err
	}
	if cmd == nil || cmd == root {
		return fmt.Errorf("example does not name a command under team")
	}
	return cmd.ParseFlags(flags)
}

func TestSliceAHelpExamplesAreWired1054(t *testing.T) {
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
	pages := collectSliceAPages(NewRootCmd(f0))
	if len(pages) < minSliceAPages {
		t.Fatalf("found only %d team host/router/ticker pages; the walk is wrong", len(pages))
	}

	var results []exampleResult
	for _, page := range pages {
		path := strings.TrimPrefix(page.CommandPath(), "hadron ")
		for _, line := range exampleLines(page.Example) {
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
				// Slice-A rule: a command that mutates the host's service
				// manager is parse-checked, never run.
				for prefix, reason := range notRunSliceA {
					if strings.HasPrefix(ex+" ", prefix) {
						res.Mode, res.Outcome = "parse-check only (not run)", reason
						words, err := splitShell(ex)
						if err != nil {
							res.Broken, res.Outcome = true, fmt.Sprintf("example does not split into words: %v", err)
						} else if perr := parseCheckSliceA(NewRootCmd(f0), words); perr != nil {
							res.Broken, res.Outcome = true, "BROKEN: does not parse: "+perr.Error()
						} else {
							res.Outcome += "; command path and flags parse"
						}
						if res.Broken {
							t.Errorf("%s: %s", ex, res.Outcome)
						}
						results = append(results, res)
						break
					}
				}
				if res.Outcome != "" {
					continue
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
						w = "fixture-" + strings.Trim(w, "<>")
					case strings.HasPrefix(w, "$") && len(w) > 1 && strings.Trim(w[1:], "ABCDEFGHIJKLMNOPQRSTUVWXYZ_") == "":
						if strings.Contains(w, "REVISION") {
							w = "3"
						} else {
							w = "fixture-" + strings.ToLower(w[1:])
						}
					}
					args = append(args, w)
				}
				if unsafe := unsafeExamplePath1053(args, dir); unsafe != "" {
					t.Errorf("example %q names the path %q, which the harness does not redirect, so it was not run", ex, unsafe)
					results = append(results, exampleResult{Page: path, Example: ex, Mode: "not run", Outcome: "BROKEN: names a path the harness does not redirect: " + unsafe, Broken: true})
					continue
				}
				for _, a := range args {
					if strings.HasPrefix(a, "@") && len(a) > 1 {
						if err := os.WriteFile(filepath.Join(dir, a[1:]), []byte(`{"team":{"x":1}}`), 0o600); err != nil {
							t.Fatal(err)
						}
					}
				}
				for i, a := range args {
					file, flag := "", ""
					if i > 0 {
						for _, fl := range sliceAFileFlags {
							if args[i-1] == fl {
								file, flag = a, fl
							}
						}
					}
					if strings.HasPrefix(a, "--") {
						if eq := strings.Index(a, "="); eq > 0 {
							for _, fl := range sliceAFileFlags {
								if strings.HasPrefix(a, fl+"=") {
									file, flag = a[eq+1:], fl
								}
							}
						}
					}
					if file != "" {
						if err := os.WriteFile(filepath.Join(dir, file), []byte(sliceAFileContent(flag, dir)), 0o600); err != nil {
							t.Fatal(err)
						}
						if flag == "--config" || flag == "--from" {
							// The host key the fixture config names (mode 0600,
							// as ReadHostKey requires).
							if err := os.WriteFile(filepath.Join(dir, "host.key"), []byte("hdr_host_fixture"), 0o600); err != nil {
								t.Fatal(err)
							}
						}
					}
				}
				t.Run(path+" | "+ex, func(t *testing.T) {
					t.Chdir(dir)
					f, _ := testFactory(t)
					setActiveApp(t, "hrn:app:acme.com:eng-team")
					f.IOStreams.In = strings.NewReader(`{"team":{"x":1}}`)
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
					if panicked != "" {
						res.Outcome = "PANIC on an empty fixture reply: " + panicked
						known := false
						for cmdPath := range knownPanicsSliceA {
							known = known || strings.Contains(ex, "hadron "+cmdPath+" ")
						}
						if !known {
							res.Broken = true
							t.Errorf("%s panicked: %s", ex, panicked)
						}
						results = append(results, res)
						return
					}
					isLocalOnly := false
					for _, prefix := range localOnlySliceA {
						isLocalOnly = isLocalOnly || strings.HasPrefix(ex+" ", prefix)
					}
					switch {
					case err == nil && len(sent) == 0 && isLocalOnly:
						res.Outcome = "ran, no request needed (local by design)"
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
						_, expected := expectedRefusalsSliceA[ex]
						switch {
						case res.Broken:
							res.Outcome = "BROKEN: " + msg
						case len(sent) > 0 && exitCodeFor(err) == exitcode.Usage:
							res.Broken = true
							res.Outcome = "BROKEN: a usage error after a request was sent: " + msg
						case len(sent) > 0:
							res.Outcome = fmt.Sprintf("reached the server (%s); then: %s", strings.Join(sent, ", "), msg)
						case strings.Contains(msg, "without --yes"):
							res.Outcome = "asks for confirmation (refused without a terminal, as documented)"
						case expected:
							res.Outcome = "refused before any request, as the example documents: " + msg + " (" + expectedRefusalsSliceA[ex] + ")"
						default:
							res.Broken = true
							res.Outcome = "BROKEN: refused locally before any request: " + msg + " (allowlist it in expectedRefusalsSliceA only if the help documents it)"
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

	if path := os.Getenv("HELP_EXAMPLES_SLICE_A_REPORT"); path != "" {
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
	if len(results) < minSliceAExamples {
		t.Fatalf("only %d examples found across the team host/router/ticker pages", len(results))
	}
	perPage := map[string]int{}
	for _, r := range results {
		perPage[r.Page]++
	}
	for page, want := range minExamplesPerPageSliceA {
		if perPage[page] < want {
			t.Errorf("%s carries %d wired examples, want at least %d (round-1 finding: no Example on the page)", page, perPage[page], want)
		}
	}
}
