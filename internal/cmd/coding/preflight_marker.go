package coding

import (
	"encoding/json"
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
	var m map[string]any
	if json.Unmarshal(*data, &m) != nil {
		return false
	}
	v, ok := m[crossMemoryKey].(bool)
	return ok && v
}

// crossMemoryData returns an edge's data with the marker set, keeping every other
// key. current is the data the edge already has (nil or not an object: none).
func crossMemoryData(current *json.RawMessage) (json.RawMessage, error) {
	m := map[string]any{}
	if current != nil {
		var existing map[string]any
		if json.Unmarshal(*current, &existing) == nil && existing != nil {
			m = existing
		}
	}
	m[crossMemoryKey] = true
	return json.Marshal(m)
}
