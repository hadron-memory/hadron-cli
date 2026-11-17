package cmd

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/exitcode"
)

// ---- spec link warns when an end is not governed (#1781, follow-up #1658) ----
//
// A tag-only node (the "spec" tag, no spec / spec.* role) is a legacy
// candidate, not law. `spec link` still creates the edge (relations between
// non-law nodes are harmless) but says which end is not governed.

const (
	linkFrom1781 = "cor:dmo:020:04"
	linkTo1781   = "cor:dmo:060:02"
	linkLabel    = "documents the thing"
)

// governedLinkDetail is linkSpecDetail carrying a governed role as well.
func governedLinkDetail() string {
	return strings.Replace(linkSpecDetail, `"tags":["spec"]`, `"tags":["spec"],"role":"spec.rule"`, 1)
}

func link1781Fixture(governed, linked bool) string {
	d := linkSpecDetail
	if governed {
		d = governedLinkDetail()
	}
	if linked {
		d = strings.Replace(d, `"outgoingEdges":[]`, `"outgoingEdges":[{"id":"le0","name":"`+linkLabel+`","loc":"x","isRunnable":false,"priority":0,"target":{"id":"sp1","loc":"`+linkTo1781+`","memoryId":"mem1"}}]`, 1)
	}
	return d
}

type link1781Result struct {
	stdout, stderr string
	err            error
	creates        int
}

func runLink1781(t *testing.T, fromGoverned, toGoverned bool, mode string, jsonOut bool) link1781Result {
	t.Helper()
	gql, captured := queueGraphQL(t, map[string][]string{
		"ResolveUrn": {resolveSpecJSON},
		"GetNode":    {link1781Fixture(fromGoverned, mode == "already"), link1781Fixture(toGoverned, false)},
		"CreateEdge": {linkEdgeResp},
	})
	f, out := testFactory(t)
	var errOut strings.Builder
	f.IOStreams.ErrOut = &errOut
	root := NewRootCmd(f)
	args := []string{"spec", "link", linkFrom1781, linkTo1781, "-m", specMem, "--label", linkLabel, "--server", gql.URL}
	if mode == "dry" {
		args = append(args, "--dry-run")
	}
	if jsonOut {
		args = append(args, "--json")
	}
	root.SetArgs(args)
	err := root.Execute()
	return link1781Result{out.String(), errOut.String(), err, len(captured["CreateEdge"])}
}

func TestSpecLinkWarnsOnUngovernedEnds1781(t *testing.T) {
	for _, tc := range []struct {
		name                     string
		fromGoverned, toGoverned bool
		want                     []string
	}{
		{"source-tag-only", false, true, []string{linkFrom1781}},
		{"target-tag-only", true, false, []string{linkTo1781}},
		{"both-tag-only", false, false, []string{linkFrom1781, linkTo1781}},
		{"both-governed", true, true, nil},
	} {
		for _, mode := range []string{"create", "dry", "already"} {
			for _, jsonOut := range []bool{true, false} {
				t.Run(fmt.Sprintf("%s/%s/json=%v", tc.name, mode, jsonOut), func(t *testing.T) {
					r := runLink1781(t, tc.fromGoverned, tc.toGoverned, mode, jsonOut)
					if r.err != nil {
						t.Fatalf("a tag-only end must still link (exit 0): %v", r.err)
					}
					if wantCreates := map[string]int{"create": 1, "dry": 0, "already": 0}[mode]; r.creates != wantCreates {
						t.Fatalf("%s: CreateEdge calls = %d, want %d", mode, r.creates, wantCreates)
					}
					if jsonOut {
						var raw map[string]json.RawMessage
						if err := json.Unmarshal([]byte(r.stdout), &raw); err != nil {
							t.Fatalf("not JSON: %v\n%s", err, r.stdout)
						}
						for _, key := range []string{"from", "to", "label", "memoryId", "alreadyLinked", "dryRun", "warnings"} {
							if _, ok := raw[key]; !ok {
								t.Fatalf("key %q missing: %s", key, r.stdout)
							}
						}
						var warnings []string
						if err := json.Unmarshal(raw["warnings"], &warnings); err != nil || warnings == nil {
							t.Fatalf("warnings must be a list ([] when none), got %s: %v", raw["warnings"], err)
						}
						if len(warnings) != len(tc.want) {
							t.Fatalf("warnings = %q, want one per ungoverned end %v", warnings, tc.want)
						}
						for i, loc := range tc.want {
							if !strings.Contains(warnings[i], loc) || !strings.Contains(warnings[i], "not governed") || !strings.Contains(warnings[i], "--role spec") {
								t.Fatalf("warning %d = %q, want it to name %s and the remedy", i, warnings[i], loc)
							}
						}
						return
					}
					for _, loc := range tc.want {
						if !strings.Contains(r.stdout, loc+" is not governed") {
							t.Fatalf("text output does not warn about %s:\n%s", loc, r.stdout)
						}
					}
					if len(tc.want) == 0 && strings.Contains(r.stdout, "not governed") {
						t.Fatalf("two governed ends must print no warning:\n%s", r.stdout)
					}
					wantVerb := map[string]string{"create": "✓ linked", "dry": "would link", "already": "✓ already linked"}[mode]
					if !strings.HasPrefix(r.stdout, wantVerb+" ") {
						t.Fatalf("the result line must stay first and unchanged (%q):\n%s", wantVerb, r.stdout)
					}
				})
			}
		}
	}
}

// The race path renders its own result; it must warn the same way.
func TestSpecLinkLostRaceStillWarns1781(t *testing.T) {
	gql, _ := queueGraphQL(t, map[string][]string{
		"ResolveUrn": {resolveSpecJSON},
		"GetNode":    {linkSpecDetail, linkSpecDetail, link1781Fixture(false, true)},
		"CreateEdge": {`{"errors":[{"message":"Unexpected error.","extensions":{"code":"INTERNAL_SERVER_ERROR"}}]}`},
	})
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"spec", "link", linkFrom1781, linkTo1781, "-m", specMem, "--label", linkLabel, "--json", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	var dto struct {
		AlreadyLinked bool     `json:"alreadyLinked"`
		Warnings      []string `json:"warnings"`
	}
	if err := json.Unmarshal([]byte(out.String()), &dto); err != nil {
		t.Fatal(err)
	}
	if !dto.AlreadyLinked || len(dto.Warnings) != 2 {
		t.Fatalf("the race path must report the link and warn for both tag-only ends: %s", out.String())
	}
}

// An end with neither marker is still refused, before any write.
func TestSpecLinkNeitherMarkerStillRefused1781(t *testing.T) {
	for _, bad := range []string{"from", "to"} {
		t.Run(bad, func(t *testing.T) {
			nodes := []string{linkSpecDetail, linkSpecDetail}
			if bad == "from" {
				nodes[0] = linkNonSpecDetail
			} else {
				nodes[1] = linkNonSpecDetail
			}
			gql, captured := queueGraphQL(t, map[string][]string{
				"ResolveUrn": {resolveSpecJSON},
				"GetNode":    nodes,
				"CreateEdge": {linkEdgeResp},
			})
			f, _ := testFactory(t)
			root := NewRootCmd(f)
			root.SetArgs([]string{"spec", "link", linkFrom1781, linkTo1781, "-m", specMem, "--server", gql.URL})
			err := root.Execute()
			if got := exitCodeFor(err); got != exitcode.Usage || err == nil || !strings.Contains(err.Error(), "is not a spec") {
				t.Fatalf("an end with neither marker must be refused (exit 2): %d %v", got, err)
			}
			if len(captured["CreateEdge"]) != 0 {
				t.Fatal("a refused link must not write")
			}
		})
	}
}
