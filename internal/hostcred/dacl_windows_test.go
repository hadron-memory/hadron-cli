//go:build windows

package hostcred

import (
	"path/filepath"
	"sort"
	"testing"
	"unsafe"

	"golang.org/x/sys/windows"
)

// hadron-cli#3239 (ruling RC5): the store is built on Windows too, where the Go
// mode means nothing and the mode checks are skipped. What protects the key is
// the file's owner-only DACL, set by internal/secretfile when Put writes it.
// This reads it back through the Windows API (never icacls, whose output is
// localized), as internal/secretfile's own DACL test does, so a change that
// writes the store some other way fails here.
func TestPutWritesAnOwnerOnlyDACL(t *testing.T) {
	dir := tempConfig(t)
	s, err := Lock()
	if err != nil {
		t.Fatal(err)
	}
	defer s.Unlock()
	if err := s.Put(entry("a1")); err != nil {
		t.Fatal(err)
	}
	requireOwnerOnly(t, filepath.Join(dir, FileName))
	// A second write replaces the file through a temp and a rename: the
	// result must carry the owner-only DACL too.
	if err := s.Put(entry("a2")); err != nil {
		t.Fatal(err)
	}
	requireOwnerOnly(t, filepath.Join(dir, FileName))
}

func requireOwnerOnly(t *testing.T, path string) {
	t.Helper()
	sd, err := windows.GetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION)
	if err != nil {
		t.Fatalf("GetNamedSecurityInfo(%s): %v", path, err)
	}
	ctrl, _, err := sd.Control()
	if err != nil {
		t.Fatal(err)
	}
	if ctrl&windows.SE_DACL_PROTECTED == 0 {
		t.Errorf("%s: DACL is not protected, so the directory's ACEs still apply", path)
	}
	acl, _, err := sd.DACL()
	if err != nil {
		t.Fatal(err)
	}
	if acl == nil {
		t.Fatalf("%s has a NULL DACL, which grants everyone full access", path)
	}
	var got []string
	for i := uint32(0); i < uint32(acl.AceCount); i++ {
		var ace *windows.ACCESS_ALLOWED_ACE
		if err := windows.GetAce(acl, i, &ace); err != nil {
			t.Fatalf("GetAce(%d): %v", i, err)
		}
		if ace.Header.AceType != windows.ACCESS_ALLOWED_ACE_TYPE {
			t.Errorf("%s: unexpected ACE type %d", path, ace.Header.AceType)
		}
		if ace.Header.AceFlags&windows.INHERITED_ACE != 0 {
			t.Errorf("%s: an ACE is inherited", path)
		}
		got = append(got, (*windows.SID)(unsafe.Pointer(&ace.SidStart)).String())
	}
	u, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		t.Fatal(err)
	}
	system, err := windows.CreateWellKnownSid(windows.WinLocalSystemSid)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{u.User.Sid.String(), system.String()}
	sort.Strings(got)
	sort.Strings(want)
	if len(got) != 2 || got[0] != want[0] || got[1] != want[1] {
		t.Errorf("%s: DACL principals = %v, want exactly the current user and SYSTEM %v", path, got, want)
	}
}
