package app

import (
	"bytes"
	"encoding/json"
	"io"
	"os"

	"github.com/hadron-memory/hadron-cli/internal/exitcode"
)

// maxPolicyFileBytes bounds --policy-file: an App policy is a short allow-list.
const maxPolicyFileBytes = 1 << 20

// readPolicyFile reads the App action-policy link from a file (cli#1640): one
// JSON value, never empty and never null. A null would clear the link, so it is
// refused and --clear-policy is the only way to do that (the same rule as
// `org update --policy`, #1489). Every refusal is a local usage error, before
// any request. The shape of the value stays the server's to judge. canClear is
// true where --clear-policy exists (update); install has nothing to clear, so
// its advice is to leave --policy-file out (#2319).
func readPolicyFile(path string, canClear bool) (json.RawMessage, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, exitcode.Newf(exitcode.Usage, "cannot read --policy-file")
	}
	defer func() { _ = f.Close() }()
	data, err := io.ReadAll(io.LimitReader(f, maxPolicyFileBytes+1))
	if err != nil {
		return nil, exitcode.Newf(exitcode.Usage, "cannot read --policy-file")
	}
	if len(data) > maxPolicyFileBytes {
		return nil, exitcode.Newf(exitcode.Usage, "--policy-file is larger than %d bytes", maxPolicyFileBytes)
	}
	data = bytes.TrimSpace(data)
	if len(data) == 0 {
		return nil, exitcode.Newf(exitcode.Usage, "--policy-file is empty; pass the policy JSON, %s", emptyAdvice(canClear))
	}
	// json.Valid accepts exactly one complete JSON value and, unlike decoding
	// into a Go value, imposes no float64 limits: the shape is the server's.
	if !json.Valid(data) {
		return nil, exitcode.Newf(exitcode.Usage, "--policy-file must contain exactly one valid JSON value")
	}
	if bytes.Equal(data, []byte("null")) {
		return nil, exitcode.Newf(exitcode.Usage, "--policy-file null %s", nullAdvice(canClear))
	}
	return json.RawMessage(data), nil
}

// The update wording is unchanged; install (no --clear-policy) says to leave
// --policy-file out.
func emptyAdvice(canClear bool) string {
	if canClear {
		return "or --clear-policy to remove the App link"
	}
	return "or leave --policy-file out for no policy link"
}

func nullAdvice(canClear bool) string {
	if canClear {
		return "would remove the App link; use --clear-policy to do that"
	}
	return "is not a policy; pass the policy JSON, or leave --policy-file out for no policy link"
}
