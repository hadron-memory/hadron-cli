package store

import "github.com/zalando/go-keyring"

const probeAccount = "hadron-cli-keyring-probe"

// Resolve picks the keychain backend when one is usable, otherwise
// the 0600-file fallback. The probe writes and deletes a throwaway
// entry once per invocation.
func Resolve() Store {
	if err := keyring.Set(keyringService, probeAccount, "probe"); err != nil {
		return File{}
	}
	_ = keyring.Delete(keyringService, probeAccount)
	return Keyring{}
}

// EnvToken is the environment variable that overrides any stored
// token (CI and scripting; same role as GH_TOKEN for gh).
const EnvToken = "HADRON_TOKEN"
