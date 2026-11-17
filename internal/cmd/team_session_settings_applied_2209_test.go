package cmd

import (
	"testing"
)

// hadron-cli#2209 (C12): a host-launched agent whose start's run settings were
// NOT applied (the exec path; hadron-cli#2204) binds with
// startSettingsApplied:false, read from HADRON_START_SETTINGS_APPLIED, so the
// server inherits none of the start's args (hadron-server#4306). Only with a
// launch grant: the server refuses false without one.
func bindWithEnv(t *testing.T, grant, applied string, args ...string) map[string]any {
	t.Helper()
	teamGitDir(t)
	t.Setenv("HADRON_LAUNCH_GRANT", grant)
	if applied == "<unset>" {
		t.Setenv("HADRON_START_SETTINGS_APPLIED", "")
	} else {
		t.Setenv("HADRON_START_SETTINGS_APPLIED", applied)
	}
	gql, captured := captureGraphQL(t, launchGrantStubs())
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs(append([]string{"team", "session", "start", "--tool", "claude-code", "--server", gql.URL}, args...))
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	return startInput(t, captured["StartTeamSession"])
}

func TestSessionStartSendsSettingsNotAppliedFromTheLaunchEnv2209(t *testing.T) {
	in := bindWithEnv(t, "hlg_abc", "false")
	if in["startSettingsApplied"] != false {
		t.Fatalf("startSettingsApplied = %#v, want false (input %#v)", in["startSettingsApplied"], in)
	}
	if in["launchGrant"] != "hlg_abc" {
		t.Fatalf("the grant must still ride: %#v", in)
	}
}

// Absent, true, garbage: nothing is sent (absent means applied). Green on main
// by design; it covers an older host and every ordinary bind.
func TestSessionStartOmitsSettingsAppliedUnlessFalse2209(t *testing.T) {
	for _, v := range []string{"<unset>", "true", "TRUE", "0", "no", "False ", "garbage"} {
		in := bindWithEnv(t, "hlg_abc", v)
		if _, sent := in["startSettingsApplied"]; sent {
			t.Errorf("env %q: startSettingsApplied must be omitted, got %#v", v, in["startSettingsApplied"])
		}
	}
}
