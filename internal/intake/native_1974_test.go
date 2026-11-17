package intake

import (
	"context"
	"errors"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

// This executes on Windows too, covering the real exec/stdin/stdout/cap path.
func TestNativeAdapter1974(t *testing.T) {
	name := "adapter"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	path := filepath.Join(t.TempDir(), name)
	if out, err := exec.Command("go", "build", "-o", path, "./testdata/adapter").CombinedOutput(); err != nil {
		t.Fatalf("build fixture: %v %s", err, out)
	}
	r := &Runner{Path: path, Timeout: 5 * time.Second}
	if d, err := r.Describe(context.Background()); err != nil || d.Name != "native" {
		t.Fatalf("describe=%+v err=%v", d, err)
	}
	p, err := r.ListUntracked(context.Background(), "", "", 20)
	if err != nil || len(p.Items) != 1 || p.Items[0].Title != "\x1b[2J😀" {
		t.Fatalf("page=%+v err=%v", p, err)
	}
	if err := r.MarkTracked(context.Background(), p.Items[0].ExternalID, Task{ID: "t1"}, Worker{}); err != nil {
		t.Fatal(err)
	}
	if _, err := r.ListUntracked(context.Background(), "", "astral-cap", 20); err != nil {
		t.Fatalf("512 astral code points / 2048 bytes: %v", err)
	}
	var oe *OutputError
	if _, err := r.ListUntracked(context.Background(), "", "invalid-utf8", 20); !errors.As(err, &oe) {
		t.Fatalf("invalid utf8: %v", err)
	}
	if _, err := r.ListUntracked(context.Background(), "", "cap", 20); !errors.As(err, &oe) {
		t.Fatalf("cap: %v", err)
	}
	var re *RefusedError
	if _, err := r.ListUntracked(context.Background(), "", "refuse", 20); !errors.As(err, &re) || re.Detail == "" {
		t.Fatalf("refusal: %v", err)
	}
	r.Timeout = 100 * time.Millisecond
	if _, err := r.ListUntracked(context.Background(), "", "timeout", 20); !errors.As(err, &re) {
		t.Fatalf("timeout: %v", err)
	}
}
