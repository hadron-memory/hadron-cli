//go:build !windows

package team

// platformNoHardLinks: the portable codes in noHardLinks are the whole answer here.
func platformNoHardLinks(error) bool { return false }
