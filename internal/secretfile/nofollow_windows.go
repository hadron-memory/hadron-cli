//go:build windows

package secretfile

// noFollow is 0 on windows, which has no O_NOFOLLOW. The guard holds anyway:
// every Open passes O_CREATE|O_EXCL, which syscall.Open maps to CREATE_NEW plus
// FILE_FLAG_OPEN_REPARSE_POINT, so a symlink or junction at the final component
// is not followed and the open fails as already existing (#1324).
const noFollow = 0
