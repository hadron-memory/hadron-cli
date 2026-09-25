package cmd

import (
	"net/http"
	"strings"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/exitcode"
)

// cli#721 — the server's ROLE_GOVERNED refusal exits 2. The envelopes below
// are RoleGovernedError's (hadron-server governedRole.ts) rebuilt from its
// source at origin/main c226af5, not captured live: its own extensions, which
// Apollo passes through unchanged, and its message templates.

// The multi-kind form: the write would leave a node carrying two governed
// kinds, which no door can make. It is the SAME condition the CLI refuses
// itself, exit 2, when it can read the node's kind (api.UpdateNodeByKind).
const roleGovernedMultiJSON = `{"errors":[{"message":"This write edits a RUNNABLE node, and the node is governed as more than one kind (task and spec). Each door is exempt from its own kind only (updateTaskNode, updateSpecNode), so no current route can write it. Authoring or retiring something the platform will execute is not the same act as writing prose; the generic node surface does not reach it.","extensions":{"code":"ROLE_GOVERNED","kind":"task","strength":"capability","door":"updateTaskNode","mcpDoor":null,"op":"update","refusal":"edits","kinds":["task","spec"]}}]}`

// The single-kind form: a generic write that edits a task.
const roleGovernedJSON = `{"errors":[{"message":"This write edits a RUNNABLE node, which only updateTaskNode may update. Authoring or retiring something the platform will execute is not the same act as writing prose; the generic node surface does not reach it. From the CLI, ` +
	"`hadron node update`" + ` routes to it by itself.","extensions":{"code":"ROLE_GOVERNED","kind":"task","strength":"capability","door":"updateTaskNode","mcpDoor":null,"op":"update","refusal":"edits"}}]}`

const kindReadFailedJSON = `{"errors":[{"message":"transient","extensions":{"code":"INTERNAL_SERVER_ERROR"}}]}`

// A spec file onto a stored task. When the CLI can read the node's kind it
// refuses this itself, exit 2, before any write (TestNodeImportTwoGoverned…).
// When the read fails, the write goes out and the SERVER refuses the same
// condition — which exited 1 before cli#721.
func TestServerRoleGovernedRefusalOfTheCLIsOwnConditionExitsTwo(t *testing.T) {
	_, err := runImport(t, map[string]string{
		"ResolveUrn":     resolveNodeJSON,
		"GetNode":        kindReadFailedJSON,
		"UpdateSpecNode": roleGovernedMultiJSON,
	}, importFile(t, "n.md", governedMd("role: spec\n")))
	assertRoleGoverned(t, err, "no current route")
}

// The same class of refusal on the fallback path: the kind read failed, so a
// kind-silent write went to the generic door, which the server refuses because
// the node is a task. Exit 2 like the rest of the class; the message is the
// server's, kept whole.
func TestServerRoleGovernedRefusalOnTheFallbackPathExitsTwo(t *testing.T) {
	t.Run("node import", func(t *testing.T) {
		_, err := runImport(t, map[string]string{
			"ResolveUrn": resolveNodeJSON,
			"GetNode":    kindReadFailedJSON,
			"UpdateNode": roleGovernedJSON,
		}, importFile(t, "n.md", governedMd("")))
		assertRoleGoverned(t, err, "updateTaskNode")
	})
	t.Run("node update", func(t *testing.T) {
		gql, _ := captureGraphQL(t, map[string]string{
			"ResolveUrn": resolveNodeJSON,
			"GetNode":    kindReadFailedJSON,
			"UpdateNode": roleGovernedJSON,
		})
		f, _ := testFactory(t)
		root := NewRootCmd(f)
		root.SetArgs([]string{"node", "update", nodeURN, "--description", "d", "--server", gql.URL})
		assertRoleGoverned(t, root.Execute(), "updateTaskNode")
	})
}

func assertRoleGoverned(t *testing.T, err error, keep string) {
	t.Helper()
	if got := exitCodeFor(err); got != exitcode.Usage {
		t.Fatalf("ROLE_GOVERNED must exit %d, got %d (%v)", exitcode.Usage, got, err)
	}
	if !strings.Contains(err.Error(), keep) {
		t.Errorf("the server's message must survive the mapping (want %q): %v", keep, err)
	}
}

// The no-route case the contract names: a governed node's revision restore.
// No door restores a revision yet (hadron-server#1204), so the server refuses
// every such restore with ROLE_GOVERNED in its 'restore' context — exit 2, and
// the message saying there is no route survives (@copilot on #726).
const roleGovernedRestoreJSON = `{"errors":[{"message":"This write edits a RUNNABLE node, and no door restores a revision: a governed node's history cannot be restored through any surface yet (tracked with deletes in #1204). Authoring or retiring something the platform will execute is not the same act as writing prose; the generic node surface does not reach it.","extensions":{"code":"ROLE_GOVERNED","kind":"task","strength":"capability","door":"updateTaskNode","mcpDoor":null,"op":"update","refusal":"edits"}}]}`

func TestServerRoleGovernedRefusalOfARevisionRestoreExitsTwo(t *testing.T) {
	gql, _ := captureGraphQL(t, map[string]string{"RestoreNodeRevision": roleGovernedRestoreJSON})
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"node", "revision", "restore", "v2", "--server", gql.URL})
	assertRoleGoverned(t, root.Execute(), "no door restores a revision")
}

// `hadron api` shares the mapper, so the raw path exits 2 as well — the
// contract says so (@copilot on #726).
func TestRawAPIRoleGovernedExitsTwo(t *testing.T) {
	gql := graphQLAlways(t, http.StatusOK, roleGovernedJSON)
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"api", "{ __typename }", "--server", gql.URL})
	if got := exitCodeFor(root.Execute()); got != exitcode.Usage {
		t.Errorf("`hadron api` must exit %d on ROLE_GOVERNED, got %d", exitcode.Usage, got)
	}
}
