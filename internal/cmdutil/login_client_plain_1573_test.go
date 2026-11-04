package cmdutil

import "testing"

// #1573: the login flow follows redirects (cor:web:010:02 governs GraphQL
// POSTs, not OAuth discovery) because it rides the factory's plain
// http.Client (factory.go -> auth login -> BrowserStrategy). api.NewClient
// copies and wraps the client it is given with a redirect policy; if the
// factory's own client ever carried one, login would inherit it. Pin that the
// shared client stays unwrapped.
func TestFactoryHTTPClientCarriesNoRedirectPolicy(t *testing.T) {
	if NewFactory().HTTPClient.CheckRedirect != nil {
		t.Fatal("the factory's shared http.Client must stay plain: the login flow rides it and must keep following redirects")
	}
}
