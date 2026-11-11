//go:build windows

package secretfile

import (
	"os"
	"unsafe"

	"golang.org/x/sys/windows"
)

// createExcl creates path exclusively with an explicit owner-only DACL (#1325),
// passed as SECURITY_ATTRIBUTES on the create itself, so the file is never
// readable by anyone else even for a moment (a chmod-after-create would leave a
// window).
//
// The no-follow guard is unchanged from #1324: CREATE_NEW with
// FILE_FLAG_OPEN_REPARSE_POINT means a symlink or junction at the final
// component is not followed and the create fails as already existing.
// Windows has no O_NOFOLLOW.
//
// Under --force the temp file made here is renamed over the destination;
// MoveFileEx keeps the moved file's own security descriptor, so the replaced
// file's ACL does not carry over (asserted on the windows CI job).
func createExcl(path string) (*os.File, error) {
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		return nil, &os.PathError{Op: "open", Path: path, Err: err}
	}
	sd, err := windows.SecurityDescriptorFromString(ownerOnlySDDL(user.User.Sid.String()))
	if err != nil {
		return nil, &os.PathError{Op: "open", Path: path, Err: err}
	}
	sa := &windows.SecurityAttributes{SecurityDescriptor: sd}
	sa.Length = uint32(unsafe.Sizeof(*sa))
	name, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return nil, &os.PathError{Op: "open", Path: path, Err: err}
	}
	h, err := windows.CreateFile(name, windows.GENERIC_WRITE,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE, sa,
		windows.CREATE_NEW, windows.FILE_ATTRIBUTE_NORMAL|windows.FILE_FLAG_OPEN_REPARSE_POINT, 0)
	if err != nil {
		return nil, &os.PathError{Op: "open", Path: path, Err: err}
	}
	return os.NewFile(uintptr(h), path), nil
}
