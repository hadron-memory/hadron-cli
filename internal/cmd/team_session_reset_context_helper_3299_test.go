//go:build !windows

package cmd

import (
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/cmd/team"
)

// cli#3299: `team session reset-context` finishes the reset through a HOST-CONFIGURED exit helper, so one command
// works on every host. Only on the exit-now branch (the checkpoint and the stamp written, the own pid not pinned),
// only from user-level host configuration (a file `session-exit-helper` in the Hadron config directory naming one
// absolute path), run without a shell. The helper ends the AGENT only, never the session (cor:agt:050:27).
//
// These are the acceptance pins of the gate (Xabat, issue comment 7215810314): helper-pending, exit-now with the
// reason when the helper is refused, no change with no helper, and the refusal set (relative path, symlink, group
// or world writable, not owned, missing), no helper without a checkpoint or a stamp or a receipt.

// helperRig is a user-level host config with a marker script as the helper.
type helperRig struct {
	cfg    string // the config file naming the helper
	script string
	marker string
}

// newHelperRig writes <config dir>/session-exit-helper naming a script that records its arguments.
func newHelperRig(t *testing.T) *helperRig {
	t.Helper()
	cfgBase := os.Getenv("XDG_CONFIG_HOME")
	if cfgBase == "" {
		t.Fatal("testFactory must pin XDG_CONFIG_HOME")
	}
	dir := filepath.Join(cfgBase, "hadron")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	work := t.TempDir()
	r := &helperRig{cfg: filepath.Join(dir, "session-exit-helper"), script: filepath.Join(work, "exit-helper"), marker: filepath.Join(work, "ran")}
	body := "#!/bin/sh\necho \"$@\" > " + r.marker + "\n"
	if err := os.WriteFile(r.script, []byte(body), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(r.script, 0o700); err != nil {
		t.Fatal(err)
	}
	r.point(t, r.script)
	return r
}

// point rewrites the config file to name path (mode 0600, as a user's own file is).
func (r *helperRig) point(t *testing.T, path string) {
	t.Helper()
	_ = os.Remove(r.cfg)
	if err := os.WriteFile(r.cfg, []byte(path+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(r.cfg, 0o600); err != nil {
		t.Fatal(err)
	}
}

func (r *helperRig) ran() bool {
	_, err := os.Stat(r.marker)
	return err == nil
}

func resetReceipt3299(t *testing.T, out string) resetContextDTO {
	t.Helper()
	var d resetContextDTO
	if err := json.Unmarshal([]byte(out), &d); err != nil {
		t.Fatalf("the receipt is not one JSON document: %v\n%s", err, out)
	}
	return d
}

// runRig3299 runs reset-context against a fresh helper rig. testFactory pins XDG_CONFIG_HOME per call, so the rig is
// built after the factory and read by the same command. mod adjusts the rig first; outW replaces stdout when set.
func runRig3299(t *testing.T, url string, mod func(*testing.T, *helperRig), outW io.Writer) (rig *helperRig, out, errOut string, err error) {
	t.Helper()
	f, o := testFactory(t)
	rig = newHelperRig(t)
	if mod != nil {
		mod(t, rig)
	}
	var e strings.Builder
	f.IOStreams.ErrOut = &e
	if outW != nil {
		f.IOStreams.Out = outW
	}
	root := NewRootCmd(f)
	root.SetArgs([]string{"team", "session", "reset-context", "--server", url, "--json"})
	err = root.Execute()
	return rig, o.String(), e.String(), err
}

// A configured helper on the exit-now branch: the receipt says helper-pending and tells the agent to end its turn
// and run nothing more; the helper ran, once, with the checkpoint's URN. The helper ends the agent, not the session.
func TestResetContextRunsTheConfiguredHelperAndSaysHelperPending3299(t *testing.T) {
	bindWorktree(t)
	t.Setenv(team.AgentPIDEnv, "")
	url, _ := shedServer(t, nil)
	rig, out, _, err := runRig3299(t, url, nil, nil)
	if err != nil {
		t.Fatalf("reset-context: %v", err)
	}
	d := resetReceipt3299(t, out)
	if d.Exit != "helper-pending" || d.Relaunch != "pending" || d.Node == nil {
		t.Fatalf("want helper-pending with the relaunch pending, got %s", out)
	}
	if d.Reason == nil || !strings.Contains(strings.ToLower(*d.Reason), "end your turn now and run nothing more") {
		t.Fatalf("the reason must tell the agent to end its turn now and run nothing more, got %v", d.Reason)
	}
	for _, bad := range []string{"session ends", "session end", "ends the session", "end the session", "binding ends"} {
		if strings.Contains(strings.ToLower(*d.Reason), bad) {
			t.Fatalf("the helper ends the AGENT only, never the session (cor:agt:050:27): the reason says %q", bad)
		}
	}
	if !rig.ran() {
		t.Fatal("the configured helper did not run")
	}
	got, _ := os.ReadFile(rig.marker)
	if strings.TrimSpace(string(got)) != "--checkpoint "+*d.Node {
		t.Fatalf("the helper is run as <helper> --checkpoint <node URN>, got %q", got)
	}
}

// No helper configured: behaviour and receipt as today.
func TestResetContextWithNoHelperConfiguredIsUnchanged3299(t *testing.T) {
	bindWorktree(t)
	t.Setenv(team.AgentPIDEnv, "")
	url, _ := shedServer(t, nil)
	out, err := runResetContext(t, url, "--json")
	if err != nil {
		t.Fatal(err)
	}
	d := resetReceipt3299(t, out)
	if d.Exit != "exit-now" || d.Reason != nil {
		t.Fatalf("want exit-now with no reason when no helper is configured, got %s", out)
	}
}

// A helper that is refused is not run: exit-now with a reason that says which, the command's exit code unchanged.
func TestResetContextRefusesAnUnsafeHelperAndSaysExitNow3299(t *testing.T) {
	cases := []struct {
		name  string
		setup func(t *testing.T, r *helperRig)
		why   string
	}{
		{"relative path", func(t *testing.T, r *helperRig) { r.point(t, "exit-helper") }, "absolute"},
		{"symlink to a good script", func(t *testing.T, r *helperRig) {
			link := filepath.Join(filepath.Dir(r.script), "link")
			if err := os.Symlink(r.script, link); err != nil {
				t.Fatal(err)
			}
			r.point(t, link)
		}, "symlink"},
		{"group writable", func(t *testing.T, r *helperRig) {
			if err := os.Chmod(r.script, 0o770); err != nil {
				t.Fatal(err)
			}
		}, "writable"},
		{"world writable", func(t *testing.T, r *helperRig) {
			if err := os.Chmod(r.script, 0o707); err != nil {
				t.Fatal(err)
			}
		}, "writable"},
		{"missing", func(t *testing.T, r *helperRig) { r.point(t, filepath.Join(filepath.Dir(r.script), "nope")) }, "missing"},
		{"a directory", func(t *testing.T, r *helperRig) { r.point(t, filepath.Dir(r.script)) }, "regular file"},
		{"config file is a symlink", func(t *testing.T, r *helperRig) {
			real := filepath.Join(filepath.Dir(r.script), "real-config")
			if err := os.WriteFile(real, []byte(r.script+"\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			_ = os.Remove(r.cfg)
			if err := os.Symlink(real, r.cfg); err != nil {
				t.Fatal(err)
			}
		}, "symlink"},
		{"config file group writable", func(t *testing.T, r *helperRig) {
			if err := os.Chmod(r.cfg, 0o660); err != nil {
				t.Fatal(err)
			}
		}, "writable"},
		{"config file names two paths", func(t *testing.T, r *helperRig) {
			if err := os.WriteFile(r.cfg, []byte(r.script+"\n"+r.script+"\n"), 0o600); err != nil {
				t.Fatal(err)
			}
		}, "one"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			bindWorktree(t)
			t.Setenv(team.AgentPIDEnv, "")
			url, _ := shedServer(t, nil)
			rig, out, _, err := runRig3299(t, url, tc.setup, nil)
			if err != nil {
				t.Fatalf("a refused helper must not change the command's exit code: %v", err)
			}
			d := resetReceipt3299(t, out)
			if d.Exit != "exit-now" || d.Reason == nil || !strings.Contains(strings.ToLower(*d.Reason), tc.why) {
				t.Fatalf("want exit-now with a reason naming %q, got %s", tc.why, out)
			}
			if rig.ran() {
				t.Fatal("a refused helper was run")
			}
			if strings.Contains(*d.Reason, rig.marker) {
				t.Fatalf("the reason must not echo helper content: %s", *d.Reason)
			}
		})
	}
}

// No checkpoint, no helper: a failed checkpoint writes no stamp, ends nothing and runs no helper.
func TestResetContextRunsNoHelperWithoutACheckpoint3299(t *testing.T) {
	bindWorktree(t)
	t.Setenv(team.AgentPIDEnv, "")
	url, _ := shedServer(t, map[string]string{"CreateNode": `{"errors":[{"message":"boom"}]}`})
	rig, out, _, err := runRig3299(t, url, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	d := resetReceipt3299(t, out)
	if d.Status != "failed" || d.Exit != "none" {
		t.Fatalf("want failed/none, got %s", out)
	}
	if rig.ran() {
		t.Fatal("the helper ran although the checkpoint failed")
	}
}

// No stamp, no helper: a stamp that cannot be written asks for no relaunch and ends nothing.
func TestResetContextRunsNoHelperWithoutTheStamp3299(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("a read-only directory does not stop root")
	}
	bindWorktree(t)
	t.Setenv(team.AgentPIDEnv, "")
	gitDir := os.Getenv(team.GitDirEnv)
	if err := os.Chmod(gitDir, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(gitDir, 0o700) })
	url, _ := shedServer(t, nil)
	rig, out, _, err := runRig3299(t, url, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	d := resetReceipt3299(t, out)
	if d.Relaunch != "failed" || d.Exit != "none" {
		t.Fatalf("want relaunch failed and exit none, got %s", out)
	}
	if rig.ran() {
		t.Fatal("the helper ran although the stamp was not written")
	}
}

// failingWriter3299 is a stdout whose write fails: the receipt cannot be written.
type failingWriter3299 struct{}

func (failingWriter3299) Write([]byte) (int, error) { return 0, errors.New("write failed") }

// No receipt, no helper: a receipt that could not be written runs nothing, because the agent has not been told;
// stderr says so.
func TestResetContextRunsNoHelperWhenTheReceiptCannotBeWritten3299(t *testing.T) {
	bindWorktree(t)
	t.Setenv(team.AgentPIDEnv, "")
	url, _ := shedServer(t, nil)
	rig, _, errOutS, _ := runRig3299(t, url, nil, failingWriter3299{})
	errOut := strings.Builder{}
	errOut.WriteString(errOutS)
	if rig.ran() {
		t.Fatal("the helper ran although the receipt could not be written")
	}
	if !strings.Contains(errOut.String(), "receipt could not be written") {
		t.Fatalf("stderr must say the receipt could not be written, got %q", errOut.String())
	}
}

// A helper that exits non-zero after the receipt is said on stderr (the receipt is already written, as for the
// signal): the agent exits itself, and the receipt stays one JSON document.
func TestResetContextSaysOnStderrWhenTheHelperFails3299(t *testing.T) {
	bindWorktree(t)
	t.Setenv(team.AgentPIDEnv, "")
	url, _ := shedServer(t, nil)
	_, out, errOut, err := runRig3299(t, url, func(t *testing.T, r *helperRig) {
		if err := os.WriteFile(r.script, []byte("#!/bin/sh\necho SECRET-OUTPUT\necho SECRET-ERR >&2\nexit 3\n"), 0o700); err != nil {
			t.Fatal(err)
		}
	}, nil)
	if err != nil {
		t.Fatalf("a failing helper must not change the command's exit code: %v", err)
	}
	d := resetReceipt3299(t, out)
	if d.Exit != "helper-pending" {
		t.Fatalf("the receipt is written before the helper runs, got %s", out)
	}
	if !strings.Contains(errOut, "exit your agent yourself") {
		t.Fatalf("stderr must tell the agent to exit itself, got %q", errOut)
	}
	if strings.Contains(out+errOut, "SECRET-") {
		t.Fatalf("the helper's output must not be echoed: %q / %q", out, errOut)
	}
}

// reset-context --help and the agentic usage describe helper-pending and the helper contract.
func TestResetContextHelpAndUsageDescribeTheHelper3299(t *testing.T) {
	help := strings.Join(strings.Fields(cmdHelpOut(t, "team", "session", "reset-context")), " ")
	for _, want := range []string{"helper-pending", "session-exit-helper", "end your turn now and run nothing more", "never the session"} {
		if !strings.Contains(help, want) {
			t.Errorf("team session reset-context --help must say %q:\n%s", want, help)
		}
	}
}
