package api

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/hadron-memory/hadron-cli/internal/exitcode"
)

// RawResult is the verbatim GraphQL response envelope.
type RawResult struct {
	Body   json.RawMessage
	Errors []rawError
}

type rawError struct {
	Message    string         `json:"message"`
	Extensions map[string]any `json:"extensions"`
}

// RawGraphQL posts an arbitrary query/mutation to the server and
// returns the raw response body. Used by `hadron api`. The returned
// error carries the mapped exit code when the response contains
// GraphQL errors.
func RawGraphQL(ctx context.Context, serverURL, token, query string, variables map[string]any, httpClient *http.Client) (*RawResult, error) {
	if err := RequireSecureURL(serverURL, token); err != nil {
		return nil, err
	}
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 60 * time.Second}
	}
	payload, err := json.Marshal(map[string]any{"query": query, "variables": variables})
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, Endpoint(serverURL), bytes.NewReader(payload))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, exitcode.New(exitcode.Error, err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode == 401 {
		return nil, exitcode.Newf(exitcode.AuthRequired, "HTTP 401: %s", bytes.TrimSpace(body))
	}
	if resp.StatusCode >= 400 {
		return nil, exitcode.Newf(exitcode.Error, "HTTP %d: %s", resp.StatusCode, bytes.TrimSpace(body))
	}

	result := &RawResult{Body: body}
	var envelope struct {
		Errors []rawError `json:"errors"`
	}
	if err := json.Unmarshal(body, &envelope); err == nil {
		result.Errors = envelope.Errors
	}
	return result, nil
}

// Err returns a CodedError summarizing the response's GraphQL errors,
// or nil when the response is error-free.
func (r *RawResult) Err() error {
	if len(r.Errors) == 0 {
		return nil
	}
	code := exitcode.Error
	if c, ok := r.Errors[0].Extensions["code"].(string); ok {
		code = codeForExtension(c)
	}
	return exitcode.New(code, fmt.Errorf("%s", r.Errors[0].Message))
}
