package repoguard

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// retiredTools are host scripts the Team on Orca work removed (cli#2059, C4):
// Orca launches and the host poller relaunches, so nothing in this repo may
// name the old wrapper or the auto-shed hook. docs/plans/ keeps its design
// history and is the one exclusion; this file names them to search for them.
var retiredTools = []string{"worker-supervise", "worker-auto-shed"}

func repoRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for d := dir; ; d = filepath.Dir(d) {
		if _, err := os.Stat(filepath.Join(d, "go.mod")); err == nil {
			return d
		}
		if d == filepath.Dir(d) {
			t.Fatalf("no go.mod above %s", dir)
		}
	}
}

func TestNothingNamesTheRetiredHostTools2059(t *testing.T) {
	root := repoRoot(t)
	self := filepath.Join("internal", "repoguard", "retired_tools_2059_test.go")
	var hits []string
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(root, path)
		if d.IsDir() {
			switch rel {
			case ".git", "bin", "dist", filepath.Join("docs", "plans"):
				return filepath.SkipDir
			}
			return nil
		}
		if rel == self || !d.Type().IsRegular() {
			return nil
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		for i, line := range strings.Split(string(b), "\n") {
			for _, name := range retiredTools {
				if strings.Contains(line, name) {
					hits = append(hits, fmt.Sprintf("%s:%d: %s", rel, i+1, name))
				}
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) > 0 {
		t.Errorf("%d reference(s) to a retired host tool outside docs/plans/ (cli#2059):\n%s", len(hits), strings.Join(hits, "\n"))
	}
}
