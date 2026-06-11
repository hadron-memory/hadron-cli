package auth

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/hadron-memory/hadron-cli/internal/exitcode"
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
// the authorization code to it at /oauth/token.
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
	if resp.StatusCode != http.StatusOK {
		return "", exitcode.Newf(exitcode.Error, "protected-resource discovery at %s returned HTTP %d", url, resp.StatusCode)
	}
	var meta struct {
		Resource string `json:"resource"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&meta); err != nil {
		return "", fmt.Errorf("parsing protected-resource metadata: %w", err)
	}
	if meta.Resource == "" {
		return "", exitcode.Newf(exitcode.Error, "protected-resource metadata from %s is missing resource", url)
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
	return &meta, nil
}
