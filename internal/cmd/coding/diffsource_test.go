package coding

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/exitcode"
)

func TestPathsFromDiffReadsBothHeaderForms(t *testing.T) {
	diff := `diff --git a/internal/api/errors.go b/internal/api/errors.go
index 111..222 100644
--- a/internal/api/errors.go
+++ b/internal/api/errors.go
@@ -1 +1 @@
-old
+new
diff --git a/lib/l10n/app_en.arb b/lib/l10n/app_en.arb
--- a/lib/l10n/app_en.arb
+++ b/lib/l10n/app_en.arb
@@ -1 +1 @@
-a
+b
`
	got, err := pathsFromDiff(strings.NewReader(diff))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	want := []string{"internal/api/errors.go", "lib/l10n/app_en.arb"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("paths = %v, want %v", got, want)
	}
}

// /dev/null is how a diff spells "this side does not exist" for an add or a
// delete. Taken literally it becomes a changed file called dev/null, which a
// pattern could then match — a phantom entry in the evidence.
func TestPathsFromDiffIgnoresDevNull(t *testing.T) {
	diff := `diff --git a/internal/cmd/new.go b/internal/cmd/new.go
new file mode 100644
--- /dev/null
+++ b/internal/cmd/new.go
@@ -0,0 +1 @@
+package cmd
`
	got, err := pathsFromDiff(strings.NewReader(diff))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	for _, p := range got {
		if strings.Contains(p, "dev/null") {
			t.Errorf("/dev/null must not appear as a changed file: %v", got)
		}
	}
	if len(got) != 1 || got[0] != "internal/cmd/new.go" {
		t.Errorf("paths = %v, want just the added file", got)
	}
}

// A --no-prefix diff carries no a//b/ prefixes; a forge's export may carry only
// the +++ line. Both are read, because a diff the command cannot parse reports
// FEWER changed files, and fewer files means fewer matched checks — the silent
// undercount direction.
func TestPathsFromDiffHandlesNoPrefixAndPlusOnly(t *testing.T) {
	got, err := pathsFromDiff(strings.NewReader("diff --git internal/x.go internal/x.go\n+++ internal/y.go\n"))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if strings.Join(got, ",") != "internal/x.go,internal/y.go" {
		t.Errorf("paths = %v", got)
	}
}

// A long line must not truncate the file list. bufio.Scanner's default 64K
// limit errors mid-read, and the failure lands on the SHORT side: a minified
// asset elsewhere in the diff would silently cost you checks.
func TestPathsFromDiffSurvivesAVeryLongLine(t *testing.T) {
	long := "+" + strings.Repeat("x", 200_000)
	diff := "+++ b/first.go\n" + long + "\n+++ b/second.go\n"
	got, err := pathsFromDiff(strings.NewReader(diff))
	if err != nil {
		t.Fatalf("a wide line must not fail the parse: %v", err)
	}
	if len(got) != 2 {
		t.Errorf("paths = %v, want both files despite the long line", got)
	}
}

// nextOffset distinguishes "that is everything" from "there is more" without
// making the caller compare counts — and it is nil in the exact-fit case, which
// is the one a naive `offset+limit < total` gets wrong.
func TestPageChecksReportsWhereToResume(t *testing.T) {
	mk := func(n int) []runCheckDTO {
		out := make([]runCheckDTO, n)
		for i := range out {
			out[i].Loc = string(rune('a' + i))
		}
		return out
	}
	for _, tc := range []struct {
		name          string
		total         int
		limit, offset int
		wantLen       int
		wantNext      *int
	}{
		{"no limit returns everything", 5, 0, 0, 5, nil},
		{"a limit shorter than the set resumes", 5, 2, 0, 2, intPtr(2)},
		{"an offset advances", 5, 2, 2, 2, intPtr(4)},
		{"an exact fit does not promise more", 5, 3, 2, 3, nil},
		{"an offset past the end is empty, not an error", 5, 0, 9, 0, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, next := pageChecks(mk(tc.total), tc.limit, tc.offset)
			if len(got) != tc.wantLen {
				t.Errorf("returned %d, want %d", len(got), tc.wantLen)
			}
			switch {
			case tc.wantNext == nil && next != nil:
				t.Errorf("nextOffset = %d, want nil (nothing was withheld)", *next)
			case tc.wantNext != nil && next == nil:
				t.Errorf("nextOffset = nil, want %d", *tc.wantNext)
			case tc.wantNext != nil && *next != *tc.wantNext:
				t.Errorf("nextOffset = %d, want %d", *next, *tc.wantNext)
			}
		})
	}
}

func intPtr(i int) *int { return &i }

// PATHS WITH SPACES AND NON-ASCII BYTES, from @codex on #562.
//
// Every one of these fails in the same direction: a path the parser mangles is a
// file it does not know changed, so a pattern like `*.go` stops matching and the
// check is EXCLUDED. The mangling is silent and the review still reports success.
func TestPathsFromDiffHandlesSpacedAndQuotedNames(t *testing.T) {
	for _, tc := range []struct {
		name, diff, want string
	}{
		{
			// `diff --git a/x b/y` separates its two paths with a space and
			// nothing says which is which, so a spaced name cannot be split
			// there. The +++ line carries it unambiguously.
			name: "a filename with spaces",
			diff: "diff --git a/file with space.go b/file with space.go\n" +
				"--- a/file with space.go\n+++ b/file with space.go\n",
			want: "file with space.go",
		},
		{
			// core.quotePath: `ü.go` arrives C-quoted, and the quoted form does
			// not end in `.go`.
			name: "a C-quoted non-ASCII filename",
			diff: "diff --git \"a/\\303\\274.go\" \"b/\\303\\274.go\"\n" +
				"--- \"a/\\303\\274.go\"\n+++ \"b/\\303\\274.go\"\n",
			want: "ü.go",
		},
		{
			// plain `diff -u` appends a tab and a timestamp
			name: "a tab-terminated timestamp is not part of the path",
			diff: "+++ b/internal/x.go\t2026-09-06 10:00:00.000000000 +0000\n",
			want: "internal/x.go",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := pathsFromDiff(strings.NewReader(tc.diff))
			if err != nil {
				t.Fatalf("parse: %v", err)
			}
			var found bool
			for _, p := range got {
				if p == tc.want {
					found = true
				}
			}
			if !found {
				t.Errorf("paths = %q, want one of them to be %q — a mangled path is a file we do not know changed",
					got, tc.want)
			}
		})
	}
}

// The mangled forms must not survive as phantom entries either: `file` and
// `with` are not files that changed, and a pattern could match one.
func TestPathsFromDiffDoesNotEmitFragmentsOfASpacedName(t *testing.T) {
	got, err := pathsFromDiff(strings.NewReader(
		"diff --git a/file with space.go b/file with space.go\n+++ b/file with space.go\n"))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	for _, p := range got {
		if p == "file" || p == "with" {
			t.Errorf("a fragment of a spaced path must not appear as a changed file: %q", got)
		}
	}
}

func TestUnquoteGitPath(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{`b/internal/x.go`, "internal/x.go"},
		{`a/internal/x.go`, "internal/x.go"},
		{`"b/\303\274.go"`, "ü.go"},
		{`"a/file with \"quote\".go"`, `file with "quote".go`},
		// Unparseable stays as-is rather than vanishing: a name that matches
		// nothing is better than a file missing from the set entirely.
		{`"b/unterminated`, `"b/unterminated`},
	} {
		if got := unquoteGitPath(tc.in); got != tc.want {
			t.Errorf("unquoteGitPath(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// AN UNDISCOVERABLE DEFAULT BRANCH REFUSES, rather than quietly becoming HEAD
// (@codex P1 on #562).
//
// The fixture is the ordinary case that hits it: a repository based on `master`
// with no remote — no `origin/HEAD`, no `origin/main`, no `main`. Falling back
// to `git diff HEAD` there looks harmless and is the worst option available:
// in a CLEAN worktree it diffs nothing, so every path-scoped check is excluded
// and the review reports success having examined an empty change set.
//
// Refusing costs that caller one flag. Guessing costs them the checks they came
// for, and says nothing.
func TestChangedFilesRefusesWhenNoBaseCanBeDiscovered(t *testing.T) {
	dir := t.TempDir()
	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@x",
			"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@x")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v (%s)", args, err, out)
		}
	}
	run("init", "-q", "--initial-branch=master", ".")
	run("commit", "-q", "--allow-empty", "-m", "base")

	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(wd) })

	_, _, err = changedFiles(context.Background(), "", "")
	if err == nil {
		t.Fatal("with no discoverable base, the command must refuse rather than diff HEAD")
	}
	if got := exitcode.FromError(err); got != exitcode.Usage {
		t.Errorf("exit code = %d, want %d (Usage)", got, exitcode.Usage)
	}
	// The refusal has to be actionable — the caller's fix is one flag.
	for _, want := range []string{"--base", "--diff"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal must name %q as the remedy: %v", want, err)
		}
	}
	// An EXPLICIT base still works in the same repo, so the refusal is about
	// discovery and not about the repository being unusable.
	if _, base, err := changedFiles(context.Background(), "master", ""); err != nil {
		t.Errorf("an explicit base must still work: %v", err)
	} else if base != "master" {
		t.Errorf("base = %q, want master", base)
	}
}

// The GIT path must return names verbatim too, and this test exists because a
// mutation found it missing: dropping `-z` left the whole suite GREEN.
//
// Without -z, git C-quotes anything with a space or a non-ASCII byte
// (core.quotePath), so `ü.go` arrives as `"\303\274.go"` — which no longer ends
// in `.go`, so `*.go` stops matching and the check is silently excluded. The
// only reason I knew the fix worked was a manual run, which is not a guard.
func TestChangedFilesReturnsAwkwardNamesVerbatim(t *testing.T) {
	dir := t.TempDir()
	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@x",
			"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@x")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v (%s)", args, err, out)
		}
	}
	run("init", "-q", "--initial-branch=main", ".")
	run("commit", "-q", "--allow-empty", "-m", "base")
	// core.quotePath defaults to true, and this test only means something with
	// the default — set it explicitly so a developer's global config cannot
	// make the assertion pass for the wrong reason.
	run("config", "core.quotePath", "true")
	for _, name := range []string{"ü.go", "file with space.go"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("package x\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(wd) })

	files, _, err := changedFiles(context.Background(), "HEAD", "")
	if err != nil {
		t.Fatalf("changedFiles: %v", err)
	}
	got := strings.Join(files, "|")
	for _, want := range []string{"ü.go", "file with space.go"} {
		if !strings.Contains(got, want) {
			t.Errorf("changed files %q must contain %q verbatim — a quoted name matches no pattern and excludes its checks",
				files, want)
		}
	}
	// And the quoted spelling must not be there instead.
	if strings.Contains(got, `\303\274`) {
		t.Errorf("a C-quoted path leaked into the change set: %q", files)
	}
}
