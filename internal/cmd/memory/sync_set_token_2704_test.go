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
	if got := carriedCode(setTokenError(mk(`BAD a\"b`), token)); got != "" {
		t.Errorf("a server code holding an escaped spelling of the token must be dropped, got %q", got)
	}
	// The literal spelling stays covered (cli#2650 item 4 regression guard).
	if got := carriedCode(setTokenError(mk("BAD "+token), token)); got != "" {
		t.Errorf("a server code holding the literal token must be dropped, got %q", got)
	}
	if got := carriedCode(setTokenError(mk("FORBIDDEN"), token)); got != "FORBIDDEN" {
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
		_ = json.NewDecoder(r.Body).Decode(&body)
		if body.OperationName != "SetMemorySourceToken" {
			t.Errorf("unexpected operation %q", body.OperationName)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":{"setMemorySourceToken":{"id":"m1","urn":"` + urn + `"}}}`))
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
