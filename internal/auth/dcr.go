package auth

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"

	"github.com/hadron-memory/hadron-cli/internal/exitcode"
)

// registerClient performs RFC 7591 dynamic client registration. The
// server matches redirect URIs exactly (no RFC 8252 loopback-port
// exception), so each login registers a fresh public client bound to
// the exact 127.0.0.1:<port> redirect URI of this run.
func registerClient(ctx context.Context, httpClient *http.Client, registrationEndpoint, redirectURI string) (string, error) {
	payload, err := json.Marshal(map[string]any{
		"client_name":                "hadron-cli",
		"redirect_uris":              []string{redirectURI},
		"grant_types":                []string{"authorization_code"},
		"response_types":             []string{"code"},
		"token_endpoint_auth_method": "none",
	})
	if err != nil {
		return "", err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, registrationEndpoint, bytes.NewReader(payload))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := httpClient.Do(req)
	if err != nil {
		return "", exitcode.Newf(exitcode.Error, "client registration failed: %v", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusOK {
		return "", exitcode.Newf(exitcode.Error, "client registration returned HTTP %d: %s", resp.StatusCode, bytes.TrimSpace(body))
	}
	var out struct {
		ClientID string `json:"client_id"`
	}
	if err := json.Unmarshal(body, &out); err != nil || out.ClientID == "" {
		return "", exitcode.Newf(exitcode.Error, "client registration response missing client_id")
	}
	return out.ClientID, nil
}
