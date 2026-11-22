package team

import (
	"fmt"
	"io"
	"strings"

	"github.com/hadron-memory/hadron-cli/internal/api"
	"github.com/hadron-memory/hadron-cli/internal/api/gen"
	"github.com/hadron-memory/hadron-cli/internal/cmdutil"
	"github.com/hadron-memory/hadron-cli/internal/exitcode"
	"github.com/hadron-memory/hadron-cli/internal/instruction"
	"github.com/hadron-memory/hadron-cli/internal/output"
	"github.com/spf13/cobra"
)

type workerLeaveAloneDTO struct {
	WorkerID   string  `json:"workerId"`
	LeaveAlone *bool   `json:"leaveAlone"`
	Reason     *string `json:"reason"`
	By         *string `json:"by"`
	At         *string `json:"at"`
}

type appSweepModeDTO struct {
	AppRef    string `json:"appRef"`
	SweepMode string `json:"sweepMode"`
}

const s3InstructionHelp = `Without --instruction the write goes out as the signed-in person, even in
a bound worktree. With --instruction, the CLI checks that the binding has
not changed and belongs to this server before attaching its worker session.
The server checks that its holder manages the target App, checks the
instruction reference, and records the manager act. No refusal is retried
as the person. Reads never carry a worker session. These controls require
a server from 2026-11-15 or later.

` + instructionHelp

func newCmdWorkerLeaveAlone(f *cmdutil.Factory) *cobra.Command {
	var on, off bool
	var reason, rawInstruction string
	c := &cobra.Command{
		Use:   "leave-alone <worker> [--on|--off] [--reason <text>]",
		Short: "Show or change a worker's leave-alone marker",
		Long: `Read the server's leave-alone marker, or change it as the App manager.
The host sweep
observes and reports a marked worker, but never starts, stops or repairs it.
This command changes the marker only; it does not change a running process.
With neither --on nor --off, read the marker without changing it. For a
write, exactly one of --on or --off must be true. --reason is optional with --on
only; the server limits it to 500 characters. Repeating a call is accepted.
Not permitted exits 8; a missing worker exits 4, retired exits 5, and bad
input exits 2. Correct the input or ask the App manager; nothing is retried.

A worker id or URN is resolved by the server; a name needs --app, the App
context or this worktree's binding. --json prints workerId, leaveAlone,
reason, by and at (each null when the server withholds it). Existing worker
get/list keep their earlier server compatibility and do not request these
fields. Null leaveAlone means unknown (not visible to you), never false. The
server withholds all four working-state fields from a profile-only reader.

` + s3InstructionHelp,
		Example: "  hadron team worker leave-alone Iris --app acme.com:eng --json\n  hadron team worker leave-alone Iris --on --reason maintenance --app acme.com:eng\n  hadron team worker leave-alone Iris --off --instruction task:hrn:task:acme.com:eng:42",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			reading := !cmd.Flags().Changed("on") && !cmd.Flags().Changed("off")
			if reading && (cmd.Flags().Changed("reason") || cmd.Flags().Changed("instruction")) {
				return exitcode.Newf(exitcode.Usage, "--reason and --instruction require --on or --off")
			}
			if !reading && (on == off || (cmd.Flags().Changed("on") && !on) || (cmd.Flags().Changed("off") && !off)) {
				return exitcode.Newf(exitcode.Usage, "pass exactly one of --on or --off, with a true value")
			}
			if strings.TrimSpace(args[0]) == "" {
				return exitcode.Newf(exitcode.Usage, "worker reference must not be empty")
			}
			if off && cmd.Flags().Changed("reason") {
				return exitcode.Newf(exitcode.Usage, "--reason is only accepted with --on")
			}
			var claim *gen.ActInstructionInput
			var err error
			if cmd.Flags().Changed("instruction") {
				claim, err = parseInstruction(rawInstruction)
				if err != nil {
					return err
				}
			}
			// The server resolves a direct worker reference's App; never infer it
			// from ambient scope. This helper snapshots before any lookup.
			instructed := BeginServerTargetInstructedWrite(f)(cmd.Context())
			ctx := cmd.Context()
			b, err := readBindingOrNilWithApp(ctx, f)
			if err != nil {
				return err
			}
			direct := cmdutil.IsEntityID(args[0]) || strings.HasPrefix(strings.ToLower(args[0]), "hrn:worker:") || strings.HasPrefix(strings.ToLower(args[0]), "urn:worker:")
			var scope appScope
			if !direct {
				scope, err = resolveTeamAppScope(ctx, f, b)
				if err != nil {
					return err
				}
			}
			input := &gen.SetWorkerLeaveAloneInput{Worker: args[0], LeaveAlone: on}
			if !direct {
				input.AppRef = &scope.Ref
			}
			if cmd.Flags().Changed("reason") {
				input.Reason = &reason
			}
			client, err := f.GraphQLClient()
			if err != nil {
				return err
			}
			if claim != nil {
				ctx, _, err = instructed(ctx)
				if err != nil {
					return err
				}
			}
			var d workerLeaveAloneDTO
			if reading {
				var appRef *string
				if !direct {
					appRef = &scope.Ref
				}
				r, err := gen.GetWorkerLeaveAlone(ctx, client, args[0], appRef)
				if err != nil {
					return api.MapErrorKeepingCode(err)
				}
				if r == nil || r.Worker == nil {
					return exitcode.Newf(exitcode.NotFound, "worker not found")
				}
				v := r.Worker
				d = workerLeaveAloneDTO{WorkerID: v.Id, LeaveAlone: v.LeaveAlone, Reason: v.LeaveAloneReason, By: v.LeaveAloneBy, At: v.LeaveAloneAt}
			} else {
				r, err := gen.SetWorkerLeaveAlone(ctx, client, input, claim)
				if err != nil {
					return api.MapErrorKeepingCode(err)
				}
				if r == nil || r.SetWorkerLeaveAlone == nil {
					return exitcode.Newf(exitcode.Error, "server returned no leave-alone result; verify the worker before retrying")
				}
				v := r.SetWorkerLeaveAlone
				marked := v.LeaveAlone
				d = workerLeaveAloneDTO{WorkerID: v.WorkerId, LeaveAlone: &marked, Reason: v.Reason, By: v.By, At: v.At}
			}
			label := lazyAppLabel(cmd.Context(), f, scope)
			return output.Write(f.IOStreams, f.JSON, d, func(w io.Writer) error {
				if !direct {
					if _, err := fmt.Fprintf(w, "app: %s\n", label()); err != nil {
						return err
					}
				}
				marker := "unknown (not visible to you)"
				if d.LeaveAlone != nil {
					marker = fmt.Sprint(*d.LeaveAlone)
				}
				_, err := fmt.Fprintf(w, "worker: %s\nleave alone: %s\nreason: %s\nby: %s\nat: %s\n", output.SafeCell(d.WorkerID), marker, output.SafeCell(dash(d.Reason)), output.SafeCell(dash(d.By)), output.SafeCell(dash(d.At)))
				return err
			})
		},
	}
	c.Flags().BoolVar(&on, "on", false, "mark the worker leave-alone")
	c.Flags().BoolVar(&off, "off", false, "clear the leave-alone marker")
	c.Flags().StringVar(&reason, "reason", "", "reason for marking leave-alone (only with --on; server checks the 500-character bound)")
	c.Flags().StringVar(&rawInstruction, "instruction", "", instruction.FlagUsageForServer("2026-11-15"))
	return c
}

func newCmdTeamApp(f *cmdutil.Factory) *cobra.Command {
	group := &cobra.Command{Use: "app <command>", Short: "Team App sweep controls"}
	var rawInstruction string
	c := &cobra.Command{
		Use:   "sweep-mode [OFF|OBSERVE|APPLY]",
		Short: "Show or change the App's host sweep mode",
		Long: `The sweep mode says what the host sweep may do for this App. The host sweep is
a pass a host runs about once a minute ('hadron team host sweep'): it compares
each worker's desired state with what is actually running on the host.
  OFF      (the default) the sweep acts on no workers of this App
  OBSERVE  it compares and records what it found, and changes nothing
  APPLY    it may also repair what it can, such as relaunching a worker session
           that is proven absent
Use OBSERVE to see what APPLY would do before turning it on. Read what a host's
sweeps found with 'hadron team host sweep-runs --host <id>' (newest first: what
each pass planned, applied, skipped or failed; 'hadron team host list' shows the
host ids), and any alarms the server raised with 'hadron team events --alarms'.
This command only records the mode and does not launch or stop any process.

With no argument it reads the App's sweep mode; with one, it sets it as the App
manager. Example reads and writes are below. App scope comes from --app, the App
context or the worktree binding. --json prints {appRef,sweepMode}. --instruction
is accepted only for a write, and then needs a mode (a bare --instruction is
refused, exit 2). A non-manager gets FORBIDDEN (exit 8); ask the App manager to
set the mode.

` + s3InstructionHelp,
		Example: "  hadron team app sweep-mode --app acme.com:eng --json\n  hadron team app sweep-mode OBSERVE --app acme.com:eng",
		Args:    cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) == 0 && cmd.Flags().Changed("instruction") {
				return exitcode.Newf(exitcode.Usage, "--instruction requires a sweep mode to write")
			}
			var mode gen.SweepMode
			if len(args) > 0 {
				mode = gen.SweepMode(args[0])
				if mode != gen.SweepModeOff && mode != gen.SweepModeObserve && mode != gen.SweepModeApply {
					return exitcode.Newf(exitcode.Usage, "mode must be OFF, OBSERVE or APPLY")
				}
			}
			claim, instructed, err := staffingInstruction(cmd, f, rawInstruction)
			if err != nil {
				return err
			}
			scope, client, err := controlsClient(cmd, f)
			if err != nil {
				return err
			}
			ctx := cmd.Context()
			d := appSweepModeDTO{AppRef: scope.Ref}
			if len(args) == 0 {
				r, err := gen.GetAppSweepMode(ctx, client, scope.Ref)
				if err != nil {
					return api.MapErrorKeepingCode(err)
				}
				if r == nil || r.App == nil {
					return exitcode.Newf(exitcode.NotFound, "App not found")
				}
				d.SweepMode = string(r.App.SweepMode)
			} else {
				if claim != nil {
					ctx, _, err = instructed(ctx, "", scope.Ref)
					if err != nil {
						return err
					}
				}
				r, err := gen.SetAppSweepMode(ctx, client, scope.Ref, mode, claim)
				if err != nil {
					return api.MapErrorKeepingCode(err)
				}
				if r == nil || r.SetAppSweepMode == nil {
					return exitcode.Newf(exitcode.Error, "server returned no sweep-mode result; verify the App before retrying")
				}
				d.SweepMode = string(r.SetAppSweepMode.SweepMode)
			}
			return output.Write(f.IOStreams, f.JSON, d, func(w io.Writer) error {
				_, err := fmt.Fprintf(w, "app: %s\nsweep mode: %s\n", lazyAppLabel(cmd.Context(), f, scope)(), output.SafeCell(d.SweepMode))
				return err
			})
		},
	}
	c.Flags().StringVar(&rawInstruction, "instruction", "", instruction.FlagUsageForServer("2026-11-15"))
	group.AddCommand(c)
	return group
}
