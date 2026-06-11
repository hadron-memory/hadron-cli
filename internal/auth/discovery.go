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
