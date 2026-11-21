package memory

import (
	"encoding/json"
	"strings"
	"testing"
)

// cli#2843 pins, red-first where main is wrong. Deferred from PR #2831's
// SEC10 review (Rodolfo's P3-1 and P3-2), follow-up to the #2777 family.

// TestSetTokenSuccessPathJudgesPrintedForms2843 pins P3-1: the success line's
// guard judged the RAW urn, but the receipt is not the raw urn — text mode
// prints strconv.Quote's re-formed spelling (quoteCapped) and --json encodes
// the DTO, and either can spell the token the raw urn never carried. The
// issue's probe: token a\"b (as typed by a script) over a server urn holding
// the decoded a"b — the raw guard finds no spelling, and the quoted receipt
// re-forms the token exactly. Such a urn must not splice; the typed ref names
// the memory instead.
func TestSetTokenSuccessPathJudgesPrintedForms2843(t *testing.T) {
	const token = `a\"b`
	const urn = `hrn:mem:acme.com:a"b`
	// Text: the quoted receipt must not re-form the token; the typed ref
	// names the memory instead.
	out, err := setTokenRun(t, "acme.com:kb", urn, token, false)
	if err != nil {
		t.Fatalf("set-token failed: %v", err)
	}
	if strings.Contains(out, token) {
		t.Errorf("the quoted receipt re-forms the token, got %q", out)
	}
	if !strings.Contains(out, `hrn:mem:acme.com:kb`) {
		t.Errorf("the typed ref must name the memory instead, got %q", out)
	}
	// --json: the envelope-encoded memory field is judged too.
	out, err = setTokenRun(t, "acme.com:kb", urn, token, true)
	if err != nil {
		t.Fatalf("set-token --json failed: %v", err)
	}
	var dto struct {
		Memory string `json:"memory"`
	}
	if err := json.Unmarshal([]byte(out), &dto); err != nil {
		t.Fatalf("receipt is not JSON: %v (%q)", err, out)
	}
	if dto.Memory != `hrn:mem:acme.com:kb` {
		t.Errorf("--json memory must fall back to the typed ref, got %q", dto.Memory)
	}
	// The token-free urn path stays byte-identical (TestSetTokenUrnSpliced-
	// WhenTokenFree2704 pins the splice; this pins it under the new guard
	// with a quote in play that the encoder must not mistake for a leak).
	out, err = setTokenRun(t, "01a13b5bc06c79faa2a41a99322be113", `hrn:mem:acme.com:kb"q`, "tok-2843", false)
	if err != nil {
		t.Fatalf("set-token failed: %v", err)
	}
	if !strings.Contains(out, `"hrn:mem:acme.com:kb\"q"`) {
		t.Errorf("a token-free urn (quotes and all) must splice into the receipt, got %q", out)
	}
	// The envelope arm alone: a decoded quote beyond quoteCapped's 300-rune
	// cap is invisible to the quoted-form check (text mode prints only the
	// capped form, so it is safe there), but the --json envelope encodes the
	// FULL urn — without the envelopeReformsToken arm the receipt re-forms
	// the token in the memory field.
	longUrn := "hrn:mem:acme.com:" + strings.Repeat("k", 320) + `a"b`
	out, err = setTokenRun(t, "acme.com:kb", longUrn, token, true)
	if err != nil {
		t.Fatalf("set-token --json failed: %v", err)
	}
	if err := json.Unmarshal([]byte(out), &dto); err != nil {
		t.Fatalf("receipt is not JSON: %v (%q)", err, out)
	}
	if dto.Memory != `hrn:mem:acme.com:kb` {
		t.Errorf("--json must not splice a urn whose envelope re-forms the token past the cap, got %q", dto.Memory)
	}
}

// htmlEscapeText is what a JSON encoder with HTML escaping writes for a raw
// < character: the six literal characters backslash, u, 0, 0, 3, c.
const htmlEscapeText = "\\" + "u003c"

// TestSetTokenGuardGrouping2843 pins the guard's parenthesization. The mode
// arm must stay parenthesized: && binds tighter than ||, so a paren-less
// `... && !f.JSON || !c3` parses as `(... && !f.JSON) || !c3` and the
// urn-empty and spelling vetoes stop binding — an empty server urn splices
// named = "" (a regression of the typed-ref fallback), and a urn whose
// QUOTED form alone carries the token (c1 false, c2 true, c3 false) splices
// in text mode. The c2-only veto is reachable where strconv.Quote and the
// JSON encoder disagree about a control character: a NUL in the urn is
// spelled \x00 (four literal characters, exactly a script-typed token) by
// strconv.Quote but \u0000 by the envelope encoder, so only the quoted-form
// check catches it.
func TestSetTokenGuardGrouping2843(t *testing.T) {
	// Empty urn: the typed ref names the memory, in both modes.
	out, err := setTokenRun(t, "acme.com:kb", "", "tok-2843", false)
	if err != nil {
		t.Fatalf("set-token failed: %v", err)
	}
	if !strings.Contains(out, `hrn:mem:acme.com:kb`) {
		t.Errorf("an empty urn must fall back to the typed ref, got %q", out)
	}
	var dto struct {
		Memory string `json:"memory"`
	}
	out, err = setTokenRun(t, "acme.com:kb", "", "tok-2843", true)
	if err != nil {
		t.Fatalf("set-token --json failed: %v", err)
	}
	if err := json.Unmarshal([]byte(out), &dto); err != nil {
		t.Fatalf("receipt is not JSON: %v (%q)", err, out)
	}
	if dto.Memory != `hrn:mem:acme.com:kb` {
		t.Errorf("--json empty urn must fall back to the typed ref, got %q", dto.Memory)
	}
	// Quote-dirty only: the token is strconv.Quote's four-character spelling
	// of a NUL; the urn carries the decoded byte, so the raw urn and both
	// envelope encodings are clean and only the quoted form carries it.
	const token = "\\" + "x00" // four literal characters: backslash x 0 0
	const urn = "hrn:mem:acme.com:kb-" + "\x00"
	out, err = setTokenRun(t, "acme.com:kb", urn, token, false)
	if err != nil {
		t.Fatalf("set-token failed: %v", err)
	}
	if strings.Contains(out, token) {
		t.Errorf("the quoted receipt re-forms the token, got %q", out)
	}
	if !strings.Contains(out, `hrn:mem:acme.com:kb`) {
		t.Errorf("the typed ref must name the memory instead, got %q", out)
	}
}

// TestEnvelopeReformsTokenEncoderArms2843 pins P3-2: envelopeReformsToken runs
// two encoder passes over the candidate (escapeHTML false, then true), and on
// main no test needs EITHER pass — deleting either arm leaves every test
// green (both mutants survive). Each case here is caught by exactly one arm,
// so deleting that arm turns its case red.
func TestEnvelopeReformsTokenEncoderArms2843(t *testing.T) {
	for _, c := range []struct {
		name, s, token string
		want           bool
	}{
		// Plain arm only: the token's literal spelling sits in the
		// un-HTML-escaped encoding (its backslash-quote survives verbatim),
		// while the HTML pass writes < for the < and no covered spelling
		// of this raw-backslash token appears in that form.
		{`plain arm: token <\" over a message holding <"`, `<"`, `<\"`, true},
		// HTML arm only: the HTML-escaping encoder writes < as the six
		// characters htmlEscapeText, which is literally the token; the plain
		// pass leaves the < raw and none of the token's covered spellings
		// (all of which double its backslash) appears there. (The issue's
		// suggested case — token < over a message holding < — cannot pin this
		// arm: escapedSpellings already covers the < spelling of <, so both
		// arms catch it and deleting either leaves it green.)
		{"html arm: token < over a message holding <", `<`, htmlEscapeText, true},
		// Negative: neither encoding re-forms an unrelated token.
		{`unrelated token`, `<"`, `tok-2843`, false},
	} {
		if got := envelopeReformsToken(c.s, c.token); got != c.want {
			t.Errorf("%s: envelopeReformsToken(%q, %q) = %v, want %v", c.name, c.s, c.token, got, c.want)
		}
	}
}
