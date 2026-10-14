package team

import (
	"context"
	"os"
	"strings"

	"github.com/Khan/genqlient/graphql"
	"github.com/spf13/cobra"

	"github.com/hadron-memory/hadron-cli/internal/api/gen"
	"github.com/hadron-memory/hadron-cli/internal/exitcode"
)

// launchGrantEnv is where the host poller (`team host run`) puts the
// single-use launch grant for the agent it starts (hadron-cli#831,
// cor:agt:070:08). It is the ONLY Hadron credential the poller passes on:
// the host key never reaches the agent, which binds on its holder's own
// token.
const launchGrantEnv = "HADRON_LAUNCH_GRANT"

// launchGrantFrom returns the launch grant for `session start`: the flag
// when given, else the environment. An explicitly empty flag is refused
// rather than read as "use the environment" — an empty flag is not an
// absent one.
func launchGrantFrom(cmd *cobra.Command, flag string) (string, error) {
	if cmd.Flags().Changed("launch-grant") {
		g := strings.TrimSpace(flag)
		if g == "" {
			return "", exitcode.Newf(exitcode.Usage, "--launch-grant must not be empty; omit it to start without a grant")
		}
		return g, nil
	}
	return strings.TrimSpace(os.Getenv(launchGrantEnv)), nil
}

// launchedWorker reads the worker a launch grant bound, by the id the new
// session carries.
func launchedWorker(ctx context.Context, client graphql.Client, s gen.TeamSessionFields) (gen.WorkerFields, error) {
	if s.WorkerId == nil || *s.WorkerId == "" {
		return gen.WorkerFields{}, exitcode.Newf(exitcode.Error, "the server bound a launched session with no worker")
	}
	return resolveWorker(ctx, client, "", *s.WorkerId)
}
