package orca

import (
	"context"
	"github.com/hadron-memory/hadron-cli/internal/cmdutil"
	"github.com/hadron-memory/hadron-cli/internal/exitcode"
	"github.com/hadron-memory/hadron-cli/internal/output"
	"testing"
)

func TestHostPlanUsageBeforeAnyRead1729(t *testing.T) {
	for _, name := range []string{"missing-roster", "explicit-app"} {
		t.Run(name, func(t *testing.T) {
			t.Setenv("HOME", t.TempDir())
			t.Setenv("XDG_CONFIG_HOME", t.TempDir())
			streams, out, _ := output.Test()
			f := &cmdutil.Factory{IOStreams: streams, JSON: true}
			reads := 0
			env := hostPlanEnv{
				Server: func() (string, error) { reads++; return "https://target.example", nil },
				App:    func(context.Context, string) (string, error) { reads++; return "app-id", nil },
				Worker: func(context.Context, string, string) (hostWorkerIdentity, error) {
					reads++
					return hostWorkerIdentity{}, nil
				},
				Inventory: func(context.Context, []hostPlanWorker) (hostInventory, error) { reads++; return hostInventory{}, nil },
			}
			c := newCmdHostPlan(f, env)
			c.SilenceErrors = true
			c.SilenceUsage = true
			if name == "explicit-app" {
				f.AppFlag = "foreign-app"
				c.SetArgs([]string{"--roster-file", roster1635(t, hostRepo1635(t), "Iris")})
			} else {
				c.SetArgs(nil)
			}
			err := c.Execute()
			if exitcode.FromError(err) != exitcode.Usage || err.Error() != "name --roster-file; its App is authoritative, so omit --app" || reads != 0 || out.Len() != 0 {
				t.Fatalf("exit%d err=%v reads%d output=%q", exitcode.FromError(err), err, reads, out.String())
			}
		})
	}
}
