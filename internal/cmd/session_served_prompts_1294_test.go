package cmd

import (
	"strings"
	"testing"
)

// cli#1294 (follow-up to #1293's P3 findings): the history page checks, the
// count-only text, the worker/run offset text and the help row alignment.

// The page check refuses a page larger than the one asked for, and a page that
// holds more than the total leaves past the offset. Each case trips ONE clause.
func TestSession1294PageChecksTripOneClauseEach(t *testing.T) {
	two := "[" + c3bRecord + "," + strings.Replace(c3bRecord, `"id":"rec1"`, `"id":"rec2"`, 1) + "]"
	for _, tc := range []struct {
		name string
		body string
		args []string
	}{
		// 2 items, total 2, offset 0: only `len(items) > limit` can refuse it.
		{"more items than the limit", session1280Page(two, 2), []string{"--limit", "1"}},
		// 1 item, limit 2, total 3, offset 3: only the offset term (total-offset = 0) can refuse it.
		{"offset leaves no room", session1280Page("["+c3bRecord+"]", 3), []string{"--limit", "2", "--offset", "3"}},
		// 2 items, limit 2, total 2, offset 1: total-offset = 1 < 2.
		{"offset leaves less room than the items", session1280Page(two, 2), []string{"--limit", "2", "--offset", "1"}},
	} {
		for _, mode := range [][]string{{"--json"}, {}} {
			t.Run(tc.name+strings.Join(mode, ""), func(t *testing.T) {
				args := append([]string{"team", "session", "served-prompts", "session-123"}, tc.args...)
				_, out, err := runTeamTask(t, map[string]string{"SessionServedPrompts": tc.body}, append(args, mode...)...)
				if exitCodeFor(err) != 1 || out != "" {
					t.Fatalf("a bad page was shown: exit %d %v %q", exitCodeFor(err), err, out)
				}
			})
		}
	}
	// The same page is fine once the arguments make it consistent (the cases
	// above are refused for the clause named, not for being malformed).
	_, out, err := runTeamTask(t, map[string]string{"SessionServedPrompts": session1280Page("["+c3bRecord+"]", 4)}, "team", "session", "served-prompts", "session-123", "--limit", "2", "--offset", "3")
	if err != nil || !strings.Contains(out, "served prompts: 1 of 4 (offset 3)") {
		t.Fatalf("a consistent offset page was refused: %v %q", err, out)
	}
}

// --limit 0 asked for counts only: say so, instead of the "empty may also mean
// unreadable" line. The concealment line stays wherever an empty page was not
// asked for, and nothing here says whether hidden records exist.
func TestSession1294CountOnlyText(t *testing.T) {
	const concealment = "No records on this page (empty may also mean unreadable or serving off)."
	run := func(items string, total int, args ...string) string {
		t.Helper()
		a := append([]string{"team", "session", "served-prompts", "session-123"}, args...)
		_, out, err := runTeamTask(t, map[string]string{"SessionServedPrompts": session1280Page(items, total)}, a...)
		if err != nil {
			t.Fatal(err)
		}
		return out
	}
	out := run("[]", 4, "--limit", "0")
	if !strings.Contains(out, "served prompts: 0 of 4 (offset 0)") || !strings.Contains(out, "Counts only") {
		t.Fatalf("count-only text: %q", out)
	}
	if strings.Contains(out, "No records") || strings.Contains(out, "unreadable") || strings.Contains(out, "serving off") {
		t.Fatalf("a count-only read must not say it is empty or unreadable: %q", out)
	}
	// Concealment intact: an empty page nobody asked to be empty, and a zero total.
	if out := run("[]", 0, "--limit", "0"); !strings.Contains(out, concealment) {
		t.Fatalf("limit 0 with total 0 lost the concealment line: %q", out)
	}
	if out := run("[]", 0); !strings.Contains(out, concealment) {
		t.Fatalf("an empty page lost the concealment line: %q", out)
	}
	if out := run("[]", 4, "--offset", "9"); !strings.Contains(out, concealment) {
		t.Fatalf("a page past the end lost the concealment line: %q", out)
	}
}

// The record text carries the provenance page's offset on the worker and run
// commands, which share writeRecord with the history (#1293).
func TestSession1294WorkerAndRunTextCarryTheOffset(t *testing.T) {
	for _, tc := range []struct {
		name, op string
		args     []string
		body     string
	}{
		{"worker", "WorkerServedPrompt", []string{"team", "worker", "served-prompt", "hrn:worker:acme.com:iris", "--offset", "1"}, c3bWorkerRecord(c3bRecord)},
		{"run", "RunServedPrompt", []string{"run", "served-prompt", "run_123", "--offset", "1"}, `{"data":{"appRun":{"servedPrompt":` + c3bRecord + `}}}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls, out, err := runTeamTask(t, map[string]string{tc.op: tc.body}, tc.args...)
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(out, "provenance: 1 of 2 (offset 1)") {
				t.Fatalf("the provenance offset is missing from the text: %q", out)
			}
			if c := c2Mutation(t, calls, tc.op); c.Vars["offset"] != float64(1) {
				t.Fatalf("offset not sent: %s", c.RawVars)
			}
		})
	}
}

// `team session --help`: every row of the "WHICH COMMAND" block starts its
// command in the same column.
func TestSession1294HelpRowsAlign(t *testing.T) {
	_, out, err := runTeamTask(t, nil, "team", "session", "--help")
	if err != nil {
		t.Fatal(err)
	}
	_, block, ok := strings.Cut(out, "WHICH COMMAND, IN ORDER:\n")
	if !ok {
		t.Fatalf("no WHICH COMMAND block: %s", out)
	}
	block, _, _ = strings.Cut(block, "\n\n")
	col, rows := -1, 0
	for _, line := range strings.Split(block, "\n") {
		if !strings.HasPrefix(line, "  ") || strings.HasPrefix(line, "   ") {
			continue // a continuation line
		}
		body := strings.TrimPrefix(line, "  ")
		gap := strings.Index(body, "  ")
		if gap < 0 {
			t.Fatalf("a row with no gap: %q", line)
		}
		at := 2 + gap + len(body[gap:]) - len(strings.TrimLeft(body[gap:], " "))
		rows++
		if col < 0 {
			col = at
		}
		if at != col {
			t.Errorf("row %q starts its command at column %d, the others at %d", line, at, col)
		}
	}
	if rows < 6 {
		t.Fatalf("only %d rows found in %q", rows, block)
	}
}
