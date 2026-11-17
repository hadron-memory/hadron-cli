package cmd

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/hadron-memory/hadron-cli/internal/exitcode"
)

// hadron-cli#1648: `team session shed --note-file` appends the worker's own
// narrative as `## Notes`, after Recovery prompt and before Transcript tail.

const shedNotesCap = 64 << 10

func shedNotesFile(t *testing.T, content []byte) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "notes.md")
	if err := os.WriteFile(p, content, 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func shedCreatedBody(t *testing.T, rig *shedRig) string {
	t.Helper()
	var in struct {
		Input struct{ Content string } `json:"input"`
	}
	if err := json.Unmarshal(rig.captured["CreateNode"], &in); err != nil {
		t.Fatalf("no CreateNode captured: %v", err)
	}
	return in.Input.Content
}

func shedStampExists() bool {
	_, err := os.Stat(filepath.Join(os.Getenv("HADRON_TEAM_GIT_DIR"), "hadron-team-shed.json"))
	return err == nil
}

func TestSessionShedNoteFileAppendsNotesAfterRecoveryPrompt(t *testing.T) {
	bindWorktree(t)
	url, rig := shedServer(t, nil)
	notes := "## In flight\n- cli#1648: keep café ✓ bytes exact\r\n\ttabbed  \n\nno trailing newline"
	if _, _, err := runShed(t, url, "--note-file", shedNotesFile(t, []byte(notes))); err != nil {
		t.Fatal(err)
	}
	body := shedCreatedBody(t, rig)
	rp, np := strings.Index(body, "## Recovery prompt"), strings.Index(body, "\n## Notes\n")
	if rp < 0 || np < rp {
		t.Fatalf("## Notes must follow ## Recovery prompt:\n%s", body)
	}
	if !strings.HasSuffix(body, "\n## Notes\n"+notes) {
		t.Errorf("the file's bytes must land unchanged and last (no tail):\n%q", body[np:])
	}
	if rig.count("CreateResumeClaim") != 1 {
		t.Errorf("a good notes file still ends with the claim: %v", rig.calls)
	}
}

func TestSessionShedNoteFileComposesWithTranscriptNotesFirst(t *testing.T) {
	bindWorktree(t)
	url, rig := shedServer(t, nil)
	tr := filepath.Join(t.TempDir(), "t.jsonl")
	if err := os.WriteFile(tr, []byte("the last transcript line\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := runShed(t, url, "--note-file", shedNotesFile(t, []byte("my narrative\n")), "--transcript", tr); err != nil {
		t.Fatal(err)
	}
	body := shedCreatedBody(t, rig)
	n, tl := strings.Index(body, "## Notes"), strings.Index(body, "## Transcript tail")
	if n < 0 || tl < 0 || n > tl {
		t.Errorf("both sections, notes before the tail:\n%s", body)
	}
}

// Without the flag the node is exactly what it was: no Notes heading at all.
func TestSessionShedWithoutNoteFileHasNoNotesSection(t *testing.T) {
	bindWorktree(t)
	url, rig := shedServer(t, nil)
	if _, _, err := runShed(t, url); err != nil {
		t.Fatal(err)
	}
	if body := shedCreatedBody(t, rig); strings.Contains(body, "## Notes") {
		t.Errorf("no flag, no section:\n%s", body)
	}
}

// The notes are part of the body the read-back compares: a server that stores
// anything else must stop the claim.
func TestSessionShedNoteFileReadBackCoversTheNotes(t *testing.T) {
	bindWorktree(t)
	url, rig := shedServer(t, map[string]string{
		"GetNode": `{"data":{"node":{"id":"nd1","urn":"hrn:node:a:b:c","loc":"{LOC}","name":"x","content":"stored without the notes"}}}`,
	})
	_, errOut, err := runShed(t, url, "--note-file", shedNotesFile(t, []byte("narrative")))
	if err != nil {
		t.Fatal(err)
	}
	if rig.count("CreateResumeClaim") != 0 || !strings.Contains(errOut, "read back") {
		t.Errorf("a body that differs must fail the read-back and write no claim: calls=%v stderr=%q", rig.calls, errOut)
	}
}

func TestSessionShedNoteFileCapBoundary(t *testing.T) {
	bindWorktree(t)
	url, rig := shedServer(t, nil)
	exact := strings.Repeat("a", shedNotesCap)
	if _, _, err := runShed(t, url, "--note-file", shedNotesFile(t, []byte(exact))); err != nil {
		t.Fatalf("exactly the cap is accepted: %v", err)
	}
	if !strings.HasSuffix(shedCreatedBody(t, rig), exact) || rig.count("CreateNode") != 1 {
		t.Errorf("65536 bytes must be stored whole: %v", rig.calls)
	}
	over := shedNotesFile(t, []byte(exact+"a"))
	_, _, err := runShed(t, url, "--note-file", over)
	if exitCodeFor(err) != exitcode.Usage || rig.count("CreateNode") != 1 {
		t.Errorf("65537 bytes is refused with the usage code and no new node: err=%v calls=%v", err, rig.calls)
	}
}

// The refusals: each is a usage exit with a fixed line naming the class, no
// node, no claim, no cooldown stamp, and neither the path nor the contents
// appear anywhere in the output.
func TestSessionShedNoteFileRefusals(t *testing.T) {
	secret := "TOPSECRETNOTE"
	dir := t.TempDir()
	type refusal struct {
		mk   func(t *testing.T) string
		want string // the fixed class line both modes print
	}
	cases := map[string]refusal{
		"missing":         {func(*testing.T) string { return filepath.Join(dir, "no-such-"+secret) }, "could not be opened"},
		"directory":       {func(*testing.T) string { return dir }, "not a regular file"},
		"empty":           {func(t *testing.T) string { return shedNotesFile(t, nil) }, "nothing but whitespace"},
		"whitespace only": {func(t *testing.T) string { return shedNotesFile(t, []byte(" \n\t \r\n")) }, "nothing but whitespace"},
		"over cap":        {func(t *testing.T) string { return shedNotesFile(t, []byte(secret+strings.Repeat("a", shedNotesCap))) }, "larger than 64 KiB"},
		"invalid utf-8":   {func(t *testing.T) string { return shedNotesFile(t, []byte(secret+"\xff\xfe")) }, "not valid UTF-8"},
		"unreadable": {func(t *testing.T) string {
			p := shedNotesFile(t, []byte(secret))
			if err := os.Chmod(p, 0); err != nil {
				t.Skip("no chmod")
			}
			if f, err := os.Open(p); err == nil { // running as root: mode is not enforced
				_ = f.Close()
				t.Skip("file mode not enforced for this user")
			}
			return p
		}, "could not be opened"},
	}
	for name, rc := range cases {
		t.Run(name, func(t *testing.T) {
			bindWorktree(t)
			url, rig := shedServer(t, nil)
			path := rc.mk(t)
			out, errOut, err := runShed(t, url, "--note-file", path)
			if exitCodeFor(err) != exitcode.Usage {
				t.Fatalf("manual refusal is a usage exit, got %v", err)
			}
			all := out + errOut + err.Error()
			if !strings.Contains(err.Error(), "--note-file") || !strings.Contains(err.Error(), rc.want) {
				t.Errorf("manual: the fixed line names the flag and the class %q, got %q", rc.want, err.Error())
			}
			for _, leak := range []string{secret, path, filepath.Base(path)} {
				if strings.Contains(all, leak) {
					t.Errorf("output leaks %q: %s", leak, all)
				}
			}
			if strings.Contains(strings.ToLower(all), "no such file") || strings.Contains(strings.ToLower(all), "permission denied") {
				t.Errorf("no raw OS error: %s", all)
			}
			if rig.count("CreateNode") != 0 || rig.count("CreateResumeClaim") != 0 || shedStampExists() {
				t.Errorf("a refusal writes no node, claim or stamp: calls=%v stamp=%v", rig.calls, shedStampExists())
			}
			// --auto never blocks: exit 0, one fixed stderr line, still nothing written.
			_, aErr, err := runShed(t, url, "--auto", "--note-file", path)
			if err != nil {
				t.Fatalf("--auto must exit 0: %v", err)
			}
			if n := strings.Count(strings.TrimSpace(aErr), "\n") + 1; strings.TrimSpace(aErr) == "" || n != 1 {
				t.Errorf("--auto: one fixed stderr line, got %q", aErr)
			}
			if !strings.Contains(aErr, rc.want) {
				t.Errorf("--auto: the fixed line names the class %q, got %q", rc.want, aErr)
			}
			for _, leak := range []string{secret, path, filepath.Base(path)} {
				if strings.Contains(aErr, leak) {
					t.Errorf("--auto stderr leaks %q: %s", leak, aErr)
				}
			}
			if rig.count("CreateNode") != 0 || rig.count("CreateResumeClaim") != 0 || shedStampExists() {
				t.Errorf("--auto refusal writes no node, claim or stamp: calls=%v", rig.calls)
			}
		})
	}
}

func TestSessionShedNoteFileEmptyFlagValueIsRefused(t *testing.T) {
	bindWorktree(t)
	url, rig := shedServer(t, nil)
	for _, args := range [][]string{{"--note-file="}, {"--note-file", ""}} {
		if _, _, err := runShed(t, url, args...); exitCodeFor(err) != exitcode.Usage || err == nil || !strings.Contains(err.Error(), "was empty") {
			t.Errorf("%q: an empty value is not an omitted flag, want usage 'was empty', got %v", args, err)
		}
	}
	if _, errOut, err := runShed(t, url, "--auto", "--note-file="); err != nil || !strings.Contains(errOut, "was empty") {
		t.Errorf("--auto: exit 0 with one stderr line, got err=%v stderr=%q", err, errOut)
	}
	if rig.count("CreateNode") != 0 || rig.count("CreateResumeClaim") != 0 || shedStampExists() {
		t.Errorf("nothing written: %v", rig.calls)
	}
}

// --print-prompt never opens, reads or validates the notes file.
func TestSessionShedPrintPromptNeverTouchesTheNotesFile(t *testing.T) {
	bindWorktree(t)
	url, rig := shedServer(t, nil)
	for _, args := range [][]string{
		{"--print-prompt", "--note-file", filepath.Join(t.TempDir(), "missing")},
		{"--print-prompt", "--note-file", t.TempDir()},
		{"--print-prompt", "--note-file="},
		{"--print-prompt", "--offline", "--note-file", filepath.Join(t.TempDir(), "missing")},
	} {
		out, errOut, err := runShed(t, url, args...)
		if err != nil || !strings.Contains(out, "whoami --check") || strings.TrimSpace(errOut) != "" {
			t.Errorf("%v: prints the prompt, silently: out=%q err=%v stderr=%q", args, out, err, errOut)
		}
	}
	if rig.count("CreateNode") != 0 || rig.count("CreateResumeClaim") != 0 || shedStampExists() {
		t.Errorf("--print-prompt writes nothing: %v", rig.calls)
	}
	// A notes file that would be refused must not be NAMED either: an
	// unreadable path under --print-prompt is not even opened (no refusal).
}

// --offline alone stays the existing usage refusal, flag or no flag.
func TestSessionShedOfflineWithNoteFileIsStillRefusedWithoutPrintPrompt(t *testing.T) {
	bindWorktree(t)
	url, _ := shedServer(t, nil)
	_, _, err := runShed(t, url, "--offline", "--note-file", shedNotesFile(t, []byte("n")))
	if exitCodeFor(err) != exitcode.Usage || !strings.Contains(err.Error(), "--offline only applies with --print-prompt") {
		t.Errorf("want the existing --offline refusal, got %v", err)
	}
}

// A skipped cooldown run, and the claim-only retry, never touch the notes file.
func TestSessionShedCooldownNeverReadsTheNotesFile(t *testing.T) {
	bindWorktree(t)
	url, rig := shedServer(t, map[string]string{"CreateResumeClaim": `{"errors":[{"message":"boom"}]}`})
	if _, _, err := runShed(t, url, "--auto"); err != nil {
		t.Fatal(err)
	}
	delete(rig.override, "CreateResumeClaim")
	bad := filepath.Join(t.TempDir(), "missing") // would be refused if read
	out, errOut, _ := runShed(t, url, "--auto", "--json", "--note-file", bad)
	if d := shedJSON(t, out); d.Status != "skipped" || d.Claim != "written" || strings.TrimSpace(errOut) != "" {
		t.Errorf("the claim-only retry does not read or validate the notes: out=%s stderr=%q", out, errOut)
	}
	out, errOut, _ = runShed(t, url, "--auto", "--json", "--note-file", bad)
	if d := shedJSON(t, out); d.Status != "skipped" || strings.TrimSpace(errOut) != "" {
		t.Errorf("a cooldown skip does not read the notes: out=%s stderr=%q", out, errOut)
	}
	if rig.count("CreateNode") != 1 {
		t.Errorf("no new node on either run: %v", rig.calls)
	}
}

func TestSessionShedNoteFileHelpStatesTheTerms(t *testing.T) {
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"team", "session", "shed", "--help"})
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	h := out.String()
	for _, w := range []string{"--note-file", "## Notes", "64 KiB", "NOT scrubbed", "whitespace"} {
		if !strings.Contains(h, w) {
			t.Errorf("help must state %q:\n%s", w, h)
		}
	}
	if !utf8.ValidString(h) {
		t.Error("help is UTF-8")
	}
}
