//go:build unix

package delivery

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/sys/unix"
)

// hadron-cli#2433: the open-time branch of lockOpenHint. An open refused by
// O_NOFOLLOW (ELOOP, or ENOTDIR for a file standing where the directory
// should be) keeps the "(a link?)" hint; an EACCES open does not. The Lstat
// refusal in bindinglock.go is pinned elsewhere; nothing pinned this branch.
func TestLockOpenHintKeepsTheLinkHintForNoFollowRefusals2433(t *testing.T) {
	for name, tc := range map[string]struct {
		err      error
		wantLink bool
	}{
		"ELOOP":            {unix.ELOOP, true},
		"ENOTDIR":          {unix.ENOTDIR, true},
		"wrapped ELOOP":    {fmt.Errorf("open: %w", unix.ELOOP), true},
		"EACCES (control)": {unix.EACCES, false},
		"wrapped EACCES":   {fmt.Errorf("open: %w", unix.EACCES), false},
	} {
		t.Run(name, func(t *testing.T) {
			got := lockOpenHint(tc.err)
			if has := strings.Contains(got, "(a link?)"); has != tc.wantLink {
				t.Fatalf("lockOpenHint(%v) = %q; link hint present=%v, want %v", tc.err, got, has, tc.wantLink)
			}
		})
	}
}

// The same branch through the real open: a link standing where the binding
// directory should be is refused by O_NOFOLLOW and the error says so.
func TestOpenLockFileRefusedByNoFollowSaysLink2433(t *testing.T) {
	d := t.TempDir()
	target := filepath.Join(d, "real")
	if err := os.Mkdir(target, 0o700); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(d, "link")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	_, err := openLockFile(link, "lock", nil)
	if err == nil {
		t.Fatal("a linked binding location was opened")
	}
	if !strings.Contains(err.Error(), "(a link?)") {
		t.Fatalf("an O_NOFOLLOW refusal lost its link hint: %v", err)
	}
}
