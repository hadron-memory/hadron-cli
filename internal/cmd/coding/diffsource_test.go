package coding

import (
	"strings"
	"testing"
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
