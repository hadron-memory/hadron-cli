package cmd

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/exitcode"
)

// cli#1906 items 1-4: the labels `spec` prints say what the stored state says.

func withAbstract(node, abstract string) string {
	b, _ := json.Marshal(abstract)
	return strings.Replace(node, `"abstract":null`, `"abstract":`+string(b), 1)
}

// Item 1: a spec rescinded WITHOUT a successor reads "(withdrawn)" or
// "(rescinded)" from its own abstract prefix, never "(replaced)". The prefix is
// the spec-fixed signal (cor:spc:010:01); an unreadable edge is not.
func TestSpecListLabelsARescindedSpecFromItsAbstract(t *testing.T) {
	for _, tc := range []struct{ abstract, want, kind string }{
		{"WITHDRAWN (2026-11-11); do not apply. Never in force.", "(withdrawn)", "withdrawn"},
		{"RESCINDED (2026-11-11); do not apply. Dropped.", "(rescinded)", "rescinded"},
	} {
		t.Run(tc.kind, func(t *testing.T) {
			batch := withAbstract(replacedBatchNode(), tc.abstract)
			text, _ := replacedList(t, `["spec","superseded"]`, batch)
			if !strings.Contains(text, tc.want) || strings.Contains(text, "replaced") {
				t.Fatalf("want %s and no 'replaced':\n%s", tc.want, text)
			}
			_, rows := replacedList(t, `["spec","superseded"]`, batch, "--json")
			if rows[0]["retirement"] != tc.kind || rows[0]["replaced"] != true || rows[0]["replacedBy"] != nil {
				t.Fatalf("row: %v", rows[0])
			}
		})
	}
}

// The prefix is the spec's own statement, so it also settles a case whose edge
// is concealed: that spec is rescinded, and nothing about the edge is revealed.
func TestSpecListRescindPrefixBeatsAConcealedEdge(t *testing.T) {
	batch := withAbstract(replacedBatchNode(supersededByEdge("", "")), "WITHDRAWN (2026-11-11); do not apply.")
	text, _ := replacedList(t, `["spec","superseded"]`, batch)
	if !strings.Contains(text, "(withdrawn)") {
		t.Fatalf("\n%s", text)
	}
}

// A readable successor still reads "replaced by", and the JSON says so.
func TestSpecListReplacedKeepsItsSuccessorLabel(t *testing.T) {
	batch := withAbstract(replacedBatchNode(supersededByEdge("msg:010:03", "mem1")), "SUPERSEDED (2026-11-11) by msg:010:03 (T); do not apply.")
	text, _ := replacedList(t, `["spec","superseded"]`, batch)
	if !strings.Contains(text, "(replaced by msg:010:03)") {
		t.Fatalf("\n%s", text)
	}
	_, rows := replacedList(t, `["spec","superseded"]`, batch, "--json")
	if rows[0]["retirement"] != "replaced" {
		t.Fatalf("row: %v", rows[0])
	}
}

// An active spec has retirement:null; the key is always present.
func TestSpecListActiveSpecHasNullRetirement(t *testing.T) {
	_, rows := replacedList(t, `["spec"]`, replacedBatchNode(), "--json")
	v, has := rows[0]["retirement"]
	if !has || v != nil {
		t.Fatalf("row: %v", rows[0])
	}
}

// Item 2: a real mint confirms after the report, and the report does not tell
// the caller to "run without --dry-run": they just did.
func TestSpecRealMintDoesNotPrintTheDryRunHint(t *testing.T) {
	url, _ := mintServer(t, mintReport(true, false, `[]`), mintReport(false, true, `[]`))
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"spec", "mint", "-m", "mem1", "--yes", "--server", url})
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out.String(), "run without --dry-run") || strings.Contains(out.String(), "mintable") {
		t.Fatalf("a real mint printed the dry-run hint:\n%s", out.String())
	}
	if !strings.Contains(out.String(), "✓ minted") {
		t.Fatalf("no outcome line:\n%s", out.String())
	}
}

// The dry run keeps its hint.
func TestSpecMintDryRunKeepsTheHint(t *testing.T) {
	url, _ := mintServer(t, mintReport(true, false, `[]`), mintReport(false, true, `[]`))
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"spec", "mint", "-m", "mem1", "--dry-run", "--server", url})
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "run without --dry-run to mint") {
		t.Fatalf("\n%s", out.String())
	}
}

// Item 3: spec has no move command, so the CLI adds its own hint to the
// server's NODE_MINTED refusal, keeping the code and the exit.
func TestSpecRenumberMintedRefusalNamesNodeMove(t *testing.T) {
	gql, _ := captureGraphQL(t, map[string]string{
		"Memories":     memListJSON,
		"RenumberSpec": `{"errors":[{"message":"pas:010:02 is minted, so only an unminted node can be renumbered. Move it instead, which keeps its old address resolving.","extensions":{"code":"NODE_MINTED"}}]}`,
	})
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"spec", "renumber", "pas:010:02", "pas:010:05", "-m", "mem1", "--server", gql.URL})
	err := root.Execute()
	if exitCodeFor(err) != exitcode.Conflict {
		t.Fatalf("exit %d: %v", exitCodeFor(err), err)
	}
	for _, want := range []string{"hadron node move", "hadron spec supersede"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("hint lacks %q: %v", want, err)
		}
	}
}

// Other refusals are unchanged: no hint.
func TestSpecRenumberOtherRefusalGetsNoHint(t *testing.T) {
	gql, _ := captureGraphQL(t, map[string]string{
		"Memories":     memListJSON,
		"RenumberSpec": `{"errors":[{"message":"nope","extensions":{"code":"FORBIDDEN"}}]}`,
	})
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"spec", "renumber", "pas:010:02", "pas:010:05", "-m", "mem1", "--server", gql.URL})
	err := root.Execute()
	if err == nil || strings.Contains(err.Error(), "node move") {
		t.Fatalf("%v", err)
	}
}
