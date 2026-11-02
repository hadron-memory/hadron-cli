package hostpoller

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func runGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t", "GIT_CONFIG_GLOBAL=/dev/null")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

// pushedClone is a clone on a branch with a remote upstream, nothing ahead.
func pushedClone(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	origin, clone := filepath.Join(root, "origin.git"), filepath.Join(root, "clone")
	runGit(t, root, "init", "-q", "--bare", origin)
	runGit(t, root, "clone", "-q", origin, clone)
	runGit(t, clone, "commit", "-q", "--allow-empty", "-m", "c1")
	runGit(t, clone, "push", "-q", "origin", "HEAD:refs/heads/work")
	runGit(t, clone, "branch", "-q", "--set-upstream-to=origin/work")
	return clone
}

func TestGitTreeOfANonGitDirectoryIsNotGit1043(t *testing.T) { // S6
	got, err := GitTree(context.Background(), t.TempDir())
	if err != nil || got.InGit {
		t.Fatalf("got %+v %v", got, err)
	}
}

func TestGitTreeOfAMissingDirectoryIsNotGit1043(t *testing.T) {
	got, err := GitTree(context.Background(), filepath.Join(t.TempDir(), "gone"))
	if err != nil || got.InGit {
		t.Fatalf("got %+v %v", got, err)
	}
}

func TestGitTreeCleanAndPushed1043(t *testing.T) {
	got, err := GitTree(context.Background(), pushedClone(t))
	if err != nil || !got.InGit || got.Dirty || got.Upstream == "" || got.Ahead != 0 {
		t.Fatalf("got %+v %v", got, err)
	}
}

func TestGitTreeDirty1043(t *testing.T) { // S5
	dir := pushedClone(t)
	if err := os.WriteFile(filepath.Join(dir, "scratch"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := GitTree(context.Background(), dir)
	if err != nil || !got.InGit || !got.Dirty {
		t.Fatalf("got %+v %v", got, err)
	}
}

func TestGitTreeAheadAndNoUpstream1043(t *testing.T) {
	dir := pushedClone(t)
	runGit(t, dir, "commit", "-q", "--allow-empty", "-m", "c2")
	got, err := GitTree(context.Background(), dir)
	if err != nil || got.Ahead != 1 {
		t.Fatalf("ahead: got %+v %v", got, err)
	}
	runGit(t, dir, "checkout", "-q", "-b", "local-only")
	got, err = GitTree(context.Background(), dir)
	if err != nil || !got.InGit || got.Upstream != "" {
		t.Fatalf("no upstream: got %+v %v", got, err)
	}
}

func TestGitTreeOfASubdirectoryIsInGit1043(t *testing.T) {
	// The ruling's test is --is-inside-work-tree, not "is the top level":
	// a worker bound in a repo's subdirectory is in a Git tree.
	dir := pushedClone(t)
	sub := filepath.Join(dir, "sub")
	if err := os.Mkdir(sub, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sub, "f"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := GitTree(context.Background(), sub)
	if err != nil || !got.InGit || !got.Dirty {
		t.Fatalf("got %+v %v", got, err)
	}
}

func TestGitTreeWithoutAGitBinaryIsNotGit1043(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	got, err := GitTree(context.Background(), t.TempDir())
	if err != nil || got.InGit {
		t.Fatalf("got %+v %v", got, err)
	}
}
