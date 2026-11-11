// Package sigpayload rebuilds the passkey-signature payload and its challenge
// (cor:dmo:130:01). v3 only (R80): there is no v1 or v2 builder.
package sigpayload

import "errors"

// VersionV3 is the only payload version signing produces.
const VersionV3 = "hadron-signature-v3"

var errTODO = errors.New("sigpayload: not implemented")

// ErrUnsupportedVersion is returned for any payload version but v3.
var ErrUnsupportedVersion = errors.New("unsupported payload version")

// FieldError names the input that BuildV3 refused.
type FieldError struct{ Field, Reason string }

func (e *FieldError) Error() string { return e.Field + ": " + e.Reason }

// BuildV3 returns the exact payload bytes for a signing request.
func BuildV3(nodeID, approvalHash, requestID string) ([]byte, error) { return nil, errTODO }

// Challenge is the raw SHA-256 of the payload bytes.
func Challenge(payload []byte) [32]byte { return [32]byte{} }

// CheckVersion refuses any payload version other than hadron-signature-v3.
func CheckVersion(version string) error { return errTODO }
