// Package store persists the Hadron access token, preferring the OS
// keychain and falling back to a 0600 file when no keychain is
// available (CI containers, headless boxes).
package store

import "errors"

// ErrNotFound is returned when no token is stored for a host.
var ErrNotFound = errors.New("no stored token")

// Store persists tokens keyed by server host.
type Store interface {
	// Name identifies the backend ("keychain" or "file") for
	// `hadron auth status` output.
	Name() string
	Get(host string) (string, error)
	Set(host, token string) error
	Delete(host string) error
}
