package auth

import (
	"errors"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/auth/store"
)

type fakeStore struct {
	token string
	err   error
}

func (fakeStore) Name() string                 { return "fake" }
func (f fakeStore) Get(string) (string, error) { return f.token, f.err }
func (fakeStore) Set(string, string) error     { return nil }
func (fakeStore) Delete(string) error          { return nil }

func TestResolveToken(t *testing.T) {
	t.Setenv(store.EnvToken, "") // ignore any ambient HADRON_TOKEN

	// A corrupt/unreadable store must PROPAGATE, not masquerade as logged-out (#125).
	if _, src, err := ResolveToken(fakeStore{err: errors.New("auth.json: invalid character")}, "https://s"); err == nil {
		t.Errorf("a non-NotFound store error must propagate, got src=%q err=nil", src)
	}
	// ErrNotFound is a genuine "no credential" → SourceNone, no error.
	if _, src, err := ResolveToken(fakeStore{err: store.ErrNotFound}, "https://s"); err != nil || src != SourceNone {
		t.Errorf("not-found = (%q, %v), want (SourceNone, nil)", src, err)
	}
	// An empty token with no error is also "no credential".
	if _, src, err := ResolveToken(fakeStore{token: ""}, "https://s"); err != nil || src != SourceNone {
		t.Errorf("empty token = (%q, %v), want (SourceNone, nil)", src, err)
	}
	// A valid token resolves from the store.
	if tok, src, err := ResolveToken(fakeStore{token: "hdr_x"}, "https://s"); err != nil || src != SourceStore || tok != "hdr_x" {
		t.Errorf("valid = (%q, %q, %v), want (hdr_x, store, nil)", tok, src, err)
	}

	// HADRON_TOKEN wins over the store, even a broken one.
	t.Setenv(store.EnvToken, "hdr_env")
	if tok, src, err := ResolveToken(fakeStore{err: errors.New("boom")}, "https://s"); err != nil || src != SourceEnv || tok != "hdr_env" {
		t.Errorf("env override = (%q, %q, %v), want (hdr_env, HADRON_TOKEN, nil)", tok, src, err)
	}
}
