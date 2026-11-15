package hostpoller

// The reset's real fixtures fetch from a local path; production allows only
// https and ssh remotes (hadron-cli#2057 §10). This is the one place that flips
// the test-only seam.
func init() { localRemotesForTests = true }
