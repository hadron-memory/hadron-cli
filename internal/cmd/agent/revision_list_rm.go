package agent

// cli#2533 (server #4488): `agent revision list` and `agent revision rm`. Each is
// one provider call with the references passed exactly as typed; who may act and
// every refusal are the server's. The server's contract, read from its SDL at the
// merged revision (hadron-server 699dccd1): slot numbers are NEVER reused, so
// deleting a revision removes it from the list and from restore but does not
// raise slotsRemaining. This file never says otherwise.

import (
	"fmt"
	"io"
	"math"
	"strconv"

	"github.com/spf13/cobra"

	"github.com/hadron-memory/hadron-cli/internal/api"
	"github.com/hadron-memory/hadron-cli/internal/api/gen"
	"github.com/hadron-memory/hadron-cli/internal/cmdutil"
	"github.com/hadron-memory/hadron-cli/internal/exitcode"
	"github.com/hadron-memory/hadron-cli/internal/output"
)

type agentRevisionListDTO struct {
	Revisions       []agentRevisionDTO `json:"revisions"`
	SlotLimit       int                `json:"slotLimit"`
	HighestSlotUsed int                `json:"highestSlotUsed"`
	SlotsRemaining  int                `json:"slotsRemaining"`
}

type agentRevisionDeletedDTO struct {
	RevisionLoc string `json:"revisionLoc"`
	Status      string `json:"status"`
}

// slotCount turns the server's Float! slot number into an int, refusing anything
// that is not a whole non-negative number: a count is shown exactly or not at all.
func slotCount(name string, v float64) (int, error) {
	if v < 0 || v != math.Trunc(v) || v > math.MaxInt32 {
		return 0, exitcode.Newf(exitcode.Unavailable, "the server returned %s %v, which is not a whole non-negative count", name, v)
	}
	return int(v), nil
}

func newCmdAgentRevisionList(f *cmdutil.Factory) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "list <agent>",
		Short: "List an Agent's revisions and its revision slots",
		Long: `List the revisions saved for an Agent's system memory, newest first, with the
slot facts the server reports. This is where "create" and "restore" get the
loc they need.

Text output is a table (REVISION, LABEL, PUBLISHED, CREATED) and a slots line.
The slot limit is how many revisions an Agent may ever save: slot numbers are
never reused, so "highest slot used" counts every revision ever saved,
deleted ones included, and "saves left" is the limit minus that. When no saves
are left, "create" and the automatic save inside "restore" are refused
(REVISION_SLOTS_EXHAUSTED); deleting a revision with "revision rm" does not
bring a save back.

--json is {"revisions": [{"revisionLoc", "label", "published", "createdAt"}],
"slotLimit", "highestSlotUsed", "slotsRemaining"}; "revisions" is [] for an
Agent with none. Needs ADMIN on the Agent; the server decides.`,
		Example: `  hadron agent revision list hrn:agent:acme.com:bot
  hadron agent revision list hrn:agent:acme.com:bot --json`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ref, err := nonBlankAgentRef(args[0])
			if err != nil {
				return err
			}
			client, err := f.GraphQLClient()
			if err != nil {
				return err
			}
			resp, err := gen.AgentRevisions(cmd.Context(), client, ref)
			if err != nil {
				return api.MapError(err)
			}
			if resp == nil || resp.AgentRevisions == nil {
				return exitcode.Newf(exitcode.Unavailable, "the server returned no revision list")
			}
			list := resp.AgentRevisions
			dto := agentRevisionListDTO{Revisions: []agentRevisionDTO{}}
			if dto.SlotLimit, err = slotCount("slotLimit", list.SlotLimit); err != nil {
				return err
			}
			if dto.HighestSlotUsed, err = slotCount("highestSlotUsed", list.HighestSlotUsed); err != nil {
				return err
			}
			if dto.SlotsRemaining, err = slotCount("slotsRemaining", list.SlotsRemaining); err != nil {
				return err
			}
			for _, r := range list.Revisions {
				if r == nil {
					return exitcode.Newf(exitcode.Unavailable, "the server returned a null revision in the list")
				}
				dto.Revisions = append(dto.Revisions, agentRevisionDTO{RevisionLoc: r.RevisionLoc, Label: r.Label, Published: r.Published, CreatedAt: r.CreatedAt})
			}
			return output.Write(f.IOStreams, f.JSON, dto, func(w io.Writer) error {
				if len(dto.Revisions) == 0 {
					if _, err := fmt.Fprintln(w, "No revisions."); err != nil {
						return err
					}
				} else {
					t := output.NewTable(w, "REVISION", "LABEL", "PUBLISHED", "CREATED")
					for _, r := range dto.Revisions {
						t.Row(output.SafeCell(r.RevisionLoc), output.SafeCell(r.Label), strconv.FormatBool(r.Published), output.SafeCell(r.CreatedAt))
					}
					if err := t.Flush(); err != nil {
						return err
					}
				}
				_, err := fmt.Fprintf(w, "slots: highest used %d of %d; %d saves left (a delete does not bring one back)\n", dto.HighestSlotUsed, dto.SlotLimit, dto.SlotsRemaining)
				return err
			})
		},
	}
	return cmd
}

func newCmdAgentRevisionRm(f *cmdutil.Factory) *cobra.Command {
	var yes bool
	cmd := &cobra.Command{
		Use:   "rm <agent> <revision-loc> [--yes]",
		Short: "Delete one of an Agent's saved revisions",
		Long: `Delete one saved revision of an Agent's system memory, by the loc "revision
list" shows. The revision leaves the list and can no longer be restored.

It does NOT free a slot: slot numbers are never reused, so after a delete the
Agent has the same number of saves left, and a user at REVISION_SLOTS_EXHAUSTED
is still there. Use it to remove a revision you do not want kept.

The server refuses, before changing anything, to delete the published revision
(REVISION_PUBLISHED: publish another first) and an Agent's only revision
(REVISION_ONLY); both exit 5. A loc that is not one of the Agent's revisions is
refused as bad input (exit 2). Needs ADMIN on the Agent; the server decides.

On a terminal this asks first; without one, --yes is required and nothing is
sent without it. One request is sent and never retried: if its answer is lost,
the message names the command that shows whether the revision still exists.

Prints "✓ deleted revision <loc>"; --json is {"revisionLoc", "status": "deleted"}.`,
		Example: `  hadron agent revision rm hrn:agent:acme.com:bot revisions:rev-3
  hadron agent revision rm hrn:agent:acme.com:bot revisions:rev-3 --yes --json`,
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			ref, err := nonBlankAgentRef(args[0])
			if err != nil {
				return err
			}
			loc, err := nonBlankRevisionLoc(args[1])
			if err != nil {
				return err
			}
			if err := cmdutil.ConfirmDeletion(f.IOStreams, yes, fmt.Sprintf("revision %s of agent %s", strconv.Quote(loc), strconv.Quote(ref))); err != nil {
				return err
			}
			client, err := f.GraphQLClient()
			if err != nil {
				return err
			}
			// What an operator may paste to learn the outcome: prose first, the
			// command alone on the last line.
			check := "Check whether it still exists with:\n  hadron agent revision list " + cmdutil.ShellQuote(ref)
			resp, err := gen.DeleteAgentRevision(cmd.Context(), client, ref, loc)
			if err != nil {
				mapped := api.MapErrorKeepingCode(err)
				if code := exitcode.FromError(mapped); code == exitcode.Error || code == exitcode.Unavailable {
					mapped = api.WithServerCode(exitcode.Newf(code, "delete outcome unknown (sent once, not retried): %s. %s", mapped, check), err)
				}
				return mapped
			}
			if resp == nil || resp.DeleteAgentRevision == nil || resp.DeleteAgentRevision.RevisionLoc == "" {
				return exitcode.Newf(exitcode.Unavailable, "the server did not confirm the delete. %s", check)
			}
			dto := agentRevisionDeletedDTO{RevisionLoc: resp.DeleteAgentRevision.RevisionLoc, Status: "deleted"}
			return output.Write(f.IOStreams, f.JSON, dto, func(w io.Writer) error {
				_, err := fmt.Fprintf(w, "✓ deleted revision %s\n", output.SafeCell(dto.RevisionLoc))
				return err
			})
		},
	}
	cmd.Flags().BoolVar(&yes, "yes", false, "do not ask first (required without a terminal)")
	return cmd
}
