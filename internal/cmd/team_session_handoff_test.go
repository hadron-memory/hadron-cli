package cmd

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/exitcode"
)

// `session end --handoff` is the #1029 continuity record (hadron-cli#505). The
// CLI track could not write one at all: `session end` took `--summary`, which
// writes the field nothing reads back — the write-only field #1029 was filed to
// FIX. So the CLI offered the one that goes nowhere and lacked the one that
// works, and five coordinator sessions ended without a record.

func endStubs() map[string]string {
	return map[string]string{
		"EndTeamSession": `{"data":{"endSession":` + endedSessionJSON + `}}`,
	}
}

func bindWorktree(t *testing.T) {
	t.Helper()
	dir := teamGitDir(t)
	if err := os.WriteFile(filepath.Join(dir, "hadron-team-session.json"),
		[]byte(bindingWithTeamFixture), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestSessionEndSendsTheHandoff(t *testing.T) {
	bindWorktree(t)
	gql, captured := captureGraphQL(t, endStubs())
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"team", "session", "end",
		"--handoff", "Shipped #504. #505 open. Do not re-run the register sweep.",
		"--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	var vars map[string]any
	_ = json.Unmarshal(captured["EndTeamSession"], &vars)
	if vars["handoff"] != "Shipped #504. #505 open. Do not re-run the register sweep." {
		t.Errorf("the handoff must reach the wire: %v", vars)
	}
}

// A handoff is prose of real length, so shell-quoting a paragraph is its own
// hazard — the file and stdin forms mirror `chat post --body-file` / `-`.
func TestSessionEndReadsTheHandoffFromAFileAndStdin(t *testing.T) {
	const text = "Line one.\nLine two, with \"quotes\" and $shell $vars.\n"

	t.Run("--handoff-file", func(t *testing.T) {
		bindWorktree(t)
		path := filepath.Join(t.TempDir(), "handoff.md")
		if err := os.WriteFile(path, []byte(text), 0o600); err != nil {
			t.Fatal(err)
		}
		gql, captured := captureGraphQL(t, endStubs())
		f, _ := testFactory(t)
		root := NewRootCmd(f)
		root.SetArgs([]string{"team", "session", "end", "--handoff-file", path, "--server", gql.URL})
		if err := root.Execute(); err != nil {
			t.Fatalf("execute: %v", err)
		}
		var vars map[string]any
		_ = json.Unmarshal(captured["EndTeamSession"], &vars)
		if vars["handoff"] != text {
			t.Errorf("the file's contents must survive verbatim: %q", vars["handoff"])
		}
	})

	t.Run("--handoff -", func(t *testing.T) {
		bindWorktree(t)
		gql, captured := captureGraphQL(t, endStubs())
		f, _ := testFactory(t)
		f.IOStreams.In = strings.NewReader(text)
		root := NewRootCmd(f)
		root.SetArgs([]string{"team", "session", "end", "--handoff", "-", "--server", gql.URL})
		if err := root.Execute(); err != nil {
			t.Fatalf("execute: %v", err)
		}
		var vars map[string]any
		_ = json.Unmarshal(captured["EndTeamSession"], &vars)
		if vars["handoff"] != text {
			t.Errorf("stdin must survive verbatim: %q", vars["handoff"])
		}
	})
}

// Ending WITHOUT a handoff is a normal thing to do, so the variable must be
// OMITTED rather than sent as null — omitted is "preserve/none" on this server
// and null is "clear" (CLAUDE.md wire semantics). This is also what keeps the
// no-handoff path identical to its behaviour before the flag existed.
func TestSessionEndOmitsAnAbsentHandoff(t *testing.T) {
	bindWorktree(t)
	gql, captured := captureGraphQL(t, endStubs())
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"team", "session", "end", "--summary", "did some work", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	var vars map[string]any
	_ = json.Unmarshal(captured["EndTeamSession"], &vars)
	if _, present := vars["handoff"]; present {
		t.Errorf("an unset handoff must be omitted, not null: %v", vars)
	}
	// …and summary still works, unchanged. Keeping both is deliberate:
	// collapsing them is a decision about #1029's shape, not this flag's.
	if vars["summary"] != "did some work" {
		t.Errorf("--summary must still be sent: %v", vars)
	}
}

// An EXPLICIT empty handoff is a mistake, not a request to skip it: somebody
// who typed --handoff "" or pointed at an empty file meant to write something,
// and silently ending with no record is what #1029 exists to prevent.
func TestSessionEndRefusesAnEmptyHandoff(t *testing.T) {
	for _, tc := range []struct {
		name string
		args []string
		file string
	}{
		{name: "empty --handoff", args: []string{"--handoff", "   "}},
		{name: "empty --handoff-file", file: "empty.md"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			bindWorktree(t)
			args := tc.args
			if tc.file != "" {
				path := filepath.Join(t.TempDir(), tc.file)
				if err := os.WriteFile(path, []byte("\n\n"), 0o600); err != nil {
					t.Fatal(err)
				}
				args = []string{"--handoff-file", path}
			}
			gql, captured := captureGraphQL(t, endStubs())
			f, _ := testFactory(t)
			root := NewRootCmd(f)
			root.SetArgs(append([]string{"team", "session", "end"}, append(args, "--server", gql.URL)...))
			err := root.Execute()
			if code := exitcode.FromError(err); code != exitcode.Usage {
				t.Errorf("exit %d, want %d (Usage); err: %v", code, exitcode.Usage, err)
			}
			// It must refuse BEFORE ending the session — otherwise the caller
			// loses the session AND the record, and the context that could
			// compose one is gone.
			if _, called := captured["EndTeamSession"]; called {
				t.Error("the session must not end when the handoff was refused")
			}
			if err == nil || !strings.Contains(err.Error(), "omit --handoff") {
				t.Errorf("the refusal must name the deliberate alternative: %v", err)
			}
		})
	}
}

// A missing handoff-file is the caller's typo, not a server problem.
func TestSessionEndUnreadableHandoffFileIsUsage(t *testing.T) {
	bindWorktree(t)
	gql, captured := captureGraphQL(t, endStubs())
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"team", "session", "end", "--handoff-file", "/nope/missing.md", "--server", gql.URL})
	if code := exitcode.FromError(root.Execute()); code != exitcode.Usage {
		t.Errorf("a missing file is a usage error, got exit %d", code)
	}
	if _, called := captured["EndTeamSession"]; called {
		t.Error("must not end the session when the handoff could not be read")
	}
}

// The help has to say WHICH of the two the next driver receives. Before this
// change it implied `--summary` was the one that mattered, which is precisely
// backwards — that field is write-only.
func TestSessionEndHelpSaysWhichOneTheNextDriverSees(t *testing.T) {
	buf := &strings.Builder{}
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetOut(buf)
	root.SetArgs([]string{"team", "session", "end", "--help"})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	help := buf.String()
	for _, want := range []string{
		"WHAT THE NEXT DRIVER READS", // --handoff
		"NEXT DRIVER NEVER SEES IT",  // --summary
		"HANDOFF_WRITE_FAILED",       // the refusal, and that it does not end anyway
		"follow the NAME",            // it may be a colleague who receives it
	} {
		if !strings.Contains(help, want) {
			t.Errorf("session end help must carry %q:\n%s", want, help)
		}
	}
}
