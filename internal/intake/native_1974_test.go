package intake

import (
	"context"
	"errors"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
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

// A bare invocation (no call argument) must refuse with a usage line and exit
// 2, not panic on os.Args[1] (#2260 F7): an operator who runs the fixture
// directly to see what it is gets a diagnosis, not a stack trace.
func TestNativeAdapterBareInvocationExits2WithUsage(t *testing.T) {
	name := "adapter"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	path := filepath.Join(t.TempDir(), name)
	if out, err := exec.Command("go", "build", "-o", path, "./testdata/adapter").CombinedOutput(); err != nil {
		t.Fatalf("build fixture: %v %s", err, out)
	}
	var out []byte
	err := func() (err error) {
		out, err = exec.Command(path).CombinedOutput()
		return err
	}()
	if ee, ok := err.(*exec.ExitError); !ok || ee.ExitCode() != 2 {
		t.Fatalf("a bare invocation must exit 2: %v (%s)", err, out)
	}
	if !strings.Contains(string(out), "usage") || !strings.Contains(string(out), "call") {
		t.Fatalf("a bare invocation must print a usage line naming the call argument: %s", out)
	}
	if strings.Contains(string(out), "panic") {
		t.Fatalf("a bare invocation is a refusal, not a panic: %s", out)
	}
}
