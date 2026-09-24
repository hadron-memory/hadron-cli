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
			// Its lint summary carries no shape finding: the loc is valid
			// (@copilot on #710).
			if strings.Contains(out.String(), "loc-shape") {
				t.Errorf("a valid loc was linted as malformed:\n%s", out.String())
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
		// A --prefix is a loc too (@copilot on #710), on every command that takes one.
		{"get --prefix", []string{"spec", "get", "--prefix", "msg::010"}},
		{"list --prefix", []string{"spec", "list", "--prefix", "has space"}},
		{"grep --prefix", []string{"spec", "grep", "x", "--prefix", "msg:"}},
		{"replace --prefix", []string{"spec", "replace", "a", "b", "--prefix", ":msg"}},
		{"check-tools --prefix", []string{"spec", "check-tools", "--prefix", "a b"}},
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

// ---- spec new <loc> (#708 slice C) ----

const notFoundResolve = `{"data":{"resolveUrn":null}}`

// newAtServer answers a `spec new <loc>` run: the first ResolveUrn (is the
// loc free?) says no node is there, every later one (an --inherit target)
// resolves to t1. Any FindNodes is unexpected — creating at an explicit loc
// scans nothing, because nothing is allocated.
func newAtServer(t *testing.T) (string, map[string]json.RawMessage) {
	t.Helper()
	resolves := 0
	gql, captured := captureGraphQLFunc(t, func(op string) string {
		switch op {
		case "ResolveUrn":
			resolves++
			if resolves == 1 {
				return notFoundResolve
			}
			return `{"data":{"resolveUrn":{"id":"t1","kind":"node","memoryId":"mem1"}}}`
		case "CreateSpecNode":
			return `{"data":{"createSpecNode":{"id":"new1","memoryId":"mem1","loc":"x","name":"x","nodeType":"info","tags":["spec"],"updatedAt":"2026-06-14T00:00:00Z"}}}`
		}
		return ""
	})
	return gql.URL, captured
}

type sentSpecInput struct {
	Input struct {
		Loc     string           `json:"loc"`
		Name    string           `json:"name"`
		Tags    []string         `json:"tags"`
		Role    *string          `json:"role"`
		Seq     *int             `json:"seq"`
		Content string           `json:"content"`
		Edges   []map[string]any `json:"edges"`
	} `json:"input"`
}

func TestSpecNewAtAnyLoc(t *testing.T) {
	for _, c := range []struct {
		loc     string
		wantSeq *int
	}{
		{"onboarding:mentor:screens:settings", nil},
		{"glossary", nil},
		{"guide:steps:07", func() *int { n := 7; return &n }()},
		{"app:onb:010:02:screens:settings:empty", nil},
	} {
		t.Run(c.loc, func(t *testing.T) {
			url, captured := newAtServer(t)
			f, _ := testFactory(t)
			root := NewRootCmd(f)
			root.SetArgs([]string{"spec", "new", c.loc, "-m", specMem, "--title", "Settings", "--json", "--server", url})
			if err := root.Execute(); err != nil {
				t.Fatalf("execute: %v", err)
			}
			if _, scanned := captured["FindNodes"]; scanned {
				t.Error("creating at an explicit loc must not scan the corpus: nothing is allocated")
			}
			var in sentSpecInput
			if err := json.Unmarshal(captured["CreateSpecNode"], &in); err != nil {
				t.Fatalf("CreateSpecNode vars: %v", err)
			}
			if in.Input.Loc != c.loc || in.Input.Name != c.loc+" — Settings" {
				t.Errorf("loc/name = %q/%q", in.Input.Loc, in.Input.Name)
			}
			if in.Input.Role == nil || *in.Input.Role != "spec" || len(in.Input.Tags) != 1 || in.Input.Tags[0] != "spec" {
				t.Errorf("a spec is written through the spec door with the spec tag and role: role=%v tags=%v", in.Input.Role, in.Input.Tags)
			}
			if len(in.Input.Edges) != 0 {
				t.Errorf("no edge is derived from the loc (no parent, no contract): %v", in.Input.Edges)
			}
			if (in.Input.Seq == nil) != (c.wantSeq == nil) || (c.wantSeq != nil && *in.Input.Seq != *c.wantSeq) {
				t.Errorf("seq = %v, want %v (a numeric last segment orders siblings)", in.Input.Seq, c.wantSeq)
			}
			if !strings.Contains(in.Input.Content, "# "+c.loc+" — Settings") || !strings.Contains(in.Input.Content, "## What invalidates this spec") {
				t.Errorf("body is not the rubric scaffold for this loc:\n%s", in.Input.Content)
			}
		})
	}
}

// The one edge a spec at an explicit loc gets is the one you name, written
// inline with the node, by resolved id.
func TestSpecNewAtExplicitInherit(t *testing.T) {
	url, captured := newAtServer(t)
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"spec", "new", "onboarding:mentor:screens", "--inherit", "onboarding:gen", "-m", specMem, "--title", "Screens", "--server", url})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	edges := sentSpecEdges(t, captured["CreateSpecNode"])
	if len(edges) != 1 || edges[0]["targetId"] != "t1" || edges[0]["name"] != "inherits the shared contract (general provisions)" {
		t.Errorf("edges = %v, want exactly the named inheritance edge, by resolved id", edges)
	}
}

func TestSpecNewAtDryRunWritesNothing(t *testing.T) {
	url, captured := newAtServer(t)
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"spec", "new", "onboarding:mentor", "--dry-run", "-m", specMem, "--title", "Mentor", "--json", "--server", url})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if _, wrote := captured["CreateSpecNode"]; wrote {
		t.Error("--dry-run wrote a node")
	}
	if !strings.Contains(out.String(), `"citation": "onboarding:mentor"`) || !strings.Contains(out.String(), `"dryRun": true`) {
		t.Errorf("dry-run output:\n%s", out.String())
	}
}

// A live node at the loc is refused, never overwritten.
func TestSpecNewAtRefusesAnExistingLoc(t *testing.T) {
	gql, captured := captureGraphQL(t, map[string]string{
		"ResolveUrn": `{"data":{"resolveUrn":{"id":"old1","kind":"node","memoryId":"mem1"}}}`,
	})
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"spec", "new", "onboarding:mentor", "-m", specMem, "--title", "Mentor", "--server", gql.URL})
	if got := exitCodeFor(root.Execute()); got != exitcode.Usage {
		t.Errorf("exit = %d, want Usage", got)
	}
	if _, wrote := captured["CreateSpecNode"]; wrote {
		t.Error("an existing loc was written over")
	}
}

// Refused before any request: a loc that breaks the generic rule, a loc
// combined with the legacy tier flags, a self-inherit, and --new-path on a
// loc that is not a legacy citation.
func TestSpecNewAtRefusals(t *testing.T) {
	for _, c := range []struct {
		name string
		args []string
		want string
	}{
		{"invalid loc", []string{"a b"}, ""},
		{"tier flags", []string{"onboarding:mentor", "--module", "msg"}, "legacy numbering"},
		{"--no-contract", []string{"onboarding:mentor", "--no-contract"}, "legacy numbering"},
		{"self inherit", []string{"onboarding:mentor", "--inherit", "onboarding:mentor"}, "itself"},
		{"--new-path, not legacy", []string{"onboarding:mentor", "--new-path"}, "drop --new-path"},
	} {
		t.Run(c.name, func(t *testing.T) {
			f, _ := testFactory(t)
			root := NewRootCmd(f)
			root.SetArgs(append(append([]string{"spec", "new"}, c.args...), "-m", specMem, "--title", "T", "--server", "http://127.0.0.1:1"))
			err := root.Execute()
			if got := exitCodeFor(err); got != exitcode.Usage {
				t.Fatalf("exit = %d, want Usage: %v", got, err)
			}
			if c.want != "" && !strings.Contains(err.Error(), c.want) {
				t.Errorf("error %q should mention %q", err, c.want)
			}
		})
	}
}

// ---- spec supersede (#708 slice C) ----

// supersedeToServer answers a supersede of the spec at oldLoc to an explicit
// --to loc: the old spec resolves and reads; the --to pre-check (the SECOND
// ResolveUrn) finds nothing there; the replacement is created; every re-read
// of the old spec shows the superseded-by edge, so it is retired. No scan is
// answered — naming the replacement allocates nothing.
func supersedeToServer(t *testing.T, oldLoc, toLoc string) (string, map[string]json.RawMessage) {
	t.Helper()
	old := hierarchyNeutralDetail(oldLoc)
	resolves, gets := 0, 0
	gql, captured := captureGraphQLFunc(t, func(op string) string {
		switch op {
		case "ResolveUrn":
			resolves++
			if resolves == 2 {
				return notFoundResolve
			}
			return `{"data":{"resolveUrn":{"id":"id-` + oldLoc + `","kind":"node","memoryId":"mem1"}}}`
		case "GetNode":
			gets++
			if gets > 1 {
				return withSupersededByEdge(old, "new1", toLoc)
			}
			return old
		case "CreateSpecNode":
			return `{"data":{"createSpecNode":{"id":"new1","memoryId":"mem1","loc":"` + toLoc + `","name":"x","nodeType":"info","tags":["spec"],"updatedAt":"2026-06-14T00:00:00Z"}}}`
		case "CreateEdge":
			return `{"data":{"createEdge":{"id":"e1","label":"superseded-by","priority":0,"source":{"id":"id-` + oldLoc + `","loc":"` + oldLoc + `"},"target":{"id":"new1","loc":"` + toLoc + `"}}}}`
		case "UpdateSpecNode":
			return `{"data":{"updateSpecNode":{"id":"id-` + oldLoc + `","memoryId":"mem1","loc":"` + oldLoc + `","name":"x","nodeType":"info","tags":["spec","superseded"],"updatedAt":"2026-06-14T00:00:00Z"}}}`
		}
		return ""
	})
	return gql.URL, captured
}

// Any spec can be superseded to a named loc: one outside the legacy numbering,
// a legacy module header (once refused as "not a rule/flow"), and a legacy
// rule that chooses its successor's loc instead of an allocated number.
func TestSpecSupersedeToAnyLoc(t *testing.T) {
	for _, c := range []struct{ old, to string }{
		{"onboarding:mentor:screens", "onboarding:mentor:screens-v2"},
		{"msg:010", "messaging:nudges"},
		{"msg:010:02", "msg:010:02:v2"},
	} {
		t.Run(c.old, func(t *testing.T) {
			url, captured := supersedeToServer(t, c.old, c.to)
			f, out := testFactory(t)
			root := NewRootCmd(f)
			root.SetArgs([]string{"spec", "supersede", c.old, "--to", c.to, "-m", specMem, "--title", "v2", "--yes", "--json", "--server", url})
			if err := root.Execute(); err != nil {
				t.Fatalf("execute: %v", err)
			}
			if _, scanned := captured["FindNodes"]; scanned {
				t.Error("a named replacement allocates nothing, so nothing is scanned")
			}
			var in sentSpecInput
			_ = json.Unmarshal(captured["CreateSpecNode"], &in)
			if in.Input.Loc != c.to || len(in.Input.Edges) != 0 {
				t.Errorf("replacement loc/edges = %q/%v, want %q with no derived edges", in.Input.Loc, in.Input.Edges, c.to)
			}
			var retire struct {
				Input struct {
					Loc  string   `json:"loc"`
					Tags []string `json:"tags"`
				} `json:"input"`
			}
			_ = json.Unmarshal(captured["UpdateSpecNode"], &retire)
			if retire.Input.Loc != c.old || !contains(retire.Input.Tags, "superseded") {
				t.Errorf("retire = %+v, want the old loc kept and tagged superseded", retire.Input)
			}
			if !strings.Contains(out.String(), `"new": "`+c.to+`"`) || !strings.Contains(out.String(), `"retired": true`) {
				t.Errorf("result:\n%s", out.String())
			}
		})
	}
}

// Without --to, only a legacy rule/flow citation can have a number allocated;
// anything else is told to name its replacement, and nothing is written.
func TestSpecSupersedeOutsideLegacyNeedsTo(t *testing.T) {
	for _, old := range []string{"onboarding:mentor:screens", "msg:010"} {
		t.Run(old, func(t *testing.T) {
			url, captured := supersedeToServer(t, old, "unused")
			f, _ := testFactory(t)
			root := NewRootCmd(f)
			root.SetArgs([]string{"spec", "supersede", old, "-m", specMem, "--title", "v2", "--yes", "--server", url})
			err := root.Execute()
			if got := exitCodeFor(err); got != exitcode.Usage || !strings.Contains(err.Error(), "--to <loc>") {
				t.Fatalf("exit %d, err %v: want Usage naming --to", got, err)
			}
			for _, op := range []string{"CreateSpecNode", "CreateEdge", "UpdateSpecNode"} {
				if _, wrote := captured[op]; wrote {
					t.Errorf("%s was sent", op)
				}
			}
		})
	}
}

// --to keeps every guard: the loc must be free, it can't be the old spec, it
// must be a valid loc, and it doesn't combine with legacy allocation flags.
func TestSpecSupersedeToRefusals(t *testing.T) {
	t.Run("occupied", func(t *testing.T) {
		gql, captured := captureGraphQL(t, map[string]string{
			"ResolveUrn": `{"data":{"resolveUrn":{"id":"x","kind":"node","memoryId":"mem1"}}}`,
			"GetNode":    hierarchyNeutralDetail("onboarding:mentor"),
		})
		f, _ := testFactory(t)
		root := NewRootCmd(f)
		root.SetArgs([]string{"spec", "supersede", "onboarding:mentor", "--to", "onboarding:mentor-v2", "-m", specMem, "--title", "v2", "--yes", "--server", gql.URL})
		if got := exitCodeFor(root.Execute()); got != exitcode.Usage {
			t.Errorf("exit = %d, want Usage", got)
		}
		if _, wrote := captured["CreateSpecNode"]; wrote {
			t.Error("an occupied --to was written over")
		}
	})
	for _, c := range []struct {
		name string
		args []string
	}{
		{"self", []string{"--to", "onboarding:mentor"}},
		{"invalid", []string{"--to", "a b"}},
		{"with --feature", []string{"--to", "x:y", "--feature", "020"}},
	} {
		t.Run(c.name, func(t *testing.T) {
			url, captured := supersedeToServer(t, "onboarding:mentor", "unused")
			f, _ := testFactory(t)
			root := NewRootCmd(f)
			root.SetArgs(append(append([]string{"spec", "supersede", "onboarding:mentor"}, c.args...), "-m", specMem, "--title", "v2", "--yes", "--server", url))
			if got := exitCodeFor(root.Execute()); got != exitcode.Usage {
				t.Errorf("exit = %d, want Usage", got)
			}
			if _, wrote := captured["CreateSpecNode"]; wrote {
				t.Error("a replacement was written")
			}
		})
	}
}

// ---- spec register (#708 slice C) ----

// The register is the legacy numbering's ledger. A spec at any other loc is
// valid but has no number, so it is NAMED as outside the numbering, never
// dropped; a non-spec node (the register itself) is neither.
func TestSpecRegisterNamesSpecsOutsideTheNumbering(t *testing.T) {
	gql, _ := captureGraphQL(t, map[string]string{
		"FindNodes": `{"data":{"nodes":[` +
			specNodeList("msg", `["spec"]`) + `,` + specNodeList("msg:010", `["spec"]`) + `,` + specNodeList("msg:010:02", `["spec"]`) + `,` +
			specNodeList("onboarding:mentor", `["spec"]`) + `,` + specNodeList("glossary", `["spec"]`) + `,` +
			specNodeList("register", `["index"]`) + `]}}`,
		"ResolveUrn": notFoundResolve,
	})
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"spec", "register", "-m", specMem, "--json", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	var dto struct {
		Modules []struct {
			Module string `json:"module"`
		} `json:"modules"`
		OutsideNumbering []string `json:"outsideNumbering"`
	}
	if err := json.Unmarshal([]byte(out.String()), &dto); err != nil {
		t.Fatalf("not JSON: %v\n%s", err, out.String())
	}
	if len(dto.OutsideNumbering) != 2 || dto.OutsideNumbering[0] != "glossary" || dto.OutsideNumbering[1] != "onboarding:mentor" {
		t.Errorf("outsideNumbering = %v, want exactly the two specs outside the numbering, sorted", dto.OutsideNumbering)
	}
	if len(dto.Modules) != 1 || dto.Modules[0].Module != "msg" {
		t.Errorf("modules = %+v, want the legacy ledger unchanged", dto.Modules)
	}
}

// An empty list renders as [], never null (the --json contract).
func TestSpecRegisterOutsideNumberingIsNeverNull(t *testing.T) {
	gql, _ := captureGraphQL(t, map[string]string{
		"FindNodes":  `{"data":{"nodes":[` + specNodeList("msg", `["spec"]`) + `]}}`,
		"ResolveUrn": notFoundResolve,
	})
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"spec", "register", "-m", specMem, "--json", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if !strings.Contains(out.String(), `"outsideNumbering": []`) {
		t.Errorf("want outsideNumbering: []:\n%s", out.String())
	}
}

// A resumed supersede (the old spec already carries a superseded-by edge from
// an earlier run) finishes against THAT successor. A --to naming a different
// one is refused as a conflict with nothing written; a --to naming the same one
// finishes (@codex on #710).
func TestSpecSupersedeResumeHonoursTo(t *testing.T) {
	const old, earlier = "onboarding:mentor", "onboarding:mentor-v2"
	resumeServer := func(t *testing.T) (string, map[string]json.RawMessage) {
		t.Helper()
		withEdge := withSupersededByEdge(hierarchyNeutralDetail(old), "new1", earlier)
		gql, captured := captureGraphQLFunc(t, func(op string) string {
			switch op {
			case "ResolveUrn":
				return `{"data":{"resolveUrn":{"id":"id-` + old + `","kind":"node","memoryId":"mem1"}}}`
			case "GetNode":
				return withEdge
			case "UpdateSpecNode":
				return `{"data":{"updateSpecNode":{"id":"id-` + old + `","memoryId":"mem1","loc":"` + old + `","name":"x","nodeType":"info","tags":["spec","superseded"],"updatedAt":"2026-06-14T00:00:00Z"}}}`
			}
			return ""
		})
		return gql.URL, captured
	}
	t.Run("a different --to is refused", func(t *testing.T) {
		url, captured := resumeServer(t)
		f, _ := testFactory(t)
		root := NewRootCmd(f)
		root.SetArgs([]string{"spec", "supersede", old, "--to", "somewhere:else", "-m", specMem, "--title", "v2", "--yes", "--server", url})
		err := root.Execute()
		if got := exitCodeFor(err); got != exitcode.Conflict || !strings.Contains(err.Error(), earlier) {
			t.Fatalf("exit %d, err %v: want a Conflict naming the earlier successor", got, err)
		}
		for _, op := range []string{"UpdateSpecNode", "CreateSpecNode", "CreateEdge"} {
			if _, wrote := captured[op]; wrote {
				t.Errorf("%s was sent: nothing may be retired or created against a --to that was ignored", op)
			}
		}
	})
	t.Run("the same --to finishes", func(t *testing.T) {
		url, captured := resumeServer(t)
		f, out := testFactory(t)
		root := NewRootCmd(f)
		root.SetArgs([]string{"spec", "supersede", old, "--to", earlier, "-m", specMem, "--title", "v2", "--yes", "--json", "--server", url})
		if err := root.Execute(); err != nil {
			t.Fatalf("execute: %v", err)
		}
		if _, retired := captured["UpdateSpecNode"]; !retired || !strings.Contains(out.String(), `"retired": true`) {
			t.Errorf("a matching --to must finish the retirement:\n%s", out.String())
		}
	})
}

// extract allocates in the legacy numbering, so it stays a legacy adapter: a
// source outside the numbering is refused before any request, with the way
// forward named instead of the old grammar error (#708 slice C).
func TestSpecExtractRefusesASourceOutsideTheNumbering(t *testing.T) {
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"spec", "extract", "onboarding:mentor:screens", "--to-feature", "020", "--title", "T", "--content", "x", "-m", specMem, "--server", "http://127.0.0.1:1"})
	err := root.Execute()
	if got := exitCodeFor(err); got != exitcode.Usage || !strings.Contains(err.Error(), "spec new <loc>") {
		t.Fatalf("exit %d, err %v: want Usage naming `spec new <loc>`", got, err)
	}
	if strings.Contains(err.Error(), "must be 3 lowercase letters") && !strings.Contains(err.Error(), "legacy citation") {
		t.Errorf("the refusal must explain the adapter, not only the grammar: %v", err)
	}
}

// An invalid positional loc (or --inherit) is refused before the memory is
// even resolved: -m here names a memory that needs a lookup, and the server is
// unreachable, so a request made first would surface as exit 7, not Usage
// (@copilot on #710).
func TestSpecNewAtValidatesBeforeResolvingTheMemory(t *testing.T) {
	for _, args := range [][]string{
		{"msg::010"},
		{"onboarding:mentor", "--inherit", "a b"},
		{"onboarding:mentor", "--inherit", "onboarding:mentor"}, // self-inherit, before any request too
		{"onboarding:mentor", "--module", "msg"},                // positional + tier flag (@codex on #710)
	} {
		f, _ := testFactory(t)
		root := NewRootCmd(f)
		root.SetArgs(append(append([]string{"spec", "new"}, args...), "-m", "some-memory-name", "--title", "T", "--server", "http://127.0.0.1:1"))
		if got := exitCodeFor(root.Execute()); got != exitcode.Usage {
			t.Errorf("%v: exit %d, want Usage before any request", args, got)
		}
	}
}

// A padded --prefix is accepted, and the TRIMMED prefix is what is queried —
// validating one value and sending another matched nothing (@copilot, @codex
// on #710).
func TestSpecPrefixIsTrimmedBeforeTheQuery(t *testing.T) {
	gql, captured := captureGraphQL(t, map[string]string{
		"FindNodes": `{"data":{"nodes":[` + specNodeList("msg:010:02", `["spec"]`) + `]}}`,
	})
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"spec", "list", "-m", specMem, "--prefix", "  msg:010 ", "--json", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	var vars findNodesVars
	_ = json.Unmarshal(captured["FindNodes"], &vars)
	if vars.Filter.LocPrefix != "msg:010" {
		t.Errorf("queried prefix = %q, want the trimmed msg:010", vars.Filter.LocPrefix)
	}
}

// Node.seq is a GraphQL Int (signed 32-bit): a numeric leaf past that range
// sets no order, rather than failing the create (@codex on #710).
func TestSpecNewAtSeqStaysInGraphQLIntRange(t *testing.T) {
	for _, c := range []struct {
		loc     string
		wantNil bool
	}{
		{"guide:2147483647", false},
		{"guide:2147483648", true},
		{"guide:99999999999999999999", true},
	} {
		t.Run(c.loc, func(t *testing.T) {
			url, captured := newAtServer(t)
			f, _ := testFactory(t)
			root := NewRootCmd(f)
			root.SetArgs([]string{"spec", "new", c.loc, "-m", specMem, "--title", "T", "--server", url})
			if err := root.Execute(); err != nil {
				t.Fatalf("execute: %v", err)
			}
			var in sentSpecInput
			_ = json.Unmarshal(captured["CreateSpecNode"], &in)
			if (in.Input.Seq == nil) != c.wantNil {
				t.Errorf("seq = %v, want nil=%v", in.Input.Seq, c.wantNil)
			}
		})
	}
}

// Every addressing command refuses an invalid loc or prefix before the memory
// is resolved: -m names a memory that needs a lookup and the server is
// unreachable, so a request made first would surface as exit 7, not Usage
// (@copilot, @codex on #710).
func TestSpecAddressesAreValidatedBeforeTheMemoryLookup(t *testing.T) {
	for _, args := range [][]string{
		{"get", "msg::010"},
		{"supersede", "msg::010", "--title", "T", "--yes"},
		{"lint", "msg::010"},
		{"lint", "--prefix", "a b"},
	} {
		f, _ := testFactory(t)
		root := NewRootCmd(f)
		root.SetArgs(append(append([]string{"spec"}, args...), "-m", "some-memory-name", "--server", "http://127.0.0.1:1"))
		if got := exitCodeFor(root.Execute()); got != exitcode.Usage {
			t.Errorf("%v: exit %d, want Usage before any request", args, got)
		}
	}
}

// lint --prefix is trimmed like every other prefix: the padded value used to
// reach the scan, so every real node was rejected and lint reported a false
// NotFound (@codex on #710).
func TestSpecLintPrefixIsTrimmed(t *testing.T) {
	gql, captured := captureGraphQL(t, map[string]string{
		"FindNodes": `{"data":{"nodes":[]}}`,
	})
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"spec", "lint", "--prefix", " msg:010 ", "-m", specMem, "--server", gql.URL})
	err := root.Execute()
	var vars findNodesVars
	_ = json.Unmarshal(captured["FindNodes"], &vars)
	if vars.Filter.LocPrefix != "msg:010" {
		t.Errorf("lint scanned prefix %q, want the trimmed msg:010", vars.Filter.LocPrefix)
	}
	if err == nil || !strings.Contains(err.Error(), `"msg:010"`) {
		t.Errorf("the empty-scope message should name the trimmed prefix: %v", err)
	}
}

// A padded single loc is linted at the trimmed loc, not reported NotFound
// (@codex, @copilot on #710).
func TestSpecLintSingleLocIsTrimmed(t *testing.T) {
	gql, captured := captureGraphQL(t, map[string]string{
		"ResolveUrn": resolveSpecJSON,
		"GetNode":    `{"data":{"node":` + cleanSpecDetail + `}}`,
		"NodeBatch":  specLintRawBodyStub(cleanSpecDetail),
		// lint's vector-index check reads the memory; its data is irrelevant.
		"GetMemory": memGetJSON(`{}`),
		"Memories":  memListJSON,
	})
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"spec", "lint", " msg:010:02 ", "-m", specMem, "--server", gql.URL})
	_ = root.Execute()
	var vars struct {
		Urn string `json:"urn"`
	}
	_ = json.Unmarshal(captured["ResolveUrn"], &vars)
	if !strings.HasSuffix(vars.Urn, "::msg:010:02") {
		t.Errorf("lint resolved %q, want the trimmed loc", vars.Urn)
	}
}

// A prefix is a BRANCH, matched on segment boundaries: the server's locPrefix
// is character-wise, so `onboarding:mentor` also returns `onboarding:mentorship`,
// and a replace would have rewritten that sibling (@codex P1 on #710).
func TestSpecPrefixMatchesWholeSegments(t *testing.T) {
	scan := `{"data":{"nodes":[` + specNodeList("onboarding:mentor", `["spec"]`) + `,` +
		specNodeList("onboarding:mentor:screens", `["spec"]`) + `,` +
		specNodeList("onboarding:mentorship", `["spec"]`) + `]}}`
	t.Run("list", func(t *testing.T) {
		gql, _ := captureGraphQL(t, map[string]string{"FindNodes": scan})
		f, out := testFactory(t)
		root := NewRootCmd(f)
		root.SetArgs([]string{"spec", "list", "-m", specMem, "--prefix", "onboarding:mentor", "--json", "--server", gql.URL})
		if err := root.Execute(); err != nil {
			t.Fatalf("execute: %v", err)
		}
		if strings.Contains(out.String(), "onboarding:mentorship") {
			t.Errorf("a sibling branch was listed:\n%s", out.String())
		}
		if !strings.Contains(out.String(), `"onboarding:mentor:screens"`) {
			t.Errorf("the branch's own descendant is missing:\n%s", out.String())
		}
	})
	t.Run("replace reads only the branch", func(t *testing.T) {
		gql, captured := captureGraphQL(t, map[string]string{
			"FindNodes":            scan,
			"SearchReplaceInNodes": searchReplaceDryJSON,
		})
		f, _ := testFactory(t)
		root := NewRootCmd(f)
		root.SetArgs([]string{"spec", "replace", "a", "b", "-m", specMem, "--prefix", "onboarding:mentor", "--dry-run", "--server", gql.URL})
		_ = root.Execute()
		var vars struct {
			Input struct {
				NodeIds []string `json:"nodeIds"`
			} `json:"input"`
		}
		_ = json.Unmarshal(captured["SearchReplaceInNodes"], &vars)
		ids := vars.Input.NodeIds
		for _, id := range ids {
			if strings.Contains(id, "mentorship") {
				t.Errorf("replace targeted a node outside the branch: %v", ids)
			}
		}
		if len(ids) != 2 {
			t.Errorf("replace targeted %v, want exactly the branch's two specs", ids)
		}
	})
}

// list's --limit/--offset with a --prefix: the window is cut after the
// segment-boundary filter (@copilot on #710).
func TestSpecListPrefixWindowSkipsSiblings(t *testing.T) {
	scan := `{"data":{"nodes":[` + specNodeList("onboarding:mentor-foo", `["spec"]`) + `,` +
		specNodeList("onboarding:mentor:screens", `["spec"]`) + `,` +
		specNodeList("onboarding:mentor:settings", `["spec"]`) + `]}}`
	for _, c := range []struct {
		args []string
		want string
	}{
		{[]string{"--limit", "1"}, "onboarding:mentor:screens"},
		{[]string{"--offset", "1", "--limit", "1"}, "onboarding:mentor:settings"},
	} {
		gql, _ := captureGraphQL(t, map[string]string{"FindNodes": scan})
		f, out := testFactory(t)
		root := NewRootCmd(f)
		root.SetArgs(append([]string{"spec", "list", "-m", specMem, "--prefix", "onboarding:mentor", "--json", "--server", gql.URL}, c.args...))
		if err := root.Execute(); err != nil {
			t.Fatalf("execute: %v", err)
		}
		var got []struct {
			Citation string `json:"citation"`
		}
		_ = json.Unmarshal([]byte(out.String()), &got)
		if len(got) != 1 || got[0].Citation != c.want {
			t.Errorf("%v: got %v, want [%s]", c.args, got, c.want)
		}
	}
}

// A GIVEN --prefix that is blank is refused, never read as "no prefix": a
// script's empty "$PREFIX" must not turn `replace --yes` into a corpus-wide
// write (@codex P1 on #710). Refused before any request, on every command.
func TestSpecBlankPrefixIsRefused(t *testing.T) {
	for _, blank := range []string{" ", "", "\t"} {
		for _, args := range [][]string{
			{"replace", "old", "new", "--yes"},
			{"list"},
			{"get"},
			{"grep", "x"},
			{"check-tools"},
			{"lint"},
		} {
			f, _ := testFactory(t)
			root := NewRootCmd(f)
			root.SetArgs(append(append([]string{"spec"}, args...), "--prefix", blank, "-m", specMem, "--server", "http://127.0.0.1:1"))
			err := root.Execute()
			if got := exitCodeFor(err); got != exitcode.Usage || !strings.Contains(err.Error(), "--prefix is blank") {
				t.Errorf("%v --prefix %q: exit %d, err %v; want Usage refusing the blank prefix", args, blank, got, err)
			}
		}
	}
}

// A GIVEN --to or --inherit that is blank is refused, never read as omitted:
// a script's empty "$TO" would otherwise allocate a numbered replacement and
// retire the old spec (@codex P1 on #710). Refused before any request.
func TestSpecBlankToAndInheritAreRefused(t *testing.T) {
	for _, args := range [][]string{
		{"supersede", "msg:010:02", "--to", "", "--title", "T", "--yes"},
		{"supersede", "msg:010:02", "--to", " ", "--title", "T", "--yes"},
		{"new", "--module", "msg", "--feature", "010", "--inherit", "", "--title", "T"},
		{"new", "onboarding:mentor", "--inherit", " ", "--title", "T"},
	} {
		f, _ := testFactory(t)
		root := NewRootCmd(f)
		root.SetArgs(append(append([]string{"spec"}, args...), "-m", specMem, "--server", "http://127.0.0.1:1"))
		err := root.Execute()
		if got := exitCodeFor(err); got != exitcode.Usage || !strings.Contains(err.Error(), "is blank") {
			t.Errorf("%v: exit %d, err %v; want Usage refusing the blank flag", args, got, err)
		}
	}
}
