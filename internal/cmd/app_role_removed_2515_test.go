package cmd

import (
	"bytes"
	"strings"
	"testing"
)

// cli#2515, stage 2 of the ruled removal of the App's own role: `--role` is gone
// from `app install` and `app update`. The flag is refused before anything is
// sent, the help no longer lists it, and no request carries a role.
func TestAppRoleFlagRemoved2515(t *testing.T) {
	for _, mode := range []string{"install", "update"} {
		for _, value := range []string{"READER", "OWNER", "boss"} {
			t.Run(mode+"/"+value, func(t *testing.T) {
				srv, calls := appSettingsServer(t)
				_, err := appSettingsRun(t, srv, mode, "--role", value, "--yes")
				if exitCodeFor(err) != 2 || len(*calls) != 0 {
					t.Fatalf("err=%v calls=%+v", err, *calls)
				}
				msg := err.Error()
				if !strings.Contains(msg, "--role") || !strings.Contains(msg, "removed") || strings.Contains(msg, "unknown flag") || strings.Contains(msg, "invalid --role") {
					t.Fatalf("the refusal must say the flag was removed: %q", msg)
				}
			})
		}
	}
}

func TestAppRoleFlagNotInHelp2515(t *testing.T) {
	for _, mode := range []string{"install", "update"} {
		t.Run(mode, func(t *testing.T) {
			f, _ := testFactory(t)
			r := NewRootCmd(f)
			var out bytes.Buffer
			r.SetOut(&out)
			r.SetErr(&out)
			args := []string{"app", mode, "--help"}
			r.SetArgs(args)
			if err := r.Execute(); err != nil {
				t.Fatal(err)
			}
			if strings.Contains(out.String(), "--role") || strings.Contains(out.String(), "own role") {
				t.Fatalf("help still offers the App's own role:\n%s", out.String())
			}
		})
	}
}

func TestAppRequestsNeverCarryARole2515(t *testing.T) {
	for _, mode := range []string{"install", "update"} {
		t.Run(mode, func(t *testing.T) {
			srv, calls := appSettingsServer(t)
			// A settings flag, so the settings document (not the legacy one) is sent.
			if _, err := appSettingsRun(t, srv, mode, "--training-mode=false", "--yes"); err != nil {
				t.Fatal(err)
			}
			c := appSettingsMutation(t, *calls)
			if _, present := c.Vars["role"]; present || strings.Contains(c.Query, "$role") || strings.Contains(c.Query, "role:") || strings.Contains(c.Query, "AppMembershipRole") {
				t.Fatalf("the document or variables still carry a role: vars=%v\n%s", c.Vars, c.Query)
			}
		})
	}
}
