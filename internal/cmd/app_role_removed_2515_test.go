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
				// The release note quotes this prefix.
				if !strings.HasPrefix(msg, "--role was retired: the App's own role setting was removed") {
					t.Fatalf("the refusal no longer matches the release note: %q", msg)
				}
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

// The refusal comes before the confirmation prompt and before any request, so a
// script that forgot --yes is told the flag is gone, not asked to confirm.
func TestAppRoleFlagRefusedBeforeConfirmation2515(t *testing.T) {
	for _, mode := range []string{"install", "update"} {
		t.Run(mode, func(t *testing.T) {
			srv, calls := appSettingsServer(t)
			out, err := appSettingsRun(t, srv, mode, "--role", "READER")
			if exitCodeFor(err) != 2 || len(*calls) != 0 || strings.Contains(out, "?") {
				t.Fatalf("err=%v calls=%+v out=%q", err, *calls, out)
			}
		})
	}
}

// The policy variants of the documents are role-free too.
func TestAppPolicyRequestsNeverCarryARole2515(t *testing.T) {
	for _, mode := range []string{"install", "update"} {
		t.Run(mode, func(t *testing.T) {
			srv, calls := appSettingsServer(t)
			if _, err := appSettingsRun(t, srv, mode, "--training-mode=false", "--policy-file", policyFile1640(t, `{"allow":[]}`), "--yes"); err != nil {
				t.Fatal(err)
			}
			c := appSettingsMutation(t, *calls)
			if !strings.Contains(c.Query, "policy") {
				t.Fatalf("this must exercise the policy document: %s", c.Query)
			}
			if _, present := c.Vars["role"]; present || strings.Contains(c.Query, "$role") || strings.Contains(c.Query, "role:") || strings.Contains(c.Query, "AppMembershipRole") {
				t.Fatalf("the policy document or variables still carry a role: vars=%v\n%s", c.Vars, c.Query)
			}
		})
	}
}
