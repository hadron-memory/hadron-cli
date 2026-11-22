package hostyaml

import (
	"strings"
	"testing"
)

func TestSupervisedRendersTheShorthandIntoAValidSupervisedConfig(t *testing.T) {
	text := "app: hrn:app:acme.com:team\nlabel: b\nworkers:\n  iris:\n    family: claude\n    launch: [claude-worker, --model, opus]\n  uma:\n    family: codex\n    launch: [codex-x]\n    disk_tmp: /d/uma\n"
	got := Supervised(text)
	for _, want := range []string{
		"launcher_profile: {family: claude, mode: long, account: acct-iris}", "disk_tmp: /tmp/supervised2914/iris",
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

// supervisedWorker2914 is a worker of family fam with what the supervised launch needs (cli#2914): a
// launcher profile on the account "x" and an absolute disk_tmp. A Config holding one also needs
// supervisedAccounts2914 (the account's bin) and an orca binary.
