package api

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"io"
	"net"
	"net/url"
	"syscall"
	"testing"

	"github.com/Khan/genqlient/graphql"
	"github.com/vektah/gqlparser/v2/ast"
	"github.com/vektah/gqlparser/v2/gqlerror"
)

type fakeData struct {
	Q *struct{ ID string }
}

func TestRequestNotProcessed(t *testing.T) {
	http := func(code int) error { return &graphql.HTTPError{StatusCode: code} }
	withData := func(code int) error {
		d := &fakeData{Q: &struct{ ID string }{ID: "q1"}}
		return &graphql.HTTPError{StatusCode: code, Response: graphql.Response{Data: d}}
	}
	emptyData := func(code int) error {
		return &graphql.HTTPError{StatusCode: code, Response: graphql.Response{Data: &fakeData{}}}
	}
	below := func(code int) error {
		return &graphql.HTTPError{StatusCode: code, Response: graphql.Response{Errors: gqlerror.List{{Message: "x", Path: ast.Path{ast.PathName("answerQuestion"), ast.PathName("answers"), ast.PathIndex(0)}}}}}
	}
	atField := func(code int) error {
		return &graphql.HTTPError{StatusCode: code, Response: graphql.Response{Errors: gqlerror.List{{Message: "x", Path: ast.Path{ast.PathName("answerQuestion")}}}}}
	}
	for name, tc := range map[string]struct {
		err  error
		want bool
	}{
		"nil":                                 {nil, false},
		"HTTP 400":                            {http(400), true},
		"HTTP 401":                            {http(401), true},
		"HTTP 403":                            {http(403), true},
		"HTTP 404":                            {http(404), true},
		"HTTP 405":                            {http(405), true},
		"HTTP 413":                            {http(413), true},
		"HTTP 415":                            {http(415), true},
		"HTTP 429":                            {http(429), true},
		"HTTP 408 a request timeout":          {http(408), false},
		"HTTP 409 a conflict":                 {http(409), false},
		"HTTP 418 an unlisted 4xx":            {http(418), false},
		"HTTP 500":                            {http(500), false},
		"HTTP 502":                            {http(502), false},
		"a 403 whose body shows data":         {withData(403), false},
		"a 403 with an empty result struct":   {emptyData(403), true},
		"a 403 with an error below the field": {below(403), false},
		"a 403 with an error at the field":    {atField(403), true},
		"a wrapped 401":                       {fmt.Errorf("x: %w", http(401)), true},
		"a dial failure":                      {&url.Error{Op: "Post", URL: "http://x", Err: &net.OpError{Op: "dial", Err: syscall.ECONNREFUSED}}, true},
		"a DNS failure":                       {&url.Error{Op: "Post", URL: "http://x", Err: &net.DNSError{Err: "no such host", Name: "x"}}, true},
		"an unknown certificate":              {&url.Error{Op: "Post", URL: "https://x", Err: x509.UnknownAuthorityError{}}, true},
		"a wrapped certificate verification":  {&url.Error{Op: "Post", URL: "https://x", Err: &tls.CertificateVerificationError{Err: x509.HostnameError{Host: "x"}}}, true},
		"a TLS record header error":           {&url.Error{Op: "Post", URL: "https://x", Err: tls.RecordHeaderError{Msg: "oversized"}}, false},
		"a read after the request":            {&url.Error{Op: "Post", URL: "http://x", Err: &net.OpError{Op: "read", Err: syscall.ECONNRESET}}, false},
		"a reset":                             {syscall.ECONNRESET, false},
		"an unexpected EOF":                   {io.ErrUnexpectedEOF, false},
		"a deadline after the request":        {context.DeadlineExceeded, false},
		"a plain error":                       {errors.New("boom"), false},
	} {
		if got := RequestNotProcessed(tc.err); got != tc.want {
			t.Errorf("%s: got %v want %v", name, got, tc.want)
		}
	}
	if !ErrorShowsData(&graphql.HTTPError{StatusCode: 403, Response: graphql.Response{Data: &fakeData{Q: &struct{ ID string }{ID: "q"}}}}) || ErrorShowsData(&graphql.HTTPError{StatusCode: 403, Response: graphql.Response{Data: &fakeData{}}}) || ErrorShowsData(errors.New("x")) {
		t.Errorf("ErrorShowsData must be true only for an HTTP error whose body held data")
	}
}
