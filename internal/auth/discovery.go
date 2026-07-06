package auth

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strings"

	"github.com/hadron-memory/hadron-cli/internal/exitcode"
	"github.com/hadron-memory/hadron-cli/internal/urlsec"
)

// Metadata is the subset of RFC 8414 authorization-server metadata
// the CLI needs.
type Metadata struct {
	AuthorizationEndpoint string `json:"authorization_endpoint"`
	TokenEndpoint         string `json:"token_endpoint"`
	RegistrationEndpoint  string `json:"registration_endpoint"`
}

// DiscoverResource fetches the RFC 9728 protected-resource metadata
// and returns its canonical resource indicator. The server requires
// a `resource` parameter (RFC 8707) on /oauth/authorize and binds
// the authorization code to it at /oauth/token. A server without
// RFC 9728 (404) yields "", and the flow omits the parameter.
func DiscoverResource(ctx context.Context, serverURL string, httpClient *http.Client) (string, error) {
	url := strings.TrimRight(serverURL, "/") + "/.well-known/oauth-protected-resource"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", err
	}
	resp, err := httpClient.Do(req)
	if err != nil {
		return "", exitcode.Newf(exitcode.Error, "protected-resource discovery failed: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return "", nil
	}
	if resp.StatusCode != http.StatusOK {
		return "", exitcode.Newf(exitcode.Error, "protected-resource discovery at %s returned HTTP %d", url, resp.StatusCode)
	}
	var meta struct {
		Resource string `json:"resource"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&meta); err != nil {
		return "", fmt.Errorf("parsing protected-resource metadata: %w", err)
	}
	return meta.Resource, nil
}

// Discover fetches /.well-known/oauth-authorization-server.
func Discover(ctx context.Context, serverURL string, httpClient *http.Client) (*Metadata, error) {
	url := strings.TrimRight(serverURL, "/") + "/.well-known/oauth-authorization-server"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, exitcode.Newf(exitcode.Error, "OAuth discovery failed: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, exitcode.Newf(exitcode.Error, "OAuth discovery at %s returned HTTP %d", url, resp.StatusCode)
	}
	var meta Metadata
	if err := json.NewDecoder(resp.Body).Decode(&meta); err != nil {
		return nil, fmt.Errorf("parsing OAuth metadata: %w", err)
	}
	if meta.AuthorizationEndpoint == "" || meta.TokenEndpoint == "" || meta.RegistrationEndpoint == "" {
		return nil, exitcode.Newf(exitcode.Error, "OAuth metadata from %s is missing required endpoints", url)
	}
	// Every endpoint prefix here is server-controlled (and, over a plain-http or
	// MITM'd discovery fetch, attacker-controlled). Validate the scheme/host up
	// front so authorization_endpoint can't point the OS URL-opener at a
	// custom-scheme handler (vscode://, slack://) or a file:// path, and so a
	// token never rides token/registration over a cleartext or exotic scheme (#120).
	for _, e := range []struct{ name, raw string }{
		{"authorization_endpoint", meta.AuthorizationEndpoint},
		{"token_endpoint", meta.TokenEndpoint},
		{"registration_endpoint", meta.RegistrationEndpoint},
	} {
		if err := validateEndpoint(e.name, e.raw); err != nil {
			return nil, err
		}
	}
	return &meta, nil
}

// validateEndpoint rejects a discovery-provided endpoint URL unless it is an
// https URL with a host — or, for local development, an http URL to a loopback
// host or under HADRON_ALLOW_HTTP=1. The scheme is allow-listed explicitly
// (http/https only): a loopback host alone must NOT admit an arbitrary scheme
// like vscode://localhost, which is exactly the URL-opener injection this guards.
func validateEndpoint(name, raw string) error {
	if strings.HasPrefix(raw, "-") {
		return exitcode.Newf(exitcode.Error, "OAuth %s %q must not begin with '-'", name, raw)
	}
	u, err := url.Parse(raw)
	if err != nil {
		return exitcode.Newf(exitcode.Error, "OAuth %s %q is not a valid URL: %v", name, raw, err)
	}
	if u.Host == "" {
		return exitcode.Newf(exitcode.Error, "OAuth %s %q has no host", name, raw)
	}
	switch u.Scheme {
	case "https":
		return nil
	case "http":
		if urlsec.IsLoopbackHost(u.Hostname()) || os.Getenv(urlsec.EnvAllowHTTP) == "1" {
			return nil
		}
	}
	return exitcode.Newf(exitcode.Error,
		"OAuth %s %q must use https (http allowed only for a loopback host or with %s=1)", name, raw, urlsec.EnvAllowHTTP)
}
