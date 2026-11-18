//go:build windows

package filterreport

import "os"

// Windows has no production /proc source; this supports regular-file fixtures.
func openRead(path string) (*os.File, error) { return os.Open(path) }
