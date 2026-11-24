package api

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/vektah/gqlparser/v2"
	"github.com/vektah/gqlparser/v2/ast"

	"github.com/hadron-memory/hadron-cli/internal/api/gen"
)

// cli#3098, the companion of hadron-server#4368 / #5046: SetAgentAssetUploadEnabledInput.agentUrn is renamed to
// agentRef, a hard rename with no alias (ruling 0274; cor:api:140:01 reserves <entity>Id and the bare URN names
// for genuine keys and spells a both-accepting reference <entity>Ref). The operation takes the whole input
// object as a variable, so the rename reaches the CLI through the snapshot and the generated input type only.

func TestSnapshotAgentAssetUploadInputCarriesAgentRef_3098(t *testing.T) {
	sdl, err := os.ReadFile(filepath.Join("..", "..", "schema", "schema.graphql"))
	if err != nil {
		t.Fatal(err)
	}
	schema, gerr := gqlparser.LoadSchema(&ast.Source{Name: "schema.graphql", Input: string(sdl)})
	if gerr != nil {
		t.Fatalf("the snapshot does not load: %v", gerr)
	}
	in := schema.Types["SetAgentAssetUploadEnabledInput"]
	if in == nil {
		t.Fatal("the snapshot has no SetAgentAssetUploadEnabledInput")
	}
	ref := in.Fields.ForName("agentRef")
	if ref == nil {
		t.Fatal("the snapshot's SetAgentAssetUploadEnabledInput lacks agentRef")
	}
	if got := ref.Type.String(); got != "String!" {
		t.Errorf("agentRef is %s, want String! (the same required string the retired agentUrn was)", got)
	}
	if in.Fields.ForName("agentUrn") != nil {
		t.Error("the snapshot's SetAgentAssetUploadEnabledInput still has the retired agentUrn")
	}
}

// The generated Go input type follows the snapshot: AgentRef, no AgentUrn. Reflection keeps this a test failure
// (not a compile error) on a tree that still has the old field.
func TestGeneratedAgentAssetUploadInputCarriesAgentRef_3098(t *testing.T) {
	typ := reflect.TypeOf(gen.SetAgentAssetUploadEnabledInput{})
	if _, ok := typ.FieldByName("AgentRef"); !ok {
		t.Error("gen.SetAgentAssetUploadEnabledInput has no AgentRef")
	}
	if _, ok := typ.FieldByName("AgentUrn"); ok {
		t.Error("gen.SetAgentAssetUploadEnabledInput still has the retired AgentUrn")
	}
}
