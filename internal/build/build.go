// Package build holds version metadata stamped at build time via
// -ldflags (see Makefile and .goreleaser.yaml).
package build

var (
	Version = "dev"
	Commit  = "none"
	Date    = "unknown"
)
