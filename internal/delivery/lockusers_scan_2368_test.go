package delivery

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// #2368: the call-site scan behind TestLockUsersHintCoversEveryCallSite2327
// must see a caller written on a one-line function declaration. It skipped
// every line starting with "func ", so `func f() { withBindingLock(...) }`
// never registered its file and a new lock user could drift past the hint.
// The scan runs here against a throwaway tree, so each case is exact.
func TestLockCallerScanSeesWhatItShould2368(t *testing.T) {
	root := t.TempDir()
	write := func(rel, body string) {
		t.Helper()
		p := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte("package x\n\n"+body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	// Must be seen.
	write("internal/a/oneline.go", "func f() { withBindingLock(nil) }\n")
	write("internal/a/oneline_method.go", "func (p *P) f() error { return delivery.LockBinding(p.dir) }\n")
	write("internal/a/oneline_params.go", "func f(x struct{}, y interface{}) { updateBinding(x, y) }\n")
	write("internal/a/multiline.go", "func f() {\n\twithBindingLockAt(nil)\n}\n")
	write("internal/a/field.go", "var v = P{\n\tlockBinding: delivery.LockBinding,\n}\n")
	// Must not be seen.
	write("internal/b/decl_only.go", "func withBindingLock(dir string) error { return nil }\nfunc (p *P) lockBinding() {}\n")
	write("internal/b/comment_only.go", "// withBindingLock( is mentioned here\nfunc g() {}\n")
	write("internal/b/call_in_test_name_only_test.go", "func TestX() { withBindingLock(nil) }\n")
	write("internal/delivery/skipped.go", "func f() { withBindingLock(nil) }\n")
	write("internal/api/gen/skipped.go", "func f() { withBindingLock(nil) }\n")

	found, err := scanLockCallers2327(root)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for f := range found {
		got = append(got, f)
	}
	sort.Strings(got)
	want := []string{
		"internal/a/field.go",
		"internal/a/multiline.go",
		"internal/a/oneline.go",
		"internal/a/oneline_method.go",
		"internal/a/oneline_params.go",
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("scan found:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}
