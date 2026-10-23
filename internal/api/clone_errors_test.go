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
