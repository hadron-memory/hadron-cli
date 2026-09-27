package cmd

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/exitcode"
)

// The server governs the whole dotted role family. A plain edit must use the
// existing role's door, while an explicit role change must use the destination
// door too; omitting role and tags must still preserve both on the wire.
func TestNodeUpdateRoutesDottedRoleFamilies(t *testing.T) {
	for _, tc := range []struct {
		name, stored, flag, door string
	}{
		{"tagless spec rule rename", "spec.rule", "", "UpdateSpecNode"},
		{"deep spec role rename", "spec.feature.screen", "", "UpdateSpecNode"},
		{"tagless review check rename", "review.security", "", "UpdateReviewNode"},
		{"deep review role rename", "review.audit.deep", "", "UpdateReviewNode"},
		{"set spec subrole", "", "spec.rule", "UpdateSpecNode"},
		{"set review subrole", "", "review.security", "UpdateReviewNode"},
		{"change away from spec subrole", "spec.rule", "note", "UpdateSpecNode"},
		{"change away from review subrole", "review.security", "note", "UpdateReviewNode"},
		{"ordinary spec prefix", "special", "", "UpdateNode"},
		{"ordinary review prefix", "reviewer", "", "UpdateNode"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			stored := "null"
			if tc.stored != "" {
				stored = strconv.Quote(tc.stored)
			}
			gql, captured := captureGraphQL(t, map[string]string{
				"ResolveUrn":       resolveNodeJSON,
				"GetNode":          kindDetail(stored, "false"),
				"UpdateNode":       `{"data":{"updateNode":` + nodeJSON + `}}`,
				"UpdateSpecNode":   `{"data":{"updateSpecNode":` + nodeJSON + `}}`,
				"UpdateReviewNode": `{"data":{"updateReviewNode":` + nodeJSON + `}}`,
			})
			f, _ := testFactory(t)
			root := NewRootCmd(f)
			args := []string{"node", "update", nodeURN, "--name", "Renamed", "--server", gql.URL}
			if tc.flag != "" {
				args = append(args, "--role", tc.flag)
			}
			root.SetArgs(args)
			if err := root.Execute(); err != nil {
				t.Fatalf("execute: %v", err)
			}
			input := doorInput(t, captured, tc.door)
			if input["name"] != "Renamed" {
				t.Errorf("name = %v, want Renamed", input["name"])
			}
			if _, sent := input["tags"]; sent {
				t.Error("a name/role edit must preserve stored tags")
			}
			if tc.flag == "" {
				if _, sent := input["role"]; sent {
					t.Error("an omitted role must preserve the stored subrole")
				}
			} else if input["role"] != tc.flag {
				t.Errorf("role = %v, want %q", input["role"], tc.flag)
			}
			for _, other := range []string{"UpdateNode", "UpdateSpecNode", "UpdateReviewNode"} {
				if other != tc.door {
					if _, sent := captured[other]; sent {
						t.Errorf("unexpected write through %s", other)
					}
				}
			}
		})
	}
}

func TestDottedSpecEditPreservesRoleAndTags(t *testing.T) {
	mocks := guardMocks(guardWriteOK)
	mocks["GetSpecNodeForEdit"][0] = strings.Replace(mocks["GetSpecNodeForEdit"][0], `"tags":["spec"],"role":null`, `"tags":[],"role":"spec.rule"`, 1)
	mocks["GetSpecNodeForEdit"][0] = strings.ReplaceAll(mocks["GetSpecNodeForEdit"][0], "msg:010:02", "app:pas:010:01")
	file := filepath.Join(t.TempDir(), "body.md")
	if err := os.WriteFile(file, []byte("# New spec body\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	abstract := filepath.Join(t.TempDir(), "abstract.txt")
	if err := os.WriteFile(abstract, []byte("New abstract\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	gql, captured := queueGraphQL(t, mocks)
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"spec", "edit", "-m", "micromentor.org:specs-2.0", "app:pas:010:01",
		"--content-file", file, "--abstract-file", abstract, "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("tagless spec.rule edit: %v", err)
	}
	if len(captured["UpdateSpecNode"]) != 1 {
		t.Fatalf("expected one guarded updateSpecNode write, got %d", len(captured["UpdateSpecNode"]))
	}
	var vars struct {
		Input map[string]json.RawMessage `json:"input"`
	}
	if err := json.Unmarshal(captured["UpdateSpecNode"][0], &vars); err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{"role", "tags"} {
		if _, sent := vars.Input[field]; sent {
			t.Errorf("spec edit must preserve %s by omission", field)
		}
	}
	if _, sent := vars.Input["expectedRevision"]; !sent {
		t.Error("spec edit must retain its revision guard")
	}
}

func TestDottedSpecLinkAcceptsTaglessEndpoints(t *testing.T) {
	detail := strings.Replace(linkSpecDetail, `"tags":["spec"],`, `"tags":[],"role":"spec.rule",`, 1)
	gql, captured := captureGraphQL(t, map[string]string{
		"ResolveUrn": resolveSpecJSON,
		"GetNode":    detail,
		"CreateEdge": linkEdgeResp,
	})
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"spec", "link", "cor:dmo:020:04", "cor:dmo:060:02", "-m", specMem,
		"--label", "documents", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("tagless spec.rule link: %v", err)
	}
	if _, sent := captured["CreateEdge"]; !sent {
		t.Error("spec link did not create the edge")
	}
}

func TestDottedSpecDoorKeepsServerAuthorization(t *testing.T) {
	gql, captured := captureGraphQL(t, map[string]string{
		"ResolveUrn":     resolveNodeJSON,
		"GetNode":        kindDetail(`"spec.rule"`, "false"),
		"UpdateSpecNode": `{"errors":[{"message":"not permitted","extensions":{"code":"FORBIDDEN"}}]}`,
	})
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"node", "update", nodeURN, "--name", "Denied", "--server", gql.URL})
	if got := exitCodeFor(root.Execute()); got != exitcode.Forbidden {
		t.Fatalf("governed-door authorization refusal exit = %d, want %d", got, exitcode.Forbidden)
	}
	if _, sent := captured["UpdateSpecNode"]; !sent {
		t.Error("authorization must be decided on the governed door")
	}
	if _, sent := captured["UpdateNode"]; sent {
		t.Error("must not fall back to the generic door after authorization refusal")
	}
}
