package coding

import (
	"path"
	"regexp"
	"sort"
	"strings"
)

// Deciding which checks a diff could fire, WITHOUT interpreting prose (#551).
//
// The issue asks `run` to "evaluate each Applies when … trigger". Triggers are
// English, this binary has no model, and `bodytrigger.go` already settled the
// neighbouring question the other way: condensing a scope paragraph into a
// trigger is "a judgement call the linter hands over rather than makes."
//
// So the rule Holger set (2026-09-05) is STRUCTURAL ONLY, three buckets, and
// never exclude on a guess. The asymmetry is the whole design:
//
//	a false positive costs the reviewer a skim;
//	a false negative ships the defect.
//
// A check is therefore dropped only on POSITIVE evidence that it cannot apply —
// it named concrete paths and the diff touches none of them. Everything else is
// returned, including every check that names no paths at all, because "I could
// not tell" and "it does not apply" are different answers and only one of them
// is safe to act on.

// verdict is why a check is in (or out of) the result.
type verdict string

const (
	// verdictMatched — a path pattern the check names matched a changed file.
	verdictMatched verdict = "matched"
	// verdictUndecided — the check names no path patterns, so nothing here can
	// speak to it. Returned in full: this is the bucket that keeps the tool
	// honest, and on a mature checklist it is the biggest one.
	verdictUndecided verdict = "undecided"
	// verdictExcluded — the check names path patterns and the diff touches none.
	// The ONLY bucket that removes a check from the reviewer's list.
	verdictExcluded verdict = "excluded"
)

// reBackticked pulls the `quoted` spans out of a trigger label or scope
// paragraph. Backticks are the convention every check in this corpus already
// uses for a concrete path, so nothing had to be re-authored to make this work
// — which is the point: a matcher that required new metadata would be a
// migration, not a feature.
var reBackticked = regexp.MustCompile("`([^`]+)`")

// sourceExt lists the extensions this repo's checklists actually name. A
// pattern ending in one of these is a path even without a slash (`*.graphql`,
// `main.dart`), which is how a single-extension scope like "Dart files" gets
// matched at all.
var sourceExt = map[string]bool{
	".go": true, ".graphql": true, ".gql": true, ".json": true, ".md": true,
	".yaml": true, ".yml": true, ".sql": true, ".sh": true, ".dart": true,
	".arb": true, ".ts": true, ".tsx": true, ".js": true, ".jsx": true,
	".svelte": true, ".css": true, ".html": true, ".toml": true, ".proto": true,
}

// looksLikeAPath reports whether a backticked span is a file path or glob
// rather than prose or an identifier.
//
// Deliberately narrow. A span that is merely plausible must NOT count: every
// pattern this admits can push a check into `excluded`, so a false path
// ("`--json`", "`WORKER_TAKEN`") would silently drop a check that applies. The
// three admitted shapes are the ones a scope paragraph uses to name a file.
func looksLikeAPath(s string) bool {
	s = strings.TrimSpace(s)
	if s == "" || strings.ContainsAny(s, " \t") {
		return false // a phrase, not a path
	}
	if strings.HasPrefix(s, "-") {
		return false // a flag, e.g. `--json`
	}
	switch {
	case strings.HasPrefix(s, "*."):
		return true // *.graphql
	case sourceExt[strings.ToLower(path.Ext(s))]:
		return true // main.dart, generated.go, internal/x/y.go
	case strings.HasSuffix(s, "/"):
		return true // internal/api/queries/
	case strings.Contains(s, "/"):
		// A slash alone is NOT enough, and this is the arm that live-running
		// paid for: every check in this corpus ends with a "Pass / fail / n-a"
		// section, and `n/a` sailed through a bare Contains("/") test. It became
		// a pattern, matched nothing, and EXCLUDED a check that had no path
		// criterion at all — the exact false-exclusion this file is built to
		// avoid, found by running the command against the real checklist rather
		// than against fixtures I wrote.
		//
		// Real path segments are not single characters, so `n/a` is out and
		// `internal/api` is in. That is as far as structure goes: `and/or` is
		// indistinguishable from `internal/api` without a dictionary, which is
		// the prose interpretation this file refuses to do. The residual risk is
		// handled by strength, below — a bare two-segment pattern can INCLUDE a
		// check but can never be the reason one is dropped.
		for _, seg := range strings.Split(s, "/") {
			if len(seg) < 2 {
				return false
			}
		}
		return true
	}
	return false
}

// scopePatterns returns the path patterns a check names, from its trigger label
// and its body's scope paragraph, de-duplicated and ordered.
//
// Both sources are read because they are two spellings of one thing: the edge
// label is the trigger `review list` shows, and the `> **Scope.**` blockquote is
// where the concrete file patterns usually live (measured in bodytrigger.go: 57
// of 90 checks state their scope in the body).
func scopePatterns(trigger, content string) []string {
	seen := map[string]bool{}
	var out []string
	collect := func(text string) {
		for _, m := range reBackticked.FindAllStringSubmatch(text, -1) {
			p := strings.TrimSpace(m[1])
			if looksLikeAPath(p) && !seen[p] {
				seen[p] = true
				out = append(out, p)
			}
		}
	}
	collect(trigger)
	if scope, ok := bodyTrigger(content); ok {
		collect(scope)
	}
	sort.Strings(out)
	return out
}

// pathMatches reports whether a changed file is covered by one pattern.
//
// Three shapes, each matching how a scope paragraph is actually written:
//
//   - a trailing slash is a DIRECTORY — `internal/api/queries/` covers
//     everything beneath it;
//   - a `*` is a glob, matched against the base name AND the full path, so
//     `*_test.go` catches `internal/cmd/x_test.go` and `docs/plans/*` catches a
//     plan doc;
//   - anything else must match a whole path SEGMENT boundary, never a bare
//     substring — `schema/schema.graphql` matches that file and `internal/api`
//     matches `internal/api/errors.go`, while `api` does not match
//     `internal/apiary/x.go`.
//
// The segment rule is the one that earns its keep: plain `strings.Contains`
// would make short patterns match almost anything, and every spurious match is
// a check the reviewer is asked to read for no reason — the direction that is
// merely wasteful, but it also erodes trust in the bucket that matters.
func pathMatches(pattern, file string) bool {
	pattern, file = strings.TrimPrefix(pattern, "./"), strings.TrimPrefix(file, "./")
	if pattern == "" || file == "" {
		return false
	}
	if strings.HasSuffix(pattern, "/") {
		return strings.HasPrefix(file, pattern)
	}
	if strings.Contains(pattern, "*") {
		if ok, _ := path.Match(pattern, path.Base(file)); ok {
			return true
		}
		if ok, _ := path.Match(pattern, file); ok {
			return true
		}
		// A directory glob like `internal/cmd/*` should cover nested files too;
		// path.Match's * does not cross separators, so try the prefix form.
		if base := strings.TrimSuffix(pattern, "*"); base != pattern && base != "" {
			return strings.HasPrefix(file, base)
		}
		return false
	}
	if file == pattern {
		return true
	}
	// Segment boundaries on both sides: pattern must start a segment and end at
	// one, so it never matches half a directory or half a filename.
	if strings.HasPrefix(file, pattern+"/") {
		return true
	}
	if i := strings.Index(file, "/"+pattern); i >= 0 {
		rest := file[i+1+len(pattern):]
		return rest == "" || strings.HasPrefix(rest, "/")
	}
	return false
}

// strongPattern reports whether a pattern is definite enough to EXCLUDE on.
//
// The distinction exists because inclusion and exclusion do not need the same
// confidence. A trailing slash, a glob or a file extension is unmistakably a
// path — nobody writes `*.graphql` or `internal/api/queries/` by accident. A
// bare `word/word` is not: it is the shape of a directory AND the shape of
// "and/or", and structure cannot tell them apart.
//
// So weak patterns still MATCH (a spurious one costs a skim), and a check whose
// only patterns are weak comes back UNDECIDED rather than excluded. That is the
// asymmetry this command is built on, applied to the evidence itself rather than
// only to the verdict.
func strongPattern(p string) bool {
	return strings.HasSuffix(p, "/") || strings.Contains(p, "*") || sourceExt[strings.ToLower(path.Ext(p))]
}

// checkMatch is one check's verdict against a diff.
type checkMatch struct {
	Verdict  verdict
	Patterns []string
	// On is the (pattern, file) pairs that fired, so a matched check can say
	// WHY it is in the list. #551 asks for applicability to be auditable, and a
	// bare "matched" is not auditable — it is a claim.
	On []patternHit
}

type patternHit struct {
	Pattern string
	File    string
}

// classify buckets one check against the changed files.
func classify(trigger, content string, files []string) checkMatch {
	patterns := scopePatterns(trigger, content)
	if len(patterns) == 0 {
		// NO EVIDENCE IS NOT EVIDENCE OF NO. A check that names no paths is
		// returned, every time — the reviewer decides, which is exactly the
		// judgement this command declines to make.
		return checkMatch{Verdict: verdictUndecided, Patterns: patterns}
	}
	var hits []patternHit
	for _, p := range patterns {
		for _, f := range files {
			if pathMatches(p, f) {
				hits = append(hits, patternHit{Pattern: p, File: f})
			}
		}
	}
	if len(hits) == 0 {
		for _, p := range patterns {
			if strongPattern(p) {
				return checkMatch{Verdict: verdictExcluded, Patterns: patterns}
			}
		}
		// Only weak evidence, and none of it fired. Not enough to drop a check.
		return checkMatch{Verdict: verdictUndecided, Patterns: patterns}
	}
	return checkMatch{Verdict: verdictMatched, Patterns: patterns, On: hits}
}
