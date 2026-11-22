package hostpoller

import "github.com/hadron-memory/hadron-cli/internal/hostpoller/hostyaml"

// cli#2914: every worker launches through the supervised (Orca) path. supervised2914 renders the older
// fixtures' `launch: [program]` worker shorthand into what that path needs (see package hostyaml).
func supervised2914(text string) string { return hostyaml.Supervised(text) }

func supervisedWorker2914(fam string) WorkerConfig {
	return WorkerConfig{Family: fam, Profile: &LauncherProfile{Family: fam, Mode: ModeLong, Account: "x"}, DiskTmp: "/tmp/supervised2914/" + fam}
}

var supervisedAccounts2914 = map[string]AccountProfile{"x": {Bin: "x"}}

// withSupervised2914 gives c the account and orca binary its supervised workers' profiles name.
func withSupervised2914(c *Config) *Config {
	c.Accounts = supervisedAccounts2914
	c.OrcaLaunch.OrcaBin = "/usr/bin/orca"
	return c
}
