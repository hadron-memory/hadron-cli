package store

import (
	"errors"

	"github.com/zalando/go-keyring"
)

const keyringService = "hadron-cli"

// Keyring stores tokens in the OS keychain (macOS Keychain,
// libsecret, Windows Credential Manager).
type Keyring struct{}

func (Keyring) Name() string { return "keychain" }

func (Keyring) Get(host string) (string, error) {
	token, err := keyring.Get(keyringService, host)
	if errors.Is(err, keyring.ErrNotFound) {
		return "", ErrNotFound
	}
	return token, err
}

func (Keyring) Set(host, token string) error {
	return keyring.Set(keyringService, host, token)
}

func (Keyring) Delete(host string) error {
	err := keyring.Delete(keyringService, host)
	if errors.Is(err, keyring.ErrNotFound) {
		return ErrNotFound
	}
	return err
}
