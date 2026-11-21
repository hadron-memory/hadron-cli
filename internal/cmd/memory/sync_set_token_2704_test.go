package memory

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/cmdutil"
	"github.com/hadron-memory/hadron-cli/internal/config"
	"github.com/hadron-memory/hadron-cli/internal/output"
	"github.com/vektah/gqlparser/v2/gqlerror"
)

// cli#2704 pins, red-first where main is wrong. The issue's four items:
// Jack F1 (sentinel-equal token wedges the URN guard open), Jack F2 (a server
// code carrying only an ESCAPED spelling of the token passes the drop guard),
// Jack F4 (no positive pin that a token-free URN splices into the receipt) and
// Rook F1 (Go escapes U+2028/U+2029 even with SetEscapeHTML(false), Node's
// JSON.stringify does not, so that spelling is never redacted).

// TestSetTokenServerCodeEscapedSpellingDropped2704 pins Jack F2: the guard in
// setTokenError must judge the code by EVERY spelling of the token, not only
// the literal. On main the guard is strings.Contains(code, token), so a code
// carrying only the escaped spelling survives and is shown to the user.
func TestSetTokenServerCodeEscapedSpellingDropped2704(t *testing.T) {
	const token = `a"b`
	mk := func(code string) error {
		return &gqlerror.Error{Message: "refused", Extensions: map[string]any{"code": code}}
	}
	// The escaped spelling only (a backslash-escaped quote): not a substring
	// match for the literal token, so main's guard keeps this code.
	if got := carriedCode(setTokenError(mk(`BAD a\"b`), token, false)); got != "" {
		t.Errorf("a server code holding an escaped spelling of the token must be dropped, got %q", got)
	}
	// The literal spelling stays covered (cli#2650 item 4 regression guard).
	if got := carriedCode(setTokenError(mk("BAD "+token), token, false)); got != "" {
		t.Errorf("a server code holding the literal token must be dropped, got %q", got)
	}
	if got := carriedCode(setTokenError(mk("FORBIDDEN"), token, false)); got != "FORBIDDEN" {
		t.Errorf("an ordinary server code must be kept, got %q", got)
	}
}

// TestRedactTokenNodeLineSeparators2704 pins Rook F1 for the token site:
// Go's encoder escapes U+2028/U+2029 even with SetEscapeHTML(false), Node's
// JSON.stringify writes them raw, so a Node echo of a token carrying a line
// separator (plus a quote, to force the mixed spelling) is a DISTINCT string
// main never generates — and therefore never redacts.
func TestRedactTokenNodeLineSeparators2704(t *testing.T) {
	for _, c := range []struct{ name, in, token, want string }{
		{
			name:  "node spelling, u2028 with a quote",
			in:    "leak x\\\"\u2028z more",
			token: "x\"\u2028z",
			want:  "leak [REDACTED] more",
		},
		{
			name:  "node spelling, u2029 with a quote",
			in:    "leak y\\\"\u2029w more",
			token: "y\"\u2029w",
			want:  "leak [REDACTED] more",
		},
		{
			name:  "go spelling stays covered",
			in:    "leak x\\\"\\u2028z more",
			token: "x\"\u2028z",
			want:  "leak [REDACTED] more",
		},
	} {
		if got := redactToken(c.in, c.token); got != c.want {
			t.Errorf("%s: redactToken = %q, want %q", c.name, got, c.want)
		}
	}
}

// setTokenRun drives the real set-token command against a fake server that
// answers SetMemorySourceToken with the given URN, typing the memory as
// typedRef, feeding the token through --token-file exactly as a script would.
// A typedRef that does not canonicalize to urn (an id) is what makes the
// splice observable: a fallback receipt then carries the typed ref instead.
func setTokenRun(t *testing.T, typedRef, urn, token string, asJSON bool) (string, error) {
	t.Helper()
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("HADRON_TOKEN", "hdr_user_test")
	t.Setenv("HADRON_SPEC_MEMORY", "")
	io, outBuf, _ := output.Test()
	f := &cmdutil.Factory{IOStreams: io, ConfigFn: config.Load, JSON: asJSON}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && r.URL.Path == "/capabilities" {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"capabilities":[]}`))
			return
		}
		var body struct {
			OperationName string `json:"operationName"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("unreadable request: %v", err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		if body.OperationName != "SetMemorySourceToken" {
			t.Errorf("unexpected operation %q", body.OperationName)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		// Encoded, not interpolated: a fixture URN carrying a quote or
		// backslash must not change the value under test (or the JSON).
		resp := map[string]any{"data": map[string]any{"setMemorySourceToken": map[string]any{"id": "m1", "urn": urn}}}
		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(resp); err != nil {
			t.Errorf("encoding fake response: %v", err)
		}
	}))
	t.Cleanup(srv.Close)
	f.ServerFlag = srv.URL
	tf := filepath.Join(t.TempDir(), "token")
	if err := os.WriteFile(tf, []byte(token), 0o600); err != nil {
		t.Fatal(err)
	}
	cmd := newCmdSyncSetToken(f)
	cmd.SetArgs([]string{typedRef, "--token-file", tf})
	err := cmd.Execute()
	return outBuf.String(), err
}

// TestSetTokenMarkerTokenNotInRenderedMessage2714 pins Dara's P1 (cli#2714
// blind round 2): redactToken writes "[REDACTED]" over the token, so a token
// that IS the marker — or contains it — is rewritten into itself (equal) or
// leaves its own material beside the marker (contains), and stays visible in
// the error text. Such a token gets a fixed refusal message that carries
// nothing from the server; the RENDERED message is asserted, not only the
// carried code.
func TestSetTokenMarkerTokenNotInRenderedMessage2714(t *testing.T) {
	mk := func(code string) error {
		return &gqlerror.Error{Message: "refused: the token [REDACTED] is not allowed", Extensions: map[string]any{"code": code}}
	}
	for _, token := range []string{"[REDACTED]", "tok[REDACTED]", "[REDACTED]tok"} {
		err := setTokenError(mk("FORBIDDEN"), token, false)
		if strings.Contains(err.Error(), "[REDACTED]") {
			t.Errorf("token %q: the marker (token material) stays visible in the rendered error %q", token, err.Error())
		}
		if got := carriedCode(err); got != "FORBIDDEN" {
			t.Errorf("token %q: an ordinary server code must be kept, got %q", token, got)
		}
	}
	// A code carrying the marker-equal token is still dropped on this path.
	if got := carriedCode(setTokenError(mk("BAD [REDACTED]"), "[REDACTED]", false)); got != "" {
		t.Errorf("a code carrying the marker-equal token must be dropped, got %q", got)
	}
}

// TestEscapedSpellingsLiteralBackslashU2714 pins Dara's P2 (cli#2714 blind
// round 2): a token containing the six LITERAL characters backslash-u-2028 Go-escapes
// to \\u2028; a blind textual replacer matches from the second backslash and
// invents a spelling Node would never emit (backslash + RAW separator), which
// can scrub unrelated text or drop an innocent server code or URN. The Node
// spelling must be derived by whole JSON escape units.
func TestEscapedSpellingsLiteralBackslashU2714(t *testing.T) {
	have := func(set []string, want string) bool {
		for _, s := range set {
			if s == want {
				return true
			}
		}
		return false
	}
	// Literal backslash-u text only (the eight characters x \ u 2 0 2 8 y):
	// Go and Node both write the backslash doubled; no raw-separator
	// spelling of this token exists.
	token := "x\\u2028y"
	got := escapedSpellings(token)
	bogus := "x\\" + "\u2028" + "y" // backslash, RAW U+2028 — Node never writes this here
	if have(got, bogus) {
		t.Errorf("bogus Node spelling generated from literal backslash-u: %q", got)
	}
	if !have(got, `x\\u2028y`) {
		t.Errorf("the true escaped spelling of the literal text is missing: %q", got)
	}
	// Literal backslash-u text AND a real separator: the true Node spelling
	// doubles the literal backslash and writes the real separator raw.
	got = escapedSpellings("x\\u2028y" + "\u2028")
	if !have(got, `x\\u2028y`+"\u2028") {
		t.Errorf("the true Node spelling of the mixed token is missing: %q", got)
	}
}

// TestSetTokenNoReplacementRecreatesToken2714 pins the round-4 rule (Bruno
// P2, Dara P2, Eden's ruling): the scrubber's only replacement is the marker
// "[REDACTED]", and any replacement can itself (re)create token text — a
// token contained in the marker is re-created inside every replacement
// ("REDACTED" → "[REDACTED]"), a token spanning a seam re-forms around it
// ("x[" in "xx[" → "x[REDACTED]"), a token containing the marker replaces to
// itself, and a token-free-looking message may still carry an escaped
// spelling. Partial redaction of the message is therefore only allowed when
// the message provably carries no covered spelling AND no replacement can
// recreate the token; every other message renders the fixed refusal.
func TestSetTokenNoReplacementRecreatesToken2714(t *testing.T) {
	const refusal = "the server refused the source token"
	mkErr := func(msg string) error {
		return &gqlerror.Error{Message: msg, Extensions: map[string]any{"code": "FORBIDDEN"}}
	}
	for _, c := range []struct{ name, token, msg string }{
		{"token equals the marker text", "[REDACTED]", "refused: [REDACTED] echo"},
		{"token is a substring of the marker", "REDACTED", "refused: REDACTED echo"},
		{"marker prefix", "[REDACT", "refused: [REDACT echo"},
		{"marker suffix", "ED]", "refused: ED] echo"},
		{"token spans the replacement seam", "x[", "refused: xx[ echo"},
		{"token contains the marker", "tok[REDACTED]", "refused: tok[REDACTED] echo"},
		{"escaped spelling only, literal absent", `a"b`, `refused: a\"b echo`},
		{"substring token, message itself clean", "REDACTED", "refused: quota exceeded"},
	} {
		err := setTokenError(mkErr(c.msg), c.token, false)
		if strings.Contains(err.Error(), c.token) {
			t.Errorf("%s: the token survives in the rendered error %q", c.name, err.Error())
		}
		// Exact equality, not containment: an error holding BOTH the refusal
		// and a leftover echo of the server message would pass a containment
		// check (cli#2714 Dara round-4 delta P3). None of these tokens can
		// reach a lower tier, so the full refusal is the exact expectation.
		if err.Error() != refusal {
			t.Errorf("%s: this message must render the fixed refusal exactly, got %q", c.name, err.Error())
		}
		if got := carriedCode(err); got != "FORBIDDEN" {
			t.Errorf("%s: an ordinary server code must be kept, got %q", c.name, got)
		}
	}
	// A code carrying the token is dropped on the same path.
	codeErr := &gqlerror.Error{Message: "refused", Extensions: map[string]any{"code": "BAD REDACTED"}}
	if got := carriedCode(setTokenError(codeErr, "REDACTED", false)); got != "" {
		t.Errorf("a code carrying the token must be dropped, got %q", got)
	}
	// The negative space: a token disjoint from the marker over a message
	// carrying no spelling keeps the server's own text (never over-redact).
	if got := setTokenError(mkErr("refused: quota exceeded"), "tok-2704", false).Error(); !strings.Contains(got, "refused: quota exceeded") || strings.Contains(got, refusal) {
		t.Errorf("a token-free message must keep the server's own text, got %q", got)
	}
}

// TestSetTokenRefusalItselfCarriesNoToken2714 pins Eden's round-5 rule after
// Bruno's round-4 delta P2: the fixed refusal is the drop path's OUTPUT, so
// the token check must hold against the refusal text too — a token "server
// refused" is inside "the server refused the source token". A token that is a
// word of (or substring of, or equal to the whole of) the first refusal must
// not be re-echoed by it: the drop path renders a second fixed text sharing
// no word with the first, and a token inside BOTH texts renders no echo at
// all — the empty message provably carries no spelling of a non-empty token
// (the drop path guarantees one), while the exit code and a clean server code
// still identify the refusal.
func TestSetTokenRefusalItselfCarriesNoToken2714(t *testing.T) {
	mkErr := func(msg string) error {
		return &gqlerror.Error{Message: msg, Extensions: map[string]any{"code": "FORBIDDEN"}}
	}
	for _, c := range []struct{ name, token, msg, want string }{
		{"token is a word of the refusal", "refused", "that value was refused", "input declined"},
		{"token is another word of the refusal", "server", "server said no", "input declined"},
		{"token equals the whole refusal", "the server refused the source token", "echo: the server refused the source token", "input declined"},
		{"token sits inside both fixed texts", "ed", "request denied", ""},
	} {
		err := setTokenError(mkErr(c.msg), c.token, false)
		if strings.Contains(err.Error(), c.token) {
			t.Errorf("%s: the token survives in the rendered error %q", c.name, err.Error())
		}
		if err.Error() != c.want {
			t.Errorf("%s: rendered error = %q, want %q", c.name, err.Error(), c.want)
		}
		if got := carriedCode(err); got != "FORBIDDEN" {
			t.Errorf("%s: an ordinary server code must be kept, got %q", c.name, got)
		}
	}
	// The negative space: the cascade only walks when the earlier tier is
	// unsafe for THIS token — a token inside the second text but not the
	// first keeps the full refusal (never over-redact).
	if got := setTokenError(mkErr("bad input"), "input", false).Error(); got != "the server refused the source token" {
		t.Errorf("a token only in the second text must render the full refusal, got %q", got)
	}
}

// TestSetTokenSanitizationCannotManufactureSpelling2714 pins the output-side
// arm of Eden's round-5 rule (flagged for verification by Dara's BLIND delta
// note): the partial-redaction path judges the RAW message, but what is
// rendered is output.SafeLine(redacted) — and SafeLine NEUTRALISES control
// characters to a space and can append an ellipsis at the rune bound. Either
// transform can join characters into a spelling the raw message never
// carried (message "a\x00b", token "a b": clean raw, token visible after
// sanitization). The FINAL candidate message must be judged, not the input:
// a candidate that carries the token renders the refusal cascade like any
// other unsafe message.
func TestSetTokenSanitizationCannotManufactureSpelling2714(t *testing.T) {
	mkErr := func(msg string) error {
		return &gqlerror.Error{Message: msg, Extensions: map[string]any{"code": "FORBIDDEN"}}
	}
	// A control character between the token's halves: no covered spelling of
	// "a b" is present raw, so the round-4 guards pass — but the rendered
	// SafeLine form spells it.
	err := setTokenError(mkErr("a\x00b is not allowed"), "a b", false)
	if strings.Contains(err.Error(), "a b") {
		t.Errorf("a token manufactured by control-character sanitization must not reach the output, got %q", err.Error())
	}
	if err.Error() != "the server refused the source token" {
		t.Errorf("a sanitization-manufactured spelling must render the full refusal, got %q", err.Error())
	}
	if got := carriedCode(err); got != "FORBIDDEN" {
		t.Errorf("an ordinary server code must be kept, got %q", got)
	}
	// The negative space: a message whose sanitization manufactures nothing
	// keeps the partial redaction (the guard must not refuse every message
	// with a control character in it).
	if got := setTokenError(mkErr("a\x00b is not allowed"), "tok-2704", false).Error(); got != "a b is not allowed" {
		t.Errorf("an unrelated token over a control-character message must keep the sanitized server text, got %q", got)
	}
}

// TestSetTokenRefusalExhaustiveSubstrings2714 is Jerry's exhaustive pin: every
// substring of both fixed refusals — and of their concatenation — is a token
// some user could hold, so each one is driven through the drop path and the
// rendered output must never contain it. The named round-5 cases stand for
// readability; this loop stands for totality, and it kills a mutant that
// drops a tier's containsAnySpelling gate on a slice the named cases do not
// cover (a reorder is caught instead by the exact-equality assertions, which
// pin WHICH tier renders). All three texts are ASCII, so byte slicing is
// rune-safe.
func TestSetTokenRefusalExhaustiveSubstrings2714(t *testing.T) {
	mkErr := func(msg string) error {
		return &gqlerror.Error{Message: msg, Extensions: map[string]any{"code": "FORBIDDEN"}}
	}
	for _, base := range []string{fullTokenRefusal, secondTokenRefusal, fullTokenRefusal + secondTokenRefusal} {
		for i := 0; i < len(base); i++ {
			for j := i + 1; j <= len(base); j++ {
				token := base[i:j]
				// The message carries the token, so the drop path fires for
				// every one of them.
				err := setTokenError(mkErr("x "+token+" x"), token, false)
				if strings.Contains(err.Error(), token) {
					t.Errorf("substring %q of the fixed refusals: the token survives in the rendered error %q", token, err.Error())
				}
			}
		}
	}
}

// TestSetTokenUrnSentinelEqualTokenIsNotSpliced2704 pins Jack F1: the URN
// guard was redactToken(u, token) == u, a fixpoint for EVERY u when the token
// equals the sentinel "[REDACTED]" (replacing the sentinel with itself is
// identity, and that token has no distinct escaped spellings) — so a URN that
// carries the token spliced into the receipt. The guard must be a containment
// test instead.
func TestSetTokenUrnSentinelEqualTokenIsNotSpliced2704(t *testing.T) {
	const token = "[REDACTED]"
	urn := "hrn:mem:acme.com:kb-" + token
	out, err := setTokenRun(t, "acme.com:kb", urn, token, false)
	if err != nil {
		t.Fatalf("set-token failed: %v", err)
	}
	if strings.Contains(out, urn) {
		t.Errorf("a URN carrying the sentinel-equal token must not splice into the receipt, got %q", out)
	}
	if !strings.Contains(out, "hrn:mem:acme.com:kb") {
		t.Errorf("the typed ref must name the memory instead, got %q", out)
	}
	// The same choice shows in the --json receipt's memory field.
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
	if dto.Memory == urn || strings.Contains(dto.Memory, token) {
		t.Errorf("--json memory must fall back to the typed ref, got %q", dto.Memory)
	}
	if dto.Memory != "hrn:mem:acme.com:kb" {
		t.Errorf("--json memory must be the canonical typed ref, got %q", dto.Memory)
	}
}

// TestSetTokenUrnSplicedWhenTokenFree2704 pins Jack F4, the positive side the
// negative pins cannot hold alone: a URN the token does NOT appear in (in any
// spelling) is still spliced into the receipt, so hardening the guard never
// becomes over-redaction. The memory is typed by ID — a ref that cannot
// canonicalize to the URN locally — so the receipt can carry the server's URN
// only through the splice; a guard that never splices fails this pin (mutant 5
// of the plan found the first draft vacuous: typed acme.com:kb, which
// CanonicalMemoryRef composes to the same URN as the splice would).
func TestSetTokenUrnSplicedWhenTokenFree2704(t *testing.T) {
	const urn = "hrn:mem:acme.com:kb"
	const byID = "01a13b5bc06c79faa2a41a99322be113"
	out, err := setTokenRun(t, byID, urn, "tok-UNTouched-2704", false)
	if err != nil {
		t.Fatalf("set-token failed: %v", err)
	}
	if !strings.Contains(out, "source token set for \""+urn+"\"") {
		t.Errorf("a token-free URN must splice into the receipt, got %q", out)
	}
	if strings.Contains(out, "tok-UNTouched-2704") {
		t.Errorf("the token must never appear in the receipt, got %q", out)
	}
	out, err = setTokenRun(t, byID, urn, "tok-UNTouched-2704", true)
	if err != nil {
		t.Fatalf("set-token --json failed: %v", err)
	}
	var dto struct {
		Memory    string `json:"memory"`
		Operation string `json:"operation"`
		TokenSet  bool   `json:"tokenSet"`
	}
	if err := json.Unmarshal([]byte(out), &dto); err != nil {
		t.Fatalf("receipt is not JSON: %v (%q)", err, out)
	}
	if dto.Memory != urn || dto.Operation != "set-token" || !dto.TokenSet {
		t.Errorf("--json receipt = %+v, want memory %q, operation set-token, tokenSet true", dto, urn)
	}
}
