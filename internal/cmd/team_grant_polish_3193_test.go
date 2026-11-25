package cmd

import (
	"strings"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/exitcode"
)

// cli#3193 item 1: an empty page with a total is "past the end" only when the
// offset is at or past that total. Below it the server's answer is inconsistent
// (it counted grants and returned none), and the text says that instead.
func TestGrantAppListEmptyPageSaysPastTheEndOnlyWhenItIs3193(t *testing.T) {
	const counted = "returned none on this page"
	for _, tc := range []struct {
		name, items, offset, says, not string
	}{
		{"null items at offset 0", `null`, "0", counted, "past the end"},
		{"no items at offset 0", `[]`, "0", counted, "past the end"},
		{"only null items", `[null,null]`, "0", counted, "past the end"},
		{"one below the total", `[]`, "1", counted, "past the end"},
		{"offset is the total", `[]`, "2", "offset 2 is past the end of 2 grants", counted},
		{"offset over the total", `[]`, "3", "offset 3 is past the end of 2 grants", counted},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, out, _, err := runManage1846(t, map[string]string{"TeamAppGrants": `{"data":{"appGrants":{"total":2,"items":` + tc.items + `}}}`},
				"team", "grant", "app-list", "acme.com:eng", "--offset", tc.offset)
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(out, tc.says) || strings.Contains(out, tc.not) || !strings.Contains(out, "2 grants") ||
				strings.Contains(strings.ToLower(out), "authori") {
				t.Errorf("want %q, not %q, with the total and no word on authorisation: %q", tc.says, tc.not, out)
			}
		})
	}
}

// cli#3193 items 3 and 4: the App an instructed revoke is checked against is
// --app, else the worktree binding's App. It decides whether the claim goes out
// through the worker session.
func TestGrantManageRevokeInstructionApp3193(t *testing.T) {
	const op = "TeamRevokeGrantInstructed"
	resp := map[string]string{
		op:                `{"data":{"revokeGrant":` + managerGrant1846 + `}}`,
		"TeamAppIdentity": `{"data":{"app":null}}`,
	}
	argv := []string{"team", "grant", "revoke", "g1", "--instruction", instrTask1592}
	for _, tc := range []struct {
		name    string
		run     run1592
		extra   []string
		session string
		ops     string
	}{
		{"the binding's App, no lookup", run1592{bound: true}, nil, "s-new", op},
		{"--app wins over the binding", run1592{bound: true}, []string{"--app", otherApp1592}, "", "TeamAppIdentity," + op},
		{"no binding", run1592{}, nil, "", op},
		{"no worktree", run1592{noWorktree: true}, nil, "", op},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tc.run.response = resp
			calls, r := tc.run.exec(t, append(append([]string{}, argv...), tc.extra...)...)
			if r.err != nil {
				t.Fatal(r.err)
			}
			var ops []string
			for _, c := range *calls {
				ops = append(ops, c.Op)
			}
			if got := strings.Join(ops, ","); got != tc.ops {
				t.Fatalf("operations %q, want %q", got, tc.ops)
			}
			if last := (*calls)[len(*calls)-1]; last.Session != tc.session {
				t.Errorf("session %q, want %q", last.Session, tc.session)
			}
		})
	}
}

// cli#3193 item 3: a binding that cannot be read is refused with nothing sent.
// Ignoring the error would send the revoke as the signed-in person instead.
func TestGrantManageRevokeInstructionUnreadableBindingIsRefused3193(t *testing.T) {
	calls, r := run1592{bound: true, corrupt: true, response: map[string]string{
		"TeamRevokeGrantInstructed": `{"data":{"revokeGrant":` + managerGrant1846 + `}}`,
	}}.exec(t, "team", "grant", "revoke", "g1", "--instruction", instrTask1592)
	if r.err == nil || exitCodeFor(r.err) != exitcode.Error || !strings.Contains(r.err.Error(), "is corrupt") {
		t.Errorf("want exit 1 naming the corrupt binding, got exit %d: %v", exitCodeFor(r.err), r.err)
	}
	if len(*calls) != 0 || r.out != "" {
		t.Errorf("nothing may be sent or printed: %+v %q", *calls, r.out)
	}
}
