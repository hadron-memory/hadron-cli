package hostyaml

import (
	"strings"
	"testing"
)

func TestSupervisedRendersTheShorthandIntoAValidSupervisedConfig(t *testing.T) {
	text := "app: hrn:app:acme.com:team\nlabel: b\nworkers:\n  iris:\n    family: claude\n    launch: [claude-worker, --model, opus]\n  uma:\n    family: codex\n    launch: [codex-x]\n    disk_tmp: /d/uma\n"
	got := Supervised(text)
	for _, want := range []string{
		"launcher_profile: {family: claude, mode: long, account: acct-iris}", "disk_tmp: /tmp/hostyaml-fixture/iris",
		"launcher_profile: {family: codex, mode: long, account: acct-uma}", "acct-iris: {bin: claude-worker}", "acct-uma: {bin: codex-x}",
		"orca_bin: /usr/bin/orca",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in:\n%s", want, got)
		}
	}
	if strings.Count(got, "disk_tmp:") != 2 || strings.Contains(got, "launch: [") {
		t.Errorf("one disk_tmp per worker (uma keeps its own) and no argv left:\n%s", got)
	}
}

// cli#3026 item 1: the limits and the pass-through cases are pinned, not left implied.
func TestSupervisedLeavesTextWithoutTheShorthandUntouched(t *testing.T) {
	for name, text := range map[string]string{
		"a key file":      "hk_SENTINEL_9f3c1b7a5d2e48c6\n",
		"a shell stub":    "#!/bin/sh\nexit 0\n",
		"no workers":      "app: a\nlabel: b\n",
		"a plain worker":  "app: a\nworkers:\n  iris:\n    family: claude\n",
		"an argv in text": "note: launch: [x]\n",
	} {
		if got := Supervised(text); got != text {
			t.Errorf("%s must come back unchanged:\n%q", name, got)
		}
	}
}

// Only block-style workers are rendered. A flow-style `workers: {iris: {launch: [x]}}` is not seen, so it
// comes back unchanged and the load refuses it: say so here rather than fail by surprise.
func TestSupervisedDoesNotSeeFlowStyleWorkers(t *testing.T) {
	text := "app: a\nworkers: {iris: {family: claude, launch: [x]}}\n"
	if got := Supervised(text); got != text {
		t.Fatalf("flow-style workers are outside the shorthand and stay as written:\n%s", got)
	}
}

func TestSupervisedGivesEachWorkerItsOwnDiskTmpAndKeepsAStatedLaunch(t *testing.T) {
	text := "workers:\n  a:\n    family: claude\n    launch: [x]\n  b:\n    family: claude\n    launch: [y]\n  c:\n    family: claude\n    launch: [z]\n    launcher_profile: {family: claude, mode: long, account: own}\n"
	got := Supervised(text)
	for _, want := range []string{"disk_tmp: /tmp/hostyaml-fixture/a", "disk_tmp: /tmp/hostyaml-fixture/b", "account: acct-a", "account: acct-b", "account: own"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "disk_tmp: /tmp/hostyaml-fixture/c") || strings.Contains(got, "acct-c") || strings.Contains(got, "launch: [z]") {
		t.Errorf("worker c states its own launcher_profile: its shorthand is dropped and nothing is rendered for it:\n%s", got)
	}
}
