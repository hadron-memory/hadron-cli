package team

// `--properties <json|@file>` / `--clear-properties` on `team task create` and
// `team task update` (hadron-cli#1118, server#2420, cor:agt:050:28). The value
// is the task's OWN optional JSON object, which the team organises into its own
// namespaces. The platform stores it and never reads a key, so the CLI does not
// either: it checks only that the text is JSON (so a typo never reaches the
// wire) and leaves "is it an object", the 16 KiB bound, the task-properties
// schema and who may write to the server's PROPERTIES_* / TASK_* codes.

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"strings"

	"github.com/hadron-memory/hadron-cli/internal/exitcode"
)

// maxPropertiesFileBytes bounds what `@file` reads. The server's limit is 16 KiB
// of canonical JSON; a file far past that is refused by the server with
// PROPERTIES_TOO_LARGE, so this is only a memory bound: it stops an accidental
// huge file from being slurped whole. It does not stop a pipe or device that
// blocks without ever producing the bytes.
const maxPropertiesFileBytes = 1 << 20

// parsePropertiesFlag turns --properties' value into the JSON to send. A value
// starting with @ names a file (the rest is the path); anything else is the JSON
// itself. A literal null is refused: clearing has its own flag, and one spelling
// each keeps "replace" and "clear" from being confused.
func parsePropertiesFlag(value string) (json.RawMessage, error) {
	usage := func(format string, a ...any) error { return exitcode.Newf(exitcode.Usage, format, a...) }
	raw := value
	if path, ok := strings.CutPrefix(value, "@"); ok {
		if path == "" {
			return nil, usage("--properties @ needs a file path: --properties @props.json")
		}
		f, err := os.Open(path)
		if err != nil {
			return nil, usage("--properties: cannot read %s: %v", path, err)
		}
		defer func() { _ = f.Close() }()
		b, err := io.ReadAll(io.LimitReader(f, maxPropertiesFileBytes+1))
		if err != nil {
			return nil, usage("--properties: cannot read %s: %v", path, err)
		}
		if len(b) > maxPropertiesFileBytes {
			return nil, usage("--properties: %s is larger than %d bytes; the server bound is 16 KiB of JSON", path, maxPropertiesFileBytes)
		}
		raw = string(b)
	}
	trimmed := bytes.TrimSpace([]byte(raw))
	if len(trimmed) == 0 {
		return nil, usage("--properties needs a JSON object (or @file); to remove the value use --clear-properties")
	}
	if !json.Valid(trimmed) {
		return nil, usage("--properties is not valid JSON; pass an object such as '{\"review\":{\"tier\":\"T1\"}}' or @file")
	}
	if bytes.Equal(trimmed, []byte("null")) {
		return nil, usage("--properties null would clear the value: use --clear-properties")
	}
	return json.RawMessage(trimmed), nil
}
