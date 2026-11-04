package api

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/vektah/gqlparser/v2/gqlerror"
)

func TestCloneDetailsScopedAndAllowlisted(t *testing.T) {
	var ext map[string]any
	if err := json.Unmarshal([]byte(`{"code":"CLONE_IMPORT_UNUSABLE","reason":"why","retryable":true,"private":"PRIVATE","blockers":[null,{"code":7},{"code":"CLONE_IMPORT_UNUSABLE","count":2,"private":"PRIVATE","subjects":[{"position":1,"name":"Visible","secret":"PRIVATE"},{"position":2},{"position":-1,"name":"PRIVATE"},null]}]}`), &ext); err != nil {
		t.Fatal(err)
	}
	raw := gqlerror.List{{Message: "cannot clone", Extensions: ext}}
	if CloneAgentDetails(MapError(raw)) != nil {
		t.Fatal("unscoped error gained clone rendering")
	}
	d := CloneAgentDetails(MapCloneAgentError(raw))
	serialized, _ := json.Marshal(d)
	if strings.Contains(string(serialized), "PRIVATE") {
		t.Fatal(string(serialized))
	}
	want := `{"blockers":[{"code":"CLONE_IMPORT_UNUSABLE","count":2,"subjects":[{"name":"Visible","position":1},{"position":2}]}],"code":"CLONE_IMPORT_UNUSABLE","reason":"why","retryable":true}`
	if string(serialized) != want {
		t.Fatalf("%s\nwant%s", serialized, want)
	}
	// The operation wrapper must leave unrelated mapping and original error chain intact.
	if !HasErrorCode(MapCloneAgentError(raw), "CLONE_IMPORT_UNUSABLE") {
		t.Fatal("lost original code")
	}
	if CloneAgentDetailText(MapError(raw)) != "" {
		t.Fatal("unrelated text changed")
	}
}

func TestCloneOrdinalsRejectMalformedValues(t *testing.T) {
	for _, v := range []any{nil, "PRIVATE", true, -1.0, 1.5, 1e30} {
		if _, ok := cloneOrdinal(v); ok {
			t.Fatalf("accepted%v", v)
		}
	}
	if n, ok := cloneOrdinal(float64(2)); !ok || n != 2 {
		t.Fatal(n, ok)
	}
	if MapCloneAgentError(nil) != nil {
		t.Fatal("nil mapped")
	}
}

func TestCloneScopeDoesNotChangeUnrelatedPermissionMappings(t *testing.T) {
	for _, code := range []string{"AGENT_NOT_FOUND", "FORBIDDEN", "CONFLICT", "NEW_UNKNOWN_CODE"} {
		e := gqlErr(code)
		if !reflect.DeepEqual(CloneAgentDetails(MapError(e)), map[string]any(nil)) {
			t.Fatal(code)
		}
	}
}

// cli#998: only typed scalars are forwarded; a nested sentinel or a wrong-typed
// reason/code/retryable never reaches the output, and the typed controls do.
func TestCloneDetailsRejectWrongTypedFields(t *testing.T) {
	for _, ext := range []string{
		`{"code":"CLONE_BASE_UNUSABLE","reason":{"nested":"SENTINEL"},"retryable":"true"}`,
		`{"code":"CLONE_BASE_UNUSABLE","reason":["SENTINEL"],"retryable":1}`,
		`{"code":{"nested":"SENTINEL"},"reason":7,"retryable":null}`,
	} {
		var m map[string]any
		if err := json.Unmarshal([]byte(ext), &m); err != nil {
			t.Fatal(err)
		}
		raw := gqlerror.List{{Message: "x", Extensions: m}}
		text := CloneAgentDetailText(MapCloneAgentError(raw))
		if strings.Contains(text, "SENTINEL") || strings.Contains(text, "reason") || strings.Contains(text, "retryable") {
			t.Fatalf("%s -> %s", ext, text)
		}
	}
	var ok map[string]any
	_ = json.Unmarshal([]byte(`{"code":"CLONE_BASE_UNUSABLE","reason":"R","retryable":false}`), &ok)
	d := CloneAgentDetails(MapCloneAgentError(gqlerror.List{{Message: "x", Extensions: ok}}))
	if d["code"] != "CLONE_BASE_UNUSABLE" || d["reason"] != "R" || d["retryable"] != false {
		t.Fatal(d)
	}
}

// cli#998: an empty-details first error keeps its place; a later error's code is not borrowed.
func TestCloneDetailsDoNotBorrowFromLaterError(t *testing.T) {
	raw := gqlerror.List{
		{Message: "first"},
		{Message: "second", Extensions: map[string]any{"code": "CLONE_BASE_UNUSABLE", "reason": "LATER"}},
	}
	if d := CloneAgentDetails(MapCloneAgentError(raw)); d != nil {
		t.Fatal(d)
	}
}
