// Package api wraps the Hadron GraphQL endpoint: a genqlient client
// for typed operations, a raw escape hatch for `hadron api`, and the
// mapping from transport/GraphQL errors to exit codes.
package api

import (
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/Khan/genqlient/graphql"
)

const graphqlPath = "/graphql"

// bearerDoer injects the Authorization header on every request.
type bearerDoer struct {
	token string
	inner *http.Client
}

func (d *bearerDoer) Do(req *http.Request) (*http.Response, error) {
	if d.token != "" {
		req.Header.Set("Authorization", "Bearer "+d.token)
	}
	return d.inner.Do(req)
}

// Endpoint joins the server base URL with the GraphQL path.
func Endpoint(serverURL string) string {
	return strings.TrimRight(serverURL, "/") + graphqlPath
}

// NewClient returns a genqlient client for the given server,
// authenticating with token (may be empty for anonymous calls).
func NewClient(serverURL, token string, httpClient *http.Client) (graphql.Client, error) {
	if _, err := url.ParseRequestURI(serverURL); err != nil {
		return nil, err
	}
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 30 * time.Second}
	}
	return graphql.NewClient(Endpoint(serverURL), &bearerDoer{token: token, inner: httpClient}), nil
}
