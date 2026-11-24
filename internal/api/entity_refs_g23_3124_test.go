package api

import (
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"testing"

	"github.com/vektah/gqlparser/v2"
	"github.com/vektah/gqlparser/v2/ast"
	"github.com/vektah/gqlparser/v2/parser"
)

// cli#3124, the entity-refs gaps 2 and 3 companion (hadron-server#5080 and
// #5081: hard renames with no alias, cor:api:140:01, ruling 0274).
//
// Gap 3: 21 organization arguments become orgRef (18 Query arguments named
// orgId, plus the three boot-prompt signing arguments named org).
// Gap 2: input-type fields that name an entity take <entity>Ref, and
// createAiServiceConfig(ownerId:) becomes ownerRef.
//
// No alias and no fallback: a CLI older than this change is refused on the
// affected commands once the server deploys. document_ref_args_test.go already
// turns red on any document that does not validate against the snapshot; this
// file names each rename so a later partial revert is caught by name.
// rootFieldArgs is the S5 pin's helper (entity_refs_s5_2935_test.go).

// g23OrgArgs: root field, the retired argument name, and the operation kind.
var g23OrgArgs = []struct {
	op      ast.Operation
	field   string
	retired string
}{
	{ast.Query, "findNodes", "orgId"},
	{ast.Query, "globalSearch", "orgId"},
	{ast.Query, "edges", "orgId"},
	{ast.Query, "impersonationSessions", "orgId"},
	{ast.Query, "memories", "orgId"},
	{ast.Query, "exchangeConnections", "orgId"},
	{ast.Query, "exchangeConnectionsPage", "orgId"},
	{ast.Query, "slackConnections", "orgId"},
	{ast.Query, "telegramConnections", "orgId"},
	{ast.Query, "mcpServers", "orgId"},
	{ast.Query, "homeAssistantInstances", "orgId"},
	{ast.Query, "resendSenders", "orgId"},
	{ast.Query, "apps", "orgId"},
	{ast.Query, "agents", "orgId"},
	{ast.Query, "scopes", "orgId"},
	{ast.Query, "channels", "orgId"},
	{ast.Query, "registerEntries", "orgId"},
	{ast.Query, "assets", "orgId"},
	{ast.Query, "orgBootPromptSigning", "org"},
	{ast.Query, "orgBootPromptSigningPreview", "org"},
	{ast.Mutation, "setOrgBootPromptSigning", "org"},
	{ast.Mutation, "createAiServiceConfig", "ownerId"},
}

func TestG23DocumentsSendOrgRefNotTheRetiredArguments_3124(t *testing.T) {
	for _, tc := range g23OrgArgs {
		want := "orgRef"
		if tc.field == "createAiServiceConfig" {
			want = "ownerRef"
		}
		uses := rootFieldArgs(t, tc.op, tc.field)
		if len(uses) == 0 {
			t.Errorf("no document selects %s: the pin cannot see it", tc.field)
			continue
		}
		// The organization argument is optional (cor:api:100:01): a document that
		// reads the caller's whole scope names none. So no document may send the
		// retired name, and at least one must send the new one.
		sends := false
		for _, u := range uses {
			if slices.Contains(u.names, want) {
				sends = true
			}
			if slices.Contains(u.names, tc.retired) {
				t.Errorf("%s: %s still sends the retired %s, sends %v", u.where, tc.field, tc.retired, u.names)
			}
		}
		if !sends {
			t.Errorf("no document sends %s(%s:) (hadron-server#5080/#5081)", tc.field, want)
		}
	}
}

// g23InputRenames: the input-type fields the CLI uses, old to new.
var g23InputRenames = []struct{ typ, retired, want string }{
	{"NodeFilter", "memoryIds", "memoryRefs"},
	{"EdgeFilter", "memoryId", "memoryRef"},
	{"EdgeFilter", "sourceNodeId", "sourceRef"},
	{"EdgeFilter", "targetNodeId", "targetRef"},
	{"AppFilter", "agentId", "agentRef"},
	{"AiServiceConfigFilter", "ownerId", "ownerRef"},
	{"CreateNodeInput", "memoryId", "memoryRef"},
	{"ImportNodeInput", "memoryId", "memoryRef"},
	{"BeginAssetUploadV2Input", "memoryId", "memoryRef"},
	{"UpdateNodeInput", "id", "ref"},
	{"UpdateNodeInput", "memoryId", "memoryRef"},
	{"NodeEdgeInput", "targetId", "targetRef"},
	{"SearchReplaceInNodesInput", "nodeIds", "nodeRefs"},
	{"SearchReplaceInNodesInput", "memoryIds", "memoryRefs"},
	{"SpecSearchReplaceInput", "nodeIds", "nodeRefs"},
	{"CreateAgentTaskInput", "parentTaskId", "parentTaskRef"},
	{"CloneAgentTaskInput", "parentTaskId", "parentTaskRef"},
	{"TickerInput", "channelIds", "channelRefs"},
	{"TickerPatch", "channelIds", "channelRefs"},
	{"BatchThrough", "channelId", "channelRef"},
	// NodeAuthoringCheckInput.memoryId is renamed too, but nodeAuthoringCheck is
	// coverage gap #3089: no CLI document sends it, so only the snapshot pins it.
	{"NodeAuthoringCheckInput", "memoryId", "memoryRef"},
}

// The snapshot carries the renamed fields and not the retired ones.
func TestG23SnapshotCarriesOnlyTheRefFields_3124(t *testing.T) {
	sdl, err := os.ReadFile(filepath.Join("..", "..", "schema", "schema.graphql"))
	if err != nil {
		t.Fatal(err)
	}
	schema, gerr := gqlparser.LoadSchema(&ast.Source{Name: "schema.graphql", Input: string(sdl)})
	if gerr != nil {
		t.Fatalf("the snapshot does not load: %v", gerr)
	}
	for _, tc := range g23InputRenames {
		def := schema.Types[tc.typ]
		if def == nil {
			t.Errorf("the snapshot has no %s", tc.typ)
			continue
		}
		if def.Fields.ForName(tc.want) == nil {
			t.Errorf("the snapshot's %s lacks %s", tc.typ, tc.want)
		}
		// CreateNodeInput.id and the other types' own id stay; only a field
		// the server retired is checked.
		if tc.retired != "id" && def.Fields.ForName(tc.retired) != nil {
			t.Errorf("the snapshot's %s still has the retired %s", tc.typ, tc.retired)
		}
	}
	// UpdateNodeInput.id is retired; CreateNodeInput.id (the new node's id) stays.
	if schema.Types["UpdateNodeInput"].Fields.ForName("id") != nil {
		t.Error("the snapshot's UpdateNodeInput still has the retired id")
	}
}

var g23DirectiveFor = regexp.MustCompile(`@genqlient\(for: "(\w+)\.(\w+)"`)

// No document's @genqlient(for:) directive names a retired field, and each
// renamed field the CLI customises is named by its new name: genqlient refuses
// a directive on a field the snapshot no longer has, but a directive dropped in
// a partial revert would silently lose its omitempty/pointer/bind behaviour.
func TestG23DirectivesNameTheRenamedFields_3124(t *testing.T) {
	files, err := filepath.Glob(filepath.Join("queries", "*.graphql"))
	if err != nil || len(files) == 0 {
		t.Fatalf("no query documents found: %v", err)
	}
	seen := map[string]bool{}
	for _, f := range files {
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		for _, m := range g23DirectiveFor.FindAllStringSubmatch(string(b), -1) {
			typ, field := m[1], m[2]
			seen[typ+"."+field] = true
			for _, tc := range g23InputRenames {
				if tc.typ == typ && tc.retired == field {
					t.Errorf("%s: the directive still names the retired %s.%s", f, typ, field)
				}
			}
		}
	}
	// The directives the CLI depends on (the options are asserted by the
	// generated code's own tests: the three-state parentTaskRef binding by
	// TestTaskCloneParentIsThreeState1565).
	for _, name := range []string{
		"NodeFilter.memoryRefs", "EdgeFilter.memoryRef", "EdgeFilter.sourceRef", "EdgeFilter.targetRef",
		"AppFilter.agentRef", "CreateNodeInput.memoryRef", "UpdateNodeInput.ref", "UpdateNodeInput.memoryRef",
		"ImportNodeInput.memoryRef", "CloneAgentTaskInput.parentTaskRef",
		"TickerInput.channelRefs", "TickerPatch.channelRefs",
	} {
		if !seen[name] {
			t.Errorf("no document carries @genqlient(for: %q): a partial revert dropped it", name)
		}
	}
}

// Inline object arguments (not typed variables) carry the new field names.
func TestG23InlineInputObjectsSendTheRefFields_3124(t *testing.T) {
	files, err := filepath.Glob(filepath.Join("queries", "*.graphql"))
	if err != nil || len(files) == 0 {
		t.Fatalf("no query documents found: %v", err)
	}
	inline := map[string]struct {
		want    []string
		retired []string
	}{
		"updateNode":         {[]string{"ref", "memoryRef"}, []string{"id", "memoryId"}},
		"beginAssetUploadV2": {[]string{"memoryRef"}, []string{"memoryId"}},
	}
	checked := map[string]int{}
	for _, f := range files {
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		doc, perr := parser.ParseQuery(&ast.Source{Name: f, Input: string(b)})
		if perr != nil {
			t.Fatalf("%s does not parse: %v", f, perr)
		}
		var walk func(set ast.SelectionSet)
		walk = func(set ast.SelectionSet) {
			for _, sel := range set {
				fld, ok := sel.(*ast.Field)
				if !ok {
					continue
				}
				if spec, hit := inline[fld.Name]; hit {
					for _, arg := range fld.Arguments {
						if arg.Name != "input" || arg.Value == nil || arg.Value.Kind != ast.ObjectValue {
							continue
						}
						var names []string
						for _, c := range arg.Value.Children {
							names = append(names, c.Name)
						}
						checked[fld.Name]++
						for _, w := range spec.want {
							if !slices.Contains(names, w) {
								t.Errorf("%s: %s(input:) must send %s, sends %v", f, fld.Name, w, names)
							}
						}
						for _, r := range spec.retired {
							if slices.Contains(names, r) {
								t.Errorf("%s: %s(input:) still sends the retired %s", f, fld.Name, r)
							}
						}
					}
				}
				walk(fld.SelectionSet)
			}
		}
		for _, op := range doc.Operations {
			walk(op.SelectionSet)
		}
	}
	for name := range inline {
		if checked[name] == 0 {
			t.Errorf("no document passes %s an inline input object: the pin cannot see it", name)
		}
	}
}
