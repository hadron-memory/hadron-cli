package api

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// capabilitiesHTTP is the client GET /capabilities runs on: unauthenticated
// by design, and short-timeout — discovery must never hang a command that
// works fine without the answer.
var capabilitiesHTTP = &http.Client{Timeout: 5 * time.Second}

// Capabilities reads GET /capabilities — hadron-server#1925, mounted by
// #1930: which deployment switches are ON, readable before sign-in. The
// answer is `{"capabilities":[...]}` with ruled token words ("node-signing",
// "passkey-registration"); an off switch is OMITTED, never false, so a
// deployment with the switches off reads exactly like a build without the
// features. Unauthenticated, ETag-cached, rate-limited, and OUTSIDE GraphQL.
//
// Callers treat every failure — non-200 (an older server's plain 404 is the
// expected one), transport error, unparsable body — as "no capabilities":
// no caller may pay a refused follow-up round trip (e.g. a signature query
// an older server would reject) just because discovery failed.
func Capabilities(ctx context.Context, baseURL string) (map[string]bool, error) {
	url := strings.TrimRight(baseURL, "/") + "/capabilities"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	// No Authorization header: the endpoint is readable before sign-in by
	// design, and sending the token to an unauthenticated route would leak
	// a credential into a cacheable response path.
	resp, err := capabilitiesHTTP.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GET /capabilities: HTTP %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, err
	}
	var parsed struct {
		Capabilities []string `json:"capabilities"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		return nil, fmt.Errorf("GET /capabilities: %w", err)
	}
	out := make(map[string]bool, len(parsed.Capabilities))
	for _, c := range parsed.Capabilities {
		out[c] = true
	}
	return out, nil
}
