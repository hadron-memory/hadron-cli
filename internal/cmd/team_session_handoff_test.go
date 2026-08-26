package cmd

import (
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/cmdutil"
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

// A REFUSED END MUST NOT TAKE THE PROSE WITH IT (cor:agt:020:10).
//
// The spec refuses the END when the handoff write fails, precisely so a
// transient failure cannot destroy "prose that exists nowhere else … at the
// moment its author has stopped working and cannot be asked to retype it".
// That protects the SESSION. It does not protect the PROSE — and this client
// was the destroyer on one path: `--handoff -` drains stdin into memory, so a
// refused end discarded the only copy while the pipe was already consumed.
//
// The stdin case is therefore the one that matters, and it is the one driven
// here: after the failure there is nowhere else the text could have come from.
func TestSessionEndRescuesTheHandoffWhenTheEndFails(t *testing.T) {
	const prose = "Shipped #522. #489 is next. Do NOT re-run the register sweep."
	bindWorktree(t)
	gql, _ := captureGraphQL(t, map[string]string{
		"EndTeamSession": `{"errors":[{"message":"The handoff could not be written, so the session was NOT ended.",` +
			`"extensions":{"code":"HANDOFF_WRITE_FAILED","sessionId":"ses_1"}}]}`,
	})
	f, _ := testFactory(t)
	f.IOStreams.In = strings.NewReader(prose)
	errOut := captureErrOut(f)
	root := NewRootCmd(f)
	root.SetArgs([]string{"team", "session", "end", "--handoff", "-", "--server", gql.URL})

	err := root.Execute()
	if err == nil {
		t.Fatal("a failed end must still fail — rescuing the prose is not succeeding")
	}
	msg := errOut()

	// The path, and the prose actually at it. Printing a path to an empty or
	// absent file would be the reassurance-without-the-thing failure.
	path := handoffSpillPath(t, msg)
	saved, rerr := os.ReadFile(path) // #nosec G304 — path came from our own output
	if rerr != nil {
		t.Fatalf("the rescued handoff must exist at the path printed: %v", rerr)
	}
	if string(saved) != prose {
		t.Errorf("the rescued handoff must be the prose verbatim:\n got: %q\nwant: %q", saved, prose)
	}
	t.Cleanup(func() { _ = os.Remove(path) })

	// A ready-to-run retry, not an instruction to reconstruct one.
	if !strings.Contains(msg, "--handoff-file "+path) {
		t.Errorf("the retry must name the rescued file: %s", msg)
	}
	if !strings.Contains(msg, "--session ") {
		t.Errorf("the retry must name the session: %s", msg)
	}
	// The ORIGINAL server is carried. A retry that falls back to the default
	// backend can fail against the wrong deployment — or act on an unrelated
	// session if ids overlap between deployments.
	if !strings.Contains(msg, "--server ") {
		t.Errorf("the retry must target the same server the failed call did: %s", msg)
	}
	// Someone who has just been refused will hesitate to retry unless told.
	if !strings.Contains(msg, "cannot double-write") {
		t.Errorf("the retry-safety guarantee is the reason they will actually retry: %s", msg)
	}
	// And it must not claim the handoff landed.
	if strings.Contains(msg, "✓") {
		t.Errorf("nothing succeeded here: %s", msg)
	}
}

// No handoff, nothing at risk: a failing end must not litter a temp file or
// print a rescue nobody needs.
func TestSessionEndWithoutAHandoffRescuesNothing(t *testing.T) {
	bindWorktree(t)
	gql, _ := captureGraphQL(t, map[string]string{
		"EndTeamSession": `{"errors":[{"message":"boom","extensions":{"code":"INTERNAL_SERVER_ERROR"}}]}`,
	})
	f, _ := testFactory(t)
	errOut := captureErrOut(f)
	root := NewRootCmd(f)
	root.SetArgs([]string{"team", "session", "end", "--server", gql.URL})
	if err := root.Execute(); err == nil {
		t.Fatal("the end still failed")
	}
	if msg := errOut(); strings.Contains(msg, "Saved it to") {
		t.Errorf("there was no handoff to rescue: %s", msg)
	}
}

// handoffSpillPath pulls the rescued file's path out of the message.
func handoffSpillPath(t *testing.T, msg string) string {
	t.Helper()
	const marker = "Saved it to "
	i := strings.Index(msg, marker)
	if i < 0 {
		t.Fatalf("no rescue line in:\n%s", msg)
	}
	rest := msg[i+len(marker):]
	if j := strings.IndexAny(rest, "\n"); j >= 0 {
		rest = rest[:j]
	}
	return strings.TrimSpace(rest)
}

// captureErrOut swaps in a capturable stderr. testFactory keeps its own and
// returns only stdout, and the rescue notice is a DIAGNOSTIC accompanying an
// error — it belongs on stderr, so a --json consumer's document stays parseable
// while a human still sees where the prose went.
func captureErrOut(f *cmdutil.Factory) func() string {
	b := &strings.Builder{}
	f.IOStreams.ErrOut = b
	return b.String
}

// A SIGNED-OUT CALLER'S PIPED HANDOFF IS STILL RESCUED (PR #528 review,
// @codex P1, second round).
//
// An earlier revision built the client first so a signed-out caller was refused
// BEFORE their pipe was read — on the reasoning that not taking custody beats
// rescuing. Two things killed it, and the first is why this test now uses a
// REAL os.Pipe:
//
//   - Not reading is not the same as not losing. Returning without reading
//     closes the consumer end, so buffered prose is discarded and the producer
//     can take SIGPIPE. The previous version of this test asserted that a
//     strings.Reader's offset was unchanged, which proves an in-process reader
//     was not advanced and says NOTHING about pipeline teardown — a test
//     asserting less than it appeared to.
//   - It moved the exit codes (see the sibling test below).
//
// So the guarantee is now uniform: if you gave us a handoff and we did not
// record it, we saved it and said where — whatever failed.
func TestSessionEndRescuesAPipedHandoffWhenSignedOut(t *testing.T) {
	const prose = "Shipped #528. Do not re-run the register sweep."
	bindWorktree(t)
	gql, _ := captureGraphQL(t, endStubs())
	f, _ := testFactory(t)
	t.Setenv("HADRON_TOKEN", "") // signed out: GraphQLClient() fails before any request

	// A real OS pipe, not a strings.Reader: this is the shape the finding is
	// about, and the one where "the producer still has it" is not a property
	// anyone can rely on.
	r, w, perr := os.Pipe()
	if perr != nil {
		t.Fatal(perr)
	}
	go func() {
		_, _ = io.WriteString(w, prose)
		_ = w.Close()
	}()
	f.IOStreams.In = r
	t.Cleanup(func() { _ = r.Close() })

	errOut := captureErrOut(f)
	root := NewRootCmd(f)
	root.SetArgs([]string{"team", "session", "end", "--handoff", "-", "--server", gql.URL})

	err := root.Execute()
	if err == nil {
		t.Fatal("a signed-out end must fail")
	}
	// The auth failure is what the caller is told — the rescue must not
	// replace the cause with something local.
	if code := exitcode.FromError(err); code == exitcode.Usage {
		t.Errorf("a signed-out failure is not a usage error: exit %d (%v)", code, err)
	}
	msg := errOut()
	path := handoffSpillPath(t, msg)
	saved, rerr := os.ReadFile(path) // #nosec G304 — path came from our own output
	if rerr != nil {
		t.Fatalf("the piped handoff must survive a pre-request failure: %v", rerr)
	}
	if string(saved) != prose {
		t.Errorf("rescued prose:\n got: %q\nwant: %q", saved, prose)
	}
	t.Cleanup(func() { _ = os.Remove(path) })
}

// …but a LOCAL usage error must still be a usage error, signed out or not
// (PR #528 review, @codex P2).
//
// Validation lives in resolveHandoff, so authenticating first reported
// AuthRequired for an explicit empty --handoff — regressing a documented exit
// code that agents branch on. The suite could not see it, because testFactory
// is always signed in: the regression only existed on the path where the auth
// preflight failed.
func TestSessionEndEmptyHandoffIsUsageEvenWhenSignedOut(t *testing.T) {
	bindWorktree(t)
	gql, _ := captureGraphQL(t, endStubs())
	f, _ := testFactory(t)
	t.Setenv("HADRON_TOKEN", "") // signed out, and it must NOT be what we report
	errOut := captureErrOut(f)
	root := NewRootCmd(f)
	root.SetArgs([]string{"team", "session", "end", "--handoff", "", "--server", gql.URL})

	err := root.Execute()
	if code := exitcode.FromError(err); code != exitcode.Usage {
		t.Errorf("an explicit empty handoff is a usage error (exit %d), got exit %d: %v",
			exitcode.Usage, code, err)
	}
	// Nothing was taken, so nothing is rescued — a spill here would litter a
	// file for prose that never existed.
	if msg := errOut(); strings.Contains(msg, "Saved it to") {
		t.Errorf("there was no handoff to rescue: %s", msg)
	}
}

// The rescue must survive the failures that happen BEFORE a session is even
// resolved (PR #528 review, @codex, third round).
//
// `readBinding` and `checkBindingServer` return for ordinary reasons — no
// binding in this worktree, an unreadable one, a binding whose server disagrees
// with --server — and all three sat ABOVE the drain. On a real pipe each closes
// the consumer end with the prose still in it, so the invariant written one
// commit earlier ("if you gave us a handoff and we did not record it, we saved
// it and said where") was untrue for three returns above the line that stated
// it.
func TestSessionEndRescuesAHandoffWhenThereIsNoBinding(t *testing.T) {
	const prose = "Everything that mattered this stint, and no binding to end."
	teamGitDir(t) // a worktree with NO binding written
	gql, _ := captureGraphQL(t, endStubs())
	f, _ := testFactory(t)
	r, w, perr := os.Pipe()
	if perr != nil {
		t.Fatal(perr)
	}
	go func() {
		_, _ = io.WriteString(w, prose)
		_ = w.Close()
	}()
	f.IOStreams.In = r
	t.Cleanup(func() { _ = r.Close() })
	errOut := captureErrOut(f)
	root := NewRootCmd(f)
	root.SetArgs([]string{"team", "session", "end", "--handoff", "-", "--server", gql.URL})

	err := root.Execute()
	if err == nil {
		t.Fatal("there is no session to end")
	}
	msg := errOut()
	path := handoffSpillPath(t, msg)
	saved, rerr := os.ReadFile(path) // #nosec G304 — path came from our own output
	if rerr != nil {
		t.Fatalf("the prose must survive a failure that precedes the session: %v", rerr)
	}
	if string(saved) != prose {
		t.Errorf("rescued prose:\n got: %q\nwant: %q", saved, prose)
	}
	t.Cleanup(func() { _ = os.Remove(path) })
	// No session id was ever resolved, so the retry must SAY the caller has to
	// supply one rather than printing a command that cannot work.
	if !strings.Contains(msg, "--session <id>") {
		t.Errorf("with no session resolved the retry must ask for the id: %s", msg)
	}
	if strings.Contains(msg, "--session ''") {
		t.Errorf("a quoted empty session id is a command that cannot run: %s", msg)
	}
}

// The retry line advertises itself as ready-to-run, so a temp directory with a
// space in it must not split it into extra arguments (PR #528 review, @codex P2
// + @copilot). TMPDIR is what os.CreateTemp honours, so the hazard is drivable.
func TestSessionEndRetryCommandSurvivesASpacyTempDir(t *testing.T) {
	spacy := filepath.Join(t.TempDir(), "Application Support")
	if err := os.MkdirAll(spacy, 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("TMPDIR", spacy)
	bindWorktree(t)
	gql, _ := captureGraphQL(t, map[string]string{
		"EndTeamSession": `{"errors":[{"message":"nope","extensions":{"code":"HANDOFF_WRITE_FAILED"}}]}`,
	})
	f, _ := testFactory(t)
	f.IOStreams.In = strings.NewReader("prose")
	errOut := captureErrOut(f)
	root := NewRootCmd(f)
	root.SetArgs([]string{"team", "session", "end", "--handoff", "-", "--server", gql.URL})
	if err := root.Execute(); err == nil {
		t.Fatal("the end still failed")
	}
	msg := errOut()
	if !strings.Contains(msg, spacy) {
		t.Fatalf("the spill must land in TMPDIR for this test to mean anything: %s", msg)
	}
	// The retry's --handoff-file argument must be ONE argument. Quoted is the
	// only way that is true for a path containing a space.
	retry := ""
	for _, line := range strings.Split(msg, "\n") {
		if strings.Contains(line, "--handoff-file") {
			retry = line
		}
	}
	if retry == "" {
		t.Fatalf("no retry line: %s", msg)
	}
	if !strings.Contains(retry, "--handoff-file '") {
		t.Errorf("a path with a space must be quoted or the retry is not runnable: %s", retry)
	}
}

// When the spill ITSELF fails, the rescue must report that and still return the
// original error (PR #528 review, @copilot's cleanup point taken one step out).
//
// Swapping the server's refusal for a local file-write error would hide the
// thing the caller has to act on — and telling them nothing at all would leave
// them believing the prose was saved somewhere. The one thing that must survive
// is the instruction to copy it out of the terminal while they still can.
func TestSessionEndSaysSoWhenItCannotEvenSaveTheHandoff(t *testing.T) {
	// TMPDIR pointed at a child of a regular FILE, so os.CreateTemp fails with
	// ENOTDIR. Deliberately not a 0500 directory (PR #528 review, @codex):
	// root ignores permission bits, so under a root-run container CreateTemp
	// would succeed, the rescue would work, and this test would fail claiming
	// the diagnostics were missing — a fixture that cannot produce the
	// condition it is named for. ENOTDIR is privilege-independent.
	notADir := filepath.Join(t.TempDir(), "regular-file")
	if err := os.WriteFile(notADir, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("TMPDIR", filepath.Join(notADir, "nope"))
	bindWorktree(t)
	gql, _ := captureGraphQL(t, map[string]string{
		"EndTeamSession": `{"errors":[{"message":"handoff write failed upstream",` +
			`"extensions":{"code":"HANDOFF_WRITE_FAILED"}}]}`,
	})
	f, _ := testFactory(t)
	f.IOStreams.In = strings.NewReader("prose that is about to be lost")
	errOut := captureErrOut(f)
	root := NewRootCmd(f)
	root.SetArgs([]string{"team", "session", "end", "--handoff", "-", "--server", gql.URL})

	err := root.Execute()
	if err == nil {
		t.Fatal("the end failed and must still fail")
	}
	// The ORIGINAL cause survives — not a file-write error the caller cannot
	// act on.
	if !strings.Contains(err.Error(), "handoff write failed upstream") {
		t.Errorf("the local failure must not mask the server's: %v", err)
	}
	msg := errOut()
	if !strings.Contains(msg, "could not be saved locally") {
		t.Errorf("a failed rescue must say so rather than stay quiet: %s", msg)
	}
	if !strings.Contains(msg, "Copy it out of your terminal") {
		t.Errorf("the only remaining remedy must be stated: %s", msg)
	}
	// It must NOT print a path, since there is no file at one.
	if strings.Contains(msg, "Saved it to") {
		t.Errorf("nothing was saved; claiming a path is the worst outcome here: %s", msg)
	}
}

// A SERVER MISMATCH MUST NOT PRINT A RETRY THAT PERFORMS THE REFUSED ACT
// (PR #528 review, @codex, fourth round).
//
// checkBindingServer refuses when the binding belongs to server A and this
// invocation resolved server B. The rescue then prints a retry — and that retry
// carries an explicit --session, which BYPASSES this very check on the next
// run. Composed from the invocation, it would name server B: pasting it would
// send the handoff to the wrong deployment, and to an unrelated session there
// if ids overlap, which is exactly what the refusal existed to stop.
//
// A consequence of the --server fix one round earlier, not of the original
// defect: adding the server to the retry is what made naming the WRONG one
// possible.
func TestSessionEndMismatchRetryNamesTheBindingsServer(t *testing.T) {
	const bindingServer = "https://hadron.example.invalid"
	// A binding that names a DIFFERENT server, written explicitly: the shared
	// fixture carries none, and a test that skipped when no mismatch occurred
	// would assert nothing while looking covered.
	dir := teamGitDir(t)
	mismatched := strings.Replace(bindingWithTeamFixture, `"appBound":true,`,
		`"appBound":true,"server":"`+bindingServer+`",`, 1)
	if err := os.WriteFile(filepath.Join(dir, "hadron-team-session.json"),
		[]byte(mismatched), 0o600); err != nil {
		t.Fatal(err)
	}
	gql, _ := captureGraphQL(t, endStubs())
	f, _ := testFactory(t)
	f.IOStreams.In = strings.NewReader("prose worth keeping")
	errOut := captureErrOut(f)
	root := NewRootCmd(f)
	// --server is the OTHER one: this is the mismatch checkBindingServer refuses.
	root.SetArgs([]string{"team", "session", "end", "--handoff", "-", "--server", gql.URL})

	err := root.Execute()
	if err == nil {
		t.Fatal("a binding/server mismatch must be refused")
	}
	msg := errOut()
	if !strings.Contains(msg, "Saved it to") {
		t.Fatalf("a refused mismatch must still rescue the prose: %s\n%v", msg, err)
	}
	// The rejected server must NOT be the one the retry hands back...
	if strings.Contains(msg, gql.URL) {
		t.Errorf("the retry names the server that was just refused, and --session bypasses the check: %s", msg)
	}
	// ...and the BINDING's server must be.
	if !strings.Contains(msg, bindingServer) {
		t.Errorf("the retry must target the binding's own server: %s", msg)
	}
}

// The rescue must not claim what it cannot know (PR #528 review, @codex).
//
// A refusal the server ANSWERED means nothing was committed. A TRANSPORT
// failure means the end may have succeeded with only the reply lost — so
// "was NOT recorded" there is a claim about server state the client does not
// have, and it is worse than vague: a later retry failing would look like
// confirmation of data loss.
//
// The remedy is identical either way (one stint, one record), so only the
// wording moves. The comment acknowledging this ambiguity was already two lines
// above the message asserting the opposite.
func TestSessionEndRescueDoesNotClaimMoreThanItKnows(t *testing.T) {
	prose := "prose whose fate is genuinely unknown"

	t.Run("server answered: the refusal is a fact", func(t *testing.T) {
		bindWorktree(t)
		gql, _ := captureGraphQL(t, map[string]string{
			"EndTeamSession": `{"errors":[{"message":"refused","extensions":{"code":"HANDOFF_WRITE_FAILED"}}]}`,
		})
		f, _ := testFactory(t)
		f.IOStreams.In = strings.NewReader(prose)
		errOut := captureErrOut(f)
		root := NewRootCmd(f)
		root.SetArgs([]string{"team", "session", "end", "--handoff", "-", "--server", gql.URL})
		if err := root.Execute(); err == nil {
			t.Fatal("the end failed")
		}
		if msg := errOut(); !strings.Contains(msg, "was NOT recorded") {
			t.Errorf("an answered refusal IS a fact and should be stated as one: %s", msg)
		}
	})

	t.Run("transport failure: the outcome is unknown", func(t *testing.T) {
		bindWorktree(t)
		f, _ := testFactory(t)
		f.IOStreams.In = strings.NewReader(prose)
		errOut := captureErrOut(f)
		root := NewRootCmd(f)
		// A server that is not there: the request never gets an answer, so
		// whether it was applied is unknowable from here.
		root.SetArgs([]string{"team", "session", "end", "--handoff", "-", "--server", "http://127.0.0.1:1"})
		if err := root.Execute(); err == nil {
			t.Fatal("the end failed")
		}
		msg := errOut()
		if !strings.Contains(msg, "may not have been recorded") {
			t.Errorf("a lost reply is not proof of a lost write: %s", msg)
		}
		if strings.Contains(msg, "was NOT recorded") {
			t.Errorf("this asserts server state the client cannot see: %s", msg)
		}
		// The prose is still rescued, and retrying is still safe — only the
		// claim changed.
		if !strings.Contains(msg, "Saved it to") || !strings.Contains(msg, "cannot double-write") {
			t.Errorf("the remedy is the same whether or not the outcome is known: %s", msg)
		}
	})
}

// A --summary the caller explicitly asked for must survive into the retry
// (PR #528 review, @codex). Dropping it lets a pasted command succeed while
// silently leaving the label unset.
func TestSessionEndRetryCarriesTheSummary(t *testing.T) {
	bindWorktree(t)
	gql, _ := captureGraphQL(t, map[string]string{
		"EndTeamSession": `{"errors":[{"message":"nope","extensions":{"code":"HANDOFF_WRITE_FAILED"}}]}`,
	})
	f, _ := testFactory(t)
	f.IOStreams.In = strings.NewReader("prose")
	errOut := captureErrOut(f)
	root := NewRootCmd(f)
	root.SetArgs([]string{"team", "session", "end", "--handoff", "-",
		"--summary", "closed out #522", "--server", gql.URL})
	if err := root.Execute(); err == nil {
		t.Fatal("the end failed")
	}
	msg := errOut()
	if !strings.Contains(msg, "--summary ") {
		t.Errorf("the retry must reproduce the whole invocation: %s", msg)
	}
	// Quoted, because it contains spaces — otherwise the retry is not runnable.
	if !strings.Contains(msg, "--summary 'closed out #522'") {
		t.Errorf("a summary with spaces must be quoted: %s", msg)
	}
}
