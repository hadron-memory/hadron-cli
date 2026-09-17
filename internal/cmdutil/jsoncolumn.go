package cmdutil

import "encoding/json"

// ProjectedColumn renders one opt-in JSONB column (`properties` / `data`) for a
// command's --json DTO, given what the server returned for it (#602).
//
// Call it ONLY on the path where the projection flag asked for the column, and
// store the result in a NON-POINTER json.RawMessage field tagged omitempty.
// That combination is what lets one key carry three distinct answers:
//
//	not requested      -> never call this; leave the field nil (len 0) and
//	                      omitempty drops the key entirely
//	requested, a value -> the value
//	requested, null    -> the 4-byte `null` literal, which is non-empty, so the
//	                      key stays PRESENT
//
// The third state is the whole point and it is easy to lose. A
// *json.RawMessage cannot express it: encoding/json sets the pointer to nil for
// a JSON `null`, so a requested-but-null column would vanish from the payload
// exactly as an unrequested one does, and a caller who asked for `data` and got
// no `data` key could not tell "this node has none" from "you did not ask".
// That ambiguity is the defect #602 was filed for — a zero you cannot interpret
// — and it is the same lesson already recorded on
// nodeDetailDTO.AbstractOriginHash (#306): jq returns null for a key that is
// not there.
//
// It follows that the FLAG must decide whether the key is present, never the
// returned value, because on the wire an unselected column and a selected-null
// one are indistinguishable. This helper deliberately cannot make that decision
// for you — it does not take the flag — so the call site has to.
func ProjectedColumn(raw *json.RawMessage) json.RawMessage {
	if raw == nil || len(*raw) == 0 {
		return json.RawMessage("null")
	}
	return *raw
}
