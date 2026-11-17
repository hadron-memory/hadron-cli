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
// any request. The shape of the value stays the server's to judge.
func readPolicyFile(path string) (json.RawMessage, error) {
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
		return nil, exitcode.Newf(exitcode.Usage, "--policy-file is empty; pass the policy JSON, or --clear-policy to remove the App link")
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	var value any
	if err := dec.Decode(&value); err != nil {
		return nil, exitcode.Newf(exitcode.Usage, "--policy-file must contain valid JSON")
	}
	if _, err := dec.Token(); err != io.EOF {
		return nil, exitcode.Newf(exitcode.Usage, "--policy-file must contain exactly one JSON value")
	}
	if value == nil {
		return nil, exitcode.Newf(exitcode.Usage, "--policy-file null would remove the App link; use --clear-policy to do that")
	}
	return json.RawMessage(data), nil
}
