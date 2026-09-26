package api

import (
	"reflect"
	"strings"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/api/gen"
)

// Every optional NodeFilter field must be OMITTED when unset, never sent as
// null. Two reasons, and the second is the one a refresh keeps re-breaking:
//
//  1. The server reads an omitted filter field as "no constraint".
//  2. A server OLDER than the committed snapshot does not have a field the
//     snapshot added, and GraphQL rejects an unknown input field whatever its
//     value — so `role: null` would fail every `nodes` / search / chat listing
//     against it, although no command asked for a role filter.
//
// #1325's refresh (cli#716, PR #733) added NodeFilter.role with a bare
// json:"role" tag, and the review caught it, not a test: every guard that
// existed listed fields BY HAND, so a field nobody had listed yet was never
// checked (findings:a-schema-refresh-can-regress-an-unrelated-write). This one
// walks the generated struct, so the next field a refresh adds is checked the
// moment it appears — and fails until all three operations sharing NodeFilter
// (nodes.graphql, search.graphql, chat.graphql) carry its omitempty directive.
func TestNodeFilterOmitsEveryUnsetField(t *testing.T) {
	assertOmitsEveryUnsetField(t, gen.NodeFilter{})
}

// The same rule for the other inputs this change sends or regenerated:
//   - UpdateNodeInput: every node update sends it. The re-export from the
//     #1360 merge brought #1352's expectedRevision in with a bare tag, so every
//     update would have sent expectedRevision: null — rejected as an unknown
//     field by any server predating #1352. Measured on cli#733, caught here.
//   - the rule inputs of `memory config rule add|update` (#1325 part b), where
//     an unset field sent as null would CLEAR it.
func TestSentInputsOmitEveryUnsetField(t *testing.T) {
	for _, v := range []any{
		gen.UpdateNodeInput{},
		gen.CreateNodeRoleRuleInput{},
		gen.UpdateNodeRoleRuleInput{},
	} {
		assertOmitsEveryUnsetField(t, v)
	}
}

func assertOmitsEveryUnsetField(t *testing.T, v any) {
	t.Helper()
	typ := reflect.TypeOf(v)
	for i := 0; i < typ.NumField(); i++ {
		f := typ.Field(i)
		switch f.Type.Kind() {
		case reflect.Pointer, reflect.Slice, reflect.Map, reflect.Interface:
		default:
			continue // a non-nullable Go value cannot be "unset"
		}
		tag := f.Tag.Get("json")
		if !strings.Contains(tag, ",omitempty") {
			t.Errorf("%s.%s has json tag %q: an unset value would be sent as null. "+
				"Add `# @genqlient(for: \"%s.<field>\", omitempty: true)` to EVERY operation using %s and regenerate",
				typ.Name(), f.Name, tag, typ.Name(), typ.Name())
		}
	}
}
