package api

import (
	"crypto/x509"
	"errors"
	"net"
	"reflect"

	"github.com/Khan/genqlient/graphql"
)

// notProcessedStatuses are the HTTP statuses the HTTP layer (the edge, the auth and
// rate-limit middleware, the GraphQL server's own request validation) answers BEFORE any
// resolver runs. 408 and 409 are deliberately absent, and so is every other 4xx: a status
// a resolver or a proxy can raise after the handler started may follow a write.
var notProcessedStatuses = map[int]bool{400: true, 401: true, 403: true, 404: true, 405: true, 413: true, 415: true, 429: true}

// RequestNotProcessed reports whether err shows that the server CERTAINLY did not
// process the request, so a write that failed this way did not happen: the answer is a
// plain refusal (or a plain "could not reach"), never an unknown outcome (cli#2465, the
// question writes).
//
//   - an HTTP answer in notProcessedStatuses whose body shows neither data nor an error
//     below the mutation field (a body that does is a resolver talking: the write may
//     have followed). 5xx, 408, 409 and any other 4xx are NOT in this class, nor is a 2xx
//     carrying GraphQL errors: those are judged by IsDefiniteRefusalAt on the error list.
//   - a connection that never opened (a dial failure, DNS) or a certificate failure
//     during the TLS handshake (an unknown authority, an invalid certificate, a hostname
//     mismatch, also when wrapped in a tls.CertificateVerificationError): delivered nothing.
//
// A timeout or reset AFTER the request left, an unexpected EOF, a cut-off body, a TLS
// record error (which can arise mid-stream) and a proxy CONNECT failure are NOT here: the
// server may have committed, and unknown is the safe direction.
func RequestNotProcessed(err error) bool {
	if err == nil {
		return false
	}
	var httpErr *graphql.HTTPError
	if errors.As(err, &httpErr) {
		if !notProcessedStatuses[httpErr.StatusCode] {
			return false
		}
		if responseCarriesData(httpErr.Response.Data) {
			return false
		}
		for _, e := range httpErr.Response.Errors {
			if e != nil && len(e.Path) > 1 {
				return false
			}
		}
		return true
	}
	var dns *net.DNSError
	if errors.As(err, &dns) {
		return true
	}
	var op *net.OpError
	if errors.As(err, &op) && op.Op == "dial" {
		return true
	}
	var unknownAuth x509.UnknownAuthorityError
	var certInvalid x509.CertificateInvalidError
	var hostName x509.HostnameError
	return errors.As(err, &unknownAuth) || errors.As(err, &certInvalid) || errors.As(err, &hostName)
}

// responseCarriesData reports whether the decoded GraphQL response holds any data. The
// client prepopulates Response.Data with a pointer to an empty result struct, so a non-nil
// value alone proves nothing: it carries data only when what it points at is not the zero
// value.
func responseCarriesData(data any) bool {
	if data == nil {
		return false
	}
	v := reflect.ValueOf(data)
	for v.Kind() == reflect.Pointer || v.Kind() == reflect.Interface {
		if v.IsNil() {
			return false
		}
		v = v.Elem()
	}
	return !v.IsZero()
}

// ErrorShowsData reports whether err is an HTTP error answer whose body carried data: a
// resolver ran and answered, so the error says nothing about whether a write followed.
func ErrorShowsData(err error) bool {
	var httpErr *graphql.HTTPError
	return errors.As(err, &httpErr) && responseCarriesData(httpErr.Response.Data)
}
