package api

import (
	"context"
	"crypto/x509"
	"errors"
	"fmt"
	"io"
	"net"
	"net/url"
	"syscall"
	"testing"

	"github.com/Khan/genqlient/graphql"
)

func TestRequestNotProcessed(t *testing.T) {
	http := func(code int) error { return &graphql.HTTPError{StatusCode: code} }
	for name, tc := range map[string]struct {
		err  error
		want bool
	}{
		"nil":                          {nil, false},
		"HTTP 400":                     {http(400), true},
		"HTTP 401":                     {http(401), true},
		"HTTP 403":                     {http(403), true},
		"HTTP 429":                     {http(429), true},
		"HTTP 500":                     {http(500), false},
		"HTTP 502":                     {http(502), false},
		"a wrapped 401":                {fmt.Errorf("x: %w", http(401)), true},
		"a dial failure":               {&url.Error{Op: "Post", URL: "http://x", Err: &net.OpError{Op: "dial", Err: syscall.ECONNREFUSED}}, true},
		"a DNS failure":                {&url.Error{Op: "Post", URL: "http://x", Err: &net.DNSError{Err: "no such host", Name: "x"}}, true},
		"an unknown certificate":       {&url.Error{Op: "Post", URL: "https://x", Err: x509.UnknownAuthorityError{}}, true},
		"a read after the request":     {&url.Error{Op: "Post", URL: "http://x", Err: &net.OpError{Op: "read", Err: syscall.ECONNRESET}}, false},
		"a reset":                      {syscall.ECONNRESET, false},
		"an unexpected EOF":            {io.ErrUnexpectedEOF, false},
		"a deadline after the request": {context.DeadlineExceeded, false},
		"a plain error":                {errors.New("boom"), false},
	} {
		if got := RequestNotProcessed(tc.err); got != tc.want {
			t.Errorf("%s: got %v want %v", name, got, tc.want)
		}
	}
}
