package cmd

import (
	"fmt"
	"os"
	"regexp"
	"strings"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/exitcode"
)

// The exit-code table in agentic-usage.md is the contract AGENTS read, and
// nothing kept it honest against internal/exitcode until now.
//
// This is a doc/code consistency guard rather than a style check: an exit code
// that exists and is undocumented cannot be branched on by the audience it was
// added for, and a documented row with no constant behind it is a promise the
// binary does not keep. #619 added exit 8 and had to update the table by hand,
// which is exactly the step that gets skipped — three prose claims went stale
// in this repo on 2026-09-19 alone, each caught by a reviewer rather than a
// test.
func TestExitCodeTableMatchesTheConstants(t *testing.T) {
	doc, err := os.ReadFile("agentic/agentic-usage.md")
	if err != nil {
		t.Fatalf("read agentic-usage.md: %v", err)
	}
	// Rows of the "Exit codes (stable contract)" table: | 7    | text |
	rowRE := regexp.MustCompile(`(?m)^\|\s*(\d+)\s*\|`)
	documented := map[int]bool{}
	for _, m := range rowRE.FindAllStringSubmatch(string(doc), -1) {
		var n int
		if _, err := fmt.Sscanf(m[1], "%d", &n); err == nil {
			documented[n] = true
		}
	}
	if len(documented) == 0 {
		t.Fatal("found no exit-code rows — the table moved or its shape changed, " +
			"which would make this guard silently vacuous")
	}

	// Every constant the package defines must appear in the table.
	for _, c := range []struct {
		name string
		code int
	}{
		{"OK", exitcode.OK},
		{"Error", exitcode.Error},
		{"Usage", exitcode.Usage},
		{"AuthRequired", exitcode.AuthRequired},
		{"NotFound", exitcode.NotFound},
		{"Conflict", exitcode.Conflict},
		{"Cancelled", exitcode.Cancelled},
		{"Unavailable", exitcode.Unavailable},
		{"Forbidden", exitcode.Forbidden},
	} {
		if !documented[c.code] {
			t.Errorf("exitcode.%s = %d is not in agentic-usage.md's table — "+
				"an undocumented exit code cannot be branched on by the agents it exists for",
				c.name, c.code)
		}
	}
}

// #619 end-to-end: a server FORBIDDEN reaches the USER as exit 8.
//
// Asserted through a real command with exitCodeFor, not by calling the mapper:
// `renderError` post-processes what the mapper returns, so a test that reads
// exitcode.FromError measures a value that exists only inside the test
// (review:assert-the-value-the-user-gets).
func TestForbiddenReachesTheUserAsExitEight(t *testing.T) {
	gql, _ := captureGraphQL(t, map[string]string{
		"MergeUsers": `{"errors":[{"message":"forbidden","extensions":{"code":"FORBIDDEN"}}]}`,
	})
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"user", "merge", "dup", "--into", "alice", "--yes", "--server", gql.URL})
	err := root.Execute()
	if code := exitCodeFor(err); code != exitcode.Forbidden {
		t.Errorf("exit = %d, want %d (Forbidden); err: %v", code, exitcode.Forbidden, err)
	}
	// And it must NOT tell an authenticated caller to sign in — the false
	// remedy exit 3 would have carried.
	if err != nil && strings.Contains(err.Error(), "auth login") {
		t.Errorf("a permission refusal must not prescribe signing in: %v", err)
	}
}
