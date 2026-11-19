package hostpoller

import (
	"strings"
	"testing"
)

// hadron-cli#2143 (S8 of #4229, server#4257): the platform starts a long-lived
// worker that should run and has no session, so the worker.start it issues
// names no task (issuedReason DESIRED_RUNNING). The host already accepts such
// a start; these pins keep it that way, on both launch paths, and keep the
// first prompt a recovery, never a task start and never a resumed conversation.

func tasklessStart(args map[string]any) Operation {
	op := startOp()
	delete(op.Args, "taskId")
	for k, v := range args {
		op.Args[k] = v
	}
	return op
}

func TestWorkerStartWithNoTaskLaunchesWithTheRecoverPrompt2143(t *testing.T) {
	for name, extra := range map[string]map[string]any{
		"absent": nil,
		"null":   {"taskId": nil},
		"empty":  {"taskId": ""},
	} {
		t.Run(name, func(t *testing.T) {
			p, srv, host := testPoller(t)
			orcaWorker(p, t.TempDir(), LauncherProfile{Family: "claude", Mode: ModeShort, Model: "m", Account: "acct1"}, AccountProfile{Bin: "claude"})
			rec := &recordedOrca{}
			p.EnableOrcaLaunch(rec.launch, ConfigProfiles{Cfg: p.startCfg})
			srv.notDone["s1"] = 2
			srv.batches = [][]Operation{{tasklessStart(extra)}}
			step(t, p)
			if r := only(t, srv); r.outcome != Succeeded {
				t.Fatalf("a start with no task must succeed once bound: %+v", r)
			}
			if len(rec.specs) != 1 || len(host.launches) != 0 {
				t.Fatalf("orca launches = %d, exec launches = %d", len(rec.specs), len(host.launches))
			}
			s := rec.specs[0]
			if !strings.Contains(s.Prompt, "hadron team session start") || !strings.Contains(s.Prompt, "hadron team session recover") {
				t.Fatalf("the prompt must bind, then recover: %q", s.Prompt)
			}
			for _, bad := range []string{"team task start", "--resume"} {
				if strings.Contains(s.Prompt, bad) {
					t.Fatalf("the prompt carries %q: %q", bad, s.Prompt)
				}
			}
			for _, a := range s.Argv {
				if a == "--resume" || a == "-r" || a == "resume" {
					t.Fatalf("a task-less start must never resume: %q", s.Argv)
				}
			}
		})
	}
}

func TestWorkerStartWithNoTaskLaunchesOnTheExecPath2143(t *testing.T) {
	p, srv, host := testPoller(t)
	srv.notDone["s1"] = 3
	srv.batches = [][]Operation{{tasklessStart(nil)}}
	step(t, p)
	if r := only(t, srv); r.outcome != Succeeded || len(host.launches) != 1 {
		t.Fatalf("a task-less start must launch once and succeed: %+v launches=%d", r, len(host.launches))
	}
}
