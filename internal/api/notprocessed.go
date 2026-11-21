package api

import (
	"crypto/tls"
	"crypto/x509"
	"errors"
	"net"

	"github.com/Khan/genqlient/graphql"
)

// RequestNotProcessed reports whether err shows that the server CERTAINLY did not
// process the request, so a write that failed this way did not happen: the
// answer is a plain refusal (or a plain "could not reach"), never an unknown outcome
// (cli#2465, the question writes).
//
//   - an HTTP 4xx answer (400 invalid request, 401/403 a refused credential, 404, 429 a
//     rate limit) is formed by the HTTP layer before any resolver runs, so no write
//     followed it. A 5xx is NOT in this class (a gateway page or a server that failed
//     after it began), nor is a 2xx carrying GraphQL errors: those are judged by
//     IsDefiniteRefusalAt on the error list.
//   - a connection that never opened (a dial failure, DNS, a TLS or certificate
//     failure before the request was sent) delivered nothing.
//
// A timeout or reset AFTER the request left, an unexpected EOF and a cut-off body are
// NOT here: the server may have committed.
func RequestNotProcessed(err error) bool {
	if err == nil {
		return false
	}
	var httpErr *graphql.HTTPError
	if errors.As(err, &httpErr) {
		return httpErr.StatusCode >= 400 && httpErr.StatusCode < 500
	}
	var dns *net.DNSError
	if errors.As(err, &dns) {
		return true
	}
	var op *net.OpError
	if errors.As(err, &op) && op.Op == "dial" {
		return true
	}
	var recHdr tls.RecordHeaderError
	if errors.As(err, &recHdr) {
		return true
	}
	var unknownAuth x509.UnknownAuthorityError
	var certInvalid x509.CertificateInvalidError
	var hostName x509.HostnameError
	return errors.As(err, &unknownAuth) || errors.As(err, &certInvalid) || errors.As(err, &hostName)
}
