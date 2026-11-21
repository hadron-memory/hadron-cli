package cmd

import (
	"strings"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/cmd/agentic"
	"github.com/hadron-memory/hadron-cli/internal/exitcode"
)

// cli#2850: the review P3s on cli#2775. The server's rule is that triggerId
// requires triggerKind; the CLI passes the server's refusal through, so the help
// has to say so, and a server that predates the trigger filter has to be named.

const idWithoutKindRefusal2850 = `{"errors":[{"message":"triggerId requires triggerKind","extensions":{"code":"BAD_USER_INPUT"}}],"data":null}`

func snapshotListHelp2850(t *testing.T) string {
	t.Helper()
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"automation", "team-snapshot-grant", "list", "--help"})
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	return strings.Join(strings.Fields(out.String()), " ")
}

// The list help says --trigger-id needs --trigger-kind and that an older server
// does not know the flags; agentic-usage names the server change they need.
func TestSnapshotListHelpStatesThePairingAndTheServerFloor2850(t *testing.T) {
	help := snapshotListHelp2850(t)
	for _, want := range []string{
		"--trigger-id needs --trigger-kind",
		"older server",
		"unknown argument",
	} {
		if !strings.Contains(help, want) {
			t.Errorf("list help lacks %q:\n%s", want, help)
		}
	}
	doc := strings.Join(strings.Fields(agentic.Doc()), " ")
	for _, want := range []string{
		"--trigger-id needs --trigger-kind",
		"server#3350",
		"Unknown argument",
	} {
		if !strings.Contains(doc, want) {
			t.Errorf("agentic-usage lacks %q", want)
		}
	}
}

// An id with no kind is the server's refusal, not a success: the CLI sends the
// id as given, once, in both modes, and shows the server's own message.
func TestSnapshotListTriggerIDAloneIsTheServersRefusal2850(t *testing.T) {
	for _, instructed := range []bool{false, true} {
		t.Run(map[bool]string{false: "person", true: "instructed"}[instructed], func(t *testing.T) {
			calls, out, err := runSnapshotList2775(t, instructed, func(snapshotCall1614) string {
				return idWithoutKindRefusal2850
			}, "--trigger-id", "hook1")
			if exitCodeFor(err) != exitcode.Usage || err == nil || !strings.Contains(err.Error(), "triggerId requires triggerKind") || out != "" {
				t.Fatalf("exit %d err %v out %q", exitCodeFor(err), err, out)
			}
			if len(*calls) != 1 {
				t.Fatalf("a refusal is never retried: %d calls", len(*calls))
			}
			v := (*calls)[0].Variables
			if v["triggerId"] != "hook1" {
				t.Errorf("triggerId not sent as given: %v", v)
			}
			if _, ok := v["triggerKind"]; ok {
				t.Errorf("the CLI must not invent a kind: %v", v)
			}
		})
	}
}

// An empty --trigger-id is sent as the empty string, like an empty kind: the
// server judges it, the CLI pre-checks nothing.
func TestSnapshotListEmptyTriggerIDIsSentAsGiven2850(t *testing.T) {
	for _, instructed := range []bool{false, true} {
		calls, _, err := runSnapshotList2775(t, instructed, snapshotPage2775, "--trigger-kind", "SCHEDULE", "--trigger-id", "")
		if err != nil {
			t.Fatal(err)
		}
		for _, c := range *calls {
			if got, ok := c.Variables["triggerId"]; !ok || got != "" {
				t.Errorf("instructed=%v %s: empty triggerId present=%v value=%#v", instructed, c.OperationName, ok, got)
			}
			if !strings.Contains(c.Query, "triggerId: $triggerId") {
				t.Errorf("%s: triggerId not wired: %s", c.OperationName, c.Query)
			}
		}
	}
}

// The ByTrigger documents get the same malformed-page refusals as the legacy
// ones, in both modes, and the failure prints nothing partial.
func TestSnapshotListByTriggerRejectsMalformedPages2850(t *testing.T) {
	cases := map[string]struct{ body, want string }{
		"null-page": {`{"data":{"automationTeamSnapshotGrants":null}}`, "the server returned no team snapshot grant page"},
		"null-item": {grantPage1578("null", 1, false), "the server returned a null team snapshot grant"},
	}
	for name, tc := range cases {
		for _, instructed := range []bool{false, true} {
			t.Run(name+map[bool]string{false: "/person", true: "/instructed"}[instructed], func(t *testing.T) {
				calls, out, err := runSnapshotList2775(t, instructed, func(snapshotCall1614) string { return tc.body },
					"--trigger-kind", "SCHEDULE", "--trigger-id", "s1")
				if err == nil || exitCodeFor(err) != exitcode.Error || !strings.Contains(err.Error(), tc.want) || out != "" {
					t.Fatalf("exit %d err %v out %q", exitCodeFor(err), err, out)
				}
				if len(*calls) != 1 || !strings.Contains((*calls)[0].OperationName, "ByTrigger") {
					t.Fatalf("want one ByTrigger call: %+v", *calls)
				}
			})
		}
	}
}

// The trigger-kind refusal is the server's in the instructed mode too (the
// 2775 test pins person mode only).
func TestSnapshotListTriggerKindRefusalIsTheServersInstructed2850(t *testing.T) {
	calls, _, err := runSnapshotList2775(t, true, func(snapshotCall1614) string {
		return `{"errors":[{"message":"triggerKind must be SCHEDULE or WEBHOOK","extensions":{"code":"BAD_USER_INPUT"}}],"data":null}`
	}, "--trigger-kind", "MANUAL")
	if exitCodeFor(err) != exitcode.Usage || len(*calls) != 1 || err == nil || !strings.Contains(err.Error(), "triggerKind must be SCHEDULE or WEBHOOK") {
		t.Fatalf("exit %d calls %d err %v", exitCodeFor(err), len(*calls), err)
	}
}
