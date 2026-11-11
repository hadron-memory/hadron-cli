// Package sigpayload rebuilds the passkey-signature payload and its challenge
// byte for byte (cor:dmo:130:01). v3 only (R80): there is no v1 or v2 builder,
// and nothing falls back to one. It never sees an assertion or a key.
package sigpayload

import (
	"crypto/sha256"
	"errors"
	"regexp"
)

// VersionV3 is the only payload version signing produces.
const VersionV3 = "hadron-signature-v3"

// ErrUnsupportedVersion is returned for any payload version but v3.
var ErrUnsupportedVersion = errors.New("unsupported payload version")

// FieldError names the input that BuildV3 refused.
type FieldError struct{ Field, Reason string }

func (e *FieldError) Error() string { return e.Field + ": " + e.Reason }

var (
	nodeIDRe       = regexp.MustCompile(`^[0-9a-f]{32}$`)
	approvalHashRe = regexp.MustCompile(`^[0-9a-f]{64}$`)
	// A 128-bit CSPRNG value in unpadded base64url, written verbatim.
	requestIDRe = regexp.MustCompile(`^[A-Za-z0-9_-]{22}$`)
)

// BuildV3 returns the exact payload bytes for a signing request: v1's lines
// under the v3 prefix plus a final request line, every line ending in LF.
func BuildV3(nodeID, approvalHash, requestID string) ([]byte, error) {
	if !nodeIDRe.MatchString(nodeID) {
		return nil, &FieldError{"nodeId", "must be 32 lowercase hex characters"}
	}
	if !approvalHashRe.MatchString(approvalHash) {
		return nil, &FieldError{"approvalHash", "must be 64 lowercase hex characters"}
	}
	if !requestIDRe.MatchString(requestID) {
		return nil, &FieldError{"requestId", "must be 22 unpadded base64url characters"}
	}
	return []byte(VersionV3 + "\nnode " + nodeID + "\napproval-hash " + approvalHash + "\nrequest " + requestID + "\n"), nil
}

// Challenge is the raw SHA-256 of the payload bytes.
func Challenge(payload []byte) [32]byte { return sha256.Sum256(payload) }

// CheckVersion refuses any payload version other than hadron-signature-v3.
func CheckVersion(version string) error {
	if version != VersionV3 {
		return ErrUnsupportedVersion
	}
	return nil
}
