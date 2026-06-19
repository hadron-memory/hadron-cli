package nodedoc

import "encoding/json"

func intptr(i int) *int { return &i }

func raw(s string) *json.RawMessage {
	r := json.RawMessage(s)
	return &r
}
