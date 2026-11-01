package team

// `--properties <json|@file>` / `--clear-properties` on `team task create` and
// `team task update` (hadron-cli#1118, server#2420, cor:agt:050:28). The value
// is the task's OWN optional JSON object, which the team organises into its own
// namespaces. The platform stores it and never reads a key, so the CLI does not
// either: it checks only that the text is JSON (so a typo never reaches the
// wire) and leaves "is it an object", the 16 KiB bound, the task-properties
// schema and who may write to the server's PROPERTIES_* / TASK_* codes.

import (
	"encoding/json"

	"github.com/hadron-memory/hadron-cli/internal/cmdutil"
)

// parsePropertiesFlag turns --properties' value into the JSON to send: the
// shared cmdutil.ParseJSONValue (also `app properties set`, cli#1524), spelled
// for this flag. A value starting with @ names a file; a literal null is
// refused, because clearing has its own flag.
func parsePropertiesFlag(value string) (json.RawMessage, error) {
	return cmdutil.ParseJSONValue(value, cmdutil.JSONValueSpelling{
		Name: "--properties", FileExample: "--properties @props.json", Clear: "--clear-properties",
	})
}
