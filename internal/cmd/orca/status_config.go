package orca

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/hadron-memory/hadron-cli/internal/api"
	"github.com/hadron-memory/hadron-cli/internal/cmdutil"
	"github.com/hadron-memory/hadron-cli/internal/exitcode"
	"github.com/hadron-memory/hadron-cli/internal/hostpoller"
	"github.com/hadron-memory/hadron-cli/internal/orcaterm"
)

// hostMode is `status --config`: the host's own configuration, Orca binary
// and App key stand in for the ambient PATH, token and App context.
type hostMode struct {
	cfg  *hostpoller.Config
	path string
	env  Env // the command's Env with Orca and Reads pointed at the host
}

// loadHostMode mirrors `team host run`'s refusals and their order: an empty
// path or an unreadable config is usage (2), a missing or too-open key file
// is auth (3). appFlagGiven is the --app flag being present at all (an unset
// shell variable makes it ""). The key lives only in the client built here: never argv, an
// environment variable or a file descriptor passed on, and never output.
func loadHostMode(f *cmdutil.Factory, env Env, path string, appFlagGiven bool) (*hostMode, error) {
	if strings.TrimSpace(path) == "" {
		return nil, exitcode.Newf(exitcode.Usage, "--config needs the host configuration file")
	}
	if f.AppFlag != "" || appFlagGiven {
		return nil, exitcode.Newf(exitcode.Usage, "--app does not combine with --config: the App is the configuration file's")
	}
	cfg, err := hostpoller.LoadConfig(path)
	if err != nil {
		return nil, exitcode.New(exitcode.Usage, err)
	}
	// LoadConfig checks orca_bin only when delivery is on; a status-only
	// configuration is held to the field's documented meaning as well.
	if bin := cfg.Delivery.OrcaBin; bin != "" && !filepath.IsAbs(bin) {
		return nil, exitcode.Newf(exitcode.Usage, "delivery.orca_bin must be an absolute path")
	}
	key, err := cfg.ReadHostKey()
	if err != nil {
		return nil, exitcode.New(exitcode.AuthRequired, err)
	}
	build := env.HostReads
	if build == nil {
		build = defaultHostReads
	}
	reads, err := build(f, cfg, key)
	if err != nil {
		return nil, api.MapError(err)
	}
	bin := cfg.Delivery.OrcaBin
	if bin == "" {
		bin = "orca"
	}
	exec := env.OrcaExec
	if exec == nil {
		// The Orca child gets the environment minus every credential and any
		// variable carrying the key, even when the launcher already holds one.
		childEnv := hostpoller.ScrubbedEnv(os.Environ(), key)
		exec = func(bin string) orcaterm.Runner { return orcaterm.ExecEnv(bin, 15*time.Second, childEnv) }
	}
	h := &hostMode{cfg: cfg, path: path, env: env}
	h.env.Orca = exec(bin)
	h.env.Reads = func(context.Context, string) (Reads, error) { return reads, nil }
	return h, nil
}

// defaultHostReads reads the server with the host key alone, as `team host
// run` does: not f.GraphQLClient(), so no ambient token and no session header
// (a session on a host call is refused APP_KEY_SESSION_BOUND).
func defaultHostReads(f *cmdutil.Factory, cfg *hostpoller.Config, key string) (Reads, error) {
	server := cfg.Server
	if server == "" {
		var err error
		if server, err = f.Server(); err != nil {
			return nil, err
		}
	}
	client, err := api.NewClient(server, key, f.HTTPClient)
	if err != nil {
		return nil, err
	}
	return graphReads{client: client, app: cfg.App}, nil
}
