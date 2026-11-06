package cmd

import (
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/exitcode"
)

// Gate6671974496: consent precedes even reading an ended or missing row.
func TestSnapshotRevoke1612NonTTYConsentBeforeLookup(t *testing.T) {
	for _, state := range []string{"live", "ended", "missing"} {
		for _, ambient := range []bool{false, true} {
			t.Run(state+map[bool]string{false: "/explicit", true: "/ambient"}[ambient], func(t *testing.T) {
				row := snapshotGrant1578
				if state == "ended" {
					row = endedSnapshot1578()
				}
				if state == "missing" {
					row = ""
				}
				replies := taskResponses()
				replies["AutomationTeamSnapshotGrants"] = grantPage1578(row, 1, false)
				writeTeamBinding(t)
				srv, calls := attnServer(t, replies)
				setTeamBindingServer(t, srv.URL)
				f, out := testFactory(t)
				args := snapshotArgs1578("revoke", "grant1", "--json")
				if ambient {
					setActiveApp(t, autoApp)
				} else {
					args = append(args, "--app", autoApp)
				}
				root := NewRootCmd(f)
				root.SetArgs(append([]string{"--server", srv.URL}, args...))
				err := root.Execute()
				if exitCodeFor(err) != exitcode.Usage || len(*calls) != 0 || out.String() != "" {
					t.Fatalf("state=%s ambient=%v exit=%d calls=%+v stdout=%q err=%v", state, ambient, exitCodeFor(err), *calls, out, err)
				}
			})
		}
	}
}
