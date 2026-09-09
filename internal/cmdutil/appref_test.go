package cmdutil

import (
	"strings"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/exitcode"
)

// Every spelling the CLI accepts lands on the wire as the canonical v2 URN —
// emit v2, accept everything (#239/#697) — and a PK passes through untouched.
func TestCanonicalAppRefAcceptsEverySpelling(t *testing.T) {
	const cuid, hex = "capp100000000000000000000", "019f01ebcafef00dcafe019f01ebcafe"
	cases := map[string]string{
		"acme.com:dev-app":          "hrn:app:acme.com:dev-app", // short form
		"acme.com::dev-app":         "hrn:app:acme.com:dev-app", // legacy separator
		"hrn:app:acme.com:dev-app":  "hrn:app:acme.com:dev-app", // already canonical
		"hrn:app:acme.com::dev-app": "hrn:app:acme.com:dev-app", // v1 URN
		"urn:app:acme.com::dev-app": "hrn:app:acme.com:dev-app", // legacy scheme
		"  acme.com:dev-app  ":      "hrn:app:acme.com:dev-app", // trimmed
		"holger:flow-lab":           "hrn:app:holger:flow-lab",  // a user-handle root
		cuid:                        cuid,                       // Prisma cuid PK
		hex:                         hex,                        // UUIDv7-hex PK
		"":                          "",                         // absent: the caller's call
	}
	for in, want := range cases {
		got, err := CanonicalAppRef("--app", in)
		if err != nil {
			t.Errorf("CanonicalAppRef(%q) errored: %v", in, err)
			continue
		}
		if got != want {
			t.Errorf("CanonicalAppRef(%q) = %q, want %q", in, got, want)
		}
	}
}

// Anything the server would refuse as "not fully qualified" is refused HERE,
// as a usage error that names the flag and the v2 forms — never the server's
// wording (#540). The bare-slug case gets the "not org-qualified" reading; the
// others say the ref does not name an App. Both list the same forms.
func TestCanonicalAppRefRefusesWhatTheServerWould(t *testing.T) {
	refused := []string{
		"hadron-dev-team",            // the #540 shape: a bare slug, no org
		"app1",                       // slug-shaped, not a PK
		"acme.com:a:b",               // three atoms — a node, not an App
		"acme.com:::dev-app",         // malformed separator
		"acme.com:",                  // empty atom
		":dev-app",                   // empty root
		"hrn:app:",                   // prefix and nothing else
		"hrn:app:acme.com",           // one atom under the prefix
		"hrn:worker:acme.com:t:iris", // a URN of another type
		"hrn:mem:acme.com:kb",        // …including one with two atoms
		"acme.com:dev app",           // charset
	}
	for _, in := range refused {
		got, err := CanonicalAppRef("--app", in)
		if err == nil {
			t.Errorf("CanonicalAppRef(%q) = %q, want a refusal", in, got)
			continue
		}
		if code := exitcode.FromError(err); code != exitcode.Usage {
			t.Errorf("CanonicalAppRef(%q) exit = %d, want Usage", in, code)
		}
		msg := err.Error()
		for _, want := range []string{`--app "` + in + `"`, "hrn:app:<root>:<slug>", "<root>:<slug>", "an App id"} {
			if !strings.Contains(msg, want) {
				t.Errorf("CanonicalAppRef(%q) message lacks %q:\n%s", in, want, msg)
			}
		}
		// The message must not teach the retired grammar, name a GraphQL
		// field, or carry a wire-location prefix — the four #540 defects.
		// The quoted input is exempt: echoing what was typed is not advice.
		advice := strings.Replace(msg, `"`+in+`"`, "", 1)
		for _, banned := range []string{"::", "workers", "input:", "a app"} {
			if strings.Contains(advice, banned) {
				t.Errorf("CanonicalAppRef(%q) message carries %q:\n%s", in, banned, msg)
			}
		}
	}
	if _, err := CanonicalAppRef("--app", "hadron-dev-team"); err == nil || !strings.Contains(err.Error(), "not org-qualified") {
		t.Errorf("a bare slug should be told it lacks its org, got %v", err)
	}
	if _, err := CanonicalAppRef("--app", "acme.com:a:b"); err == nil || strings.Contains(err.Error(), "not org-qualified") {
		t.Errorf("a three-atom ref IS org-qualified; it just is not an App: %v", err)
	}
}

// `what` is whatever the caller's surface is called — the message must read
// as being about the argument the user typed, not about a GraphQL field.
func TestCanonicalAppRefNamesTheSource(t *testing.T) {
	for _, what := range []string{"--install-into", "<app-ref>", "the configured App context"} {
		_, err := CanonicalAppRef(what, "dev-team")
		if err == nil || !strings.HasPrefix(err.Error(), what+` "dev-team"`) {
			t.Errorf("CanonicalAppRef(%q, …) = %v, want it to lead with the source", what, err)
		}
	}
}

func TestIsAppID(t *testing.T) {
	for in, want := range map[string]bool{
		"capp100000000000000000000":        true,  // cuid: c + 24
		"019f01ebcafef00dcafe019f01ebcafe": true,  // 32 hex
		"capp10000000000000000000":         false, // cuid one short
		"Capp100000000000000000000":        false, // cuid is lowercase
		"019F01EBCAFEF00DCAFE019F01EBCAFE": false, // hex is lowercase
		"app1":                             false,
		"hadron-dev-team":                  false,
		"acme.com:dev-app":                 false,
	} {
		if got := IsAppID(in); got != want {
			t.Errorf("IsAppID(%q) = %v, want %v", in, got, want)
		}
	}
}

func TestAppParts(t *testing.T) {
	type parts struct {
		root, slug string
		ok         bool
	}
	cases := map[string]parts{
		"acme.com:dev-app":          {"acme.com", "dev-app", true},
		"acme.com::dev-app":         {"acme.com", "dev-app", true},
		"hrn:app:acme.com:dev-app":  {"acme.com", "dev-app", true},
		"urn:app:acme.com::dev-app": {"acme.com", "dev-app", true},
		"capp100000000000000000000": {"", "", false}, // a PK has no atoms
		"dev-app":                   {"", "", false},
		"acme.com:a:b":              {"", "", false},
		"hrn:mem:acme.com:kb":       {"", "", false}, // not an App URN
		"":                          {"", "", false},
	}
	for in, want := range cases {
		root, slug, ok := AppParts(in)
		if got := (parts{root, slug, ok}); got != want {
			t.Errorf("AppParts(%q) = %+v, want %+v", in, got, want)
		}
	}
}
