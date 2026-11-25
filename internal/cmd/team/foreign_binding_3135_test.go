package team

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/Khan/genqlient/graphql"
)

// A call marked as the person's passes the guard from a foreign worktree; the
// same call unmarked is refused (cli#3135: the mark is checked first).
func TestForeignBindingAsPersonPasses3135(t *testing.T) {
	dir := t.TempDir()
	t.Setenv(GitDirEnv, dir)
	if err := os.WriteFile(filepath.Join(dir, "hadron-team-session.json"), []byte(`{"sessionId":"s-new","workerName":"Iris"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv(invokerWorkspaceEnv, "id1::/work/gloria")
	t.Setenv(ForeignBindingOptInEnv, "")
	t.Cleanup(SetGitToplevelRunner(func(context.Context) (string, error) { return "/work/tina", nil }))
	req := &graphql.Request{OpName: "A", Query: `mutation A { startAgentTask(id: "t") { id } }`}
	guard := ForeignBindingGuard()
	if err := guard(context.Background(), req); err == nil {
		t.Fatal("control: the unmarked call must be refused")
	}
	if err := guard(withAsPerson(context.Background()), req); err != nil {
		t.Fatalf("as the person: %v", err)
	}
}
