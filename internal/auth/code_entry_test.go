package auth

// hadron-cli#3180: code-entry login for a host with no browser. The CLI asks
// the server to SHOW the code (code_delivery=page), takes it pasted, and
// exchanges it with its own verifier and registered redirect_uri; the code is
// opaque, never echoed, and a refusal is one fixed line.

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/hadron-memory/hadron-cli/internal/exitcode"
	"github.com/hadron-memory/hadron-cli/internal/output"
)

// printedAuthorizeURL pulls the LAST authorize address the CLI printed on
// stderr (after an open failure the default address precedes the code-page one).
func printedAuthorizeURL(t *testing.T, errOut string) *url.URL {
	t.Helper()
	var last string
	for _, line := range strings.Split(errOut, "\n") {
		if line = strings.TrimSpace(line); strings.Contains(line, "/oauth/authorize?") {
			last = line
		}
	}
	if last == "" {
		t.Fatalf("no authorize address was printed:\n%s", errOut)
	}
	u, err := url.Parse(last)
	if err != nil {
		t.Fatalf("printed address does not parse: %v", err)
	}
	return u
}

// readingCode answers with code, first checking (while the login is still
// running, when a listener would still be up) that nothing listens on the
// redirect address the client registered.
func readingCode(t *testing.T, as *fakeAS, code string) CodeReader {
	return func(context.Context) (string, error) {
		if as.registeredRedirect == "" {
			t.Errorf("no redirect_uri was registered before the prompt")
		} else if c, err := net.DialTimeout("tcp", strings.TrimSuffix(strings.TrimPrefix(as.registeredRedirect, "http://"), "/callback"), time.Second); err == nil {
			_ = c.Close()
			t.Errorf("no loopback listener may run in code-entry mode")
		}
		return code, nil
	}
}

func staticCode(code string) CodeReader {
	return func(context.Context) (string, error) { return code, nil }
}

// Acceptance 2: no flag, the browser cannot be opened. The CLI takes the code
// by hand instead of waiting 15 minutes on a redirect that cannot arrive, and
// the address it prints carries the code-page parameter.
func TestOpenFailureFallsBackToCodeEntry_3180(t *testing.T) {
	as := newFakeAS(t)
	streams, _, errOut := output.Test()
	start := time.Now()
	tok, err := BrowserStrategy{}.Login(loginCtx(t), LoginOptions{
		ServerURL:   as.server.URL,
		IO:          streams,
		HTTPClient:  as.server.Client(),
		OpenBrowser: func(string) error { return errors.New("no display") },
		ReadCode:    readingCode(t, as, as.authCode+"\n"), // a paste drags its newline along
	})
	if err != nil {
		t.Fatalf("login: %v", err)
	}
	if tok == nil || !strings.HasPrefix(tok.AccessToken, "hdr_user_") {
		t.Fatalf("no usable token: %+v", tok)
	}
	if time.Since(start) > 5*time.Second {
		t.Errorf("code entry must not wait on the loopback")
	}
	if got := printedAuthorizeURL(t, errOut.String()).Query().Get("code_delivery"); got != "page" {
		t.Errorf("the address printed after the open failure must ask for the code page, code_delivery=%q", got)
	}
}

// Acceptance 3 and 4: the flag enters code-entry mode even where a browser
// would work, never opens one, starts no listener, and the exchange presents
// the pasted code, the CLI's own verifier and its registered redirect_uri.
func TestCodeEntryFlagExchangesPastedCode_3180(t *testing.T) {
	as := newFakeAS(t)
	streams, out, errOut := output.Test()
	opened := false
	tok, err := BrowserStrategy{}.Login(loginCtx(t), LoginOptions{
		ServerURL:   as.server.URL,
		IO:          streams,
		HTTPClient:  as.server.Client(),
		CodeEntry:   true,
		OpenBrowser: func(string) error { opened = true; return nil },
		ReadCode:    readingCode(t, as, "  "+as.authCode+"  "),
	})
	if err != nil {
		t.Fatalf("login: %v", err)
	}
	if opened {
		t.Errorf("--no-browser must not open a browser")
	}
	if tok == nil || !strings.HasPrefix(tok.AccessToken, "hdr_user_") {
		t.Fatalf("no usable token: %+v", tok)
	}
	u := printedAuthorizeURL(t, errOut.String())
	q := u.Query()
	if q.Get("code_delivery") != "page" {
		t.Errorf("code_delivery=%q, want page", q.Get("code_delivery"))
	}
	if q.Get("redirect_uri") != as.registeredRedirect || as.seenRedirect != as.registeredRedirect {
		t.Errorf("the exchange must present the registered redirect_uri: authorize %q, registered %q, exchanged %q", q.Get("redirect_uri"), as.registeredRedirect, as.seenRedirect)
	}
	if as.seenCode != as.authCode {
		t.Errorf("the code must be sent whole and only trimmed of surrounding whitespace, got %q", as.seenCode)
	}
	sum := sha256.Sum256([]byte(as.seenVerifier))
	if base64.RawURLEncoding.EncodeToString(sum[:]) != q.Get("code_challenge") {
		t.Errorf("the exchange must use the verifier of the printed challenge")
	}
	if out.Len() != 0 {
		t.Errorf("nothing belongs on stdout: %q", out.String())
	}
}

// Acceptance 5 and 6: a refused code is one fixed line, whatever the server
// said about why, and neither the code nor the token reaches any output.
func TestRefusedCodeIsOneFixedLineAndNeverEchoed_3180(t *testing.T) {
	const secret = "SECRET.pasted-code-0123456789"
	var lines []string
	for _, desc := range []string{"code is unknown", "code expired", "redirect_uri mismatch", ""} {
		as := newFakeAS(t)
		as.tokenOverride = func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusBadRequest)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": "invalid_grant", "error_description": desc})
		}
		streams, out, errOut := output.Test()
		tok, err := BrowserStrategy{}.Login(loginCtx(t), LoginOptions{
			ServerURL: as.server.URL, IO: streams, HTTPClient: as.server.Client(),
			CodeEntry: true, ReadCode: staticCode(secret),
		})
		if err == nil || tok != nil {
			t.Fatalf("a refused code must fail, got token %+v err %v", tok, err)
		}
		if got := exitcode.FromError(err); got != exitcode.Error {
			t.Errorf("exit = %d, want %d", got, exitcode.Error)
		}
		if err.Error() != codeRefusal {
			t.Errorf("the refusal must be the one fixed line, got %q", err.Error())
		}
		for name, text := range map[string]string{"error": err.Error(), "stdout": out.String(), "stderr": errOut.String()} {
			if strings.Contains(text, secret) || strings.Contains(text, "SECRET") {
				t.Errorf("the pasted code must never be echoed (%s): %q", name, text)
			}
		}
		lines = append(lines, err.Error())
	}
	for _, l := range lines[1:] {
		if l != lines[0] {
			t.Errorf("the CLI must not distinguish the server's states: %q vs %q", l, lines[0])
		}
	}
}

// Acceptance 8: a host with nowhere to paste (stdin not a terminal) does not
// hang. It refuses at once and points at the token paths; the explicit flag
// refuses before any request is made, and so does the open-failure fallback.
func TestCodeEntryWithoutTerminalRefuses_3180(t *testing.T) {
	for name, opts := range map[string]LoginOptions{
		"flag":         {CodeEntry: true},
		"open failure": {OpenBrowser: func(string) error { return errors.New("no display") }},
	} {
		t.Run(name, func(t *testing.T) {
			as := newFakeAS(t)
			streams, _, _ := output.Test() // stdin is not a terminal
			opts.ServerURL, opts.IO, opts.HTTPClient = as.server.URL, streams, as.server.Client()
			start := time.Now()
			tok, err := BrowserStrategy{}.Login(loginCtx(t), opts)
			if err == nil || tok != nil {
				t.Fatalf("must refuse, got token %+v err %v", tok, err)
			}
			if got := exitcode.FromError(err); got != exitcode.Usage {
				t.Errorf("exit = %d, want Usage %d (%v)", got, exitcode.Usage, err)
			}
			for _, want := range []string{"--with-token", "HADRON_TOKEN"} {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("the refusal must point to %s: %v", want, err)
				}
			}
			if strings.Contains(err.Error(), "\n") {
				t.Errorf("the refusal is one line: %q", err.Error())
			}
			if time.Since(start) > 5*time.Second {
				t.Errorf("must not wait")
			}
			if as.tokenCalls != 0 {
				t.Errorf("nothing may be exchanged")
			}
		})
	}
}

// Acceptance 8, the bound: the wait for a code ends at the login timeout with
// the same Cancelled answer, nothing stored.
func TestCodeEntryWaitIsBoundedByLoginTimeout_3180(t *testing.T) {
	as := newFakeAS(t)
	streams, _, _ := output.Test()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	waiting := make(chan struct{})
	tok, err := BrowserStrategy{}.Login(ctx, LoginOptions{
		ServerURL: as.server.URL, IO: streams, HTTPClient: as.server.Client(), CodeEntry: true,
		ReadCode: func(ctx context.Context) (string, error) {
			close(waiting)
			<-ctx.Done()
			return "", ctx.Err()
		},
	})
	select {
	case <-waiting:
	default:
		t.Fatalf("the deadline fired before the prompt was reached: %v", err)
	}
	if tok != nil || exitcode.FromError(err) != exitcode.Cancelled {
		t.Fatalf("want Cancelled and no token, got %+v / %v", tok, err)
	}
	for _, want := range []string{"did not finish within", "nothing was stored"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("timeout message must say %q: %v", want, err)
		}
	}
}

// Acceptance 9, paste first: with a browser and no flag, a code pasted before
// the redirect arrives completes the login with the same exchange.
func TestPasteBeatsRedirectInDefaultMode_3180(t *testing.T) {
	as := newFakeAS(t)
	streams, _, errOut := output.Test()
	tok, err := BrowserStrategy{}.Login(loginCtx(t), LoginOptions{
		ServerURL: as.server.URL, IO: streams, HTTPClient: as.server.Client(),
		OpenBrowser: func(string) error { return nil }, // the redirect never comes
		ReadCode:    staticCode(as.authCode),
	})
	if err != nil {
		t.Fatalf("login: %v", err)
	}
	if tok == nil || as.seenCode != as.authCode || as.seenRedirect != as.registeredRedirect {
		t.Errorf("the paste must run the same exchange: token %+v code %q redirect %q", tok, as.seenCode, as.seenRedirect)
	}
	// Loopback stays the default: the address the browser got does not ask for the page.
	if got := printedAuthorizeURL(t, errOut.String()).Query().Get("code_delivery"); got != "" {
		t.Errorf("the default address must not ask for the code page, code_delivery=%q", got)
	}
}

// Acceptance 1 and 9, redirect first: the redirect wins, exactly as today, and
// the paste reader is stopped (its context ends) rather than left reading.
func TestRedirectBeatsPasteInDefaultMode_3180(t *testing.T) {
	as := newFakeAS(t)
	streams, _, _ := output.Test()
	stopped := make(chan struct{})
	tok, err := BrowserStrategy{}.Login(loginCtx(t), LoginOptions{
		ServerURL: as.server.URL, IO: streams, HTTPClient: as.server.Client(),
		OpenBrowser: consentingBrowser(as),
		ReadCode: func(ctx context.Context) (string, error) {
			<-ctx.Done()
			close(stopped)
			return "", ctx.Err()
		},
	})
	if err != nil || tok == nil {
		t.Fatalf("login: %v", err)
	}
	select {
	case <-stopped:
	case <-time.After(2 * time.Second):
		t.Errorf("the paste reader must be stopped once the redirect wins")
	}
}
