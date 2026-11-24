package filterreport

import "testing"

// cli#2059: the worker's launcher is the Orca terminal's login shell, matched
// by shape, never by the name of a host script. A shell running a script or a
// command is not one.
func TestWrapperIsTheTerminalLoginShell2059(t *testing.T) {
	for _, tc := range []struct {
		name string
		argv []string
		want bool
	}{
		{"login bash", []string{"-bash"}, true},
		{"plain zsh", []string{"/usr/bin/zsh"}, true},
		{"sh", []string{"sh"}, true},
		{"dash --login", []string{"dash", "--login"}, true},
		{"bash -l", []string{"/bin/bash", "-l"}, true},
		{"a script run by a shell", []string{"bash", "/opt/bin/start"}, false},
		{"a command run by a shell", []string{"bash", "-c", "node /opt/bin/codex"}, false},
		{"login with more arguments", []string{"bash", "-l", "x"}, false},
		{"a different interpreter", []string{"python3"}, false},
		{"the retired wrapper's old shape is just a script now", []string{"bash", "/home/u/.local/bin/old-wrapper"}, false},
		{"empty", nil, false},
	} {
		if got := wrapper(tc.argv); got != tc.want {
			t.Errorf("%s: wrapper(%q) = %v, want %v", tc.name, tc.argv, got, tc.want)
		}
	}
}
