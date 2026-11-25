package auth

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os/exec"
	"runtime"
	"slices"
	"strings"
	"time"

	"github.com/hadron-memory/hadron-cli/internal/api"
	"github.com/hadron-memory/hadron-cli/internal/exitcode"
)

// loginTimeout bounds the whole browser dance. 15 minutes is a CLI engineering
// choice, not a contract value: first passkey enrolment on a #1897 server takes
// two inbox round trips plus a registration ceremony inside one login, which 5
// minutes did not reliably hold (cli#875). It is an upper bound only. If the
// server's pending-flow lifetime is shorter, this drops to match, because
// waiting past the point the server can still issue a code only delays the
// Cancelled message.
//
// A var only so a test can shorten it to prove the internal deadline fires
// (cli#1624); nothing else assigns it, and TestLoginTimeoutIsFifteenMinuteUpperBound
// pins the shipped value.
var loginTimeout = 15 * time.Minute

// accountScope is the OAuth scope the CLI requests (cor:aut:010:02). The CLI
// drives the GraphQL data API, which refuses a key carrying `mcp` alone, so
// `account` is the only scope that yields a usable credential. There is
// deliberately no fallback to `mcp`: that would store a key the CLI cannot
// use (#658).
const accountScope = "account"

// withTokenHint is the remedy when the server cannot issue an `account` key
// through the browser flow: a portal token is unscoped (cor:aut:010:02 rule 3).
const withTokenHint = "sign in with a portal token instead: echo $TOKEN | hadron auth login --with-token"

// BrowserStrategy signs in via authorization-code + PKCE with a
// loopback redirect: discover endpoints, bind 127.0.0.1:<port>,
// register a one-shot public client for that exact redirect URI,
// send the user to the consent screen, exchange the code.
type BrowserStrategy struct{}

func (BrowserStrategy) Name() string { return "browser" }

func (BrowserStrategy) Login(ctx context.Context, opts LoginOptions) (*Token, error) {
	loginProvider, err := loginProviderParam(opts.LoginProvider)
	if err != nil {
		return nil, err
	}

	httpClient := opts.HTTPClient
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 30 * time.Second}
	}
	openBrowser := opts.OpenBrowser
	if openBrowser == nil {
		openBrowser = OpenInBrowser
	}

	readCode := opts.ReadCode
	if readCode == nil {
		readCode = pasteReader(opts.IO)
	}
	// A code can be pasted only where something reads it: an injected reader,
	// or a terminal on standard input. Without one, code entry refuses at once
	// instead of hanging until the login timeout.
	canReadCode := opts.ReadCode != nil || opts.IO.IsInputTerminal()
	if opts.CodeEntry && !canReadCode {
		return nil, errNoCodeInput()
	}

	// The limit named on timeout is the one that will actually fire: ours, or
	// a caller's earlier deadline.
	bound := loginTimeout
	if deadline, ok := ctx.Deadline(); ok && time.Until(deadline) < bound {
		bound = time.Until(deadline)
	}
	ctx, cancel := context.WithTimeout(ctx, loginTimeout)
	defer cancel()

	meta, err := Discover(ctx, opts.ServerURL, httpClient)
	if err != nil {
		return nil, err
	}
	// Refuse before opening a browser: a server that predates `account`
	// would otherwise walk the user through consent only to bounce them.
	if err := requireAccountScope(meta.ScopesSupported, opts.ServerURL); err != nil {
		return nil, err
	}
	resource, err := DiscoverResource(ctx, opts.ServerURL, httpClient)
	if err != nil {
		return nil, err
	}

	pk, err := newPKCE()
	if err != nil {
		return nil, err
	}

	// Code-entry mode (the flag) starts no loopback listener at all: nothing
	// will be redirected, so there is nothing to wait on. The default keeps the
	// loopback redirect exactly as before.
	var loopback *loopbackServer
	var redirectURI string
	if opts.CodeEntry {
		redirectURI, err = reserveRedirectURI()
	} else {
		loopback, err = startLoopback(pk.State)
		if err == nil {
			defer func() { _ = loopback.Close() }()
			redirectURI = loopback.RedirectURI()
		}
	}
	if err != nil {
		return nil, err
	}

	clientID, err := registerClient(ctx, httpClient, meta.RegistrationEndpoint, redirectURI)
	if err != nil {
		return nil, err
	}

	authorizeURL := func(codePage bool) string {
		params := url.Values{
			"response_type":         {"code"},
			"client_id":             {clientID},
			"redirect_uri":          {redirectURI},
			"state":                 {pk.State},
			"code_challenge":        {pk.Challenge},
			"code_challenge_method": {"S256"},
			"scope":                 {accountScope},
		}
		if resource != "" {
			params.Set("resource", resource)
		}
		if loginProvider != "" {
			params.Set("login_provider", loginProvider)
		}
		if codePage {
			params.Set(codeDeliveryParam, codeDeliveryPage)
		}
		return meta.AuthorizationEndpoint + "?" + params.Encode()
	}
	promptForCode := func() {
		fmt.Fprintf(opts.IO.ErrOut, "Open this address in a browser, on any device, and sign in:\n  %s\n", authorizeURL(true))
		fmt.Fprint(opts.IO.ErrOut, "Then paste the code the page shows (it is not shown as you type): ")
	}

	if opts.CodeEntry {
		promptForCode()
		code, err := waitForCode(ctx, readCode, bound)
		if err != nil {
			return nil, err
		}
		return exchangeEnteredCode(ctx, httpClient, meta.TokenEndpoint, clientID, code, pk.Verifier, redirectURI, resource)
	}

	fmt.Fprintln(opts.IO.ErrOut, "Opening your browser to sign in to Hadron...")
	defaultURL := authorizeURL(false)
	// refusalEnds: in the fallback below the paste is the way in, so a refused
	// code ends the login. With a browser open the redirect may still arrive,
	// so a refused paste is only reported.
	refusalEnds := false
	if err := openBrowser(defaultURL); err != nil {
		fmt.Fprintf(opts.IO.ErrOut, "(could not open browser automatically: %v)\n", err)
		// No browser here, so take the code by hand, chosen on this side (the
		// server never infers it). Only the code-entry address is printed: the
		// default one would send the code to a loopback address nobody can
		// reach. The loopback listener stays up for a forwarded port.
		if !canReadCode {
			return nil, errNoCodeInput()
		}
		promptForCode()
		refusalEnds = true
	} else {
		fmt.Fprintf(opts.IO.ErrOut, "If the browser doesn't open, visit:\n  %s\n", defaultURL)
		if canReadCode {
			fmt.Fprintln(opts.IO.ErrOut, "If you were given a code instead, paste it here (it is not shown as you type):")
		}
	}

	// A code pasted while the redirect is awaited completes the login too,
	// whichever comes first (cor:aut:030:01: the client always accepts a
	// pasted code). The reader stops when ctx ends, which Login's return
	// causes, and leaves the terminal as it found it.
	var pasted chan pasteResult
	if canReadCode {
		pasted = make(chan pasteResult, 1)
		readCtx, stopReading := context.WithCancel(ctx)
		readerDone := make(chan struct{})
		go func() {
			defer close(readerDone)
			c, err := readCode(readCtx)
			pasted <- pasteResult{code: c, err: err}
		}()
		// Whatever way this returns (a token, an error, Ctrl+C), stop the
		// reader and wait for it, so the terminal's echo is back before the
		// process can exit.
		defer func() {
			stopReading()
			<-readerDone
		}()
	}
	for {
		select {
		case res := <-loopback.results:
			if errors.Is(res.err, errInvalidScope) {
				return nil, exitcode.Newf(exitcode.Error,
					"%s refused the %q OAuth scope (%v); the CLI needs a server that issues %q keys (hadron-server#1261) — %s",
					opts.ServerURL, accountScope, res.err, accountScope, withTokenHint)
			}
			if res.err != nil {
				return nil, res.err
			}
			return exchangeCode(ctx, httpClient, meta.TokenEndpoint, clientID, res.code, pk.Verifier, redirectURI, resource)
		case p := <-pasted:
			pasted = nil // a failed read does not end the wait for the redirect
			if p.err != nil {
				continue
			}
			code := cleanCode(p.code)
			if code == "" {
				continue
			}
			tok, err := exchangeEnteredCode(ctx, httpClient, meta.TokenEndpoint, clientID, code, pk.Verifier, redirectURI, resource)
			if err != nil && !refusalEnds && isCodeRefusal(err) {
				fmt.Fprintf(opts.IO.ErrOut, "note: %s; still waiting for the browser sign-in to finish\n", codeRefusal)
				continue
			}
			return tok, err
		case <-ctx.Done():
			return nil, ctxDoneError(ctx, bound)
		}
	}
}

type pasteResult struct {
	code string
	err  error
}

// loginProviderParam returns the provider hint sent to /oauth/authorize.
// GitHub is deliberately omitted so the default CLI remains compatible with
// servers predating provider selection. Credentials stay on the server; this
// value only chooses which first-party login route performs the browser hop.
func loginProviderParam(provider string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(provider)) {
	case "", "github":
		return "", nil
	case "google":
		return "google", nil
	default:
		return "", exitcode.Newf(exitcode.Usage, "unsupported login provider %q (choose github or google)", provider)
	}
}

// exchangeEnteredCode is exchangeCode for a code a person pasted. The server
// refuses a wrong, expired, used, exhausted or mismatched code with the same
// invalid_grant, and the CLI answers all of them with the one fixed line: it
// never repeats the code and counts nothing itself (the limit is the server's).
func exchangeEnteredCode(ctx context.Context, httpClient *http.Client, tokenEndpoint, clientID, code, verifier, redirectURI, resource string) (*Token, error) {
	tok, err := exchangeCodeWith(ctx, httpClient, tokenEndpoint, clientID, code, verifier, redirectURI, resource, true)
	if errors.Is(err, errCodeRefused) {
		return nil, exitcode.Newf(exitcode.Error, "%s", codeRefusal)
	}
	return tok, err
}

func exchangeCode(ctx context.Context, httpClient *http.Client, tokenEndpoint, clientID, code, verifier, redirectURI, resource string) (*Token, error) {
	return exchangeCodeWith(ctx, httpClient, tokenEndpoint, clientID, code, verifier, redirectURI, resource, false)
}

func exchangeCodeWith(ctx context.Context, httpClient *http.Client, tokenEndpoint, clientID, code, verifier, redirectURI, resource string, entered bool) (*Token, error) {
	form := url.Values{
		"grant_type":    {"authorization_code"},
		"code":          {code},
		"redirect_uri":  {redirectURI},
		"client_id":     {clientID},
		"code_verifier": {verifier},
	}
	if resource != "" {
		form.Set("resource", resource)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, tokenEndpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, exitcode.Newf(exitcode.Error, "token exchange failed: %v", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		if limited := tokenRateLimit(resp.StatusCode, resp.Header, body); limited != nil {
			return nil, limited
		}
		if fault := api.PlainHTTPInternalFault(resp.Header, body); fault != nil {
			return nil, fault
		}
		if entered && resp.StatusCode == http.StatusBadRequest && isInvalidGrant(body) {
			return nil, errCodeRefused
		}
		return nil, exitcode.Newf(exitcode.Error, "token exchange returned HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	var out struct {
		AccessToken string `json:"access_token"`
		TokenType   string `json:"token_type"`
		// Raw, because presence is the whole question: an omitted scope
		// means "as requested" (RFC 6749 §5.1), while a present "" or null
		// is not a grant of `account`. A *string would decode null as nil,
		// indistinguishable from omitted.
		Scope json.RawMessage `json:"scope"`
	}
	if err := json.Unmarshal(body, &out); err != nil || out.AccessToken == "" {
		return nil, exitcode.Newf(exitcode.Error, "token response missing access_token")
	}
	// RFC 6749 §5.1: an omitted scope means the grant equals the request.
	// A present one that is not a string naming `account` — "", null, or a
	// non-string included — is not a key the data API accepts, so it is not
	// handed back to be stored.
	if out.Scope != nil && !grantsAccount(out.Scope) {
		return nil, exitcode.Newf(exitcode.Error,
			"the server granted OAuth scope %s, not %q — refusing to store a key the CLI cannot use (the issued key was not stored; revoke it on the portal's API keys page); %s",
			out.Scope, accountScope, withTokenHint)
	}
	return &Token{AccessToken: out.AccessToken}, nil
}

// grantsAccount reports whether a PRESENT token-response scope is a JSON
// string whose space-delimited values include `account`. A non-string fails
// to decode; null decodes as "", which names nothing — both are refused.
// The delimiter is the single space of RFC 6749 §3.3, not any whitespace: a
// tab or newline is not a legal scope-token character, so "mcp\taccount" is
// one malformed token rather than a grant of `account`.
func grantsAccount(raw json.RawMessage) bool {
	var granted string
	if json.Unmarshal(raw, &granted) != nil {
		return false
	}
	return slices.Contains(strings.Split(granted, " "), accountScope)
}

// requireAccountScope refuses a server whose discovery metadata lists its
// supported OAuth scopes without `account`. An ABSENT list (nil) is not a
// refusal: scopes_supported is optional (RFC 8414), and the authorize step
// still reports an unsupported scope as invalid_scope. A present empty list
// is a server offering nothing, and is refused.
func requireAccountScope(supported []string, serverURL string) error {
	if supported == nil || slices.Contains(supported, accountScope) {
		return nil
	}
	advertised := strings.Join(supported, ", ")
	if advertised == "" {
		advertised = "none"
	}
	return exitcode.Newf(exitcode.Error,
		"%s does not offer the %q OAuth scope (it advertises: %s); the CLI needs a server that issues %q keys (hadron-server#1261) — %s",
		serverURL, accountScope, advertised, accountScope, withTokenHint)
}

// OpenInBrowser launches the platform's URL opener. The target is passed as
// argv (never via a shell), but a leading '-' would still be parsed as a flag
// by `open`, so it is rejected outright and, on darwin, an explicit `--`
// end-of-options separator is used as belt-and-suspenders. Callers that route
// through Discover have already scheme/host-validated the URL (#120); this guard
// keeps the opener safe for any other caller too.
func OpenInBrowser(target string) error {
	if strings.HasPrefix(target, "-") {
		return fmt.Errorf("refusing to open a URL beginning with '-': %q", target)
	}
	switch runtime.GOOS {
	case "darwin":
		return exec.Command("open", "--", target).Start()
	case "windows":
		return exec.Command("rundll32", "url.dll,FileProtocolHandler", target).Start()
	default:
		return exec.Command("xdg-open", target).Start()
	}
}

// isInvalidGrant reports whether a token-endpoint body is the OAuth
// invalid_grant error (RFC 6749 §5.2).
func isInvalidGrant(body []byte) bool {
	var e struct {
		Error string `json:"error"`
	}
	return json.Unmarshal(body, &e) == nil && e.Error == "invalid_grant"
}
