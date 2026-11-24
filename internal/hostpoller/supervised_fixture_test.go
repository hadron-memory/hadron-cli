package hostpoller

import "github.com/hadron-memory/hadron-cli/internal/hostpoller/hostyaml"

// Every worker launches through the supervised (Orca) path (cli#2914). supervisedYAML renders the older
// fixtures' `launch: [program]` worker shorthand into what that path needs (see package hostyaml).
func supervisedYAML(text string) string { return hostyaml.Supervised(text) }

// supervisedWorker is worker ref of family fam with what the supervised launch needs: a launcher profile
// on the account "x" and its own absolute disk_tmp. A Config holding one also needs
// withSupervisedAccounts, which adds the account's bin and an orca binary.
func supervisedWorker(ref, fam string) WorkerConfig {
	return WorkerConfig{Family: fam, Profile: &LauncherProfile{Family: fam, Mode: ModeLong, Account: "x"}, DiskTmp: "/tmp/hostyaml-fixture/" + ref}
}

var supervisedAccounts = map[string]AccountProfile{"x": {Bin: "x"}}

// withSupervisedAccounts gives c the account and orca binary its supervised workers' profiles name.
func withSupervisedAccounts(c *Config) *Config {
	c.Accounts = supervisedAccounts
	c.OrcaLaunch.OrcaBin = "/usr/bin/orca"
	return c
}
