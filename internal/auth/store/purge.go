package store

import "errors"

// allStores is every credential backend, for operations that must touch
// all of them regardless of which Resolve() currently picks. A token can
// live in a different backend than the one resolved now — e.g. written to
// the plaintext file on a headless box, then a desktop session later makes
// Resolve() pick the keychain (#116).
func allStores() []Store { return []Store{Keyring{}, File{}} }

// Purge removes host's token from EVERY backend, so a token persisted in
// one store during an earlier login can't survive a logout that resolved to
// the other (#116). Keyring errors are best-effort (an unavailable keyring
// has nothing to remove); a genuine file error propagates so logout can
// surface it. Returns whether any backend actually held a token.
func Purge(host string) (removed bool, err error) {
	return purge(host, allStores()...)
}

// purge is the testable core of Purge, parameterized on the backends.
func purge(host string, stores ...Store) (removed bool, err error) {
	for _, s := range stores {
		switch derr := s.Delete(host); {
		case derr == nil:
			removed = true
		case errors.Is(derr, ErrNotFound):
			// nothing stored here
		case s.Name() == Keyring{}.Name():
			// keyring unavailable/flaky (headless box, no dbus) — best effort
		default:
			// a real file error (e.g. corrupt auth.json): remember it but keep
			// purging the other backends so the token is still removed where possible
			err = derr
		}
	}
	return removed, err
}

// ClearExcept removes host's token from every backend EXCEPT keep, so a
// login that writes to one store doesn't leave a stale duplicate readable in
// the other (#116). Best-effort: errors are ignored because the freshly
// written keep store is the source of truth.
func ClearExcept(keep Store, host string) {
	clearExcept(keep, host, allStores()...)
}

// clearExcept is the testable core of ClearExcept, parameterized on the backends.
func clearExcept(keep Store, host string, stores ...Store) {
	for _, s := range stores {
		if s.Name() != keep.Name() {
			_ = s.Delete(host)
		}
	}
}
