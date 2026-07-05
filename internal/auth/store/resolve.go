package store

import (
	"errors"

	"github.com/zalando/go-keyring"
)

const probeAccount = "hadron-cli-keyring-probe"

// keyringAvailable reports whether the OS keychain is usable. The probe is a
// read-only Get: a usable keyring answers ErrNotFound for the nonexistent probe
// account, while an unavailable one (headless box, no dbus) errors otherwise.
func keyringAvailable() bool {
	_, err := keyring.Get(keyringService, probeAccount)
	return err == nil || errors.Is(err, keyring.ErrNotFound)
}

// Resolve picks the keychain backend when one is usable, otherwise the
// 0600-file fallback.
func Resolve() Store {
	if keyringAvailable() {
		return Keyring{}
	}
	return File{}
}

// EnvToken is the environment variable that overrides any stored
// token (CI and scripting; same role as GH_TOKEN for gh).
const EnvToken = "HADRON_TOKEN"
