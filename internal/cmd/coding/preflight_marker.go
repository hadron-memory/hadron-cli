package coding

import (
	"bytes"
	"encoding/json"

	"github.com/hadron-memory/hadron-cli/internal/exitcode"
)

// crossMemoryKey is the route marker (cli#2736): a boolean in the ROUTE EDGE's own
// data. It is never part of the edge name, which cor:dmo:060:03 makes
// non-behavioral. Only the boolean true marks a route; any other value, an absent
// key or no data at all is an ordinary route.
const crossMemoryKey = "crossMemory"

// edgeCrossMemory reports whether an edge's data marks it as an intentional
// cross-memory route.
func edgeCrossMemory(data *json.RawMessage) bool {
	if data == nil {
		return false
	}
	var m map[string]json.RawMessage
	if json.Unmarshal(*data, &m) != nil {
		return false
	}
	return string(bytes.TrimSpace(m[crossMemoryKey])) == "true"
}

// withCrossMemoryMarker returns an edge's data with the marker set, keeping every
// other key's VALUE exactly: the values stay raw JSON, so a number a float64 would
// round (9007199254740993) or a number written 2.50 comes back as it went in. The
// object around them is re-encoded: keys are sorted, insignificant whitespace is
// dropped and < > & are escaped, which changes no value.
// current is the data the edge already has. No data (nil or JSON null) starts an
// empty object; data that is not an object is refused, because setting a key on
// it would overwrite what it holds.
func withCrossMemoryMarker(current *json.RawMessage) (json.RawMessage, error) {
	m := map[string]json.RawMessage{}
	if current != nil && string(bytes.TrimSpace(*current)) != "null" {
		if json.Unmarshal(*current, &m) != nil || m == nil {
			return nil, exitcode.Newf(exitcode.Usage,
				"the route edge's data is not an object, so the cross-memory marker cannot be added without overwriting it; nothing was written")
		}
	}
	m[crossMemoryKey] = json.RawMessage("true")
	return json.Marshal(m)
}
