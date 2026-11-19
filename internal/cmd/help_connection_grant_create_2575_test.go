package cmd

import (
	"strings"
	"testing"
)

// cli#2575: connection grant create must not send a non-admin owner to the
// org-admin-only lookup as the only way to find the ID; the flag and the text
// agree it is an ID; each scope is explained; the example carries no date that
// goes stale.
func TestConnectionGrantCreateHelpFindsID2575(t *testing.T) {
	help := flatHelp1053S3(t, "connection", "grant", "create")
	for _, want := range []string{
		"hadron connection connect",
		"prints the connection ID",
		"an org admin can look it up",
		"mail.read lets the App read mail",
		"mail.send lets the App send mail",
		"calendar.freebusy shows busy windows only",
		"calendar.read lets the App read events",
		"<expiry-in-the-future>",
	} {
		if !strings.Contains(help, want) {
			t.Errorf("connection grant create help lacks %q", want)
		}
	}
	for _, banned := range []string{
		"2027-01-01",
		"ID or ref of the connection",
		"Find it with 'hadron email connections --org <org>' (org admin; the ID column); you must be",
	} {
		if strings.Contains(help, banned) {
			t.Errorf("connection grant create help still carries %q", banned)
		}
	}
}
