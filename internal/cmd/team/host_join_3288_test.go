package team

import "testing"

// hadron-cli#3288 F10: a stored key is presented only over the session's scheme, and over plain http only to this
// machine.
func TestProbeTransport3288(t *testing.T) {
	for _, c := range []struct {
		entry, session string
		ok             bool
	}{
		{"https://hadron.example", "https://hadron.example", true},
		{"HTTPS://hadron.example", "https://hadron.example", true},
		{"http://hadron.example", "https://hadron.example", false}, // the downgrade
		{"https://hadron.example", "http://hadron.example", false}, // the reverse
		{"http://hadron.example", "http://hadron.example", false},  // plaintext off this machine
		{"http://127.0.0.1:8080", "http://127.0.0.1:8080", true},
		{"http://localhost:8080", "http://localhost:8080", true},
		{"http://[::1]:8080", "http://[::1]:8080", true},
		{"http://127.0.0.1.evil.example", "http://127.0.0.1.evil.example", false},
		{"://bad", "https://hadron.example", false},
	} {
		err := probeTransport(c.entry, c.session)
		if (err == nil) != c.ok {
			t.Errorf("%s with session %s: %v, want ok=%v", c.entry, c.session, err, c.ok)
		}
	}
}
