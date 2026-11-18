package intake

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
)

// buildNativeAdapter compiles the fixture once for the package (two tests
// want it; a second `go build` is pure suite latency).
var (
	nativeAdapterOnce sync.Once
	nativeAdapterPath string
	nativeAdapterDir  string // removed by TestMain: the shared build outlives any one test
	nativeAdapterErr  error
)

// The fixture dir cannot be a t.TempDir: the sync.Once build is shared by the
// package, and the temp dir of the test that happened to build first is reaped
// when that test ends, yanking the binary from under the rest. TestMain owns
// the cleanup instead, so no native-adapter-* dir leaks per `go test` run.
func TestMain(m *testing.M) {
	code := m.Run()
	if nativeAdapterDir != "" {
		_ = os.RemoveAll(nativeAdapterDir)
	}
	os.Exit(code)
}

func buildNativeAdapter(t *testing.T) string {
	t.Helper()
	nativeAdapterOnce.Do(func() {
		name := "adapter"
		if runtime.GOOS == "windows" {
			name += ".exe"
		}
		dir, err := os.MkdirTemp("", "native-adapter-")
		if err != nil {
			nativeAdapterErr = err
			return
		}
		nativeAdapterDir = dir
		path := filepath.Join(dir, name)
		out, err := exec.Command("go", "build", "-o", path, "./testdata/adapter").CombinedOutput()
		if err != nil {
			nativeAdapterErr = fmt.Errorf("build fixture: %v %s", err, out)
			return
		}
		nativeAdapterPath = path
	})
	if nativeAdapterErr != nil {
		t.Fatal(nativeAdapterErr)
	}
	return nativeAdapterPath
}

// This executes on Windows too, covering the real exec/stdin/stdout/cap path.
func TestNativeAdapter1974(t *testing.T) {
	path := buildNativeAdapter(t)
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
// directly to see what it is gets a diagnosis, not a stack trace. The request
// is VALID JSON on stdin on purpose: with an empty stdin the pre-guard fixture
// exits 2 at the JSON decode before it ever touches os.Args[1], which proves
// nothing about the guard — the guard is only reachable past a decodable
// request. The deadline is belt: a regression that blocks reading stdin must
// fail the suite, not hang it.
func TestNativeAdapterBareInvocationExits2WithUsage(t *testing.T) {
	path := buildNativeAdapter(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, path)
	cmd.Stdin = strings.NewReader("{}")
	out, err := cmd.CombinedOutput()
	var ee *exec.ExitError
	if !errors.As(err, &ee) || ee.ExitCode() != 2 {
		t.Fatalf("a bare invocation must exit 2: %v (%s)", err, out)
	}
	if !strings.Contains(string(out), "usage") || !strings.Contains(string(out), "call") {
		t.Fatalf("a bare invocation must print a usage line naming the call argument: %s", out)
	}
	if strings.Contains(string(out), "panic") {
		t.Fatalf("a bare invocation is a refusal, not a panic: %s", out)
	}
}
