//go:build !windows

package hostfacts

import "os"

// accountsOwnerOf reads a path's owner (cli#3285); a variable so a test can report another user.
var accountsOwnerOf = func(path string, fi os.FileInfo) (uint32, bool) { return 0, false }
