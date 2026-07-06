// Package urlsec holds the URL-scheme security primitives shared by the API
// transport guard (which refuses to put a bearer token on a cleartext wire) and
// the OAuth login flow (which validates server-controlled discovery endpoints
// before handing them to the OS URL-opener or sending a token to them).
package urlsec

import (
	"net"
	"strings"
)

// EnvAllowHTTP opts out of HTTPS enforcement for a trusted local or self-hosted
// backend reached over plain http. It is deliberately scoped to cleartext http
// only — it must never green-light a non-HTTP scheme.
const EnvAllowHTTP = "HADRON_ALLOW_HTTP"

// IsLoopbackHost reports whether host is a loopback name or IP. Per RFC 6761,
// `localhost` and any `*.localhost` name resolve to loopback; a trailing dot
// (root-zone form) and case are normalized away.
func IsLoopbackHost(host string) bool {
	host = strings.ToLower(strings.TrimSuffix(host, "."))
	if host == "localhost" || strings.HasSuffix(host, ".localhost") {
		return true
	}
	if ip := net.ParseIP(host); ip != nil {
		return ip.IsLoopback()
	}
	return false
}
