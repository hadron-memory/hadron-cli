package cmd

import (
	"encoding/json"
	"fmt"
	"github.com/hadron-memory/hadron-cli/internal/hostpoller/hostyaml"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	"github.com/hadron-memory/hadron-cli/internal/cmd/agentic"
	"github.com/hadron-memory/hadron-cli/internal/config"
	"github.com/hadron-memory/hadron-cli/internal/exitcode"
)

// cli#1054 slice A (team host, team router, team ticker): every example in the
// help of these 26 pages is run through the real command tree against a local
// fixture server — the slice-1 method (cli#1053, gate amendment r2) carried to
// the host/router/ticker families. The fixture answers every GraphQL operation
// with an empty data object, so this verifies that each example PARSES and is
// WIRED, not what it prints. The dispatch's caution ("team host is destructive
// and outward, so be careful") is honored by three slice-A rules:
//
//   - the harness runs SANDBOXED (sandboxSliceA): fake orca/systemctl/
//     launchctl first on PATH, HOME, XDG_STATE_HOME and XDG_CONFIG_HOME in
//     temp dirs, and a
//     before/after assertion that the real host trees are untouched — a host
//     example can never reach this Orca host's real CLI or state (#2578).
//   - `sweep install`, `sweep uninstall` and `team host run` change the HOST
//     (the service manager; the poller launches real worker processes) —
//     they are parse-checked and never executed, so "wired" for them means
//     only that the command path and flags parse.
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
// and parse. `team host run` is parse-checked only (notRunSliceA), so its one
// example counts as parsing, not as having run.
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
// before any request. The reason names where the help documents it. The two
// `team host up` examples shell out to the Orca CLI (--orca-bin, default
// `orca`): the sandbox's stub orca (sandboxSliceA) refuses on EVERY host, so
// the command refuses before any request exactly as the page's exit-code
// list documents ("2 usage, or Orca is not reachable here") — on CI and on
// an Orca host alike, never reaching the real Orca CLI (Jonas's BLIND P2s on
// #2578, Wolfgang's stop, hadron-cli
// findings:host-command-help-harness-must-sandbox).
var expectedRefusalsSliceA = map[string]string{
	"hadron team host up --app acme.com:eng --host orca1 --worktrees-root /home/orca/worktrees":                                                         "Orca-hosted workers only (exit 2 before any request), documented in the page's exit-code list: 'Orca is not reachable here'",
	"hadron team host up --app acme.com:eng --host orca1 --worktrees-root /home/orca/worktrees --apply":                                                 "Orca-hosted workers only (exit 2 before any request), documented in the page's exit-code list: 'Orca is not reachable here'",
	"hadron team host sweep --config /etc/hadron/host.yaml":                                                                                             "refuses before any request when the host has no saved registration; the page documents it in its exit codes (5: no saved registration, start the host poller first)",
	"hadron team host up --app acme.com:eng --host orca1 --worktrees-root /home/orca/worktrees --desired-state-file desired.json":                       "Orca-hosted workers only (exit 2 before any request), documented in the page's exit-code list: 'Orca is not reachable here'",
	"hadron team host up --app acme.com:eng --host orca1 --worktrees-root /home/orca/worktrees --desired-state-file desired.json --worker Iris --apply": "Orca-hosted workers only (exit 2 before any request), documented in the page's exit-code list: 'Orca is not reachable here'",
}

// knownPanicsSliceA lists commands that panic on a reply with no data, each
// tracked by a follow-up issue (a panic is a defect of the command, not of
// the help). It is empty: `team host issue` (#2572) and `team host ops` (#3077)
// were its entries and no longer panic. An entry that stops panicking fails the
// harness as stale (cli#2617), so a fixed panic cannot stay allowlisted.
var knownPanicsSliceA = map[string]string{}

// sliceAFileContent is the fixture document a file-taking flag of this slice
// gets. A host.yaml must pass LoadConfig's full validation (app, absolute
// worktrees_root, one worker with a family and a launch), or the example would
// fail on the fixture, not the wiring; the test directory is templated in at
// write time.
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

// sliceAFileFlags are the file-taking flags of this slice. Each must be a real
// flag of a team host/router/ticker page (TestSliceAFileFlagsAreRealFlags2617);
// the slice-1 flags --policy-file, --payload-file and --override-file are not.
var sliceAFileFlags = []string{"--from", "--config", "--desired-state-file"}

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

// sandboxSliceA seals the environment the examples run in (Jonas's BLIND P2s
// 2 and 3 on #2578; Wolfgang's stop; hadron-cli
// findings:host-command-help-harness-must-sandbox): fake orca, systemctl and
// launchctl FIRST on PATH, and HOME plus XDG_STATE_HOME inside a temp dir.
// The stubs log each invocation beside themselves and exit 3, so `team host
// up` takes its documented "Orca is not reachable here" refusal on EVERY
// host — the real Orca CLI of an Orca host is never reached — and a
// systemctl or launchctl invocation (those examples are parse-checked, not
// run) is a harness defect the caller asserts on. Returns the stub directory.
func sandboxSliceA(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_STATE_HOME", filepath.Join(home, ".local", "state"))
	// config.Dir() honours $XDG_CONFIG_HOME before HOME (internal/config/
	// paths.go), so a host or CI runner that exports it would otherwise
	// still reach the REAL hadron config dir (Jonas's BLIND delta P2 on
	// #2578: unset on orca, set elsewhere). Seal it into the temp home too.
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	stubDir := t.TempDir()
	const stub = "#!/bin/sh\nprintf '%s\\n' \"$*\" >> \"$0.log\"\nexit 3\n"
	for _, name := range []string{"orca", "systemctl", "launchctl"} {
		if err := os.WriteFile(filepath.Join(stubDir, name), []byte(stub), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", stubDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	return stubDir
}

// realHostRootsSliceA names the real (pre-sandbox) trees an unsandboxed host
// command would write: the hadron config dir (hostup locks, saved
// registrations) and the user service-manager unit dirs (systemd user units,
// launchd agents). Resolve them BEFORE sandboxSliceA overrides any env —
// re-resolving afterwards would point at the sandbox and read as "removed".
func realHostRootsSliceA() []string {
	var roots []string
	if d, err := config.Dir(); err == nil {
		roots = append(roots, d)
	}
	if home, err := os.UserHomeDir(); err == nil {
		roots = append(roots,
			filepath.Join(home, ".config", "systemd", "user"),
			filepath.Join(home, "Library", "LaunchAgents"),
		)
	}
	return roots
}

// snapshotRealHostStateSliceA records path -> size:mtime for one root set;
// assertRealHostUntouchedSliceA compares the before/after snapshots. On a
// shared host another worker's action can change these trees mid-run; the
// assertion lists the exact paths, so a trip names what changed and a person
// judges it — silently trusting the env redirects is what #2578's blind
// review caught.
func snapshotRealHostStateSliceA(roots ...string) map[string]string {
	snap := map[string]string{}
	for _, root := range roots {
		_ = filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
			if err != nil {
				return nil // absent or unreadable: nothing to compare
			}
			if info, err := d.Info(); err == nil {
				snap[p] = fmt.Sprintf("%d:%d", info.Size(), info.ModTime().UnixNano())
			} else {
				snap[p] = "dir"
			}
			return nil
		})
	}
	return snap
}

func assertRealHostUntouchedSliceA(t *testing.T, roots []string, before map[string]string) {
	t.Helper()
	after := snapshotRealHostStateSliceA(roots...)
	var changed []string
	for p, v := range after {
		if bv, ok := before[p]; !ok {
			changed = append(changed, p+" (created)")
		} else if bv != v {
			changed = append(changed, p+" (modified)")
		}
	}
	for p := range before {
		if _, ok := after[p]; !ok {
			changed = append(changed, p+" (removed)")
		}
	}
	if len(changed) > 0 {
		sort.Strings(changed)
		t.Errorf("the harness touched the real host outside its temp dirs:\n  %s", strings.Join(changed, "\n  "))
	}
}

// assertHostToolStubsNeverRanSliceA fails when a service-manager stub was
// invoked: the examples that would exec systemctl or launchctl are
// parse-checked (notRunSliceA), so any invocation means a harness change
// started driving the (stubbed, not real) service manager — re-check the
// sandbox before letting it run.
func assertHostToolStubsNeverRanSliceA(t *testing.T, stubDir string) {
	t.Helper()
	for _, name := range []string{"systemctl", "launchctl"} {
		if _, err := os.Stat(filepath.Join(stubDir, name+".log")); err == nil {
			t.Errorf("the harness invoked the %s stub; service-manager examples must stay parse-checked", name)
		}
	}
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

	// Seal before any example runs (Jonas's BLIND P2s 2/3 on #2578): fake
	// host tools on PATH, HOME, XDG_STATE_HOME and XDG_CONFIG_HOME in temp
	// dirs, and a
	// before/after snapshot of the real trees an unsandboxed host command
	// would write.
	realRoots := realHostRootsSliceA()
	realBefore := snapshotRealHostStateSliceA(realRoots...)
	stubDir := sandboxSliceA(t)

	f0, _ := testFactory(t)
	pages := collectSliceAPages(NewRootCmd(f0))
	if len(pages) < minSliceAPages {
		t.Fatalf("found only %d team host/router/ticker pages; the walk is wrong", len(pages))
	}

	// used records which allowlist entries an example actually exercised, so an
	// entry that nothing reaches (a deleted example, a fixed panic) fails the
	// harness instead of lingering as a silent exemption (cli#2617 P3-2/F3).
	used := map[string]bool{}
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
						used["notRun "+prefix] = true
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
						// An absolute value is the already-redirected path (the
						// /etc/, /home/, /var/ rewrite above); joining the test
						// dir onto it would nest dirs and miss the file.
						p := file
						if !filepath.IsAbs(p) {
							p = filepath.Join(dir, p)
						}
						if err := os.WriteFile(p, []byte(hostyaml.Supervised(sliceAFileContent(flag, dir))), 0o600); err != nil {
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
							if strings.Contains(ex, "hadron "+cmdPath+" ") {
								known = true
								used["panic "+cmdPath] = true
							}
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
						if strings.HasPrefix(ex+" ", prefix) {
							isLocalOnly = true
							if err == nil && len(sent) == 0 {
								used["localOnly "+prefix] = true
							}
						}
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
							used["refusal "+ex] = true
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
		if err := os.WriteFile(path, []byte(hostyaml.Supervised(b.String())), 0o600); err != nil {
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

	// Every allowlist entry was exercised: an entry nothing reaches is a stale
	// exemption (a deleted example, a panic that was fixed) and must go.
	var declared []string
	for k := range notRunSliceA {
		declared = append(declared, "notRun "+k)
	}
	for _, k := range localOnlySliceA {
		declared = append(declared, "localOnly "+k)
	}
	for k := range expectedRefusalsSliceA {
		declared = append(declared, "refusal "+k)
	}
	for k := range knownPanicsSliceA {
		declared = append(declared, "panic "+k)
	}
	for _, stale := range unusedAllowlistEntries(declared, used) {
		t.Errorf("stale allowlist entry %q: no example exercises it any more; remove it", stale)
	}

	// The sandbox held: nothing outside the temp dirs changed, and no
	// service-manager stub was ever invoked.
	assertRealHostUntouchedSliceA(t, realRoots, realBefore)
	assertHostToolStubsNeverRanSliceA(t, stubDir)
}

// agenticUsageSection1054 returns one "## <name>" section of agentic-usage,
// from its header to the next header or the end of the document.
func agenticUsageSection1054(t *testing.T, name string) string {
	t.Helper()
	doc := agentic.Doc()
	i := strings.Index(doc, "## "+name)
	if i < 0 {
		t.Fatalf("agentic-usage has no section %q", name)
	}
	sec := doc[i:]
	if j := strings.Index(sec, "\n## "); j >= 0 {
		sec = sec[:j+1]
	}
	return sec
}

// The three host writes (assign, unassign, setup) share hostSetupWriteHelp,
// which points the reader at agentic-usage's "App-manager writes with an
// instruction" for the instruction forms, the server floor they need and
// the refusal codes. That section must name the trio and give them the flag's
// base floor their --instruction usage names (2026-11-03) — not only the
// dated minimum the cli#2448 six need (QA F2 on #2578: the section said
// "exactly six" with one 2026-11-04 minimum, so the pointer landed the reader
// on a list their command was not in).
func TestSliceAHostTrioNamedInAppManagerInstructionSection1054(t *testing.T) {
	sec := agenticUsageSection1054(t, "App-manager writes with an instruction")
	for _, cmd := range []string{"team host assign", "team host unassign", "team host setup"} {
		if !strings.Contains(sec, "`"+cmd+"`") {
			t.Errorf("the App-manager instruction section must name %s — the host pages point the reader here", "`"+cmd+"`")
		}
	}
	if !namesFloorDate2849(sec, "2026-11-03") {
		t.Error("the section must give the host trio the flag's base floor, \"2026-11-03 or later\" (their pages' --instruction usage names it)")
	}
}

// The sandbox itself is pinned (Jonas's BLIND P2s 2/3 on #2578): the stubs
// shadow the real host tools on PATH and refuse, and HOME/XDG_STATE_HOME/XDG_CONFIG_HOME
// point into the test's own temp dirs — the sealing the main harness's
// nothing-outside-the-temp-dirs assertion relies on.
func TestSliceASandboxSealsHostTools1054(t *testing.T) {
	realHome, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	stubDir := sandboxSliceA(t)
	for _, name := range []string{"orca", "systemctl", "launchctl"} {
		got, err := exec.LookPath(name)
		if err != nil || got != filepath.Join(stubDir, name) {
			t.Fatalf("LookPath(%q) = %q, %v; the stub must shadow the real tool", name, got, err)
		}
		if err := exec.Command(got, "probe").Run(); err == nil {
			t.Fatalf("the %s stub must refuse, not succeed", name)
		}
	}
	home, _ := os.UserHomeDir()
	if home == realHome {
		t.Error("HOME is still the real home; the sandbox must move it into a temp dir")
	}
	if state := os.Getenv("XDG_STATE_HOME"); !strings.HasPrefix(state, home+string(filepath.Separator)) {
		t.Errorf("XDG_STATE_HOME %q must sit under the sandbox home %q", state, home)
	}
	// XDG_CONFIG_HOME must seal the config dir too (Jonas's BLIND delta P2 on
	// #2578): config.Dir() honours it before HOME, so assert the resolved dir
	// sits under the sandbox, not at the real host's config location.
	cfgDir, err := config.Dir()
	if err != nil {
		t.Fatalf("config.Dir(): %v", err)
	}
	if !strings.HasPrefix(cfgDir, home+string(filepath.Separator)) {
		t.Errorf("config dir %q must sit under the sandbox home %q (XDG_CONFIG_HOME must seal config.Dir)", cfgDir, home)
	}
}

// unusedAllowlistEntries returns the declared allowlist entries that no example
// exercised, sorted. The harness fails on each one (cli#2617 P3-2, F3).
func unusedAllowlistEntries(declared []string, used map[string]bool) []string {
	var stale []string
	for _, d := range declared {
		if !used[d] {
			stale = append(stale, d)
		}
	}
	sort.Strings(stale)
	return stale
}

// A stale allowlist entry must fail: leaving one declared entry unused (the
// mutant) is reported, and a fully used set is not.
func TestSliceAAllowlistStalenessIsDetected2617(t *testing.T) {
	declared := []string{"notRun a", "panic b", "refusal c"}
	if got := unusedAllowlistEntries(declared, map[string]bool{"notRun a": true, "panic b": true, "refusal c": true}); len(got) != 0 {
		t.Errorf("a fully used set reported stale entries: %v", got)
	}
	got := unusedAllowlistEntries(declared, map[string]bool{"notRun a": true, "refusal c": true})
	if len(got) != 1 || got[0] != "panic b" {
		t.Errorf("an unused entry must be reported, got %v", got)
	}
}

// Every flag the harness treats as file-taking must exist on some slice-A page:
// a name no command has is a dead entry (cli#2617 P3-3 — --policy-file,
// --payload-file and --override-file belonged to slice 1, not to team
// host/router/ticker).
func TestSliceAFileFlagsAreRealFlags2617(t *testing.T) {
	f, _ := testFactory(t)
	have := map[string]bool{}
	for _, page := range collectSliceAPages(NewRootCmd(f)) {
		page.Flags().VisitAll(func(fl *pflag.Flag) { have["--"+fl.Name] = true })
	}
	for _, fl := range sliceAFileFlags {
		if !have[fl] {
			t.Errorf("sliceAFileFlags names %s, which no team host/router/ticker page has", fl)
		}
	}
}
