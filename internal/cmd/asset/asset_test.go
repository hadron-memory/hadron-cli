package asset

import (
	"strings"
	"testing"
)

func TestParseAssetRef(t *testing.T) {
	cases := []struct {
		name    string
		in      string
		wantID  string
		wantMem string
		wantErr bool
	}{
		{"bare id", "01j2xabc", "01j2xabc", "", false},
		{"canonical v2 urn", "hrn:asset:acme.com:kb:assets:01j2xabc", "01j2xabc", "acme.com:kb", false},
		{"urn without the hrn prefix", "asset:acme.com:kb:assets:01j2xabc", "01j2xabc", "acme.com:kb", false},
		{"bare root:mem:assets:id", "acme.com:kb:assets:01j2xabc", "01j2xabc", "acme.com:kb", false},
		// #239 — every legacy spelling is accepted forever; these are the
		// acceptance coverage, not fixtures to "fix".
		{"legacy double-colon", "hrn:asset:acme.com::kb::assets::01j2xabc", "01j2xabc", "acme.com:kb", false},
		{"urn: prefix", "urn:asset:acme.com:kb:assets:01j2xabc", "01j2xabc", "acme.com:kb", false},
		{"empty", "  ", "", "", true},
		// A node URN is NOT an asset URN; without the marker there is no way to
		// tell which trailing segment is the id, so this must fail loudly
		// rather than guess.
		{"node urn rejected", "hrn:node:acme.com:kb:findings:x", "", "", true},
		{"missing id after marker", "acme.com:kb:assets:", "", "", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := parseAssetRef(tc.in)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("expected an error for %q, got %+v", tc.in, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("parseAssetRef(%q): %v", tc.in, err)
			}
			if got.ID != tc.wantID || got.MemoryRef != tc.wantMem {
				t.Errorf("parseAssetRef(%q) = {id:%q mem:%q}, want {id:%q mem:%q}",
					tc.in, got.ID, got.MemoryRef, tc.wantID, tc.wantMem)
			}
		})
	}
}

func TestMemoryScope(t *testing.T) {
	urnRef := assetRef{ID: "a1", MemoryRef: "acme.com:kb"}
	bareRef := assetRef{ID: "a1"}

	if got, err := memoryScope(urnRef, ""); err != nil || got != "acme.com:kb" {
		t.Errorf("a URN should supply its own memory; got %q, %v", got, err)
	}
	// An explicit -m wins: the caller said it out loud, and it is the escape
	// hatch when a URN names a memory that has since been re-minted.
	if got, err := memoryScope(urnRef, "other.com:kb"); err != nil || got != "other.com:kb" {
		t.Errorf("-m should override the URN's memory; got %q, %v", got, err)
	}
	if got, err := memoryScope(bareRef, "acme.com:kb"); err != nil || got != "acme.com:kb" {
		t.Errorf("-m should supply the memory for a bare id; got %q, %v", got, err)
	}
	_, err := memoryScope(bareRef, "")
	if err == nil {
		t.Fatal("a bare id with no -m must be a usage error, not a guess")
	}
}

func TestHumanSize(t *testing.T) {
	cases := map[int]string{0: "0 B", 512: "512 B", 1024: "1.0 KB", 1536: "1.5 KB", 1048576: "1.0 MB"}
	for in, want := range cases {
		if got := humanSize(in); got != want {
			t.Errorf("humanSize(%d) = %q, want %q", in, got, want)
		}
	}
}

func TestHotlinkAbsentReasonNamesTheActionableCause(t *testing.T) {
	// A null publicUrl has several causes; only the scan ones are readable off
	// the asset, and they are the only ones the caller can wait out.
	if got := hotlinkAbsentReason("PENDING"); got == "" || !strings.Contains(got, "scan") {
		t.Errorf("PENDING should mention the scan; got %q", got)
	}
	if got := hotlinkAbsentReason("BLOCKED"); !strings.Contains(got, "blocked") {
		t.Errorf("BLOCKED should say so; got %q", got)
	}
	if got := hotlinkAbsentReason("CLEAN"); !strings.Contains(got, "encrypted") {
		t.Errorf("a CLEAN asset with no URL is an encryption/deployment cause; got %q", got)
	}
}
