package configcmd

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/cmdutil"
	"github.com/hadron-memory/hadron-cli/internal/output"
)

// runConfig executes one `hadron config ...` invocation against a fresh command
// tree and returns stdout. config never talks to the server, so the factory
// needs only isolated streams and an isolated config dir.
func runConfigCmd(t *testing.T, jsonMode bool, args ...string) string {
	t.Helper()
	io, _, _ := output.Test()
	out := &strings.Builder{}
	io.Out = output.Tracked(out)
	f := cmdutil.NewFactory()
	f.IOStreams = io
	f.JSON = jsonMode
	cmd := NewCmdConfig(f)
	cmd.SetArgs(args)
	cmd.SilenceUsage = true
	cmd.SilenceErrors = true
	if err := cmd.Execute(); err != nil {
		t.Fatalf("config %s: %v", strings.Join(args, " "), err)
	}
	return out.String()
}

// #2500 pin: after `config set skills.installPrompt false`, `config get`
// prints the effective boolean and `config list` shows the row with that
// value — a string in --json, never a JSON boolean. An unset key reads back
// its default (true), which is the value Get derives, not one stored in the
// file. Together these pin the get/list surface users silence the offer
// through; SkillsInstallPrompt() itself is pinned in internal/config.
func TestConfigGetListSkillsInstallPrompt(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	if got := runConfigCmd(t, false, "get", "skills.installPrompt"); got != "true\n" {
		t.Fatalf("get before any set = %q, want the default true\n", got)
	}
	oneJSONString(t, "get", runConfigCmd(t, true, "get", "skills.installPrompt"), "true")

	runConfigCmd(t, false, "set", "skills.installPrompt", "false")

	if got := runConfigCmd(t, false, "get", "skills.installPrompt"); got != "false\n" {
		t.Fatalf("get after set false = %q, want false\n", got)
	}
	oneJSONString(t, "get", runConfigCmd(t, true, "get", "skills.installPrompt"), "false")

	list := runConfigCmd(t, false, "list")
	if !strings.Contains(list, "skills.installPrompt") || !strings.Contains(list, "false") {
		t.Fatalf("config list must show the skills.installPrompt row with false:\n%s", list)
	}
	var all map[string]string
	if err := json.Unmarshal([]byte(runConfigCmd(t, true, "list")), &all); err != nil {
		t.Fatalf("config list --json must be an object of string values: %v\n", err)
	}
	if all["skills.installPrompt"] != "false" {
		t.Fatalf("list --json skills.installPrompt = %q, want the string \"false\"\n", all["skills.installPrompt"])
	}
}

// oneJSONString asserts a `config get --json` body is exactly one object
// mapping the skills.installPrompt key to the given STRING. Unmarshalling into
// map[string]string fails on a JSON boolean, so the pin holds even though the
// renderer may indent.
func oneJSONString(t *testing.T, cmd, raw, want string) {
	t.Helper()
	var got map[string]string
	if err := json.Unmarshal([]byte(raw), &got); err != nil {
		t.Fatalf("config %s --json = %q, want one object of string values: %v", cmd, raw, err)
	}
	if len(got) != 1 || got["skills.installPrompt"] != want {
		t.Fatalf("config %s --json = %q, want skills.installPrompt = %q as a string", cmd, raw, want)
	}
}
