package cmd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/exitcode"
)

// cli#3286: follow-ups from the blind reviews of the host doctor (#3284, #3283). Wording, tests, and one
// --json field (workersNotChecked); no exit code and no request changes.

func hostArgs3286() []string {
	return []string{"--host", "orca1", "--app", "hrn:app:acme.com:ops"}
}

// writeOrca3286 replaces the fake orca with one whose worktree list answers as given.
func writeOrca3286(t *testing.T, h *doctorHost3236, worktreeArm string) {
	t.Helper()
	script := "#!/bin/sh\ncase \"$*\" in\n" +
		" \"--version\") printf '%s\\n' '1.4.247' ;;\n" +
		" \"repo list --json\") printf '%s\\n' '" + oneLine3236(t, "orca_repo_list.linux.json") + "' ;;\n" +
		" \"worktree list --json --limit 5000\") " + worktreeArm + " ;;\n" +
		" *) echo \"unexpected orca call: $*\" >&2; exit 3 ;;\nesac\n"
	if err := os.WriteFile(filepath.Join(h.bin, "orca"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
}

// Item 1 (F1, N4): the result line and the NOT_FOUND line say why no worker was checked, and give the
// "pass --host" advice only when --host was not given.
func TestDoctorSaysWhyNoWorkerWasChecked3286(t *testing.T) {
	const advice = "pass --host"
	cases := []struct {
		name      string
		responses map[string]string
		args      []string
		why       any    // the workersNotChecked value
		notFound  string // what the NOT_FOUND line says in place of the advice
		advice    bool
		exit      int
	}{
		{"no --host", nil, nil, "no --host", "pass --host to check", true, 0},
		{"host-sourced setup", setup3236("HOST"), hostArgs3286(), "host-sourced setup", "the App takes its setup from the host file", false, 0},
		{"read failed", map[string]string{"TeamHostSetupWithProfile": gqlErrorJSON("FORBIDDEN")}, hostArgs3286(), "read failed", "the assignments could not be read", false, exitcode.Forbidden},
		{"checked", setup3236("SERVER", assignment3236("hadron-server/antonio", "claude", "")), hostArgs3286(), nil, "no assigned worker runs it by default", false, 0},
		// A successful read of zero assignments: nothing to check, nothing left unchecked.
		{"zero assignments", setup3236("SERVER"), hostArgs3286(), nil, "no assigned worker runs it by default", false, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			newDoctorHost3236(t, true, "claude")
			out, code, _ := doctor3236(t, tc.responses, tc.args...)
			if code != tc.exit {
				t.Fatalf("exit %d, want %d\n%s", code, tc.exit, out)
			}
			notFound := factLine3236(out, "agent program (codex)")
			result := ""
			for _, l := range strings.Split(out, "\n") {
				if strings.HasPrefix(l, "result:") {
					result = l
				}
			}
			if got := strings.Contains(notFound, advice); got != tc.advice {
				t.Errorf("NOT_FOUND line advice=%v, want %v: %q", got, tc.advice, notFound)
			}
			if !strings.Contains(notFound, tc.notFound) {
				t.Errorf("NOT_FOUND line must say %q: %q", tc.notFound, notFound)
			}
			switch tc.name {
			case "no --host", "host-sourced setup":
				// Exit 0 and nothing checked: the result line names the reason, with the advice only for "no --host".
				want := "result: ok for this machine; no workers were checked (" + tc.notFound
				if tc.name == "no --host" {
					want = "result: ok for this machine; no workers were checked (pass --host <id> to check"
				}
				if !strings.Contains(result, want) {
					t.Errorf("result line must say %q: %q", want, result)
				}
			case "checked", "zero assignments":
				if result != "result: ok" {
					t.Errorf("a successful read, even of zero assignments, is plain ok: %q", result)
				}
			default: // read failed: the report is not ok, so the result line says something is missing; the reason is on the fact lines
				if !strings.Contains(result, "something is missing") {
					t.Errorf("a failed read is not ok: %q", result)
				}
			}
			if got := strings.Contains(result, advice); got != tc.advice {
				t.Errorf("result line advice=%v, want %v: %q", got, tc.advice, result)
			}
			newDoctorHost3236(t, true, "claude")
			jout, _, _ := doctor3236(t, tc.responses, append(append([]string{}, tc.args...), "--json")...)
			d := doctorJSON3236(t, jout)
			if v, ok := d["workersNotChecked"]; !ok || v != tc.why {
				t.Errorf("--json workersNotChecked = %v (present %v), want %v", v, ok, tc.why)
			}
			if tc.why == nil && d["workersChecked"] != true {
				t.Errorf("workersChecked must be true when the reason is null: %v", d["workersChecked"])
			}
			if tc.why != nil && d["workersChecked"] != false {
				t.Errorf("workersChecked must be false beside a reason: %v", d["workersChecked"])
			}
		})
	}
}

// Item 2 (F2): Orca answers, but a read of it fails or is cut: exit 10 with its own result line, and a
// concurrent accounts problem still shows.
func TestDoctorAFailedOrcaReadIsExit10AndKeepsOtherProblems3286(t *testing.T) {
	for name, arm := range map[string]string{
		"refused":   "echo boom >&2; exit 3",
		"truncated": `printf '%s\n' '{"ok":true,"result":{"worktrees":[],"totalCount":9,"truncated":true}}'`,
	} {
		t.Run(name, func(t *testing.T) {
			h := newDoctorHost3236(t, true, "claude")
			writeOrca3286(t, h, arm)
			h.accounts(t, "accounts:\n  work:\n    family: claude\n    program: /nonexistent/prog-3286\n", 0o600)
			out, code, _ := doctor3236(t, nil)
			if code != exitcode.OrcaUnreachable {
				t.Fatalf("exit %d, want 10 (Orca found, its worktree list unusable)\n%s", code, out)
			}
			for _, want := range []string{"result: Orca is missing or did not answer (exit 10)", "ACCOUNT_PROGRAM_NOT_FOUND"} {
				if !strings.Contains(out, want) {
					t.Errorf("must say %q:\n%s", want, out)
				}
			}
			if !strings.Contains(out, "orca: found") {
				t.Errorf("Orca itself was found:\n%s", out)
			}
		})
	}
}

// Item 7 (N3): with Orca up but its reads failed, a worker's row does not read plain OK: it says its workdir
// was not checked.
func TestDoctorAWorkerRowSaysWhenItsWorkdirWasNotChecked3286(t *testing.T) {
	h := newDoctorHost3236(t, true, "claude")
	writeOrca3286(t, h, "exit 3")
	out, code, _ := doctor3236(t, setup3236("SERVER", assignment3236("hadron-server/antonio", "claude", "")), hostArgs3286()...)
	if code != exitcode.OrcaUnreachable {
		t.Fatalf("exit %d, want 10\n%s", code, out)
	}
	row := ""
	for _, l := range strings.Split(out, "\n") {
		if strings.Contains(l, "worker Iris hadron-server/antonio") {
			row = l
		}
	}
	if !strings.Contains(row, "workdir not checked") {
		t.Errorf("the row must say the workdir was not checked: %q\n%s", row, out)
	}
	// With Orca answering, the row says nothing of the kind.
	newDoctorHost3236(t, true, "claude")
	out, _, _ = doctor3236(t, setup3236("SERVER", assignment3236("hadron-server/antonio", "claude", "")), hostArgs3286()...)
	if strings.Contains(out, "workdir not checked") {
		t.Errorf("a checked workdir is not reported unchecked:\n%s", out)
	}
}

// Item 8 (N5): a family this hadron does not know gets advice that can be followed: the accounts file cannot
// name a program for a family the loader refuses.
func TestDoctorAnUnknownFamilyIsNotToldToUseTheAccountsFile3286(t *testing.T) {
	newDoctorHost3236(t, true, "claude")
	out, code, _ := doctor3236(t, setup3236("SERVER", assignment3236("hadron-server/antonio", "llama", "")), hostArgs3286()...)
	if code != exitcode.Conflict {
		t.Fatalf("exit %d, want 5\n%s", code, out)
	}
	if !strings.Contains(out, "does not know the family llama") || strings.Contains(out, "name its `program`") {
		t.Errorf("say this hadron does not know the family; do not point at the accounts file:\n%s", out)
	}
}

// Item 3 (F6, F7): the middle case of the needed-program rule, a null reserved key, and an unavailable read.
func TestDoctorAnEntryWithNoProgramNeedsTheFamilyDefault3286(t *testing.T) {
	h := newDoctorHost3236(t, true) // no claude on PATH
	h.accounts(t, "accounts:\n  work:\n    family: claude\n", 0o600)
	out, code, _ := doctor3236(t, setup3236("SERVER", assignment3236("hadron-server/antonio", "claude", "work")), hostArgs3286()...)
	if code != exitcode.Conflict || !strings.Contains(out, "PROGRAM_NOT_ON_HOST") {
		t.Fatalf("exit %d: an entry with no program runs the family default, so a missing claude is MISSING:\n%s", code, out)
	}
	if line := factLine3236(out, "agent program (claude)"); !strings.Contains(line, "MISSING") {
		t.Errorf("claude is needed by that worker:\n%s", out)
	}
}

func TestDoctorANullReservedKeyIsAnsweredNeedsANewerHadron3286(t *testing.T) {
	h := newDoctorHost3236(t, true, "claude")
	h.accounts(t, "accounts:\n  work:\n    family: claude\n    tool:\n", 0o600)
	out, code, _ := doctor3236(t, nil)
	if code != exitcode.Conflict || !strings.Contains(out, "needs a newer hadron") {
		t.Errorf("exit %d: a null `tool:` is the reserved key, not a type error:\n%s", code, out)
	}
}

func TestDoctorAnUnavailableAssignmentsReadExits7WithTheLocalLines3286(t *testing.T) {
	newDoctorHost3236(t, true, "claude")
	out, code, _ := doctor3236(t, map[string]string{"TeamHostSetupWithProfile": gqlErrorJSON("GITHUB_UNAVAILABLE")}, hostArgs3286()...)
	if code != exitcode.Unavailable {
		t.Fatalf("exit %d, want 7\n%s", code, out)
	}
	if !strings.Contains(out, "assignments: could not be read") || !strings.Contains(out, "1.4.247") {
		t.Errorf("local lines then the failed read:\n%s", out)
	}
}

// Item 6 (N1, documentation arm): exit 10 dominates a failed assignments read, and all three places say so.
func TestDoctorExit10DominatesAFailedReadAndTheDocsSayIt3286(t *testing.T) {
	newDoctorHost3236(t, false, "claude") // no Orca
	out, code, _ := doctor3236(t, map[string]string{"TeamHostSetupWithProfile": gqlErrorJSON("FORBIDDEN")}, hostArgs3286()...)
	if code != exitcode.OrcaUnreachable {
		t.Fatalf("exit %d, want 10 to dominate the read's 8\n%s", code, out)
	}
	if !strings.Contains(doctorStderr3236.String(), "FORBIDDEN") {
		t.Errorf("the failed read's error is still printed on stderr, as documented: %q", doctorStderr3236.String())
	}
	read := func(path ...string) string {
		b, err := os.ReadFile(filepath.Join(path...))
		if err != nil {
			t.Fatal(err)
		}
		return strings.Join(strings.Fields(string(b)), " ")
	}
	f, helpOut := testFactory(t)
	root := NewRootCmd(f)
	root.SetOut(f.IOStreams.Out)
	root.SetArgs([]string{"team", "host", "doctor", "--help"})
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	usage := read("agentic", "agentic-usage.md")
	i := strings.Index(usage, "hadron team host doctor [--host")
	if i < 0 {
		t.Fatal("agentic-usage has no doctor entry")
	}
	entry := usage[i:]
	if j := strings.Index(entry, " hadron team host sweep"); j > 0 {
		entry = entry[:j]
	}
	for name, text := range map[string]string{
		"help":          strings.Join(strings.Fields(helpOut.String()), " "),
		"plan":          read("..", "..", "docs", "plans", "host-doctor-3236.md"),
		"agentic-usage": entry,
	} {
		if !strings.Contains(text, "exit 10 wins over a failed assignments read") && !strings.Contains(text, "Exit 10 wins over a failed assignments read") {
			t.Errorf("%s must state that exit 10 wins over a failed assignments read", name)
		}
	}
}

// Item 9 (N6), the lost half of the old source scan, as behaviour: a host file that would break any loader
// (bad app ref, missing key file, broken YAML) beside the working directory and the home directory changes
// nothing about a --host run: the only request is the assignments read, carrying only appRef and hostId, and
// the report never mentions the file.
func TestDoctorWithHostIgnoresAHostFile3286(t *testing.T) {
	h := newDoctorHost3236(t, true, "claude")
	bad := []byte("app: not-a-ref\nkey_file: /nonexistent/key-3286\nworkers: [broken\n")
	scratch := t.TempDir()
	t.Chdir(scratch)
	for _, p := range []string{filepath.Join(h.home, "host.yaml"), filepath.Join(scratch, "host.yaml")} {
		if err := os.WriteFile(p, bad, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	out, code, calls := doctor3236(t, setup3236("SERVER", assignment3236("hadron-server/antonio", "claude", "")), hostArgs3286()...)
	if code != 0 {
		t.Fatalf("a malformed host.yaml must not affect doctor: exit %d\n%s", code, out)
	}
	if len(*calls) != 1 || (*calls)[0].Op != "TeamHostSetupWithProfile" {
		t.Fatalf("the one request is the assignments read: %v", *calls)
	}
	for k := range (*calls)[0].Vars {
		if k != "appRef" && k != "hostId" {
			t.Errorf("the read carries only appRef and hostId, got %q", k)
		}
	}
	if strings.Contains(out, "host.yaml") || strings.Contains(out, "key-3286") {
		t.Errorf("the report never mentions a host file:\n%s", out)
	}
}
