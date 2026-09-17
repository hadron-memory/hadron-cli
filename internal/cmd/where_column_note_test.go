package cmd

import (
	"strings"
	"testing"
)

// emptySearchEnvelope is a search that matched nothing — the exact result the
// reported predicate produced (#603).
const emptySearchEnvelope = `{"data":{"findNodes":{"total":0,"degraded":null,"reason":null,"hits":[]}}}`

const emptyNodesEnvelope = `{"data":{"nodes":[]}}`

// A `--where` that names no column and matches nothing gets the note, in BOTH
// commands and BOTH output modes. The --json case is the one that matters most:
// an agent parsing stdout would otherwise receive a bare `0` with nothing
// qualifying it, which is how this defect reached two published measurements.
func TestWhereColumnNoteFiresOnSilentZero(t *testing.T) {
	cases := []struct {
		name string
		args []string
		op   string
		resp string
	}{
		{"node ls text", []string{"node", "ls", "-m", "acme.com::kb"}, "FindNodes", emptyNodesEnvelope},
		{"node ls json", []string{"node", "ls", "-m", "acme.com::kb", "--json"}, "FindNodes", emptyNodesEnvelope},
		{"search text", []string{"search", "identity"}, "SearchNodes", emptySearchEnvelope},
		{"search json", []string{"search", "identity", "--json"}, "SearchNodes", emptySearchEnvelope},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			gql, _ := captureGraphQL(t, map[string]string{tc.op: tc.resp})
			f, _ := testFactory(t)
			root := NewRootCmd(f)
			args := append([]string{}, tc.args...)
			args = append(args, "--where", `{"path":["identity"],"exists":true}`, "--server", gql.URL)
			root.SetArgs(args)
			if err := root.Execute(); err != nil {
				t.Fatalf("execute: %v", err)
			}
			note := stderrOf(t, f)
			if !strings.Contains(note, `"field":"data"`) {
				t.Errorf("a field-less predicate matching nothing must be qualified on stderr, got %q", note)
			}
		})
	}
}

// And it must stay quiet otherwise: hits mean the predicate worked, and an
// author who passed "field" has already met the key. A note on either would be
// noise on a correct call, which is how a useful warning gets ignored.
func TestWhereColumnNoteStaysQuiet(t *testing.T) {
	cases := []struct {
		name  string
		args  []string
		op    string
		resp  string
		where string
	}{
		{"node ls, predicate names the column", []string{"node", "ls", "-m", "acme.com::kb", "--json"},
			"FindNodes", emptyNodesEnvelope, `{"field":"data","path":["identity"],"exists":true}`},
		{"node ls, predicate matched rows", []string{"node", "ls", "-m", "acme.com::kb", "--json"},
			"FindNodes", `{"data":{"nodes":[` + nodeJSON + `]}}`, `{"path":["identity"],"exists":true}`},
		{"search, predicate names the column", []string{"search", "identity", "--json"},
			"SearchNodes", emptySearchEnvelope, `{"field":"data","path":["identity"],"exists":true}`},
		{"search, predicate matched rows", []string{"search", "identity", "--json"},
			"SearchNodes", searchEnvelope, `{"path":["identity"],"exists":true}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			gql, _ := captureGraphQL(t, map[string]string{tc.op: tc.resp})
			f, _ := testFactory(t)
			root := NewRootCmd(f)
			args := append([]string{}, tc.args...)
			args = append(args, "--where", tc.where, "--server", gql.URL)
			root.SetArgs(args)
			if err := root.Execute(); err != nil {
				t.Fatalf("execute: %v", err)
			}
			if note := stderrOf(t, f); strings.Contains(note, `"field":"data"`) {
				t.Errorf("must not warn here, got %q", note)
			}
		})
	}
}

// No predicate at all, and no rows: an empty listing is just an empty listing.
// Warning here would put a JSONB-column hint on every bare `node ls` that
// happens to match nothing.
func TestWhereColumnNoteSilentWithoutWhere(t *testing.T) {
	gql, _ := captureGraphQL(t, map[string]string{"FindNodes": emptyNodesEnvelope})
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"node", "ls", "-m", "acme.com::kb", "--json", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if note := stderrOf(t, f); note != "" {
		t.Errorf("no --where must produce no note, got %q", note)
	}
}

// @codex, PR #604: the note must key off what the PREDICATE matched, never off
// the rows that survived to the screen. Anything that narrows the result AFTER
// the predicate — `--offset` past the last row, `node list`'s client-side
// `--seq-gt` — empties the output while the predicate matched plenty. A note
// there tells a caller whose paging was wrong to go and query a different
// column: a confident wrong answer about their data, which is the exact failure
// this note exists to prevent, aimed the other way.
func TestWhereColumnNoteDoesNotFireOnAnEmptyPage(t *testing.T) {
	// An empty page is what the server returns for an --offset past the end.
	// `total` is null here ON PURPOSE: that is what the real server sends even
	// when rows match, so a fixture carrying a helpful total would test a
	// server we do not have.
	const matchedButPagedPast = `{"data":{"findNodes":{"total":null,"degraded":null,"reason":null,"hits":[]}}}`

	cases := []struct {
		name string
		args []string
		op   string
		resp string
	}{
		{"node ls --offset past the end",
			[]string{"node", "ls", "-m", "acme.com::kb", "--offset", "500", "--json"},
			"FindNodes", matchedButPagedPast},
		{"search --offset past the end",
			[]string{"search", "identity", "--offset", "500", "--json"},
			"SearchNodes", matchedButPagedPast},
		// The client-side filter: the server DID return the matching rows, and
		// --seq-gt then discarded every one of them. Exercises the seq path,
		// where the total has to be captured inside the pagination loop.
		{"node ls --seq-gt filters every matched row",
			[]string{"node", "ls", "-m", "acme.com::kb", "--seq-gt", "9999", "--json"},
			"FindNodes", `{"data":{"nodes":[` + projNodeJSON + `]}}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			gql, _ := captureGraphQL(t, map[string]string{tc.op: tc.resp})
			f, out := testFactory(t)
			root := NewRootCmd(f)
			args := append([]string{}, tc.args...)
			args = append(args, "--where", `{"path":["identity"],"exists":true}`, "--server", gql.URL)
			root.SetArgs(args)
			if err := root.Execute(); err != nil {
				t.Fatalf("execute: %v", err)
			}
			// The premise: the displayed result really is empty, so a note keyed
			// off the row count WOULD have fired here. Without this the test
			// could pass because nothing was empty at all.
			if strings.Contains(out.String(), `"loc"`) {
				t.Fatalf("this case must display no rows, or it does not test what it claims:\n%s", out.String())
			}
			if note := stderrOf(t, f); strings.Contains(note, `"field":"data"`) {
				t.Errorf("the predicate matched rows; paging emptied the page. Must not warn, got %q", note)
			}
		})
	}
}

// The seq path needs its own POSITIVE case, and the mutation that proved it is
// worth recording: deleting the total-capture inside the pagination loop left
// every seq-mode test green. It had to — with no total captured the note can
// never fire, and the case above asserts it does not fire. The two agree for
// opposite reasons, so that case alone pins nothing about the capture.
//
// Here the predicate genuinely matched nothing (total 0) while --seq-gt is
// active, so the note MUST arrive — which it can only do if the seq path
// captured the total.
func TestWhereColumnNoteFiresInSeqModeToo(t *testing.T) {
	gql, _ := captureGraphQL(t, map[string]string{
		"FindNodes": `{"data":{"nodes":[]}}`,
	})
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"node", "ls", "-m", "acme.com::kb", "--seq-gt", "1", "--json",
		"--where", `{"path":["identity"],"exists":true}`, "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if note := stderrOf(t, f); !strings.Contains(note, `"field":"data"`) {
		t.Errorf("a genuine zero must still be qualified on the --seq-gt path, got %q", note)
	}
}

// Pins the seqMode arm specifically, and it took a mutation to notice it was
// unpinned: the case above uses --seq-gt at offset 0, so it passes whether the
// arm reads `seqMode || offset == 0` or just `offset == 0`.
//
// Here --offset is set AND the seq path is active. On that path the offset is
// applied client-side, after we have paged to exhaustion, so the fetched set is
// the complete match set and a zero in it really is a zero — the note must
// still fire. Only the seqMode arm makes that true.
func TestWhereColumnNoteFiresInSeqModeEvenWithAnOffset(t *testing.T) {
	gql, _ := captureGraphQL(t, map[string]string{
		"FindNodes": `{"data":{"nodes":[]}}`,
	})
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"node", "ls", "-m", "acme.com::kb", "--seq-gt", "1", "--offset", "500", "--json",
		"--where", `{"path":["identity"],"exists":true}`, "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if note := stderrOf(t, f); !strings.Contains(note, `"field":"data"`) {
		t.Errorf("the seq path pages to exhaustion, so its zero is a real zero and must be qualified; got %q", note)
	}
}
