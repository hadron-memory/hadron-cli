package cmd

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/cmd/team"
	"github.com/hadron-memory/hadron-cli/internal/exitcode"
)

// cli#3091: a team write run from another worker's worktree must not act as
// that worker. The invoker signal is the launcher's ORCA_WORKSPACE_ID; the
// cwd's worktree root is stubbed. The binding fixture is "Tina's" (s-new).

const (
	foreignRoot3091 = "/work/tina"
	ownRoot3091     = "/work/gloria"
)

func run3091(t *testing.T, invoker, optIn string, argv ...string) (*[]attnCall, error) {
	t.Helper()
	t.Setenv("ORCA_WORKSPACE_ID", invoker)
	t.Setenv(team.ForeignBindingOptInEnv, optIn)
	t.Cleanup(team.SetGitToplevelRunner(func(_ context.Context) (string, error) { return foreignRoot3091, nil }))
	r := runTeamTaskFactory(t, map[string]string{
		"AgentTaskStart":        `{"data":{"startAgentTask":{"id":"task1","status":"STARTED"}}}`,
		"PRVerdict":             `{"data":{"recordReviewVerdict":{"task":{"id":"t1","handle":"AT0.7","status":"STARTED"},"action":{"code":"review.verdict","state":"WITHHELD","taskLoc":null,"taskUrn":null}}}}`,
		"TeamDefaultChannel":    `{"data":{"app":{"id":"capp100000000000000000000","defaultChannel":{"id":"ch1"}}}}`,
		"MarkOwnTeamChatRead":   markReadJSON,
		"CreateTeamChatMessage": `{"data":{"createTeamChatMessage":{"id":"m1","seq":1,"body":"hello","createdAt":"2026-11-24T00:00:00Z"}}}`,
		"TeamAppIdentity":       teamAppIdentityJSON,
		"EndTeamSession":        `{"data":{"endTeamSession":{"id":"s-new","status":"ENDED"}}}`,
	}, argv...)
	return r.Calls, r.Err
}

func sessionsSent3091(calls *[]attnCall) (n int, foreign bool) {
	for _, c := range *calls {
		if c.Op == "AgentTaskStart" || c.Op == "PRVerdict" || c.Op == "MarkOwnTeamChatRead" || c.Op == "CreateTeamChatMessage" || c.Op == "EndTeamSession" {
			n++
			if c.Session == "s-new" || strings.Contains(fmt.Sprint(c.Vars), "s-new") {
				foreign = true
			}
		}
	}
	return n, foreign
}

var cmds3091 = map[string][]string{
	"task start":  {"team", "task", "start", "task1"},
	"pr verdict":  {"team", "pr", "verdict", "task1", "--head", prHead1664, "--verdict", "APPROVE"},
	"mark-read":   {"team", "chat", "mark-read", "--through", "5"},
	"chat post":   {"team", "chat", "post", "hello"},
	"session end": {"team", "session", "end"},
}

func TestForeignBindingRefusesCrossWorkerMutations3091(t *testing.T) {
	for name, argv := range cmds3091 {
		t.Run(name, func(t *testing.T) {
			calls, err := run3091(t, "id1::"+ownRoot3091, "", argv...)
			if err == nil || exitCodeFor(err) != exitcode.Usage || !strings.Contains(err.Error(), "nothing was sent") {
				t.Fatalf("want a usage refusal, got %v", err)
			}
			if n, _ := sessionsSent3091(calls); n != 0 {
				t.Fatalf("a mutation was sent: %v", opsOf(*calls))
			}
		})
	}
}

func TestForeignBindingOptInAndOwnWorktreeUnchanged3091(t *testing.T) {
	for name, argv := range cmds3091 {
		t.Run(name+" opt-in", func(t *testing.T) {
			calls, err := run3091(t, "id1::"+ownRoot3091, "1", argv...)
			if err != nil {
				t.Fatal(err)
			}
			if n, foreign := sessionsSent3091(calls); n != 1 || !foreign {
				t.Fatalf("opt-in must send once through the binding: %v", opsOf(*calls))
			}
		})
		t.Run(name+" own worktree", func(t *testing.T) {
			calls, err := run3091(t, "id1::"+foreignRoot3091, "", argv...)
			if err != nil {
				t.Fatal(err)
			}
			if n, foreign := sessionsSent3091(calls); n != 1 || !foreign {
				t.Fatalf("own binding must behave as before: %v", opsOf(*calls))
			}
		})
		t.Run(name+" no launcher signal", func(t *testing.T) {
			calls, err := run3091(t, "", "", argv...)
			if err != nil {
				t.Fatal(err)
			}
			if n, _ := sessionsSent3091(calls); n != 1 {
				t.Fatalf("no signal means unchanged: %v", opsOf(*calls))
			}
		})
	}
}
