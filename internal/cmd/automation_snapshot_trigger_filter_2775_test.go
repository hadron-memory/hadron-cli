package cmd

import (
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/api/gen"
	"github.com/hadron-memory/hadron-cli/internal/cmd/team"
	"github.com/hadron-memory/hadron-cli/internal/exitcode"
)

// cli#2775: `automation team-snapshot-grant list` takes --trigger-kind and
// --trigger-id and passes each through unchanged (the server validates
// triggerKind; the CLI pre-checks nothing), once per request, only when set.
// Without either flag the request is the legacy document, byte for byte.

func runSnapshotList2775(t *testing.T, instructed bool, reply func(snapshotCall1614) string, extra ...string) (*[]snapshotCall1614, string, error) {
	t.Helper()
	teamGitDir(t)
	srv, calls := snapshotServer1614(t, reply)
	writeTeamBindingIn(t, os.Getenv(team.GitDirEnv), srv.URL)
	f, out := testFactory(t)
	root := NewRootCmd(f)
	args := append(snapshotArgs1578("list", autoApp, "--json", "--server", srv.URL), extra...)
	if instructed {
		args = append(args, "--instruction", "task:t1")
	}
	root.SetArgs(args)
	err := root.Execute()
	return calls, out.String(), err
}

func snapshotPage2775(c snapshotCall1614) string {
	row, more := snapshotGrant1578, true
	if c.Variables["offset"] == float64(1) {
		row = strings.ReplaceAll(row, "grant1", "grant2")
		more = false
	}
	return fmt.Sprintf(`{"data":{"automationTeamSnapshotGrants":{"items":[%s],"total":2,"hasMore":%t}}}`, row, more)
}

func TestSnapshotListTriggerFiltersPassThrough2775(t *testing.T) {
	cases := map[string]struct {
		args []string
		want map[string]string
	}{
		"kind only":       {[]string{"--trigger-kind", "schedule "}, map[string]string{"triggerKind": "schedule "}},
		"id only":         {[]string{"--trigger-id", "hook:1 "}, map[string]string{"triggerId": "hook:1 "}},
		"both":            {[]string{"--trigger-kind", "WEBHOOK", "--trigger-id", "hook1"}, map[string]string{"triggerKind": "WEBHOOK", "triggerId": "hook1"}},
		"empty kind sent": {[]string{"--trigger-kind", ""}, map[string]string{"triggerKind": ""}},
	}
	for name, tc := range cases {
		for _, instructed := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/instructed=%v", name, instructed), func(t *testing.T) {
				calls, out, err := runSnapshotList2775(t, instructed, snapshotPage2775, tc.args...)
				if err != nil {
					t.Fatal(err)
				}
				if len(*calls) != 2 || !strings.Contains(out, "grant2") {
					t.Fatalf("want the whole two-page walk: %d calls, out %s", len(*calls), out)
				}
				for _, c := range *calls {
					for _, k := range []string{"triggerKind", "triggerId"} {
						got, present := c.Variables[k]
						want, set := tc.want[k]
						if present != set || (set && got != want) {
							t.Errorf("%s: %s present=%v value=%#v, want set=%v %q", c.OperationName, k, present, got, set, want)
						}
						if !strings.Contains(c.Query, k+": $"+k) {
							t.Errorf("%s: %s not wired to the field: %s", c.OperationName, k, c.Query)
						}
					}
					claim, hasInstruction := c.Variables["instruction"].(map[string]any)
					if hasInstruction != instructed || strings.Contains(c.Query, "instruction: $instruction") != instructed {
						t.Errorf("%s: instruction carried=%v, want %v", c.OperationName, hasInstruction, instructed)
					}
					if instructed && (claim["kind"] != "TASK" || claim["ref"] != "t1" || c.Session != "s-new") {
						t.Errorf("%s: instruction or session lost: %v session %q", c.OperationName, claim, c.Session)
					}
					if !instructed && c.Session != "" {
						t.Errorf("%s: person list sent a worker session %q", c.OperationName, c.Session)
					}
				}
			})
		}
	}
}

func TestSnapshotListWithoutTriggerFiltersIsTheLegacyRequest2775(t *testing.T) {
	for _, instructed := range []bool{false, true} {
		t.Run(fmt.Sprint(instructed), func(t *testing.T) {
			op, doc := "AutomationTeamSnapshotGrants", gen.AutomationTeamSnapshotGrants_Operation
			if instructed {
				op, doc = op+"Instructed", gen.AutomationTeamSnapshotGrantsInstructed_Operation
			}
			calls, _, err := runSnapshotList2775(t, instructed, snapshotPage2775)
			if err != nil {
				t.Fatal(err)
			}
			for _, c := range *calls {
				if c.OperationName != op || c.Query != doc || strings.Contains(c.Query, "$trigger") {
					t.Fatalf("unfiltered request changed: op %s query %q", c.OperationName, c.Query)
				}
				if _, ok := c.Variables["triggerKind"]; ok {
					t.Fatalf("triggerKind sent without the flag: %v", c.Variables)
				}
				if _, ok := c.Variables["triggerId"]; ok {
					t.Fatalf("triggerId sent without the flag: %v", c.Variables)
				}
			}
		})
	}
}

// The server owns triggerKind's validation: its refusal is passed through with
// its exit code, sent once, never retried or rewritten.
func TestSnapshotListTriggerKindRefusalIsTheServers2775(t *testing.T) {
	calls, _, err := runSnapshotList2775(t, false, func(snapshotCall1614) string {
		return `{"errors":[{"message":"triggerKind must be SCHEDULE or WEBHOOK","extensions":{"code":"BAD_USER_INPUT"}}],"data":null}`
	}, "--trigger-kind", "MANUAL")
	if exitCodeFor(err) != exitcode.Usage || len(*calls) != 1 || !strings.Contains(err.Error(), "triggerKind must be SCHEDULE or WEBHOOK") {
		t.Fatalf("exit %d calls %d err %v", exitCodeFor(err), len(*calls), err)
	}
}
