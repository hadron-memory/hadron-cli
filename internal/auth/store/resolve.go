package store

import (
	"errors"

	"github.com/zalando/go-keyring"
)

const probeAccount = "hadron-cli-keyring-probe"

// Resolve picks the keychain backend when one is usable, otherwise
// the 0600-file fallback. The probe is a read-only Get: a usable
// keyring answers ErrNotFound for the nonexistent probe account,
// while an unavailable one (headless box, no dbus) errors otherwise.
func Resolve() Store {
	_, err := keyring.Get(keyringService, probeAccount)
	if err != nil && !errors.Is(err, keyring.ErrNotFound) {
		return File{}
	}
	return Keyring{}
}

// EnvToken is the environment variable that overrides any stored
// token (CI and scripting; same role as GH_TOKEN for gh).
const EnvToken = "HADRON_TOKEN"
