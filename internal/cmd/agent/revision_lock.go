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

// cli#61 first cut: `agent revision create` and `agent lock acquire|release`;
// cli#2495 adds `revision publish` and `revision restore`. Each verb is one
// provider call with the references passed exactly as typed. Nothing follows a
// result: no retry, force or renewal. cli#2533 adds `revision list` and `revision rm`
// (revision_list_rm.go) over the server contract of hadron-server#4488.
// Who may act, and how long a lock lives, stay server-owned.

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
		Short: "List, create, publish, restore, or delete revisions of an Agent's system memory",
		Long: `List, save, publish, restore and delete versions of an Agent's system memory (the Memory
holding the Agent's prompts and tasks). A revision is a saved copy of that
memory, named by its location (a "revision-loc" such as revisions:rev-7).

  list      show the saved revisions and how many saves the Agent has left
  create    save the system memory as it is now; prints the new revision's loc
  publish   make a saved revision the one the Agent serves: new chats load it
  restore   put an earlier revision back as the Agent's current system memory
  rm        delete one saved revision (this does not give a save back)

Use the loc that "list" or "create" prints with publish, restore and rm. An
Agent can save a limited number of revisions, ever: slot numbers are never
reused, so deleting a revision does not free one. This is not node history: a
single node's earlier versions live under "hadron node revision".
Each command needs ADMIN on the Agent; the server decides.`,
	}
	cmd.AddCommand(newCmdAgentRevisionList(f), newCmdAgentRevisionCreate(f), newCmdAgentRevisionPublish(f), newCmdAgentRevisionRestore(f), newCmdAgentRevisionRm(f))
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
				return exitcode.Newf(exitcode.Error, "server returned no revision result")
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

// nonBlankRevisionLoc refuses a blank loc before any call; the original string
// (never the trimmed one) is what goes to the server.
func nonBlankRevisionLoc(arg string) (string, error) {
	if strings.TrimSpace(arg) == "" {
		return "", exitcode.Newf(exitcode.Usage, "the revision location is empty")
	}
	return arg, nil
}

func newCmdAgentRevisionPublish(f *cmdutil.Factory) *cobra.Command {
	var versionLabel string
	cmd := &cobra.Command{
		Use:   "publish <agent> <revision-loc> [--version-label <text>]",
		Short: "Publish a revision of an Agent's system memory",
		Long: `Publish a saved revision of the Agent's system memory: the Agent then serves
that revision, so new chats load their system memory from it. Give the
revision's loc as "hadron agent revision list" or "create" printed it. Needs
ADMIN on the Agent; the server decides.

--version-label names the published version; left out, the server picks a
label, and an explicit empty label stores an empty one. Prints "✓ published
revision <loc> (label <label>, published: <true|false>)"; --json is
{"revisionLoc", "label", "published", "createdAt"}.`,
		Example: `  hadron agent revision publish hrn:agent:acme.com:bot revisions:rev-7 --version-label launch`,
		Args:    cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			ref, err := nonBlankAgentRef(args[0])
			if err != nil {
				return err
			}
			loc, err := nonBlankRevisionLoc(args[1])
			if err != nil {
				return err
			}
			var label *string
			if cmd.Flags().Changed("version-label") {
				label = &versionLabel
			}
			client, err := f.GraphQLClient()
			if err != nil {
				return err
			}
			resp, err := gen.PublishRevision(cmd.Context(), client, ref, loc, label)
			if err != nil {
				return api.MapError(err)
			}
			r := resp.PublishRevision
			if r == nil {
				return exitcode.Newf(exitcode.Error, "server returned no revision result")
			}
			dto := agentRevisionDTO{RevisionLoc: r.RevisionLoc, Label: r.Label, Published: r.Published, CreatedAt: r.CreatedAt}
			return output.Write(f.IOStreams, f.JSON, dto, func(w io.Writer) error {
				_, err := fmt.Fprintf(w, "✓ published revision %s (label %s, published: %t)\n",
					output.SafeCell(dto.RevisionLoc), output.SafeCell(dto.Label), dto.Published)
				return err
			})
		},
	}
	cmd.Flags().StringVar(&versionLabel, "version-label", "", "a version label for the published revision (omitted: the server default)")
	return cmd
}

func newCmdAgentRevisionRestore(f *cmdutil.Factory) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "restore <agent> <revision-loc>",
		Short: "Restore an earlier revision of an Agent's system memory",
		Long: `Put an earlier revision back as the Agent's current system memory. This
replaces the live system memory: the server first saves its present state as a
new revision of its own, then rebuilds the memory from the revision you name.
That automatic save can be refused when the Agent's revision slots are full
(REVISION_SLOTS_EXHAUSTED); the refusal is printed as the server gives it.
"hadron agent revision list" shows how many saves are left. Deleting a revision
with "hadron agent revision rm" does not bring a save back: slot numbers are
never reused. Give the revision's loc as "list" or "create" printed it. Needs
ADMIN on the Agent; the server decides.

Prints "✓ restored revision <loc> (label <label>, published: <true|false>)";
--json is {"revisionLoc", "label", "published", "createdAt"}.`,
		Example: `  hadron agent revision restore hrn:agent:acme.com:bot revisions:rev-5`,
		Args:    cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			ref, err := nonBlankAgentRef(args[0])
			if err != nil {
				return err
			}
			loc, err := nonBlankRevisionLoc(args[1])
			if err != nil {
				return err
			}
			client, err := f.GraphQLClient()
			if err != nil {
				return err
			}
			resp, err := gen.RestoreRevision(cmd.Context(), client, ref, loc)
			if err != nil {
				return api.MapError(err)
			}
			r := resp.RestoreRevision
			if r == nil {
				return exitcode.Newf(exitcode.Error, "server returned no revision result")
			}
			dto := agentRevisionDTO{RevisionLoc: r.RevisionLoc, Label: r.Label, Published: r.Published, CreatedAt: r.CreatedAt}
			return output.Write(f.IOStreams, f.JSON, dto, func(w io.Writer) error {
				_, err := fmt.Fprintf(w, "✓ restored revision %s (label %s, published: %t)\n",
					output.SafeCell(dto.RevisionLoc), output.SafeCell(dto.Label), dto.Published)
				return err
			})
		},
	}
	return cmd
}

func newCmdAgentLock(f *cmdutil.Factory) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "lock",
		Short: "Take or give up an Agent's editor lock",
		Long: `The editor lock for an Agent's system memory: a server-side marker that says
who is editing it. While someone holds it, anyone else who asks for it is told
"not acquired", who holds it and until when.

When you need it: only when you want to tell others you are editing this
Agent's system memory. No other hadron command takes, checks or waits for the
lock, so editing nodes does not require holding it from this CLI. Whether the
server refuses an edit from someone who does not hold it is the server's
decision; this CLI does not enforce it.

A lock does not last forever: "lock acquire" prints the time it runs out
("until <time>"), asking again while you hold it renews it, and "lock release"
gives it up early. A lock nobody releases ends at that time.

The usual order is: "hadron agent lock acquire <agent>", make your edits,
"hadron agent lock release <agent>". The server owns who may take the lock and
how long it lasts; the CLI never retries, forces, renews or releases on your
behalf.`,
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
		Long: `Ask for the editor lock. If someone else holds it, the server says the lock
was not acquired and names who holds it and until when (null when unknown): the
result is printed and the command exits 5 (conflict). It does not retry.
Asking for a lock you already hold renews it.

Prints "✓ lock acquired until <time>" on success ("✓ lock acquired" when the
server gives no expiry), or "✗ lock not acquired (held by <who>) until <time>"
when someone else has it; --json is {"locked": bool, "lockedBy": <who or null>,
"expiresAt": <time or null>}.`,
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
