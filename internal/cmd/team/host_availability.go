package team

import (
	"fmt"
	"github.com/hadron-memory/hadron-cli/internal/api"
	"github.com/hadron-memory/hadron-cli/internal/api/gen"
	"github.com/hadron-memory/hadron-cli/internal/cmdutil"
	"github.com/hadron-memory/hadron-cli/internal/exitcode"
	"github.com/hadron-memory/hadron-cli/internal/output"
	"github.com/spf13/cobra"
	"io"
	"strconv"
	"strings"
	"time"
)

type hostAvailabilityDTO struct {
	workerAvailabilityDTO
	SetByHostID *string `json:"setByHostId"`
}

func newCmdTeamHostAvailability(f *cmdutil.Factory) *cobra.Command {
	var config, reason, until string
	var clear bool
	cmd := &cobra.Command{
		Use:   "availability <worker> --config <host.yaml> (--reason <reason> | --clear)",
		Short: "Set or clear this host's own worker availability mark",
		Long: `Use only the host:operate key_file from --config, never HADRON_TOKEN or a
worker session. The App comes from that configuration; --app does not change it.
The server resolves the registered worker's name, id or URN and authorizes
the call (cor:agt:060:08). This command does not register or poll the host.

--reason OUT_OF_TOKENS or HOST_DOWN marks the worker unavailable, optionally
until an RFC 3339 time. --clear sets it available. A host can clear or replace
only its own mark; another setter's mark, including a stall, is refused with
AVAILABILITY_NOT_PERMITTED (exit 8). No automatic failure classification.
The server decides ownership and whether --until is in the future.`,
		Example: "  hadron team host availability Iris --config host.yaml --reason OUT_OF_TOKENS\n  hadron team host availability Iris --config host.yaml --clear",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if strings.TrimSpace(args[0]) == "" {
				return exitcode.Newf(exitcode.Usage, "worker must not be empty")
			}
			if clear {
				if cmd.Flags().Changed("reason") || cmd.Flags().Changed("until") {
					return exitcode.Newf(exitcode.Usage, "--clear does not combine with --reason or --until")
				}
			} else if reason != "OUT_OF_TOKENS" && reason != "HOST_DOWN" {
				return exitcode.Newf(exitcode.Usage, "pass --clear or --reason OUT_OF_TOKENS|HOST_DOWN")
			}
			if cmd.Flags().Changed("until") {
				if _, err := time.Parse(time.RFC3339, until); err != nil {
					return exitcode.Newf(exitcode.Usage, "--until must be an RFC 3339 timestamp")
				}
			}
			cfg, _, server, err := loadHostPoller(f, config)
			if err != nil {
				return err
			}
			in := &gen.SetHostWorkerAvailabilityInput{AppRef: cfg.App, Worker: args[0], State: gen.AvailabilityStateAvailable}
			if !clear {
				in.State = gen.AvailabilityStateUnavailable
				r := availabilityReasons[reason]
				in.Reason = &r
				in.Until = changedString1337(cmd, "until", until)
			}
			r, err := gen.SetHostWorkerAvailability(cmd.Context(), server.client, in)
			if err != nil {
				return api.WithServerCode(api.MapError(err), err)
			}
			if r == nil || r.SetHostWorkerAvailability == nil {
				return exitcode.Newf(exitcode.Error, "server returned no availability record")
			}
			v := r.SetHostWorkerAvailability
			party := partyDTO(v.Worker)
			if party == nil {
				return exitcode.Newf(exitcode.Error, "server returned no availability worker")
			}
			dto := hostAvailabilityDTO{workerAvailabilityDTO: workerAvailabilityDTO{Worker: *party, State: string(v.State), Reason: reasonPtr(v.Reason), Until: v.Until, SetAt: v.SetAt, SetBy: string(v.SetBy), SetByParty: partyDTO(v.SetByParty), InEffect: v.InEffect}, SetByHostID: v.SetByHostId}
			return output.Write(f.IOStreams, f.JSON, dto, func(w io.Writer) error {
				_, err := fmt.Fprintf(w, "%s: %s (set by %s)\n", strconv.Quote(args[0]), output.SafeCell(dto.State), output.SafeCell(dto.SetBy))
				return err
			})
		}}
	cmd.Flags().StringVar(&config, "config", "", "host configuration file (its host key and App are used)")
	cmd.Flags().StringVar(&reason, "reason", "", "OUT_OF_TOKENS or HOST_DOWN; requires an unavailable mark")
	cmd.Flags().StringVar(&until, "until", "", "optional unavailable-until time (RFC 3339)")
	cmd.Flags().BoolVar(&clear, "clear", false, "clear this host's own mark, setting the worker available")
	return cmd
}
