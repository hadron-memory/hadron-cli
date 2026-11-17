//go:build unix

package delivery

import "os"

// These narrow seams let tests model a host running as another uid without
// changing process credentials or the ownership of shared paths.
var bindingEffectiveUID = os.Geteuid
var bindingOwner = os.Lstat
