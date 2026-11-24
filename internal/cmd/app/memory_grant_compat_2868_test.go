package app

import (
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/api/gen"
)

// cli#2868: the older-server document always carries a prefix (a string); converting it to the
// current fields (prefix is a pointer there) must keep the value, never drop it to nil, which
// would show a plain grant as concealed.
func TestLegacyGrantFieldsKeepThePrefix2868(t *testing.T) {
	prefix := "reports"
	got := legacyGrantFields(gen.AppMemoryGrantLegacyFields{Id: "g1", AppId: "a1", Prefix: &prefix})
	if got.Prefix == nil || *got.Prefix != "reports" {
		t.Fatalf("the legacy prefix must survive the conversion: %v", got.Prefix)
	}
}

// hadron-server#4734 makes the legacy prefix nullable too (null whenever the
// memory URN is concealed): a nil prefix passes through as nil, never as "".
func TestLegacyGrantFieldsKeepANilPrefix_1964(t *testing.T) {
	got := legacyGrantFields(gen.AppMemoryGrantLegacyFields{Id: "g1", AppId: "a1"})
	if got.Prefix != nil {
		t.Fatalf("a nil legacy prefix must stay nil: %q", *got.Prefix)
	}
}
