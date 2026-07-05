package store

import "errors"

// allStores is every credential backend, for operations that must touch all of
// them regardless of which Resolve() currently picks. A token can live in a
// different backend than the one resolved now — e.g. written to the plaintext
// file on a headless box, then a desktop session later makes Resolve() pick the
// keychain (#116).
func allStores() []Store { return []Store{Keyring{}, File{}} }

// Purge removes host's token from EVERY backend, so a token persisted in one
// store during an earlier login can't survive a logout that resolved to the
// other (#116). A delete failure against an UNAVAILABLE keychain (headless box)
// is skipped — nothing can be deleted there and the user can't act on it — but
// a genuine file error, or a delete that fails against an AVAILABLE keychain
// (the token is still stored), propagates so logout can't falsely report
// success. Returns whether any backend actually held a token.
func Purge(host string) (removed bool, err error) {
	return purge(host, keyringAvailable(), allStores()...)
}

// purge is the testable core of Purge, parameterized on the backends and on
// whether the keychain is available (so tests don't touch the real keyring).
func purge(host string, keyringUp bool, stores ...Store) (removed bool, err error) {
	for _, s := range stores {
		switch derr := s.Delete(host); {
		case derr == nil:
			removed = true
		case errors.Is(derr, ErrNotFound):
			// nothing stored here
		case unavailableKeyring(s, keyringUp):
			// keychain genuinely unavailable — can't delete, skip
		default:
			// real failure: file IO, or a delete that failed on an available
			// keychain (token still there) — surface it, don't swallow
			err = derr
		}
	}
	return removed, err
}

// ClearExcept removes host's token from every backend EXCEPT keep, so a login
// that writes to one store doesn't leave a stale duplicate readable in the
// other (#116). It returns a non-nil error when a stale token could NOT be
// removed from another backend (so the caller can warn) — a silent failure
// would preserve exactly the cross-backend stale credential this is meant to
// eliminate. An unavailable keychain is skipped.
func ClearExcept(keep Store, host string) error {
	return clearExcept(keep, host, keyringAvailable(), allStores()...)
}

// clearExcept is the testable core of ClearExcept.
func clearExcept(keep Store, host string, keyringUp bool, stores ...Store) (err error) {
	for _, s := range stores {
		if s.Name() == keep.Name() {
			continue
		}
		switch derr := s.Delete(host); {
		case derr == nil, errors.Is(derr, ErrNotFound):
			// removed, or nothing there
		case unavailableKeyring(s, keyringUp):
			// keychain unavailable — skip
		default:
			err = derr
		}
	}
	return err
}

// unavailableKeyring reports whether s is the keychain backend and it is not
// currently available — the only case in which a delete failure is expected
// and safe to ignore.
func unavailableKeyring(s Store, keyringUp bool) bool {
	return s.Name() == (Keyring{}).Name() && !keyringUp
}
