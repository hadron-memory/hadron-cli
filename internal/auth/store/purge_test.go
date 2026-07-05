package store

import (
	"errors"
	"testing"
)

// fakeStore is an in-memory Store for exercising the multi-backend purge
// logic without touching the real keychain or filesystem.
type fakeStore struct {
	name    string
	tokens  map[string]string
	delErr  error // non-nil → Delete returns this instead of touching tokens
	deleted []string
}

func newFake(name string, seed map[string]string) *fakeStore {
	m := map[string]string{}
	for k, v := range seed {
		m[k] = v
	}
	return &fakeStore{name: name, tokens: m}
}

func (f *fakeStore) Name() string { return f.name }
func (f *fakeStore) Get(h string) (string, error) {
	if t, ok := f.tokens[h]; ok {
		return t, nil
	}
	return "", ErrNotFound
}
func (f *fakeStore) Set(h, t string) error { f.tokens[h] = t; return nil }
func (f *fakeStore) Delete(h string) error {
	if f.delErr != nil {
		return f.delErr
	}
	f.deleted = append(f.deleted, h)
	if _, ok := f.tokens[h]; !ok {
		return ErrNotFound
	}
	delete(f.tokens, h)
	return nil
}

// A token in ONLY the non-resolved backend must still be purged, and purge
// must report it removed — the core #116 leak.
func TestPurgeRemovesFromEveryBackend(t *testing.T) {
	kc := newFake("keychain", nil)                              // empty
	file := newFake("file", map[string]string{"h": "hdr_leak"}) // token lurks here
	removed, err := purge("h", true, kc, file)
	if err != nil {
		t.Fatalf("purge err: %v", err)
	}
	if !removed {
		t.Error("purge should report removed=true when any backend held the token")
	}
	if _, ok := file.tokens["h"]; ok {
		t.Error("token must be deleted from the file backend")
	}
}

// Nothing anywhere → removed=false (drives logout's "no stored credential").
func TestPurgeNothingStored(t *testing.T) {
	removed, err := purge("h", true, newFake("keychain", nil), newFake("file", nil))
	if err != nil || removed {
		t.Fatalf("purge on empty stores = (%v, %v), want (false, nil)", removed, err)
	}
}

// A delete failure against an UNAVAILABLE keychain is best-effort and must NOT
// fail logout, but the file token still purges.
func TestPurgeToleratesKeyringUnavailable(t *testing.T) {
	kc := newFake("keychain", nil)
	kc.delErr = errors.New("dbus: no session bus")
	file := newFake("file", map[string]string{"h": "hdr_leak"})
	removed, err := purge("h", false /* keyring down */, kc, file)
	if err != nil {
		t.Fatalf("keyring-unavailable must not surface as error, got %v", err)
	}
	if !removed || file.tokens["h"] != "" {
		t.Errorf("file token should still purge; removed=%v file=%v", removed, file.tokens)
	}
}

// A delete failure against an AVAILABLE keychain means the token is STILL there,
// so it must propagate — logout must not falsely report success (Codex #143 P2).
func TestPurgeSurfacesAvailableKeyringFailure(t *testing.T) {
	kc := newFake("keychain", map[string]string{"h": "still-here"})
	kc.delErr = errors.New("keychain locked / access denied")
	_, err := purge("h", true /* keyring up */, kc, newFake("file", nil))
	if err == nil {
		t.Error("a failed delete on an available keychain must propagate, not be swallowed")
	}
}

// A genuine file error (e.g. corrupt auth.json) propagates so logout surfaces it.
func TestPurgeSurfacesFileError(t *testing.T) {
	file := newFake("file", nil)
	file.delErr = errors.New("auth.json: invalid character")
	_, err := purge("h", true, newFake("keychain", nil), file)
	if err == nil {
		t.Error("a real file error must propagate from purge")
	}
}

// clearExcept wipes the OTHER backend(s) but never the freshly-written keep.
func TestClearExceptLeavesKeep(t *testing.T) {
	keep := newFake("file", map[string]string{"h": "keep"})
	other := newFake("keychain", map[string]string{"h": "stale-dup"})
	if err := clearExcept(keep, "h", true, keep, other); err != nil {
		t.Fatalf("clearExcept err: %v", err)
	}
	if keep.tokens["h"] != "keep" {
		t.Error("clearExcept must not delete from the keep store")
	}
	if _, ok := other.tokens["h"]; ok {
		t.Error("clearExcept must delete the stale duplicate from the other store")
	}
}

// If the other backend's stale token can't be removed, clearExcept reports it so
// login can warn instead of silently leaving the duplicate (Codex #143 P2).
func TestClearExceptReportsUnclearableDuplicate(t *testing.T) {
	keep := newFake("keychain", map[string]string{"h": "new"})
	other := newFake("file", map[string]string{"h": "stale-dup"})
	other.delErr = errors.New("permission denied")
	if err := clearExcept(keep, "h", true, keep, other); err == nil {
		t.Error("clearExcept must report a stale duplicate it could not remove")
	}
}
