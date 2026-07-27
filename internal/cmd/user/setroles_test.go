package user

import (
	"strings"
	"testing"
)

func TestSameRoleSet(t *testing.T) {
	tests := []struct {
		name string
		a, b []string
		want bool
	}{
		{"identical", []string{"ADMIN"}, []string{"ADMIN"}, true},
		// The server grants from the SET, so order carries no meaning.
		{"same set, different order", []string{"ADMIN", "CONTRIBUTOR"}, []string{"CONTRIBUTOR", "ADMIN"}, true},
		{"different member", []string{"ADMIN"}, []string{"READER"}, false},
		{"subset", []string{"ADMIN", "READER"}, []string{"ADMIN"}, false},
		{"both empty", nil, nil, true},
		{"empty vs one", nil, []string{"ADMIN"}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := sameRoleSet(tt.a, tt.b); got != tt.want {
				t.Errorf("sameRoleSet(%v, %v) = %v, want %v", tt.a, tt.b, got, tt.want)
			}
		})
	}
}

// Mirrors the server's ROLE_ORDER: requireRole compares the caller's HIGHEST
// role against ADMIN, and OWNER (3) outranks ADMIN (2).
func TestGrantsPlatformAdmin(t *testing.T) {
	tests := []struct {
		roles []string
		want  bool
	}{
		{[]string{"ADMIN"}, true},
		{[]string{"OWNER"}, true},
		{[]string{"OWNER", "READER"}, true},
		{[]string{"CONTRIBUTOR"}, false},
		{[]string{"READER", "CONTRIBUTOR"}, false},
		{nil, false},
	}
	for _, tt := range tests {
		if got := grantsPlatformAdmin(tt.roles); got != tt.want {
			t.Errorf("grantsPlatformAdmin(%v) = %v, want %v", tt.roles, got, tt.want)
		}
	}
}

func TestSetRolesPrompt(t *testing.T) {
	const undoable = "could not undo it themselves"

	tests := []struct {
		name        string
		previous    []string
		known       bool
		next        []string
		mustContain []string
		mustNot     []string
	}{
		{
			name:     "names the removed roles",
			previous: []string{"ADMIN", "CONTRIBUTOR"}, known: true, next: []string{"ADMIN"},
			mustContain: []string{"ADMIN, CONTRIBUTOR", "REMOVES CONTRIBUTOR"},
			// ADMIN is retained, so this is not a lockout.
			mustNot: []string{undoable},
		},
		{
			// The P3 case: OWNER is dropped but ADMIN remains, and ADMIN is
			// enough to run this command — so it IS undoable.
			name:     "dropping OWNER while keeping ADMIN is not a lockout",
			previous: []string{"OWNER", "ADMIN"}, known: true, next: []string{"ADMIN"},
			mustContain: []string{"REMOVES OWNER"},
			mustNot:     []string{undoable},
		},
		{
			name:     "demotion out of admin warns",
			previous: []string{"ADMIN"}, known: true, next: []string{"READER"},
			mustContain: []string{"REMOVES ADMIN", undoable},
		},
		{
			// OWNER alone still satisfies requireRole('ADMIN').
			name:     "OWNER-only result is not a lockout",
			previous: []string{"ADMIN"}, known: true, next: []string{"OWNER"},
			mustNot: []string{undoable},
		},
		{
			name:     "promotion warns about nothing",
			previous: []string{"CONTRIBUTOR"}, known: true, next: []string{"ADMIN"},
			mustContain: []string{"CONTRIBUTOR", "ADMIN"},
			mustNot:     []string{"REMOVES ADMIN", undoable},
		},
		{
			// Unknown prior roles must not read as "none" — the account may
			// hold ADMIN or OWNER and the operator would not be told.
			name:     "unknown prior roles say so",
			previous: nil, known: false, next: []string{"READER"},
			mustContain: []string{"could not be read", "ADMIN", "OWNER", undoable},
			mustNot:     []string{"none →", ": none"},
		},
		{
			name:     "unknown prior roles with an admin result is not a lockout",
			previous: nil, known: false, next: []string{"ADMIN"},
			mustContain: []string{"could not be read"},
			mustNot:     []string{undoable},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := setRolesPrompt("usr1 (@alice, alice@acme.com)", tt.previous, tt.known, tt.next)
			// The resolved account is always named — that is what the
			// operator confirms against, not the ref they typed.
			if !strings.Contains(got, "usr1 (@alice, alice@acme.com)") {
				t.Errorf("prompt must name the resolved account: %q", got)
			}
			for _, want := range tt.mustContain {
				if !strings.Contains(got, want) {
					t.Errorf("prompt %q missing %q", got, want)
				}
			}
			for _, bad := range tt.mustNot {
				if strings.Contains(got, bad) {
					t.Errorf("prompt %q should not contain %q", got, bad)
				}
			}
		})
	}
}
