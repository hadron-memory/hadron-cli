package secretfile

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// #1430 item 4: `app key create` says "no file was kept" whenever Abort returns
// nil, so a nil Abort must mean the file Open created is gone — in both modes,
// when called twice, and when something else already removed it. A removal that
// fails must be a *LeftBehindError naming the file, never nil.
func TestAbortNilMeansTheFileIsGone(t *testing.T) {
	for _, force := range []bool{false, true} {
		name := "plain"
		if force {
			name = "force"
		}
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			final := filepath.Join(dir, "k")
			p, err := Open(final, force)
			if err != nil {
				t.Fatal(err)
			}
			if err := p.Abort(); err != nil {
				t.Fatalf("Abort: %v", err)
			}
			ents, err := os.ReadDir(dir)
			if err != nil {
				t.Fatal(err)
			}
			if len(ents) != 0 {
				t.Errorf("nil Abort left files behind: %v", ents)
			}
			if err := p.Abort(); err != nil {
				t.Errorf("second Abort must stay nil: %v", err)
			}
		})
	}
}

func TestAbortAfterExternalRemovalIsNil(t *testing.T) {
	dir := t.TempDir()
	p, err := Open(filepath.Join(dir, "k"), false)
	if err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS == "windows" {
		// Windows cannot delete a file that is open without FILE_SHARE_DELETE,
		// which Go's opens (and ours, #1325) do not set. Close it first; the
		// property under test, Abort on an already-absent file, is unchanged.
		_ = p.f.Close()
		p.f = nil
	}
	if err := os.Remove(filepath.Join(dir, "k")); err != nil {
		t.Fatal(err)
	}
	if err := p.Abort(); err != nil {
		t.Errorf("an already-absent file is gone, Abort must be nil: %v", err)
	}
}

func TestAbortRemovalFailureIsLeftBehindNotNil(t *testing.T) {
	old := Remove
	Remove = func(string) error { return errors.New("injected remove failure") }
	defer func() { Remove = old }()
	dir := t.TempDir()
	p, err := Open(filepath.Join(dir, "k"), false)
	if err != nil {
		t.Fatal(err)
	}
	err = p.Abort()
	var lb *LeftBehindError
	if !errors.As(err, &lb) {
		t.Fatalf("a failed removal must be a *LeftBehindError, got %v", err)
	}
	if lb.Path == "" {
		t.Error("LeftBehindError must name the file")
	}
}
