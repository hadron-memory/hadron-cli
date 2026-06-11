package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
)

type pkce struct {
	Verifier  string
	Challenge string // S256
	State     string
}

func newPKCE() (*pkce, error) {
	verifier, err := randomURLSafe(32)
	if err != nil {
		return nil, err
	}
	state, err := randomURLSafe(16)
	if err != nil {
		return nil, err
	}
	sum := sha256.Sum256([]byte(verifier))
	return &pkce{
		Verifier:  verifier,
		Challenge: base64.RawURLEncoding.EncodeToString(sum[:]),
		State:     state,
	}, nil
}

func randomURLSafe(n int) (string, error) {
	buf := make([]byte, n)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}
