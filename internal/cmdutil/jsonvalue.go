package cmdutil

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"strings"

	"github.com/hadron-memory/hadron-cli/internal/exitcode"
)

// MaxJSONValueFileBytes bounds what `@file` reads for a properties-style JSON
// value. The server's limit is 16 KiB of canonical JSON; a file far past that
// is refused by the server (PROPERTIES_TOO_LARGE), so this is only a memory
// bound: it stops an accidental huge file from being slurped whole. It does
// not stop a pipe or device that blocks without ever producing the bytes.
const MaxJSONValueFileBytes = 1 << 20

// JSONValueSpelling names a JSON-value argument in its usage messages, so each
// caller's remedy is specific: Name is how the value is called ("--properties"),
// FileExample a working @file spelling ("--properties @props.json"), and Clear
// how to remove the value instead ("--clear-properties").
type JSONValueSpelling struct {
	Name        string
	FileExample string
	Clear       string
}

// ParseJSONValue turns a properties-style argument into the JSON to send
// (hadron-cli#1118 task properties; #1524 App properties). A value starting
// with @ names a file (the rest is the path); anything else is the JSON
// itself. It checks only that the text is JSON, so a typo never reaches the
// wire: "is it an object", the size bound and any schema are the server's. A
// literal null is refused: clearing has its own spelling, and one spelling
// each keeps "replace" and "clear" from being confused.
func ParseJSONValue(value string, sp JSONValueSpelling) (json.RawMessage, error) {
	usage := func(format string, a ...any) error { return exitcode.Newf(exitcode.Usage, format, a...) }
	raw := value
	if path, ok := strings.CutPrefix(value, "@"); ok {
		if path == "" {
			return nil, usage("%s @ needs a file path: %s", sp.Name, sp.FileExample)
		}
		f, err := os.Open(path)
		if err != nil {
			return nil, usage("%s: cannot read %s: %v", sp.Name, path, err)
		}
		defer func() { _ = f.Close() }()
		b, err := io.ReadAll(io.LimitReader(f, MaxJSONValueFileBytes+1))
		if err != nil {
			return nil, usage("%s: cannot read %s: %v", sp.Name, path, err)
		}
		if len(b) > MaxJSONValueFileBytes {
			return nil, usage("%s: %s is larger than %d bytes; the server bound is 16 KiB of JSON", sp.Name, path, MaxJSONValueFileBytes)
		}
		raw = string(b)
	}
	trimmed := bytes.TrimSpace([]byte(raw))
	if len(trimmed) == 0 {
		return nil, usage("%s needs a JSON object (or @file); to remove the value use %s", sp.Name, sp.Clear)
	}
	if !json.Valid(trimmed) {
		return nil, usage("%s is not valid JSON; pass an object such as '{\"review\":{\"tier\":\"T1\"}}' or @file", sp.Name)
	}
	if bytes.Equal(trimmed, []byte("null")) {
		return nil, usage("%s null would clear the value: use %s", sp.Name, sp.Clear)
	}
	return json.RawMessage(trimmed), nil
}
