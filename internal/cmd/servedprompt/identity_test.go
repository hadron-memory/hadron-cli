package servedprompt

import (
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/cmd/lintwarn"
)

// cli#2007: currentLint is output-only. The provenance-paging identity check
// must not move when it is present, or a lint read would read as "the record
// changed during pagination".
func TestIdentityIgnoresCurrentLint(t *testing.T) {
	page := &lintwarn.Page{Items: []lintwarn.Warning{{Kind: "BROKEN_INCLUDE", Text: "x"}}, Total: 1}
	slot := &page
	plain := &recordDTO{ID: "rec1", ServedRevSeq: 7, ProvenanceRevSeq: 5}
	withLint := &recordDTO{ID: "rec1", ServedRevSeq: 7, ProvenanceRevSeq: 5, CurrentLint: slot}
	if identity(plain) != identity(withLint) {
		t.Fatalf("identity must ignore currentLint:\n%s\n%s", identity(plain), identity(withLint))
	}
}
