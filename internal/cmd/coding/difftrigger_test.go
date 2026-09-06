package coding

import (
	"strings"
	"testing"
)

// EXCLUSION IS THE ONLY BUCKET THAT REMOVES ANYTHING, so it is the one these
// tests are really about. A check wrongly matched costs a skim; a check wrongly
// excluded ships the defect, and the reviewer never learns it existed.
func TestClassifyNeverExcludesWithoutPositiveEvidence(t *testing.T) {
	for _, tc := range []struct {
		name    string
		trigger string
		content string
		files   []string
		want    verdict
	}{
		{
			name:    "a check naming no paths is undecided, whatever changed",
			trigger: "Applies when a refusal's wording changes",
			content: "> **Scope.** Run this when a refusal is reworded.",
			files:   []string{"internal/api/errors.go"},
			want:    verdictUndecided,
		},
		{
			// The bucket that must never shrink by accident: an EMPTY diff
			// cannot rule anything out either. A check that names no paths is
			// still undecided against no files at all.
			name:    "an empty diff does not exclude a path-less check",
			trigger: "Applies when a guard is verified by mutation",
			content: "> **Scope.** Run this whenever a guard is proven by mutation.",
			files:   nil,
			want:    verdictUndecided,
		},
		{
			name:    "a named path that changed is matched",
			trigger: "Applies when a `.graphql` query file changed",
			content: "> **Scope.** ONLY when a file under `internal/api/queries/` changed.",
			files:   []string{"internal/api/queries/team.graphql"},
			want:    verdictMatched,
		},
		{
			name:    "a named path the diff does not touch is excluded",
			trigger: "Applies when a query file changed",
			content: "> **Scope.** ONLY when a file under `internal/api/queries/` or `schema/schema.graphql` changed.",
			files:   []string{"README.md"},
			want:    verdictExcluded,
		},
		{
			// Prose in backticks is NOT a path, and this is the case that would
			// silently exclude. `--json` and `WORKER_TAKEN` are quoted the same
			// way a filename is; admitting them would give the check patterns it
			// never meant, and the first diff that missed them would drop it.
			name:    "backticked non-paths do not become patterns",
			trigger: "Applies when a command's `--json` output changes",
			content: "> **Scope.** Run this when the server returns `WORKER_TAKEN`.",
			files:   []string{"internal/cmd/team/session.go"},
			want:    verdictUndecided,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := classify(tc.trigger, tc.content, tc.files)
			if got.Verdict != tc.want {
				t.Errorf("verdict = %q, want %q (patterns %v)", got.Verdict, tc.want, got.Patterns)
			}
		})
	}
}

// A matched check must say WHICH pattern fired on WHICH file. #551 asks for
// applicability to be auditable, and "matched" on its own is a claim, not
// evidence.
func TestAMatchReportsThePairThatFiredIt(t *testing.T) {
	got := classify(
		"Applies when a `.graphql` file changed",
		"> **Scope.** ONLY when a file under `internal/api/queries/` changed. Skip otherwise.",
		[]string{"README.md", "internal/api/queries/team.graphql"},
	)
	if got.Verdict != verdictMatched {
		t.Fatalf("verdict = %q, want matched", got.Verdict)
	}
	if len(got.On) == 0 {
		t.Fatal("a match must name the pattern and file that produced it")
	}
	var found bool
	for _, h := range got.On {
		if h.Pattern == "internal/api/queries/" && h.File == "internal/api/queries/team.graphql" {
			found = true
		}
		if h.File == "README.md" {
			t.Errorf("a file no pattern matched must not appear as evidence: %+v", h)
		}
	}
	if !found {
		t.Errorf("the firing pair is missing from %+v", got.On)
	}
}

func TestPathMatchesRespectsSegmentBoundaries(t *testing.T) {
	for _, tc := range []struct {
		pattern, file string
		want          bool
	}{
		// Directory patterns cover everything beneath.
		{"internal/api/queries/", "internal/api/queries/team.graphql", true},
		{"internal/api/queries/", "internal/api/errors.go", false},
		// Exact files.
		{"schema/schema.graphql", "schema/schema.graphql", true},
		{"schema/schema.graphql", "schema/other.graphql", false},
		// Globs match the base name and the whole path.
		{"*_test.go", "internal/cmd/team_cmd_test.go", true},
		{"*_test.go", "internal/cmd/team.go", false},
		{"*.dart", "lib/main.dart", true},
		{"*.arb", "lib/l10n/app_en.arb", true},
		{"docs/plans/*", "docs/plans/a-plan.md", true},
		// SEGMENT BOUNDARIES — the rule that stops a short pattern matching
		// almost everything. Every spurious match is a check the reviewer is
		// asked to read for nothing, which erodes trust in the whole result.
		{"internal/api", "internal/api/errors.go", true},
		{"internal/api", "internal/apiary/x.go", false},
		{"cmd", "internal/cmd/root.go", true},
		{"cmd", "internal/cmdutil/factory.go", false},
		// A leading ./ on either side is noise, not a difference.
		{"./internal/api", "internal/api/errors.go", true},
	} {
		if got := pathMatches(tc.pattern, tc.file); got != tc.want {
			t.Errorf("pathMatches(%q, %q) = %v, want %v", tc.pattern, tc.file, got, tc.want)
		}
	}
}

func TestLooksLikeAPathAdmitsOnlyPaths(t *testing.T) {
	for _, s := range []string{
		"internal/api/queries/", "schema/schema.graphql", "*.graphql", "main.dart",
		"generated.go", "app_en.arb", "*_test.go",
	} {
		if !looksLikeAPath(s) {
			t.Errorf("%q should be admitted as a path", s)
		}
	}
	// The rejections matter more: each of these, admitted, would give a check
	// patterns it never meant and could then EXCLUDE it.
	for _, s := range []string{
		"--json", "-m", "WORKER_TAKEN", "tookOver", "session start",
		"cor:agt:020:03", "", "   ", "hasLiveSession",
		// FOUND BY RUNNING IT, not by writing fixtures: every check in this
		// corpus ends with a "Pass / fail / n-a" section, and `n/a` passed a
		// bare "contains a slash" test — becoming a pattern that matched
		// nothing and EXCLUDED a check with no path criterion at all.
		"n/a", "a/b",
	} {
		if looksLikeAPath(s) {
			t.Errorf("%q must not be treated as a path — it would become an exclusion criterion", s)
		}
	}
}

// A WEAK pattern can include a check but must never drop one.
//
// `and/or` is structurally identical to `internal/api` — two alphabetic
// segments, no extension, no slash at the end — so no amount of structure tells
// them apart, and a rule that tried would need a dictionary, which is the prose
// interpretation this command refuses to do.
//
// The answer is not a better classifier, it is to require STRONGER evidence for
// the one verdict that removes something: a trailing slash, a glob or a file
// extension. A check whose only patterns are weak comes back undecided, so the
// worst a stray `and/or` in a scope paragraph can cost is a check the reviewer
// reads anyway.
func TestWeakPatternsCannotExcludeACheck(t *testing.T) {
	got := classify("Applies when the copy says `and/or`", "> **Scope.** Prose only.", []string{"README.md"})
	if got.Verdict == verdictExcluded {
		t.Errorf("a weak pattern must not be able to drop a check: %+v", got)
	}
	// The strong forms still do exclude — otherwise the filter does nothing.
	for _, p := range []string{"`internal/api/queries/`", "`*.graphql`", "`schema/schema.graphql`"} {
		strong := classify("Applies when "+p+" changed", "", []string{"README.md"})
		if strong.Verdict != verdictExcluded {
			t.Errorf("%s is unmistakably a path and must still exclude, got %q", p, strong.Verdict)
		}
	}
}

// The scope paragraph and the trigger label are two spellings of one thing, and
// both are read: 57 of 90 checks state their scope in the BODY (measured in
// bodytrigger.go), so a matcher reading only the edge label would find patterns
// for a minority of the corpus.
func TestScopePatternsReadsBothTheTriggerAndTheBody(t *testing.T) {
	got := scopePatterns(
		"Applies when `schema/schema.graphql` changed",
		"> **Scope.** Run this ONLY when a file under `internal/api/queries/` changed.\n> Skip otherwise.",
	)
	joined := strings.Join(got, ",")
	for _, want := range []string{"schema/schema.graphql", "internal/api/queries/"} {
		if !strings.Contains(joined, want) {
			t.Errorf("pattern %q missing from %v", want, got)
		}
	}
	// De-duplicated, so a pattern named in both places is one criterion.
	dup := scopePatterns("`*.go`", "> **Scope.** `*.go` again.")
	if len(dup) != 1 {
		t.Errorf("a pattern named twice must yield one criterion, got %v", dup)
	}
}
