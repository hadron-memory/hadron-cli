package filterreport

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/orcaterm"
)

func write(t *testing.T, path, text string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(text), 0600); err != nil {
		t.Fatal(err)
	}
}
func proc(t *testing.T, root string, pid, ppid, uid int, cwd string, args ...string) {
	t.Helper()
	dir := filepath.Join(root, strconv.Itoa(pid))
	write(t, filepath.Join(dir, "status"), fmt.Sprintf("Uid:\t%d %d %d %d\n", uid, uid, uid, uid))
	write(t, filepath.Join(dir, "stat"), fmt.Sprintf("%d (name with ) spaces) S %d 0 0", pid, ppid))
	write(t, filepath.Join(dir, "cmdline"), strings.Join(args, "\x00")+"\x00")
	if err := os.Symlink(cwd, filepath.Join(dir, "cwd")); err != nil {
		t.Fatal(err)
	}
}
func TestProcessTree953(t *testing.T) {
	for _, want := range []string{"ok", "multiple_agents", "multiple_wrappers", "no_wrapper", "no_agent", "foreign_uid", "cwd_outside_worktree"} {
		t.Run(want, func(t *testing.T) {
			root, wt, outside := t.TempDir(), t.TempDir(), t.TempDir()
			uid := os.Getuid()
			if want != "no_wrapper" {
				proc(t, root, 10, 1, uid, wt, "/bin/bash", "/bin/worker-supervise", "codex")
			}
			if want != "no_agent" {
				cwd := wt
				if want == "cwd_outside_worktree" {
					cwd = outside
				}
				owner := uid
				if want == "foreign_uid" {
					owner = uid + 1
				}
				proc(t, root, 11, 10, owner, cwd, "/usr/bin/node", "/opt/bin/codex")
			}
			if want == "multiple_agents" {
				proc(t, root, 12, 10, uid, wt, "node", "/opt/bin/codex")
			}
			if want == "multiple_wrappers" {
				proc(t, root, 13, 1, uid, wt, "bash", "/bin/worker-supervise")
			}
			all, err := readProcesses(root, uid)
			if err != nil {
				t.Fatal(err)
			}
			if got := treeState(all, wt); got != want {
				t.Fatalf("got %s want %s", got, want)
			}
		})
	}
}

func snapshot(t *testing.T, root string) map[string]string {
	t.Helper()
	out := map[string]string{}
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(root, path)
		if d.Type()&os.ModeSymlink != 0 {
			s, e := os.Readlink(path)
			out[rel] = "link:" + s
			return e
		}
		if d.IsDir() {
			info, e := d.Info()
			if e != nil {
				return e
			}
			out[rel] = fmt.Sprintf("dir:%s:%d", info.Mode(), info.ModTime().UnixNano())
			return nil
		}
		b, e := os.ReadFile(path)
		info, ie := d.Info()
		if ie != nil {
			return ie
		}
		out[rel] = fmt.Sprintf("%s:%d:%x", info.Mode(), info.ModTime().UnixNano(), sha256.Sum256(b))
		return e
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func TestScanIsReadOnlyAndRedacted953(t *testing.T) {
	root := t.TempDir()
	wt := filepath.Join(root, "worktree")
	procRoot := filepath.Join(root, "proc")
	if err := os.MkdirAll(procRoot, 0700); err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(wt, ".hadron-team", "hadron-team-session.json"), `{"workerId":"w1","appId":"app1","server":"https://test"}`)
	write(t, filepath.Join(root, "wrapper.prompt"), "DO NOT MODIFY PROMPT SENTINEL")
	proc(t, procRoot, 10, 1, os.Getuid(), wt, "bash", "/bin/worker-supervise")
	proc(t, procRoot, 11, 10, os.Getuid(), wt, "node", "/opt/bin/codex")
	sentinel := "RAW_SCREEN_DRAFT_SECRET_SENTINEL_953"
	for _, unreadable := range []bool{false, true} {
		t.Run(fmt.Sprint(unreadable), func(t *testing.T) {
			before := snapshot(t, root)
			calls := []string{}
			runner := func(_ context.Context, args []string) orcaterm.Result {
				if len(args) < 2 || (args[0] != "terminal") || (args[1] != "list" && args[1] != "read") {
					t.Fatalf("forbidden operation %v", args)
				}
				calls = append(calls, args[0]+" "+args[1])
				if args[1] == "list" {
					return orcaterm.Result{Stdout: `{"ok":true,"result":{"terminals":[{"handle":"term1","worktreePath":` + strconv.Quote(wt) + `}]}}`}
				}
				if unreadable {
					return orcaterm.Result{Code: 1, Stderr: sentinel, Stdout: sentinel}
				}
				b, _ := json.Marshal(map[string]any{"ok": true, "result": map[string]any{"terminal": map[string]any{"source": "screen", "tail": []string{"ⓘ This content can’t be shown", "  Apply for Daybreak: https://openai.com/form/", "› "}, "draft": sentinel}}})
				return orcaterm.Result{Stdout: string(b)}
			}
			c := orcaterm.NewReadOnlyClient(runner)
			terms, err := c.List(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			rows, err := Scan(context.Background(), c, terms, []Target{{Name: "Joost", ID: "w1"}}, "https://test", procRoot, os.Getuid())
			if err != nil {
				t.Fatal(err)
			}
			b, _ := json.Marshal(rows)
			if strings.Contains(string(b), sentinel) {
				t.Fatal("secret leaked")
			}
			if len(rows) != 1 || rows[0].AgentTree != "ok" {
				t.Fatalf("bad rows %+v", rows)
			}
			want := "BLOCKED"
			if unreadable {
				want = "UNREADABLE"
			}
			if rows[0].Classification != want {
				t.Fatalf("got %+v", rows[0])
			}
			if !reflect.DeepEqual(calls, []string{"terminal list", "terminal read"}) {
				t.Fatal(calls)
			}
			if !reflect.DeepEqual(before, snapshot(t, root)) {
				t.Fatal("scan changed files")
			}
		})
	}
}

func TestNoWriteOrSignalCapability953(t *testing.T) {
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if !strings.HasSuffix(e.Name(), ".go") || strings.HasSuffix(e.Name(), "_test.go") {
			continue
		}
		f, err := parser.ParseFile(token.NewFileSet(), e.Name(), nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		for _, im := range f.Imports {
			for _, bad := range []string{"os/exec", "syscall", "/delivery", "/hostpoller", "/guardedpush"} {
				if strings.Contains(im.Path.Value, bad) {
					t.Fatalf("unexpected capability import %s", im.Path.Value)
				}
			}
		}
		ast.Inspect(f, func(n ast.Node) bool {
			if call, ok := n.(*ast.CallExpr); ok {
				if s, ok := call.Fun.(*ast.SelectorExpr); ok {
					switch s.Sel.Name {
					case "WriteFile", "Write", "OpenFile", "Create", "Remove", "RemoveAll", "Rename", "Mkdir", "MkdirAll", "Chmod", "Chown", "Chtimes", "Truncate", "Link", "Symlink", "Signal", "Kill", "Start", "Run", "Send":
						t.Errorf("unexpected mutating call %s in %s", s.Sel.Name, e.Name())
					}
				}
			}
			return true
		})
	}
}

func TestBindingPointer953(t *testing.T) {
	root := t.TempDir()
	write(t, filepath.Join(root, ".git"), "gitdir: metadata\n")
	write(t, filepath.Join(root, "metadata", "hadron-team-session.json"), `{"workerId":"w1"}`)
	b, err := ReadBinding(root)
	if err != nil || b.WorkerID != "w1" {
		t.Fatal(b, err)
	}
}

func TestScanEmptyAndDuplicateTerminals953(t *testing.T) {
	root := t.TempDir()
	write(t, filepath.Join(root, ".hadron-team", "hadron-team-session.json"), `{"workerId":"w1"}`)
	c := orcaterm.NewReadOnlyClient(func(context.Context, []string) orcaterm.Result {
		t.Fatal("ambiguous/empty report must not pick a screen")
		return orcaterm.Result{}
	})
	targets := []Target{{Name: "Joost", ID: "w1"}}
	rows, err := Scan(context.Background(), c, nil, targets, "", t.TempDir(), os.Getuid())
	if err != nil || rows == nil || len(rows) != 0 {
		t.Fatal(rows, err)
	}
	terms := []orcaterm.Terminal{{Handle: "two", WorktreePath: root}, {Handle: "one", WorktreePath: root}}
	rows, err = Scan(context.Background(), c, terms, targets, "", t.TempDir(), os.Getuid())
	if err != nil || len(rows) != 1 || rows[0].Classification != "AMBIGUOUS" || rows[0].Note != "multiple_terminals" || rows[0].Terminal != "" {
		t.Fatal(rows, err)
	}
}

func TestBindingDoesNotFollowFileSymlinks953(t *testing.T) {
	root := t.TempDir()
	other := filepath.Join(t.TempDir(), "private.json")
	write(t, other, `{"workerId":"secret"}`)
	if err := os.Mkdir(filepath.Join(root, ".hadron-team"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(other, filepath.Join(root, ".hadron-team", "hadron-team-session.json")); err != nil {
		t.Skip("symlinks unavailable", err)
	}
	if _, err := ReadBinding(root); err == nil {
		t.Fatal("symlink binding read")
	}
}

func TestProcessIdentityGuards953(t *testing.T) {
	for _, tc := range []struct {
		name                   string
		parent                 int
		wrapperArgs, agentArgs []string
		want                   string
	}{
		{"wrong parent", 99, []string{"bash", "/bin/worker-supervise"}, []string{"node", "/bin/codex"}, "no_wrapper"},
		{"mere wrapper mention", 10, []string{"bash", "-c", "echo worker-supervise"}, []string{"node", "/bin/codex"}, "no_wrapper"},
		{"mere agent mention", 10, []string{"bash", "/bin/worker-supervise"}, []string{"echo", "node /bin/codex"}, "no_agent"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root, wt := t.TempDir(), t.TempDir()
			proc(t, root, 10, 1, os.Getuid(), wt, tc.wrapperArgs...)
			proc(t, root, 11, tc.parent, os.Getuid(), wt, tc.agentArgs...)
			all, err := readProcesses(root, os.Getuid())
			if err != nil || treeState(all, wt) != tc.want {
				t.Fatal(all, err, treeState(all, wt))
			}
		})
	}
}

func TestOutsideChildIsNotHiddenByInsideChild953(t *testing.T) {
	root, wt := t.TempDir(), t.TempDir()
	proc(t, root, 10, 1, os.Getuid(), wt, "bash", "/bin/worker-supervise")
	proc(t, root, 11, 10, os.Getuid(), wt, "node", "/bin/codex")
	proc(t, root, 12, 10, os.Getuid(), t.TempDir(), "node", "/bin/codex")
	all, err := readProcesses(root, os.Getuid())
	if err != nil || treeState(all, wt) != "cwd_outside_worktree" {
		t.Fatal(all, err, treeState(all, wt))
	}
}
