package spec

import (
	"errors"
	"testing"

	"github.com/vektah/gqlparser/v2/gqlerror"

	"github.com/hadron-memory/hadron-cli/internal/api"
	"github.com/hadron-memory/hadron-cli/internal/api/gen"
)

func TestCollectSpecNodesUnionsMarkersBeforePaging(t *testing.T) {
	role := "spec.rule"
	tagged := &api.ListNode{Id: "tag", MemoryId: "m", Loc: "b", Tags: []string{"spec"}}
	both := &api.ListNode{Id: "both", MemoryId: "m", Loc: "c", Tags: []string{"spec"}, Role: &role}
	roleOnly := &api.ListNode{Id: "role", MemoryId: "m", Loc: "a", Role: &role}
	outside := &api.ListNode{Id: "other", MemoryId: "m", Loc: "aa"}
	calls := 0
	got, err := collectSpecNodes(nil, nil, func(filter *gen.NodeFilter) ([]*api.ListNode, error) {
		calls++
		switch {
		case filter != nil && len(filter.Tags) == 1 && filter.Tags[0] == "spec":
			return []*api.ListNode{tagged, both}, nil
		case filter != nil && filter.Role != nil && *filter.Role == "spec":
			return []*api.ListNode{roleOnly, outside, both}, nil
		default:
			t.Fatalf("unexpected filter: %+v", filter)
			return nil, nil
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	if calls != 2 || len(got) != 3 || got[0].Id != "role" || got[1].Id != "tag" || got[2].Id != "both" {
		t.Fatalf("union = %+v after %d calls; want three distinct, loc-sorted specs", got, calls)
	}
	if page := pageBranch(got, "", 1, 1); len(page) != 1 || page[0].Id != "tag" {
		t.Errorf("offset/limit must cut the deduped union, got %+v", page)
	}
}

func TestCollectSpecNodesFallbackOnOldRoleFilter(t *testing.T) {
	role := "spec.rule"
	validation := gqlerror.List{&gqlerror.Error{
		Message:    `Field "role" is not defined by type "NodeFilter".`,
		Extensions: map[string]any{"code": "GRAPHQL_VALIDATION_FAILED"},
	}}
	calls := 0
	got, err := collectSpecNodes(nil, nil, func(filter *gen.NodeFilter) ([]*api.ListNode, error) {
		calls++
		switch {
		case filter != nil && len(filter.Tags) == 1:
			return []*api.ListNode{{Id: "tag", Loc: "b", Tags: []string{"spec"}}}, nil
		case filter != nil && filter.Role != nil:
			return nil, validation
		case filter == nil:
			return []*api.ListNode{
				{Id: "tag", Loc: "b", Tags: []string{"spec"}},
				{Id: "role", Loc: "a", Role: &role},
				{Id: "other", Loc: "c"},
			}, nil
		default:
			t.Fatalf("unexpected filter: %+v", filter)
			return nil, nil
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	if calls != 3 || len(got) != 2 || got[0].Id != "role" || got[1].Id != "tag" {
		t.Fatalf("old-server fallback = %+v after %d calls; want complete role+tag union", got, calls)
	}
}

func TestCollectSpecNodesDoesNotHideOtherRoleQueryFailures(t *testing.T) {
	denied := errors.New("access denied")
	got, err := collectSpecNodes(nil, nil, func(filter *gen.NodeFilter) ([]*api.ListNode, error) {
		if filter != nil && filter.Role != nil {
			return nil, denied
		}
		return nil, nil
	})
	if !errors.Is(err, denied) || got != nil {
		t.Fatalf("failure = (%+v, %v), want original access error", got, err)
	}
}
