package cmd

import (
	"testing"

	urnlib "github.com/hadron-memory/urn-lib-go"
)

// cli#1994 (S0b): the CLI keeps no secret grammar; this pins that the bumped
// urn-lib-go entry points admit the App shape and refuse the superseded #679
// shape in both spellings.
func TestSecretURN1994(t *testing.T) {
	for _, ok := range []string{
		"hrn:secret:acme.com:app:eng-team:openai-key",
		"hrn:secret:acme.com:stripe-key",
		"hrn:secret:acme.com:app", // a flat secret named "app" stays flat
	} {
		p, err := urnlib.ParseUrnV2(ok)
		if err != nil {
			t.Errorf("%s: %v", ok, err)
			continue
		}
		if p.Type != "secret" {
			t.Errorf("%s: type %q", ok, p.Type)
		}
	}
	for _, bad := range []string{
		"hrn:secret:acme.com:a:b",
		"hrn:secret:acme.com:memory:eng:key",
		"hrn:secret:acme.com:org:eng:key",
		"hrn:secret:acme.com::app:eng::key",
		"hrn:secret:acme.com::memory:eng::key",
	} {
		if _, err := urnlib.ParseUrnV2(bad); err == nil {
			t.Errorf("%s: want refused", bad)
		}
	}
}
