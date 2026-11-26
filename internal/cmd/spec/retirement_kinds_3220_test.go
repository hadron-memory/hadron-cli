package spec

import (
	"bytes"
	"strings"
	"testing"
)

// Every kind the listing can read from an abstract survives the word round trip
// the register uses: one list of kinds feeds both directions (cli#3220 item 3).
func TestEveryRescindKindSurvivesTheWordRoundTrip3220(t *testing.T) {
	if len(rescindKinds) < 2 {
		t.Fatalf("rescindKinds = %v, want at least withdrawn and rescinded", rescindKinds)
	}
	for _, k := range rescindKinds {
		abstract := k.prefixWord + " (2026-11-20); because"
		if got := rescindKind(abstract); got != k.kind {
			t.Errorf("rescindKind(%q) = %q, want %q", abstract, got, k.kind)
		}
		if got := (replacement{Replaced: true, Kind: k.kind}).word(); got != k.kind {
			t.Errorf("word() of kind %q = %q", k.kind, got)
		}
		if got := kindOfWord(k.kind); got != k.kind {
			t.Errorf("kindOfWord(%q) = %q: a kind the abstract can carry is dropped on the way back", k.kind, got)
		}
	}
	if kindOfWord("replaced") != "" || kindOfWord("retired") != "" || kindOfWord("") != "" {
		t.Error("a word that is not a rescind kind must map to no kind")
	}
}

// cli#3220 item 5: the "run without --dry-run to mint" hint is switched by an explicit
// argument, not by editing a copy of the report. A real mint shows the dry run's report
// without the hint; a dry run keeps it.
func TestRenderMintHintFollowsTheArgument3220(t *testing.T) {
	locs := []string{"cli:cha:010:01"}
	report := mintDTO{Memory: "acme.com::specs", DryRun: true, MintLocs: &locs}
	var asDry, asReal bytes.Buffer
	if err := renderMint(&asDry, report, true); err != nil {
		t.Fatal(err)
	}
	if err := renderMint(&asReal, report, false); err != nil {
		t.Fatal(err)
	}
	const hint = "run without --dry-run to mint"
	if !strings.Contains(asDry.String(), hint) {
		t.Errorf("a dry run's report must end with the hint:\n%s", asDry.String())
	}
	if strings.Contains(asReal.String(), hint) {
		t.Errorf("a real mint's report must not tell the caller to run without --dry-run:\n%s", asReal.String())
	}
	// cli#3250 item 7: with the hint off, the report's own DryRun changes nothing printed, so the
	// argument is the only DryRun-dependent output.
	notDry := report
	notDry.DryRun = false
	var asNotDry bytes.Buffer
	if err := renderMint(&asNotDry, notDry, false); err != nil {
		t.Fatal(err)
	}
	if asReal.String() != asNotDry.String() {
		t.Errorf("DryRun true and false differ with the hint off:\n--- DryRun true\n%s\n--- DryRun false\n%s", asReal.String(), asNotDry.String())
	}
}
