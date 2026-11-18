package filterreport

import (
	"context"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/orcaterm"
)

func set(paths ...string) map[string]bool {
	m := map[string]bool{}
	for _, p := range paths {
		m[p] = true
	}
	return m
}

// procSymlinkCmdline is proc with a cmdline that openRead can never follow:
// a symlink. It proves the reader never inspects a foreign-uid argv — the
// read would fail and the process vanish from the tree (cli#2483, §10 F2).
func procSymlinkCmdline(t *testing.T, root string, pid, ppid, uid int, cwd string, args ...string) {
	t.Helper()
	dir := filepath.Join(root, strconv.Itoa(pid))
	write(t, filepath.Join(dir, "status"), "Uid:\t"+strconv.Itoa(uid)+" "+strconv.Itoa(uid)+" "+strconv.Itoa(uid)+" "+strconv.Itoa(uid)+"\n")
	write(t, filepath.Join(dir, "stat"), strconv.Itoa(pid)+" (name) S "+strconv.Itoa(ppid)+" 0 0")
	target := filepath.Join(t.TempDir(), "elsewhere")
	write(t, target, strings.Join(args, " "))
	if err := os.Symlink(target, filepath.Join(dir, "cmdline")); err != nil {
		t.Skip("symlinks unavailable", err)
	}
	if err := os.Symlink(cwd, filepath.Join(dir, "cwd")); err != nil {
		t.Fatal(err)
	}
}

// TestTreeStateOrderIndependentPrecedence2483 pins F8: several coexisting
// process-tree problems resolve to one code by fixed precedence, whatever
// order the process list arrived in.
func TestTreeStateOrderIndependentPrecedence2483(t *testing.T) {
	for _, order := range []string{"ascending", "descending"} {
		t.Run(order, func(t *testing.T) {
			root, wt := t.TempDir(), t.TempDir()
			below := filepath.Join(wt, "sub") // strictly inside the worktree, below its root
			if err := os.MkdirAll(below, 0700); err != nil {
				t.Fatal(err)
			}
			uid := os.Getuid()
			// pid 9: wrapper strictly below the worktree root (cwd anomaly).
			proc(t, root, 9, 1, uid, below, "bash", "/bin/worker-supervise")
			// pid 5: foreign-uid process inside the worktree.
			proc(t, root, 5, 1, uid+1, wt, "sleep", "1000")
			all, err := readProcesses(root, uid)
			if err != nil {
				t.Fatal(err)
			}
			sorted := true
			for i := 1; i < len(all); i++ {
				if all[i-1].pid > all[i].pid {
					sorted = false
				}
			}
			if !sorted {
				t.Fatal("readProcesses must list processes by pid")
			}
			if order == "descending" {
				for i, j := 0, len(all)-1; i < j; i, j = i+1, j-1 {
					all[i], all[j] = all[j], all[i]
				}
			}
			if got := treeState(all, wt); got != "foreign_uid" {
				t.Fatalf("order %s: got %s, want foreign_uid (foreign_uid outranks a cwd anomaly)", order, got)
			}
		})
	}
}

// TestTreeStateSymlinkedWorktree2483 pins F16: a fixture worktree reached
// through a symlinked temp path still classifies ok. The /proc side is
// compared in resolved form, like the worktree root already is. This pins
// path canonicalization in fixtures only; no native macOS process support
// is claimed (there is no production /proc source there).
func TestTreeStateSymlinkedWorktree2483(t *testing.T) {
	real := t.TempDir()
	linked := filepath.Join(t.TempDir(), "link")
	if err := os.Symlink(real, linked); err != nil {
		t.Skip("symlinks unavailable", err)
	}
	procRoot := t.TempDir()
	uid := os.Getuid()
	proc(t, procRoot, 10, 1, uid, linked, "bash", "/bin/worker-supervise")
	proc(t, procRoot, 11, 10, uid, linked, "node", "/opt/bin/codex")
	all, err := readProcesses(procRoot, uid)
	if err != nil {
		t.Fatal(err)
	}
	if got := treeState(all, real); got != "ok" {
		t.Fatalf("symlinked fixture classified %s, want ok", got)
	}
}

// TestScanServerOriginCanonicalization2483 pins F15: scheme/host case and
// the scheme's default port are canonicalized on both sides of the
// binding↔server match; everything else — a different host or port, a
// missing server, a path difference — stays refused.
func TestScanServerOriginCanonicalization2483(t *testing.T) {
	for _, tc := range []struct {
		name, binding, server string
		wantRow               bool
	}{
		{"case and default port", "HTTPS://Test.COM:443/", "https://test.com", true},
		{"http default port", "http://Other.Host:80", "http://other.host", true},
		{"trailing slash", "https://test.com/", "https://test.com", true},
		{"different host", "https://other.example", "https://test.com", false},
		{"non-default port", "https://test.com:8443", "https://test.com", false},
		{"missing binding server", "", "https://test.com", false},
		{"missing scan server", "https://test.com", "", false},
		{"path difference", "https://test.com/api", "https://test.com", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			write(t, filepath.Join(root, ".hadron-team", "hadron-team-session.json"),
				`{"workerId":"w1","appId":"app1","server":`+strconv.Quote(tc.binding)+`}`)
			c := orcaterm.NewReadOnlyClient(func(context.Context, []string) orcaterm.Result {
				return orcaterm.Result{Stdout: `{"ok":true,"result":{"terminals":[{"handle":"term1","worktreePath":` + strconv.Quote(root) + `}]}}`}
			})
			terms, err := c.List(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			rows, err := Scan(context.Background(), c, terms, []Target{{Name: "Joost", ID: "w1"}}, tc.server, t.TempDir(), os.Getuid())
			if err != nil {
				t.Fatal(err)
			}
			if tc.wantRow && len(rows) != 1 {
				t.Fatalf("canonical-equal server origin dropped the row: %+v", rows)
			}
			if !tc.wantRow && len(rows) != 0 {
				t.Fatalf("server-origin mismatch produced a row: %+v", rows)
			}
		})
	}
}

// TestScanAmbiguousAcrossWorktrees2483 pins F10: a worker with terminals in
// more than one worktree reports neither — the worktree (and the process tree
// derived from it) is as ambiguous as the terminal, and stays empty instead of
// showing the handle-sorted first. Terminals sharing one worktree keep it.
func TestScanAmbiguousAcrossWorktrees2483(t *testing.T) {
	one, two := t.TempDir(), t.TempDir()
	for _, dir := range []string{one, two} {
		write(t, filepath.Join(dir, ".hadron-team", "hadron-team-session.json"), `{"workerId":"w1","appId":"app1","server":"https://test"}`)
	}
	targets := []Target{{Name: "Joost", ID: "w1"}}
	for _, tc := range []struct {
		name             string
		terms            []orcaterm.Terminal
		wantWorktreeKeep bool
	}{
		{"same worktree", []orcaterm.Terminal{{Handle: "b", WorktreePath: one}, {Handle: "a", WorktreePath: one}}, true},
		{"different worktrees", []orcaterm.Terminal{{Handle: "b", WorktreePath: one}, {Handle: "a", WorktreePath: two}}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := orcaterm.NewReadOnlyClient(func(context.Context, []string) orcaterm.Result {
				t.Fatal("ambiguous report must not pick a screen")
				return orcaterm.Result{}
			})
			rows, err := Scan(context.Background(), c, tc.terms, targets, "https://test", t.TempDir(), os.Getuid())
			if err != nil || len(rows) != 1 || rows[0].Classification != "AMBIGUOUS" || rows[0].Terminal != "" || rows[0].Note != "multiple_terminals" {
				t.Fatal(rows, err)
			}
			if tc.wantWorktreeKeep && rows[0].Worktree == "" {
				t.Fatal("shared worktree blanked", rows[0])
			}
			if !tc.wantWorktreeKeep && (rows[0].Worktree != "" || rows[0].AgentTree != "") {
				t.Fatal("ambiguous worktree or tree kept", rows[0])
			}
		})
	}
}

// TestForeignUIDUnreadableCmdline2483 pins §10 F2: a foreign-uid process is
// reported even when its cmdline could never be read (here: a symlink, which
// the no-follow open refuses at any uid). Reading foreign argv before the
// ownership check would drop the process and lose the foreign_uid row.
func TestForeignUIDUnreadableCmdline2483(t *testing.T) {
	root, wt := t.TempDir(), t.TempDir()
	procSymlinkCmdline(t, root, 7, 1, os.Getuid()+1, wt, "secret", "argv")
	all, err := readProcesses(root, os.Getuid())
	if err != nil {
		t.Fatal(err)
	}
	if got := treeState(all, wt); got != "foreign_uid" {
		t.Fatalf("foreign-uid process lost to an unreadable cmdline: %s", got)
	}
}

// TestImportAllowlistPerFile2483 strengthens the capability pin (§10 F1,
// issue 2483 F17): every non-test file in this package imports only from its
// own allowlist, so a mutation-capable import lands as a review diff, not a
// silent addition.
func TestImportAllowlistPerFile2483(t *testing.T) {
	allow := map[string]map[string]bool{
		"binding.go":      set("encoding/json", "errors", "io", "os", "path/filepath", "strings"),
		"process.go":      set("errors", "os", "path/filepath", "sort", "strconv", "strings"),
		"scan.go":         set("context", "net/url", "sort", "strings", "github.com/hadron-memory/hadron-cli/internal/orcaterm", "github.com/hadron-memory/hadron-cli/internal/output"),
		"open_unix.go":    set("golang.org/x/sys/unix", "os"),
		"open_windows.go": set("os"),
	}
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for _, e := range entries {
		if !strings.HasSuffix(e.Name(), ".go") || strings.HasSuffix(e.Name(), "_test.go") {
			continue
		}
		want, ok := allow[e.Name()]
		if !ok {
			t.Fatalf("no import allowlist for %s: add one", e.Name())
		}
		seen[e.Name()] = true
		f, err := parser.ParseFile(token.NewFileSet(), e.Name(), nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		for _, im := range f.Imports {
			path := strings.Trim(im.Path.Value, `"`)
			if !want[path] {
				t.Errorf("%s imports %s, outside its allowlist", e.Name(), path)
			}
		}
	}
	for name := range allow {
		if !seen[name] {
			t.Errorf("allowlisted file %s disappeared", name)
		}
	}
}

// TestUnixOpenReadOnlyFlags2483 pins #2483 F17: the one unix.Open in this
// package may combine only read-safe flags, and no non-test file may call a
// signal-capable unix selector however it is spelled.
func TestUnixOpenReadOnlyFlags2483(t *testing.T) {
	const file = "open_unix.go"
	f, err := parser.ParseFile(token.NewFileSet(), file, nil, 0)
	if err != nil {
		t.Skip("no unix open on this build", err)
	}
	allowed := map[string]bool{"O_RDONLY": true, "O_NONBLOCK": true, "O_NOFOLLOW": true, "O_CLOEXEC": true}
	opens := 0
	ast.Inspect(f, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		sel, ok := call.Fun.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		pkg, ok := sel.X.(*ast.Ident)
		if !ok || pkg.Name != "unix" || sel.Sel.Name != "Open" {
			return true
		}
		opens++
		if len(call.Args) < 2 {
			t.Errorf("unix.Open without flags in %s", file)
			return true
		}
		ast.Inspect(call.Args[1], func(m ast.Node) bool {
			if sel, ok := m.(*ast.SelectorExpr); ok {
				if pkg, ok := sel.X.(*ast.Ident); ok && pkg.Name == "unix" && !allowed[sel.Sel.Name] {
					t.Errorf("unix.Open flag %s is not read-only", sel.Sel.Name)
				}
			}
			return true
		})
		return true
	})
	if opens != 1 {
		t.Fatalf("%s calls unix.Open %d times, want exactly 1", file, opens)
	}
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if !strings.HasSuffix(e.Name(), ".go") || strings.HasSuffix(e.Name(), "_test.go") {
			continue
		}
		g, err := parser.ParseFile(token.NewFileSet(), e.Name(), nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		ast.Inspect(g, func(n ast.Node) bool {
			if call, ok := n.(*ast.CallExpr); ok {
				if sel, ok := call.Fun.(*ast.SelectorExpr); ok {
					switch sel.Sel.Name {
					case "Tgkill", "PidfdSendSignal":
						t.Errorf("signal-capable selector %s in %s", sel.Sel.Name, e.Name())
					}
				}
			}
			return true
		})
	}
}
