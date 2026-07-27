package serverinfo

import "testing"

// The mismatch warning must fire only on a real disagreement. api.Endpoint
// already trims a trailing slash when building the request URL, so warning on
// that spelling would cry proxy-misconfiguration at a healthy server — and a
// warning that fires on healthy setups stops being read (review on #302).
func TestSameServer(t *testing.T) {
	same := [][2]string{
		{"https://srv.example", "https://srv.example"},
		{"https://srv.example/", "https://srv.example"},
		{"https://srv.example", "https://srv.example///"},
		{"HTTPS://SRV.EXAMPLE", "https://srv.example"},
		{"https://srv.example:443", "https://srv.example"},
		{"http://localhost:80", "http://localhost"},
		{"https://srv.example/api/", "https://srv.example/api"},
	}
	for _, tt := range same {
		if !sameServer(tt[0], tt[1]) {
			t.Errorf("sameServer(%q, %q) = false, want true", tt[0], tt[1])
		}
	}

	differ := [][2]string{
		// The cases the warning exists for.
		{"https://srv.example", "http://srv.example"},
		{"https://srv.example", "http://internal:4000"},
		{"https://srv.example", "https://other.example"},
		{"https://srv.example", "https://srv.example/api"},
		// A non-default explicit port is a real difference.
		{"https://srv.example:8443", "https://srv.example"},
	}
	for _, tt := range differ {
		if sameServer(tt[0], tt[1]) {
			t.Errorf("sameServer(%q, %q) = true, want false", tt[0], tt[1])
		}
	}
}

// An unparseable value must not be silently declared equal to anything.
func TestSameServerUnparseable(t *testing.T) {
	if sameServer("://nonsense", "https://srv.example") {
		t.Error("an unparseable URL must not compare equal to a real one")
	}
	if !sameServer("://nonsense", "://nonsense") {
		t.Error("identical unparseable values should still compare equal")
	}
}
