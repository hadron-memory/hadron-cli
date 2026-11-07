package spec

import (
	"bytes"
	"strings"
	"testing"
)

func TestMintZeroStaleScopeAndQuotedAudit1807(t *testing.T) {
	var out bytes.Buffer
	if err := renderMint(&out, mintDTO{Memory: "acme.com::team's $(memory)"}); err != nil {
		t.Fatal(err)
	}
	want := "  stale abstracts: 0 (reported only) — this mint's scope only; whole-memory audit: hadron spec validate --all -m 'acme.com::team'\\''s $(memory)'\n"
	if !strings.Contains(out.String(), want) {
		t.Fatalf("want %q in %q", want, out.String())
	}
}
