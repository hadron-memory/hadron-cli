package cmd

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/exitcode"
)

// #708: the spec commands are neutral to the old fixed hierarchy. A spec is a
// node carrying the `spec` tag (or the governed spec role), at ANY loc the
// generic node rule allows — the legacy grammar (a 3-letter module, 3-digit
// feature, 2-digit rule and flow, at most five segments) is no longer a
// validity rule for reading, listing or linking.
//
// The shapes below are deliberately varied: the legacy grammar itself (old
// citations must keep resolving), a single segment, a word-only path, and a
// path deeper than the old five-segment ceiling.
var hierarchyNeutralLocs = []string{
	"msg:010:02",                            // legacy flat citation
	"cli:cha:010:01:02",                     // legacy product-rooted flow (the old maximum depth)
	"glossary",                              // one segment
	"onboarding:mentor:screens",             // words, no tier shape
	"app:onb:010:02:screens:settings:empty", // deeper than the old ceiling
}

// hierarchyNeutralDetail is a spec node at loc, as GetNode returns it.
func hierarchyNeutralDetail(loc string) string {
	name, _ := json.Marshal(loc + " — T")
	content, _ := json.Marshal("# " + loc + " — T\n")
	return `{"data":{"node":{"id":"id-` + loc + `","memoryId":"mem1","loc":"` + loc + `","name":` + string(name) + `,` +
		`"description":null,"abstract":"About this.","abstractOriginHash":null,"nodeType":"info","tags":["spec"],` +
		`"content":` + string(content) + `,"data":{"version":"0.0.1"},"seq":null,` +
		`"createdAt":"2026-06-10T00:00:00Z","updatedAt":"2026-06-14T00:00:00Z","outgoingEdges":[],"incomingEdges":[]}}}`
}

func TestSpecGetAddressesAnyValidLoc(t *testing.T) {
	for _, loc := range hierarchyNeutralLocs {
		t.Run(loc, func(t *testing.T) {
			detail := hierarchyNeutralDetail(loc)
			raw := strings.TrimSuffix(strings.TrimPrefix(detail, `{"data":{"node":`), `}}`)
			gql, captured := captureGraphQL(t, map[string]string{
				"ResolveUrn": resolveSpecJSON,
				"GetNode":    detail,
				"NodeBatch":  specLintRawBodyStub(raw),
			})
			f, out := testFactory(t)
			root := NewRootCmd(f)
			root.SetArgs([]string{"spec", "get", loc, "-m", specMem, "--json", "--server", gql.URL})
			if err := root.Execute(); err != nil {
				t.Fatalf("a spec at %q must be readable: %v", loc, err)
			}
			var vars struct {
				Urn string `json:"urn"`
			}
			_ = json.Unmarshal(captured["ResolveUrn"], &vars)
			if !strings.HasSuffix(vars.Urn, "::"+loc) {
				t.Errorf("resolved %q, want the loc exactly as given", vars.Urn)
			}
			if !strings.Contains(out.String(), `"citation": "`+loc+`"`) {
				t.Errorf("output does not name %q:\n%s", loc, out.String())
			}
		})
	}
}

// The corpus scans no longer drop a tagged spec for its shape: what the
// server returns for the `spec` tag is what `spec ls` lists.
func TestSpecLsListsEveryShape(t *testing.T) {
	var hits []string
	for _, loc := range hierarchyNeutralLocs {
		hits = append(hits, specNodeList(loc, `["spec"]`))
	}
	gql, captured := captureGraphQL(t, map[string]string{
		"FindNodes": `{"data":{"nodes":[` + strings.Join(hits, ",") + `]}}`,
	})
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"spec", "ls", "-m", specMem, "--json", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	for _, loc := range hierarchyNeutralLocs {
		if !strings.Contains(out.String(), `"citation": "`+loc+`"`) {
			t.Errorf("spec ls dropped %q:\n%s", loc, out.String())
		}
	}
	// The corpus is still selected by the tag, server-side — not by shape, and
	// not by scanning everything.
	var vars findNodesVars
	_ = json.Unmarshal(captured["FindNodes"], &vars)
	if len(vars.Filter.Tags) != 1 || vars.Filter.Tags[0] != "spec" {
		t.Errorf("ls must still select the corpus by the spec tag, got %v", vars.Filter.Tags)
	}
}

// A legacy citation and a spec outside the grammar link like any two specs.
func TestSpecLinkAcrossShapes(t *testing.T) {
	gql, _ := captureGraphQL(t, map[string]string{
		"ResolveUrn": resolveSpecJSON,
		"GetNode":    linkSpecDetail,
		"CreateEdge": linkEdgeResp,
	})
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"spec", "link", "msg:010:02", "onboarding:mentor:screens", "-m", specMem, "--label", "shown on", "--json", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	var dto linkDTO
	if err := json.Unmarshal([]byte(out.String()), &dto); err != nil {
		t.Fatalf("output not JSON: %v\n%s", err, out.String())
	}
	if dto.From != "msg:010:02" || dto.To != "onboarding:mentor:screens" {
		t.Errorf("from/to = %q/%q", dto.From, dto.To)
	}
}

// The controls: what #708 removes is the TIER grammar, not validation. A loc
// that breaks the generic node rule is still refused before any request —
// the unreachable server proves nothing was sent — on every addressing
// command, and linking a spec to itself is still refused.
func TestSpecAddressingStillRefusesInvalidLocs(t *testing.T) {
	for _, c := range []struct {
		name string
		args []string
	}{
		{"get, empty segment", []string{"spec", "get", "msg::010"}},
		{"get, whitespace", []string{"spec", "get", "has space"}},
		{"edit, trailing colon", []string{"spec", "edit", "msg:010:", "--content", "x"}},
		{"link, bad target", []string{"spec", "link", "msg:010:02", "a b"}},
		{"link, self", []string{"spec", "link", "onboarding:mentor", "onboarding:mentor"}},
	} {
		t.Run(c.name, func(t *testing.T) {
			f, _ := testFactory(t)
			root := NewRootCmd(f)
			root.SetArgs(append(c.args, "-m", specMem, "--server", "http://127.0.0.1:1"))
			if got := exitCodeFor(root.Execute()); got != exitcode.Usage {
				t.Errorf("exit = %d, want Usage (%d)", got, exitcode.Usage)
			}
		})
	}
}
