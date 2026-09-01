package cmd

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// #537: a command-level test must assert the exit code a USER gets.
//
// `renderError` maps an error in two steps — `exitcode.FromError`, then
// `isUsageError` to upgrade cobra's OWN refusals from the generic 1 to Usage 2.
// Cobra's refusals (`required flag(s) … not set`, `unknown flag`, `unknown
// command`, `accepts N args`, the flag-group failures) are plain errors carrying
// no exit code, so step one reads every one of them as 1.
//
// A test calling `exitcode.FromError` on a command result therefore measures a
// value that exists only inside the test. Both functions return `int`, both are
// in scope, and neither NAME says which one the process exits with — so the
// wrong one is exactly as easy to reach for as the right one. That is why this
// is a check rather than a convention.
//
// The failure directions are asymmetric, which is why the whole 177-site sweep
// was worth doing even though the audit found no live defect: a test expecting
// Usage on a cobra refusal FAILS loudly (that is how #533 surfaced), while one
// expecting the generic 1 PASSES while pinning the opposite of a published
// contract.
var rawExitCodeAssertion = regexp.MustCompile(`exitcode\.FromError\(`)

func TestTestsAssertTheUserVisibleExitCode(t *testing.T) {
	entries, err := filepath.Glob(filepath.Join("*_test.go"))
	if err != nil {
		t.Fatalf("globbing test files: %v", err)
	}
	checked := 0
	for _, path := range entries {
		src, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("reading %s: %v", path, err)
		}
		// This file names the banned call in its own documentation.
		if filepath.Base(path) == "exit_code_assertion_test.go" {
			continue
		}
		checked++
		for i, line := range strings.Split(string(src), "\n") {
			if rawExitCodeAssertion.MatchString(line) {
				t.Errorf("%s:%d asserts the PRE-classification exit code; use exitCodeFor, which is what renderError runs:\n\t%s",
					path, i+1, strings.TrimSpace(line))
			}
		}
	}
	// FLOOR the count: a glob that matches nothing passes vacuously, which is
	// the green-and-empty failure this repo keeps finding in its own guards.
	if checked < 30 {
		t.Errorf("only %d test files scanned; the glob is not covering the package", checked)
	}
}
