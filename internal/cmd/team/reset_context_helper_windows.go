//go:build !unix

package team

import "os"

// A host-configured exit helper is a unix mechanism (mode and owner checks): nothing is configured on Windows.
func unsafeFile(fi os.FileInfo) string { return "is not supported on this platform" }

func openNoFollow(path string) (*os.File, error) { return os.Open(path) }
func unsafeAncestors(path string) string         { return "is not supported on this platform" }
