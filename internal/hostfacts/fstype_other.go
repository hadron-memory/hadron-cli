//go:build !linux

package hostfacts

// defaultFSType: the filesystem kind is not detected off Linux (macOS fixtures are pending, hadron-server#5210
// U5), so the answer is unknown and the state directory counts as on disk.
func defaultFSType(string) (string, error) { return "", nil }
