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
					findNode("msg:010:02"),                              // citation-shaped but untagged: not a spec (#708)
					findNode("app:onb:010:02:screens:settings", "spec"), // any shape (#708)
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
	if len(got) != 2 || got[0].Citation != "msg:010:01" || got[1].Citation != "app:onb:010:02:screens:settings" {
		t.Fatalf("got specs %+v, want the two tagged specs, whatever their shape, and not the untagged citation", got)
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

func TestCollectSpecFindResultsCapsRawPageSize(t *testing.T) {
	offsets := []int{}
	got, _, _, err := collectSpecFindResults(nodesPageSize+1, func(limit, offset int) (*api.FindNodesPage, error) {
		offsets = append(offsets, offset)
		if limit != nodesPageSize {
			t.Fatalf("raw page size = %d, want capped page size %d", limit, nodesPageSize)
		}
		switch offset {
		case 0:
			total := 2 * nodesPageSize
			nodes := make([]*api.ListNode, 0, nodesPageSize)
			for i := 0; i < nodesPageSize; i++ {
				nodes = append(nodes, findNode("note-"+string(rune('a'+i%26)), "misc"))
			}
			return &api.FindNodesPage{Total: &total, Nodes: nodes}, nil
		case nodesPageSize:
			return &api.FindNodesPage{Nodes: []*api.ListNode{findNode("msg:010:01", "spec")}}, nil
		default:
			t.Fatalf("unexpected offset %d", offset)
			return nil, nil
		}
	})
	if err != nil {
		t.Fatalf("collectSpecFindResults: %v", err)
	}
	if len(offsets) != 2 || offsets[0] != 0 || offsets[1] != nodesPageSize {
		t.Fatalf("offsets = %v, want [0 %d]", offsets, nodesPageSize)
	}
	if len(got) != 1 || got[0].Citation != "msg:010:01" {
		t.Fatalf("got %+v, want one spec from second capped page", got)
	}
}

// #708: what makes a node a spec is its tag or its governed role, never the
// shape of its loc. An untagged node at a legacy-shaped loc is not a spec, and
// a tagged node at any shape is.
func TestIsSpecIgnoresLocShape(t *testing.T) {
	role := api.SpecNodeRole
	other := "review"
	for _, c := range []struct {
		name string
		tags []string
		role *string
		want bool
	}{
		{"tagged", []string{"spec"}, nil, true},
		{"governed role only", nil, &role, true},
		{"untagged, no role", nil, nil, false},
		{"another governed role", nil, &other, false},
		{"other tags only", []string{"draft"}, nil, false},
	} {
		if got := isSpec(c.tags, c.role); got != c.want {
			t.Errorf("%s: isSpec = %v, want %v", c.name, got, c.want)
		}
	}
}
