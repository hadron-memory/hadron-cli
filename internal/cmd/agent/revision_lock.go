package agent

import (
	"fmt"
	"io"
	"strings"

	"github.com/spf13/cobra"

	"github.com/hadron-memory/hadron-cli/internal/api"
	"github.com/hadron-memory/hadron-cli/internal/api/gen"
	"github.com/hadron-memory/hadron-cli/internal/cmdutil"
	"github.com/hadron-memory/hadron-cli/internal/exitcode"
	"github.com/hadron-memory/hadron-cli/internal/output"
)

// cli#61 first cut: `agent revision create` and `agent lock acquire|release`.
// Each verb is one provider call with the agent reference passed exactly as
// typed. Nothing follows a result: no retry, force, renewal, publish, restore
// or listing. Who may act, and how long a lock lives, stay server-owned.

// nonBlankAgentRef refuses a blank reference before any call; the original string
// (never the trimmed one) is what goes to the server.
func nonBlankAgentRef(arg string) (string, error) {
	if strings.TrimSpace(arg) == "" {
		return "", exitcode.Newf(exitcode.Usage, "the agent reference is empty")
	}
	return arg, nil
}

func newCmdAgentRevision(f *cmdutil.Factory) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "revision",
		Short: "Snapshot an Agent's system memory (draft workflow)",
		Long: `The Agent system-memory draft workflow. This is not node history: per-node
revisions live under 'hadron node revision'. Only 'create' exists here so far;
publishing and restoring a revision are not available from the CLI yet.`,
	}
	cmd.AddCommand(newCmdAgentRevisionCreate(f))
	return cmd
}

type agentRevisionDTO struct {
	RevisionLoc string `json:"revisionLoc"`
	Label       string `json:"label"`
	Published   bool   `json:"published"`
	CreatedAt   string `json:"createdAt"`
}

func newCmdAgentRevisionCreate(f *cmdutil.Factory) *cobra.Command {
	var description string
	cmd := &cobra.Command{
		Use:     "create <agent> [--description <text>]",
		Short:   "Create a revision of an Agent's system memory",
		Long:    "Create a revision of the Agent's system memory and print what the server returned. Needs ADMIN on the Agent; the server decides.",
		Example: `  hadron agent revision create hrn:agent:acme.com:bot --description "before launch"`,
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ref, err := nonBlankAgentRef(args[0])
			if err != nil {
				return err
			}
			var desc *string
			if cmd.Flags().Changed("description") {
				desc = &description
			}
			client, err := f.GraphQLClient()
			if err != nil {
				return err
			}
			resp, err := gen.CreateRevision(cmd.Context(), client, ref, desc)
			if err != nil {
				return api.MapError(err)
			}
			r := resp.CreateRevision
			if r == nil {
				return exitcode.Newf(exitcode.Error, "server returned no revision")
			}
			dto := agentRevisionDTO{RevisionLoc: r.RevisionLoc, Label: r.Label, Published: r.Published, CreatedAt: r.CreatedAt}
			return output.Write(f.IOStreams, f.JSON, dto, func(w io.Writer) error {
				_, err := fmt.Fprintf(w, "✓ created revision %s (label %s, published: %t)\n",
					output.SafeCell(dto.RevisionLoc), output.SafeCell(dto.Label), dto.Published)
				return err
			})
		},
	}
	cmd.Flags().StringVar(&description, "description", "", "a description of the revision (omitted: the server default)")
	return cmd
}

func newCmdAgentLock(f *cmdutil.Factory) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "lock",
		Short: "Take or give up an Agent's editor lock",
		Long: `The editor lock for an Agent's system memory. The server owns who may take it
and how long it lasts; the CLI never retries, forces, renews or releases on
your behalf.`,
	}
	cmd.AddCommand(newCmdAgentLockAcquire(f), newCmdAgentLockRelease(f))
	return cmd
}

type agentLockDTO struct {
	Locked    bool    `json:"locked"`
	LockedBy  *string `json:"lockedBy"`
	ExpiresAt *string `json:"expiresAt"`
}

func newCmdAgentLockAcquire(f *cmdutil.Factory) *cobra.Command {
	return &cobra.Command{
		Use:   "acquire <agent>",
		Short: "Acquire an Agent's editor lock",
		Long: `Ask for the editor lock. If someone else holds it the server answers "not
locked" with who holds it and until when (null when unknown): the result is
printed and the command exits 5 (conflict). It does not retry.`,
		Example: `  hadron agent lock acquire hrn:agent:acme.com:bot`,
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ref, err := nonBlankAgentRef(args[0])
			if err != nil {
				return err
			}
			client, err := f.GraphQLClient()
			if err != nil {
				return err
			}
			resp, err := gen.AcquireEditorLock(cmd.Context(), client, ref)
			if err != nil {
				return api.MapError(err)
			}
			r := resp.AcquireEditorLock
			if r == nil {
				return exitcode.Newf(exitcode.Error, "server returned no lock result")
			}
			dto := agentLockDTO{Locked: r.Locked, LockedBy: r.LockedBy, ExpiresAt: r.ExpiresAt}
			if err := output.Write(f.IOStreams, f.JSON, dto, func(w io.Writer) error {
				if dto.Locked {
					_, err := fmt.Fprintf(w, "✓ lock acquired%s\n", lockUntil(dto))
					return err
				}
				_, err := fmt.Fprintf(w, "✗ lock not acquired%s%s\n", lockHolder(dto), lockUntil(dto))
				return err
			}); err != nil {
				return err
			}
			if !dto.Locked {
				return exitcode.Silent(exitcode.Conflict)
			}
			return nil
		},
	}
}

func lockHolder(d agentLockDTO) string {
	if d.LockedBy == nil {
		return ""
	}
	return " (held by " + output.SafeCell(*d.LockedBy) + ")"
}

func lockUntil(d agentLockDTO) string {
	if d.ExpiresAt == nil {
		return ""
	}
	return " until " + output.SafeCell(*d.ExpiresAt)
}

type agentLockReleaseDTO struct {
	Released bool `json:"released"`
}

func newCmdAgentLockRelease(f *cmdutil.Factory) *cobra.Command {
	return &cobra.Command{
		Use:   "release <agent>",
		Short: "Release an Agent's editor lock",
		Long: `Ask the server to release the lock and print its answer. A "false" answer is
printed as not released and exits 5 (conflict); the CLI makes no claim about who
held the lock.`,
		Example: `  hadron agent lock release hrn:agent:acme.com:bot`,
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ref, err := nonBlankAgentRef(args[0])
			if err != nil {
				return err
			}
			client, err := f.GraphQLClient()
			if err != nil {
				return err
			}
			resp, err := gen.ReleaseEditorLock(cmd.Context(), client, ref)
			if err != nil {
				return api.MapError(err)
			}
			dto := agentLockReleaseDTO{Released: resp.ReleaseEditorLock}
			if err := output.Write(f.IOStreams, f.JSON, dto, func(w io.Writer) error {
				if dto.Released {
					_, err := fmt.Fprintln(w, "✓ lock released")
					return err
				}
				_, err := fmt.Fprintln(w, "✗ lock not released")
				return err
			}); err != nil {
				return err
			}
			if !dto.Released {
				return exitcode.Silent(exitcode.Conflict)
			}
			return nil
		},
	}
}
