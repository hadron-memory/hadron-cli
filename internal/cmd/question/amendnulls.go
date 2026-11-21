package question

import (
	"bytes"
	"encoding/json"

	"github.com/hadron-memory/hadron-cli/internal/exitcode"
)

// amendNullClears are the amend keys the server clears with an explicit null.
// Every other key is unchanged when omitted, and null says nothing useful there.
var amendNullClears = map[string]bool{
	"background": true, "whyNow": true, "assignee": true, "neededBy": true, "defaultAction": true,
}

// refuseAmendNulls refuses a key given as null that does not clear anything. A
// pointer or list field decodes null to nil, which is then omitted (or sent as the
// "unchanged" null): the file would be accepted and the key silently dropped while
// the amend went ahead without it (#2647). data is the --input-file bytes, already
// through the strict decode, so it is one JSON object.
func refuseAmendNulls(data []byte) error {
	var keys map[string]json.RawMessage
	if err := json.Unmarshal(data, &keys); err != nil {
		return exitcode.Newf(exitcode.Usage, "parsing --input-file: %v", err)
	}
	for k, v := range keys {
		if string(bytes.TrimSpace(v)) == "null" && !amendNullClears[k] {
			return exitcode.Newf(exitcode.Usage,
				"parsing --input-file: %q is null, which changes nothing here: leave the key out to keep it (only background, whyNow, assignee, neededBy and defaultAction clear with null; options, blocks, sources, specsTouched and specsMustChange clear with [])", k)
		}
	}
	return nil
}
