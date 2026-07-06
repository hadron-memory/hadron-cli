package auth

import (
	"strings"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/auth/store"
	"github.com/hadron-memory/hadron-cli/internal/output"
)

// namedStore is a store stub whose Name reports a chosen backend, for exercising
// the plaintext-downgrade warning without touching a real keychain.
type namedStore string

func (n namedStore) Name() string             { return string(n) }
func (namedStore) Get(string) (string, error) { return "", store.ErrNotFound }
func (namedStore) Set(string, string) error   { return nil }
func (namedStore) Delete(string) error        { return nil }

func TestWarnIfPlaintext(t *testing.T) {
	// Landing on the file backend means no usable keychain — warn (#122).
	io, _, errOut := output.Test()
	warnIfPlaintext(io, namedStore((store.File{}).Name()))
	if !strings.Contains(errOut.String(), "unencrypted") {
		t.Errorf("file store should warn about plaintext storage, got: %q", errOut.String())
	}

	// The keychain backend stores securely — no warning.
	io2, _, errOut2 := output.Test()
	warnIfPlaintext(io2, namedStore((store.Keyring{}).Name()))
	if errOut2.String() != "" {
		t.Errorf("keychain store must not warn, got: %q", errOut2.String())
	}
}
