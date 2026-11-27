package team

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/exitcode"
	"github.com/hadron-memory/hadron-cli/internal/hostfacts"
	"github.com/hadron-memory/hadron-cli/internal/orcaterm"
)

// cli#3236: the parts of `team host doctor` a command test cannot reach: a tmpfs state directory and the
// precedence of exit 10 over exit 5.

func doctorEnv3236(orcaUp bool, fstype string) hostDoctorEnv {
	run := func(_ context.Context, argv []string) orcaterm.Result {
		switch strings.Join(argv, " ") {
		case "--version":
			return orcaterm.Result{Stdout: "1.4.247\n"}
		case "repo list --json":
			return orcaterm.Result{Stdout: `{"ok":true,"result":{"repos":[]}}`}
		case "worktree list --json --limit 5000":
			return orcaterm.Result{Stdout: `{"ok":true,"result":{"worktrees":[],"totalCount":0,"truncated":false}}`}
		}
		return orcaterm.Result{Code: 1}
	}
	f := hostfacts.New(run)
	f.GOOS, f.Home = "linux", "/home/u"
	f.Getenv = func(string) string { return "" }
	f.LookPath = func(n string) (string, error) {
		if n == "orca" && orcaUp {
			return "/bin/orca", nil
		}
		if n == "claude" || n == "codex" || n == "claude-glm" || n == "claude-kimi" {
			return "/bin/" + n, nil
		}
		return "", os.ErrNotExist
	}
	f.IsExecutable = func(string) bool { return false }
	f.FSType = func(string) (string, error) { return fstype, nil }
	return hostDoctorEnv{
		Facts:        f,
		AccountsPath: func() (string, error) { return "/nonexistent/hadron/accounts.yaml", nil },
		IsDir:        func(string) bool { return false },
	}
}

func TestDoctorATmpfsStateDirectoryIsExit5(t *testing.T) {
	d, code, _ := buildHostDoctor(context.Background(), doctorEnv3236(true, "tmpfs"), "", false)
	if code != exitcode.Conflict || d.OK {
		t.Fatalf("code %d ok %v, want 5 for a state directory emptied at reboot", code, d.OK)
	}
	var state *doctorFact
	for i := range d.Facts {
		if d.Facts[i].Fact == "state directory" {
			state = &d.Facts[i]
		}
	}
	if state == nil || state.Status != "MISSING" || state.Fix == nil || !strings.Contains(*state.Fix, "XDG_STATE_HOME") {
		t.Errorf("the fact names XDG_STATE_HOME as the fix: %+v", state)
	}
	if _, code, _ := buildHostDoctor(context.Background(), doctorEnv3236(true, "ext4"), "", false); code != 0 {
		t.Errorf("a state directory on disk is exit 0, got %d", code)
	}
}

func TestDoctorExit10DominatesExit5(t *testing.T) {
	// Orca is absent AND the state directory is on tmpfs: both are problems, and Orca not answering is 10.
	d, code, _ := buildHostDoctor(context.Background(), doctorEnv3236(false, "tmpfs"), "", false)
	if code != exitcode.OrcaUnreachable {
		t.Fatalf("code %d, want 10 to dominate 5", code)
	}
	if d.Orca.Status != "MISSING" || d.Orca.Bin != nil {
		t.Errorf("the DTO still reports Orca missing: %+v", d.Orca)
	}
	for _, f := range d.Facts {
		if (f.Fact == "orca repos" || f.Fact == "orca worktrees") && f.Status != "UNKNOWN" {
			t.Errorf("%s cannot be read without Orca: %+v", f.Fact, f)
		}
	}
}

type failingWriter3236 struct{ n int }

func (w *failingWriter3236) Write(p []byte) (int, error) {
	w.n++
	return 0, os.ErrClosed
}

// A closed stdout must not read as a clean exit with a truncated report: the first write error is returned.
func TestDoctorRenderReturnsTheFirstWriteError(t *testing.T) {
	d, _, _ := buildHostDoctor(context.Background(), doctorEnv3236(true, "ext4"), "", false)
	w := &failingWriter3236{}
	if err := renderHostDoctor(w, d); err == nil {
		t.Fatal("a failed write is an error")
	}
	if w.n != 1 {
		t.Errorf("after the first failed write nothing more is written, got %d writes", w.n)
	}
}
