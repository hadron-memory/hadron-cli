package cmdutil

import "testing"

func TestInstallRef(t *testing.T) {
	for _, tc := range []struct {
		ref, kind, want string
		invalid         bool
	}{
		{"hrn:app:acme.com:team", "app", "hrn:app:acme.com:team", false},
		{"hrn:app:acme.com::team", "app", "hrn:app:acme.com:team", false},
		{"urn:agent:acme.com::bot", "agent", "urn:agent:acme.com::bot", false},
		{"hrn:agent:acme.com:bot", "agent", "hrn:agent:acme.com:bot", false},
		{"01a1815f9fc375348c31188474140ad5", "app", "01a1815f9fc375348c31188474140ad5", false},
		{"", "app", "", true}, {"team", "app", "", true},
		{"hrn:agent:acme.com:bot", "app", "", true},
		{"hrn:app:acme.com:team", "agent", "", true},
		{"hrn:agent:acme.com:", "agent", "", true},
		{"hrn:app:acme.com:team:extra", "app", "", true},
	} {
		t.Run(tc.ref+tc.kind, func(t *testing.T) {
			got, err := InstallRef(tc.ref, tc.kind)
			if (err != nil) != tc.invalid || got != tc.want {
				t.Fatalf("got %q, %v; want %q invalid=%t", got, err, tc.want, tc.invalid)
			}
		})
	}
}

func TestParseInstallTarget(t *testing.T) {
	target, err := ParseInstallTarget("hrn:app:acme.com:team/hrn:agent:acme.com:bot")
	if err != nil || target.AppRef != "hrn:app:acme.com:team" || target.AgentRef != "hrn:agent:acme.com:bot" {
		t.Fatalf("wrong target: %#v %v", target, err)
	}
	for _, raw := range []string{"", "team/bot", "hrn:app:acme.com:team", "/hrn:agent:acme.com:bot", "hrn:app:acme.com:team/", "hrn:app:acme.com:team/hrn:agent:acme.com:bot/extra", "hrn:agent:acme.com:bot/hrn:app:acme.com:team"} {
		if got, err := ParseInstallTarget(raw); err == nil || got != nil {
			t.Fatalf("accepted %q: %#v %v", raw, got, err)
		}
	}
}
