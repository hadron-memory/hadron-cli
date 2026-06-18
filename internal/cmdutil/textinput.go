package cmdutil

import (
	"io"
	"os"

	"github.com/hadron-memory/hadron-cli/internal/exitcode"
)

// ResolveTextInput resolves a text field that may be supplied inline, from a
// file, or on stdin — the same convention the --content / --content-file / "-"
// flags already use, factored out so every write flag behaves identically.
//
// flag is the user-facing flag name (e.g. "abstract"): the inline value comes
// from --<flag>, the file from --<flag>-file, and a sentinel value of "-"
// reads stdin. The inline value and the file are mutually exclusive. Callers
// that gate on cmd.Flags().Changed decide whether an empty result means
// "clear" (an explicit empty string) or "leave unset".
//
// A paragraph-length field with backticks or newlines is hostile to inline
// shell quoting, so --<flag>-file / stdin are the ergonomic path (issue #38).
func ResolveTextInput(flag, value, file string, stdin io.Reader) (string, error) {
	if value != "" && file != "" {
		return "", exitcode.Newf(exitcode.Usage, "--%s and --%s-file are mutually exclusive", flag, flag)
	}
	if file != "" {
		data, err := os.ReadFile(file)
		if err != nil {
			return "", exitcode.Newf(exitcode.Usage, "reading --%s-file: %v", flag, err)
		}
		return string(data), nil
	}
	if value == "-" {
		if stdin == nil {
			return "", exitcode.Newf(exitcode.Usage, "stdin is not available")
		}
		data, err := io.ReadAll(stdin)
		if err != nil {
			return "", err
		}
		return string(data), nil
	}
	return value, nil
}
