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
		"hrn:secret:acme.com:agent:eng:key",
		"hrn:secret:acme.com:user:eng:key",
		"hrn:secret:acme.com::app:eng::key",
		"hrn:secret:acme.com::memory:eng::key",
	} {
		if _, err := urnlib.ParseUrnV2(bad); err == nil {
			t.Errorf("%s: want refused", bad)
		}
	}
}

// The App shape must parse as the App owner kind with its segments and
// round-trip byte-exact through the compose entry point (gate item 3).
func TestSecretURN1994AppRoundTrip(t *testing.T) {
	const urn = "hrn:secret:acme.com:app:eng-team:openai-key"
	p, err := urnlib.ParseSecretUrnV2(urn)
	if err != nil {
		t.Fatal(err)
	}
	if p.OwnerKind != urnlib.SecretOwnerApp {
		t.Errorf("owner kind %v", p.OwnerKind)
	}
	if p.Root != "acme.com" || p.AppSlug != "eng-team" || p.Name != "openai-key" {
		t.Errorf("components root=%q app=%q name=%q", p.Root, p.AppSlug, p.Name)
	}
	got, err := urnlib.ComposeAppSecretUrnV2("acme.com", "eng-team", "openai-key")
	if err != nil {
		t.Fatal(err)
	}
	if got != urn {
		t.Errorf("compose %q, want %q", got, urn)
	}
}
