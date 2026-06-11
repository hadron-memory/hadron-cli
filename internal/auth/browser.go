package auth

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os/exec"
	"runtime"
	"strings"
	"time"

	"github.com/hadron-memory/hadron-cli/internal/exitcode"
)

// loginTimeout bounds the whole browser dance.
const loginTimeout = 5 * time.Minute

// BrowserStrategy signs in via authorization-code + PKCE with a
// loopback redirect: discover endpoints, bind 127.0.0.1:<port>,
// register a one-shot public client for that exact redirect URI,
// send the user to the consent screen, exchange the code.
type BrowserStrategy struct{}

func (BrowserStrategy) Name() string { return "browser" }

func (BrowserStrategy) Login(ctx context.Context, opts LoginOptions) (*Token, error) {
	httpClient := opts.HTTPClient
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 30 * time.Second}
	}
	openBrowser := opts.OpenBrowser
	if openBrowser == nil {
		openBrowser = OpenInBrowser
	}

	ctx, cancel := context.WithTimeout(ctx, loginTimeout)
	defer cancel()

	meta, err := Discover(ctx, opts.ServerURL, httpClient)
	if err != nil {
		return nil, err
	}

	pk, err := newPKCE()
	if err != nil {
		return nil, err
	}

	loopback, err := startLoopback(pk.State)
	if err != nil {
		return nil, err
	}
	defer func() { _ = loopback.Close() }()

	clientID, err := registerClient(ctx, httpClient, meta.RegistrationEndpoint, loopback.RedirectURI())
	if err != nil {
		return nil, err
	}

	authorizeURL := meta.AuthorizationEndpoint + "?" + url.Values{
		"response_type":         {"code"},
		"client_id":             {clientID},
		"redirect_uri":          {loopback.RedirectURI()},
		"state":                 {pk.State},
		"code_challenge":        {pk.Challenge},
		"code_challenge_method": {"S256"},
	}.Encode()

	fmt.Fprintln(opts.IO.ErrOut, "Opening your browser to sign in to Hadron...")
	fmt.Fprintf(opts.IO.ErrOut, "If the browser doesn't open, visit:\n  %s\n", authorizeURL)
	if err := openBrowser(authorizeURL); err != nil {
		fmt.Fprintf(opts.IO.ErrOut, "(could not open browser automatically: %v)\n", err)
	}

	code, err := loopback.Wait(ctx)
	if err != nil {
		return nil, err
	}

	return exchangeCode(ctx, httpClient, meta.TokenEndpoint, clientID, code, pk.Verifier, loopback.RedirectURI())
}

func exchangeCode(ctx context.Context, httpClient *http.Client, tokenEndpoint, clientID, code, verifier, redirectURI string) (*Token, error) {
	form := url.Values{
		"grant_type":    {"authorization_code"},
		"code":          {code},
		"redirect_uri":  {redirectURI},
		"client_id":     {clientID},
		"code_verifier": {verifier},
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
		return nil, exitcode.Newf(exitcode.Error, "token exchange returned HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	var out struct {
		AccessToken string `json:"access_token"`
		TokenType   string `json:"token_type"`
	}
	if err := json.Unmarshal(body, &out); err != nil || out.AccessToken == "" {
		return nil, exitcode.Newf(exitcode.Error, "token response missing access_token")
	}
	return &Token{AccessToken: out.AccessToken}, nil
}

// OpenInBrowser launches the platform's URL opener.
func OpenInBrowser(target string) error {
	switch runtime.GOOS {
	case "darwin":
		return exec.Command("open", target).Start()
	case "windows":
		return exec.Command("rundll32", "url.dll,FileProtocolHandler", target).Start()
	default:
		return exec.Command("xdg-open", target).Start()
	}
}
