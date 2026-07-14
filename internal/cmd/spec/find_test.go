package spec

import (
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/api"
)

func findNode(loc string, tags ...string) *api.ListNode {
	return &api.ListNode{
		Id:        "id-" + loc,
		MemoryId:  "mem1",
		Loc:       loc,
		Name:      loc + " — T",
		NodeType:  "info",
		Tags:      tags,
		UpdatedAt: "2026-07-14T00:00:00Z",
	}
}

func TestCollectSpecFindResultsLimitsAfterSpecFiltering(t *testing.T) {
	calls := 0
	got, _, _, err := collectSpecFindResults(2, func(limit, offset int) (*api.FindNodesPage, error) {
		calls++
		if limit != specFindPageSize {
			t.Fatalf("raw page size = %d, want %d", limit, specFindPageSize)
		}
		switch offset {
		case 0:
			total := 2 * specFindPageSize
			nodes := make([]*api.ListNode, 0, specFindPageSize)
			for i := 0; i < specFindPageSize; i++ {
				nodes = append(nodes, findNode("note-"+string(rune('a'+i%26)), "misc"))
			}
			return &api.FindNodesPage{
				Total: &total,
				Nodes: nodes,
			}, nil
		case specFindPageSize:
			return &api.FindNodesPage{
				Nodes: []*api.ListNode{
					findNode("msg:010:01", "spec"),
					findNode("msg:010:02"),
					findNode("another-note", "misc"),
				},
			}, nil
		default:
			t.Fatalf("unexpected offset %d", offset)
			return nil, nil
		}
	})
	if err != nil {
		t.Fatalf("collectSpecFindResults: %v", err)
	}
	if calls != 2 {
		t.Fatalf("calls = %d, want 2", calls)
	}
	if len(got) != 2 || got[0].Citation != "msg:010:01" || got[1].Citation != "msg:010:02" {
		t.Fatalf("got specs %+v, want msg:010:01 and untagged citation msg:010:02", got)
	}
}

func TestCollectSpecFindResultsStopsWhenExhausted(t *testing.T) {
	got, _, _, err := collectSpecFindResults(3, func(limit, offset int) (*api.FindNodesPage, error) {
		if offset != 0 {
			t.Fatalf("should stop after a short page, got offset %d", offset)
		}
		return &api.FindNodesPage{Nodes: []*api.ListNode{findNode("msg:010:01", "spec")}}, nil
	})
	if err != nil {
		t.Fatalf("collectSpecFindResults: %v", err)
	}
	if len(got) != 1 || got[0].Citation != "msg:010:01" {
		t.Fatalf("got %+v, want the one available spec", got)
	}
}

func TestIsSpecNodeSemanticIncludesUntaggedCitation(t *testing.T) {
	if !isSpecNode(nil, "msg:010:02") {
		t.Fatal("semantic spec filtering should include citation-shaped untagged nodes")
	}
	if isSpecNode(nil, "register") {
		t.Fatal("non-citation nodes without the spec tag must be filtered out")
	}
}
