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
