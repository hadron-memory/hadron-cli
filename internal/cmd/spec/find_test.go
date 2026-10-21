package spec

import (
	"fmt"
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
		if offset > 1 {
			t.Fatalf("should stop after the empty page, got offset %d", offset)
		}
		if offset == 1 {
			// A short page is not the end (cli#967): the empty probe is.
			return &api.FindNodesPage{Nodes: []*api.ListNode{}}, nil
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
		case nodesPageSize + 1:
			return &api.FindNodesPage{Nodes: []*api.ListNode{}}, nil
		default:
			t.Fatalf("unexpected offset %d", offset)
			return nil, nil
		}
	})
	if err != nil {
		t.Fatalf("collectSpecFindResults: %v", err)
	}
	// The second page is short, which proves nothing (cli#967): the walk
	// advances by the rows served and ends on the empty page after it.
	if len(offsets) != 3 || offsets[0] != 0 || offsets[1] != nodesPageSize || offsets[2] != nodesPageSize+1 {
		t.Fatalf("offsets = %v, want [0 %d %d]", offsets, nodesPageSize, nodesPageSize+1)
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
	subrole := "spec.rule"
	deepSubrole := "spec.feature.screen"
	nearPrefix := "special"
	otherNearPrefix := "specification"
	for _, c := range []struct {
		name string
		tags []string
		role *string
		want bool
	}{
		{"tagged", []string{"spec"}, nil, true},
		{"governed role only", nil, &role, true},
		{"dotted role only", nil, &subrole, true},
		{"deep dotted role only", nil, &deepSubrole, true},
		{"untagged, no role", nil, nil, false},
		{"another governed role", nil, &other, false},
		{"ordinary prefix", nil, &nearPrefix, false},
		{"another ordinary prefix", nil, &otherNearPrefix, false},
		{"legacy tag remains sufficient", []string{"spec"}, &nearPrefix, true},
		{"other tags only", []string{"draft"}, nil, false},
	} {
		if got := isSpec(c.tags, c.role); got != c.want {
			t.Errorf("%s: isSpec = %v, want %v", c.name, got, c.want)
		}
	}
}

// A server that serves fewer rows than asked for must not hide specs behind
// the first page (cli#967).
func TestCollectSpecFindResultsPastAClampedPage(t *testing.T) {
	var all []*api.ListNode
	for i := 0; i < 30; i++ {
		tag := "misc"
		if i%10 == 9 {
			tag = "spec"
		}
		all = append(all, findNode(fmt.Sprintf("loc:%02d", i), tag))
	}
	got, _, _, err := collectSpecFindResults(3, func(limit, offset int) (*api.FindNodesPage, error) {
		end := offset + 7 // clamp every page to 7 rows
		if end > len(all) {
			end = len(all)
		}
		if offset >= len(all) {
			return &api.FindNodesPage{Nodes: []*api.ListNode{}}, nil
		}
		return &api.FindNodesPage{Nodes: all[offset:end]}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 {
		t.Fatalf("got %d specs, want all 3 across clamped pages", len(got))
	}
}

// A provider returning the same non-empty page for every offset is a failed
// read, not a successful partial result (cli#967, Gene's P2-1).
func TestCollectSpecFindResultsFailsWhenThePageDoesNotAdvance(t *testing.T) {
	_, _, _, err := collectSpecFindResults(5, func(limit, offset int) (*api.FindNodesPage, error) {
		return &api.FindNodesPage{Nodes: []*api.ListNode{findNode("msg:010:01", "spec")}}, nil
	})
	if err == nil {
		t.Fatal("a repeated non-empty page must be an error")
	}
}
