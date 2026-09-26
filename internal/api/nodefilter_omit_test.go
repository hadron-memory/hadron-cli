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
	typ := reflect.TypeOf(gen.NodeFilter{})
	for i := 0; i < typ.NumField(); i++ {
		f := typ.Field(i)
		switch f.Type.Kind() {
		case reflect.Pointer, reflect.Slice, reflect.Map, reflect.Interface:
		default:
			continue // a non-nullable Go value cannot be "unset"
		}
		tag := f.Tag.Get("json")
		if !strings.Contains(tag, ",omitempty") {
			t.Errorf("NodeFilter.%s has json tag %q: an unset value would be sent as null. "+
				"Add `# @genqlient(for: \"NodeFilter.<field>\", omitempty: true)` to every operation using NodeFilter and regenerate",
				f.Name, tag)
		}
	}
}
