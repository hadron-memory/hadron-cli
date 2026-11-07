//go:build windows

package secretfile

import (
	"os"
	"path/filepath"
	"sort"
	"testing"
	"unsafe"

	"golang.org/x/sys/windows"
)

// aclEntry is one ACE of a file's DACL, read back through the Windows API
// (never icacls, whose output is localized).
type aclEntry struct {
	sid       string
	aceType   byte
	inherited bool
}

// readDACL returns the file's DACL entries and whether the DACL is protected
// (SE_DACL_PROTECTED: the parent's ACEs are not applied to it).
func readDACL(t *testing.T, path string) (entries []aclEntry, protected bool) {
	t.Helper()
	sd, err := windows.GetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION)
	if err != nil {
		t.Fatalf("GetNamedSecurityInfo(%s): %v", path, err)
	}
	ctrl, _, err := sd.Control()
	if err != nil {
		t.Fatalf("Control: %v", err)
	}
	protected = ctrl&windows.SE_DACL_PROTECTED != 0
	acl, _, err := sd.DACL()
	if err != nil {
		t.Fatalf("DACL: %v", err)
	}
	if acl == nil {
		t.Fatalf("%s has a NULL DACL, which grants everyone full access", path)
	}
	for i := uint32(0); i < uint32(acl.AceCount); i++ {
		var ace *windows.ACCESS_ALLOWED_ACE
		if err := windows.GetAce(acl, i, &ace); err != nil {
			t.Fatalf("GetAce(%d): %v", i, err)
		}
		sid := (*windows.SID)(unsafe.Pointer(&ace.SidStart))
		entries = append(entries, aclEntry{
			sid:       sid.String(),
			aceType:   ace.Header.AceType,
			inherited: ace.Header.AceFlags&windows.INHERITED_ACE != 0,
		})
	}
	return entries, protected
}

func currentUserSID(t *testing.T) string {
	t.Helper()
	u, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		t.Fatalf("GetTokenUser: %v", err)
	}
	return u.User.Sid.String()
}

func systemSID(t *testing.T) string {
	t.Helper()
	sid, err := windows.CreateWellKnownSid(windows.WinLocalSystemSid)
	if err != nil {
		t.Fatalf("CreateWellKnownSid(SYSTEM): %v", err)
	}
	return sid.String()
}

// requireOwnerOnly asserts the #1325 contract: a protected DACL whose only
// entries are ALLOW ACEs for the current user and SYSTEM, none inherited.
func requireOwnerOnly(t *testing.T, path string) {
	t.Helper()
	entries, protected := readDACL(t, path)
	if !protected {
		t.Errorf("%s: DACL is not protected, so the directory's ACEs still apply", path)
	}
	var got []string
	for _, e := range entries {
		if e.aceType != windows.ACCESS_ALLOWED_ACE_TYPE {
			t.Errorf("%s: unexpected ACE type %d for %s", path, e.aceType, e.sid)
		}
		if e.inherited {
			t.Errorf("%s: ACE for %s is inherited", path, e.sid)
		}
		got = append(got, e.sid)
	}
	want := []string{currentUserSID(t), systemSID(t)}
	sort.Strings(got)
	sort.Strings(want)
	if len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Errorf("%s: DACL principals = %v, want exactly the current user and SYSTEM %v (Administrators and Users must not be granted)", path, got, want)
	}
}

// TestOpenCreatesOwnerOnlyDACL pins a fresh key file: current user + SYSTEM,
// protected, nothing inherited (#1325).
func TestOpenCreatesOwnerOnlyDACL(t *testing.T) {
	path := filepath.Join(t.TempDir(), "key")
	p, err := Open(path, false)
	if err != nil {
		t.Fatal(err)
	}
	// The DACL is already right while the file is still empty: it is set at
	// create, not after the secret is written.
	requireOwnerOnly(t, path)
	if err := p.Commit([]byte("secret")); err != nil {
		t.Fatal(err)
	}
	requireOwnerOnly(t, path)
	if b, err := os.ReadFile(path); err != nil || string(b) != "secret" {
		t.Fatalf("content = %q, %v", b, err)
	}
}

// TestForceKeepsOwnerOnlyDACL pins (c): --force writes a temp and renames it
// over an existing destination that has the ordinary inherited ACL; the result
// carries the temp's owner-only DACL, not the replaced file's.
func TestForceKeepsOwnerOnlyDACL(t *testing.T) {
	path := filepath.Join(t.TempDir(), "key")
	if err := os.WriteFile(path, []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}
	if entries, _ := readDACL(t, path); len(entries) < 2 {
		t.Logf("precondition: the pre-existing file has only %d ACEs", len(entries))
	}
	p, err := Open(path, true)
	if err != nil {
		t.Fatal(err)
	}
	if err := p.Commit([]byte("new")); err != nil {
		t.Fatal(err)
	}
	requireOwnerOnly(t, path)
	if b, err := os.ReadFile(path); err != nil || string(b) != "new" {
		t.Fatalf("content = %q, %v", b, err)
	}
}
