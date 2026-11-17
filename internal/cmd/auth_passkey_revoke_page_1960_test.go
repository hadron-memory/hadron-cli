package cmd

import (
	"context"
	"errors"
	"github.com/hadron-memory/hadron-cli/internal/cmd/auth"
	"strings"
	"testing"
)

func TestPasskeyRevokePage1960InsecureServer(t *testing.T) {
	for _, args := range [][]string{nil, {"--json"}, {"--open"}} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			t.Setenv("HADRON_ALLOW_HTTP", "")
			old := auth.PasskeyCapabilities
			calls := 0
			auth.PasskeyCapabilities = func(context.Context, string) (map[string]bool, error) {
				calls++
				return nil, errors.New("unexpected discovery")
			}
			defer func() { auth.PasskeyCapabilities = old }()
			r := runRevokePage(t, "http://insecure.example", true, nil, map[string]string{"HADRON_TOKEN": ""}, args...)
			if r.code != 2 || calls != 0 || len(r.opened) != 0 || !strings.Contains(r.out+r.errOut, "refusing an insecure server address") {
				t.Fatalf("code=%d calls=%d opened=%v out=%q err=%q", r.code, calls, r.opened, r.out, r.errOut)
			}
		})
	}
}
func TestPasskeyRevokePage1960MissingAddress(t *testing.T) {
	for _, body := range []string{`{"data":{"serverInfo":null}}`, `{"data":{"serverInfo":{}}}`, revokeURLBody("")} {
		s := newRevokePageServer(t, 200, passkeysOn, 200, body)
		r := runRevokePage(t, s.url, true, nil, nil, "--open")
		if r.code != 1 || len(r.opened) != 0 || !strings.Contains(r.out+r.errOut, "the server did not return a passkey revoke page address") {
			t.Fatalf("%+v", r)
		}
	}
}
func TestPasskeyRevokePage1960NoPositional(t *testing.T) {
	s := newRevokePageServer(t, 200, passkeysOn, 200, revokeURLBody(revokeURLFixture))
	r := runRevokePage(t, s.url, true, nil, nil, "unexpected")
	if r.code != 2 || s.caps.Load() != 0 || s.posts.Load() != 0 || len(r.opened) != 0 {
		t.Fatalf("%+v", r)
	}
}

func TestPasskeyRevokePage1960SecureServerPolicy(t *testing.T) {
	for _, tc := range []struct{ server, allow string }{{"https://server.example", ""}, {"http://127.0.0.1:1234", ""}, {"http://[::1]:1234", ""}, {"http://server.example", "1"}} {
		t.Run(tc.server+tc.allow, func(t *testing.T) {
			old := auth.PasskeyCapabilities
			calls := 0
			auth.PasskeyCapabilities = func(context.Context, string) (map[string]bool, error) {
				calls++
				return nil, errors.New("synthetic stop before GraphQL")
			}
			defer func() { auth.PasskeyCapabilities = old }()
			r := runRevokePage(t, tc.server, false, nil, map[string]string{"HADRON_ALLOW_HTTP": tc.allow, "HADRON_TOKEN": ""})
			if calls != 1 || r.code != 2 || !strings.Contains(r.out+r.errOut, "synthetic stop before GraphQL") {
				t.Fatalf("calls=%d result=%+v", calls, r)
			}
		})
	}
}
