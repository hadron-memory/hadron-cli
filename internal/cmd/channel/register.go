package channel

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

// registerEntryDTO is the stable --json shape of one RegisterEntry (#636).
//
// AttendeeURN is a *string and is NOT omitempty: null is the WIDE row — "every
// attendee in the owner's context" — which is a real answer and a much broader
// declaration than naming one. An agent has to be able to tell that apart from a
// field this projection simply did not select.
type registerEntryDTO struct {
	ID             string  `json:"id"`
	ChannelID      string  `json:"channelId"`
	AttendeeURN    *string `json:"attendeeUrn"`
	Role           string  `json:"role"`
	MentionOnly    bool    `json:"mentionOnly"`
	InstallDefault bool    `json:"installDefault"`
	Description    *string `json:"description"`
	OwnerType      string  `json:"ownerType"`
	OwnerID        string  `json:"ownerId"`
	OrganizationID *string `json:"organizationId"`
	CreatedAt      string  `json:"createdAt"`
	UpdatedAt      *string `json:"updatedAt"`
}

func dtoFromRegisterEntry(e gen.RegisterEntryFields) registerEntryDTO {
	return registerEntryDTO{
		ID:             e.Id,
		ChannelID:      e.ChannelId,
		AttendeeURN:    e.AttendeeUrn,
		Role:           string(e.Role),
		MentionOnly:    e.MentionOnly,
		InstallDefault: e.InstallDefault,
		Description:    e.Description,
		OwnerType:      string(e.OwnerType),
		OwnerID:        e.OwnerId,
		OrganizationID: e.OrganizationId,
		CreatedAt:      e.CreatedAt,
		UpdatedAt:      e.UpdatedAt,
	}
}

// registerRemovalDTO is the stable --json shape of `register rm`.
//
// An explicit struct rather than a map literal: --json is an agent-facing
// contract, and a map's keys are invisible to the compiler, so a rename or a
// typo changes the published shape with nothing to catch it (@codex, #638).
// `channel rm` still emits a map literal — same package, same shape, reported
// rather than changed here because it is not this PR's surface.
type registerRemovalDTO struct {
	Ref    string `json:"ref"`
	Status string `json:"status"`
}

// everyAttendee is what a null attendeeUrn means. Rendered rather than left
// blank: an empty cell reads as "missing", and this is an answer.
const everyAttendee = "(every attendee)"

func attendeeLabel(u *string) string {
	if u == nil {
		return everyAttendee
	}
	return *u
}

// parseRegisterRole returns nil for an unset flag (so it is omitted and the
// server's own default applies) rather than defaulting client-side — the
// default is the server's to choose.
func parseRegisterRole(s string) (*gen.RegisterRole, error) {
	if strings.TrimSpace(s) == "" {
		return nil, nil
	}
	switch strings.ToUpper(strings.TrimSpace(s)) {
	case "BOTH":
		r := gen.RegisterRoleBoth
		return &r, nil
	case "POST":
		r := gen.RegisterRolePost
		return &r, nil
	case "WATCH":
		r := gen.RegisterRoleWatch
		return &r, nil
	default:
		return nil, exitcode.Newf(exitcode.Usage, "invalid --role %q (want both, post, or watch)", s)
	}
}

// newCmdRegister builds `hadron channel register` — the participation surface.
func newCmdRegister(f *cmdutil.Factory) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "register <command>",
		Short: "Declare which attendees take part in a Channel",
		Long: `Manage a Channel's register — which attendees TAKE PART in it.

THE REGISTER NEVER GRANTS ACCESS. A row declares intent, not permission
(spec 049, D-2026-09-13-008): what an attendee may actually read or post is
the Channel's HOST MEMORY's decision, and nothing here changes that. Removing
a row does not revoke access, and adding one does not confer it — to change
who can reach a Channel, change access to the memory hosting it.

A register entry names an attendee (a Worker or an Agent), the part it
declares, and whether only messages mentioning the attendee count as new:

  both    takes part in reading and posting   (the server's default)
  post    posts
  watch   reads

The entry belongs to an OWNER — an App or an organization — which is the
context the attendee is named in. An entry whose attendee is unset declares
EVERY attendee in that owner's context, which is far wider than naming one, so
it is spelled --all-attendees rather than left to an omitted flag.

Not to be confused with the worker-name register, which was removed in
hadron-server#1050 and is a different thing entirely.`,
	}
	cmd.AddCommand(newCmdRegisterList(f))
	cmd.AddCommand(newCmdRegisterAdd(f))
	cmd.AddCommand(newCmdRegisterSet(f))
	cmd.AddCommand(newCmdRegisterRm(f))
	return cmd
}

func newCmdRegisterList(f *cmdutil.Factory) *cobra.Command {
	var channel, attendee, owner, org string
	var limit, offset int
	cmd := &cobra.Command{
		Use:     "list [--channel <ref>] [--attendee <ref>] [--owner <ref>]",
		Aliases: []string{"ls"},
		Short:   "List register entries",
		Long: `List register entries you can read.

Every filter is optional and they AND together. A filter is a read, so an
omitted one means "do not narrow by this" — the safe direction.

By default this drains every page, so the listing is the whole slice rather
than a first page that looks like one. Passing --limit or --offset says you
are paging deliberately, and returns exactly that page.

A ref that names nothing MATCHES NOTHING rather than failing, so an empty
result is ambiguous: either nothing is registered, or a ref was mistyped. When
a filter was given and nothing came back, that is said on stderr rather than
left as an empty table.`,
		Example: `  hadron channel register list --channel hrn:node:acme.com:team-shared:chats:team
  hadron channel register list --attendee hrn:worker:acme.com:eng:iris --json`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := f.GraphQLClient()
			if err != nil {
				return err
			}
			var filter *gen.RegisterEntryFilter
			if channel != "" || attendee != "" || owner != "" {
				filter = &gen.RegisterEntryFilter{}
				if channel != "" {
					filter.ChannelRef = &channel
				}
				if attendee != "" {
					filter.AttendeeRef = &attendee
				}
				if owner != "" {
					filter.OwnerRef = &owner
				}
			}
			var orgPtr *string
			if org != "" {
				orgPtr = &org
			}
			// DEFAULT = the whole slice. The server caps a page, so a single
			// call silently truncates and an agent reading "who is registered
			// here" would get a prefix that looks like the answer
			// (@copilot, #638) — the same reason `channel list`, `memory list`
			// and `app list` all drain. An EXPLICIT --limit/--offset means the
			// caller is paging deliberately, so that is honoured as one page
			// rather than overridden.
			paging := cmd.Flags().Changed("limit") || cmd.Flags().Changed("offset")
			entries := []registerEntryDTO{}
			collect := func(items []*gen.RegisterEntriesRegisterEntriesRegisterEntriesPageItemsRegisterEntry) {
				for _, e := range items {
					if e == nil {
						continue
					}
					entries = append(entries, dtoFromRegisterEntry(e.RegisterEntryFields))
				}
			}
			if paging {
				var lim, off *int
				if cmd.Flags().Changed("limit") {
					lim = &limit
				}
				if cmd.Flags().Changed("offset") {
					off = &offset
				}
				resp, err := gen.RegisterEntries(cmd.Context(), client, filter, lim, off, orgPtr)
				if err != nil {
					return api.MapError(err)
				}
				if resp != nil && resp.RegisterEntries != nil {
					collect(resp.RegisterEntries.Items)
				}
			} else {
				items, err := api.CollectAll(func(limit, offset int) ([]*gen.RegisterEntriesRegisterEntriesRegisterEntriesPageItemsRegisterEntry, int, error) {
					resp, err := gen.RegisterEntries(cmd.Context(), client, filter, &limit, &offset, orgPtr)
					if err != nil {
						return nil, 0, api.MapError(err)
					}
					if resp == nil || resp.RegisterEntries == nil {
						return nil, 0, nil
					}
					return resp.RegisterEntries.Items, resp.RegisterEntries.Total, nil
				})
				if err != nil {
					return err
				}
				collect(items)
			}
			// An empty page under ANY narrowing cannot be told from a mistyped
			// ref, because the server matches nothing rather than refusing.
			// --org narrows through its own argument rather than through
			// filter, so keying on `filter != nil` alone let a mistyped
			// organization produce a clean empty result (@codex, #638).
			// Gate on the FACT that something narrowed, not on one carrier.
			narrowed := filter != nil || orgPtr != nil
			if len(entries) == 0 && narrowed {
				fmt.Fprintln(f.IOStreams.ErrOut,
					"note: no entries matched. A ref that names nothing matches nothing rather than failing, so check the filter refs as well as the register.")
			}
			return output.Write(f.IOStreams, f.JSON, entries, func(w io.Writer) error {
				t := output.NewTable(w, "ID", "CHANNEL", "ATTENDEE", "ROLE", "MENTION-ONLY", "OWNER")
				for _, e := range entries {
					t.Row(e.ID, e.ChannelID, attendeeLabel(e.AttendeeURN), e.Role,
						fmt.Sprintf("%t", e.MentionOnly), fmt.Sprintf("%s %s", e.OwnerType, e.OwnerID))
				}
				return t.Flush()
			})
		},
	}
	cmd.Flags().StringVar(&channel, "channel", "", "only entries for this Channel (id or address)")
	cmd.Flags().StringVar(&attendee, "attendee", "", "only entries for this Worker or Agent")
	cmd.Flags().StringVar(&owner, "owner", "", "only entries owned by this App or organization")
	cmd.Flags().StringVar(&org, "org", "", "organization scope (ID or URN)")
	cmd.Flags().IntVar(&limit, "limit", 0, "max results (server default when unset)")
	cmd.Flags().IntVar(&offset, "offset", 0, "results to skip")
	return cmd
}

func newCmdRegisterAdd(f *cmdutil.Factory) *cobra.Command {
	var channel, owner, attendee, role, description string
	var allAttendees, mentionOnly bool
	cmd := &cobra.Command{
		Use:   "add --channel <ref> --owner <ref> (--attendee <ref> | --all-attendees)",
		Short: "Register an attendee on a Channel",
		Long: `Declare that an attendee takes part in a Channel.

This does NOT grant access: the register declares intent, and the host
memory decides what the attendee may read or post.

--attendee names one Worker or Agent. --all-attendees creates the WIDE entry
that applies to every attendee in the owner's context. Exactly one is
required, and the wide form has to be asked for by name.

That is deliberate, and it is the CLI declining to expose a server default
rather than disagreeing with it: the field is genuinely optional on the wire
and an omitted attendee genuinely means "everyone". A forgotten flag that
declares too LITTLE fails loudly and costs seconds, while one that declares
too MUCH succeeds silently — so the default follows the cheap failure.

--role defaults to the server's own default (both) when unset; it is not
defaulted here.`,
		Example: `  hadron channel register add --channel hrn:node:acme.com:team-shared:chats:standup \
    --owner hrn:app:acme.com:eng-team --attendee hrn:worker:acme.com:eng-team:iris --role watch
  hadron channel register add --channel chn_123 --owner hrn:app:acme.com:eng-team --all-attendees`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			// Changed(), not the value: `--attendee=` is "asked for nothing",
			// which must not be read as "did not ask" and silently widen into
			// the every-attendee declaration.
			attendeeGiven := cmd.Flags().Changed("attendee")
			switch {
			case attendeeGiven && allAttendees:
				return exitcode.Newf(exitcode.Usage, "--attendee names one attendee and --all-attendees registers every attendee in the owner's context; pass one or the other")
			case !attendeeGiven && !allAttendees:
				return exitcode.Newf(exitcode.Usage, "specify --attendee <worker-or-agent> to register one attendee, or --all-attendees to register every attendee in the owner's context")
			case attendeeGiven && strings.TrimSpace(attendee) == "":
				return exitcode.Newf(exitcode.Usage, "--attendee needs a Worker or Agent ref; it was given an empty value (use --all-attendees for the every-attendee entry)")
			case cmd.Flags().Changed("role") && strings.TrimSpace(role) == "":
				return exitcode.Newf(exitcode.Usage, "--role needs both, post, or watch; it was given an empty value (omit it entirely to accept the server's default)")
			}
			r, err := parseRegisterRole(role)
			if err != nil {
				return err
			}
			client, err := f.GraphQLClient()
			if err != nil {
				return err
			}
			in := gen.CreateRegisterEntryInput{ChannelRef: channel, OwnerRef: owner, Role: r}
			// Omitted, not null: --all-attendees is the ABSENCE of attendeeRef
			// on the wire, which is what the server reads as "everyone".
			if attendeeGiven {
				in.AttendeeRef = &attendee
			}
			if cmd.Flags().Changed("mention-only") {
				in.MentionOnly = &mentionOnly
			}
			if cmd.Flags().Changed("description") {
				in.Description = &description
			}
			resp, err := gen.CreateRegisterEntry(cmd.Context(), client, &in)
			if err != nil {
				return api.MapError(err)
			}
			if resp == nil || resp.CreateRegisterEntry == nil {
				return exitcode.Newf(exitcode.Unavailable, "the server accepted the registration but returned no entry")
			}
			dto := dtoFromRegisterEntry(resp.CreateRegisterEntry.RegisterEntryFields)
			return output.Write(f.IOStreams, f.JSON, dto, func(w io.Writer) error {
				_, err := fmt.Fprintf(w, "✓ registered %s on channel %s as %s (%s)\n",
					attendeeLabel(dto.AttendeeURN), dto.ChannelID, dto.Role, dto.ID)
				return err
			})
		},
	}
	cmd.Flags().StringVar(&channel, "channel", "", "the Channel (id or address) (required)")
	cmd.Flags().StringVar(&owner, "owner", "", "the owning App or organization (URN or id) (required)")
	cmd.Flags().StringVar(&attendee, "attendee", "", "the Worker or Agent to register")
	cmd.Flags().BoolVar(&allAttendees, "all-attendees", false, "register EVERY attendee in the owner's context (the wide entry)")
	cmd.Flags().StringVar(&role, "role", "", "both, post, or watch (server default when unset)")
	cmd.Flags().BoolVar(&mentionOnly, "mention-only", false, "only messages mentioning the attendee count as new")
	cmd.Flags().StringVar(&description, "description", "", "what this registration is for")
	_ = cmd.MarkFlagRequired("channel")
	_ = cmd.MarkFlagRequired("owner")
	return cmd
}

func newCmdRegisterSet(f *cmdutil.Factory) *cobra.Command {
	var role, description string
	var mentionOnly bool
	cmd := &cobra.Command{
		Use:   "set <entry-id> [--role <r>] [--mention-only[=false]] [--description <s>]",
		Short: "Change a register entry's role, mention-only, or description",
		Long: `Change an existing register entry.

Only the role, the mention-only setting and the description are mutable. The
attendee and the Channel are NOT: moving a registration means removing it and
adding the new one, and this command does not pretend otherwise.

An unset flag is omitted rather than sent as null, so it preserves the
current value instead of clearing it. --mention-only is a true/false flag
here rather than a switch: pass --mention-only=false to turn it off, and
leave it out entirely to leave it alone.`,
		Example: `  hadron channel register set reg_123 --role watch
  hadron channel register set reg_123 --mention-only=false`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			// A set that names no field is a no-op the server would accept,
			// returning the row unchanged and reading as success. Refuse it:
			// the likely cause is a mistyped flag name.
			if !cmd.Flags().Changed("role") && !cmd.Flags().Changed("mention-only") && !cmd.Flags().Changed("description") {
				return exitcode.Newf(exitcode.Usage, "nothing to change: pass at least one of --role, --mention-only or --description")
			}
			// `--role=` passes the no-op guard (it IS changed) but would parse
			// to nil, be omitted, and leave the server preserving the current
			// role while this command printed success (@codex, #638).
			if cmd.Flags().Changed("role") && strings.TrimSpace(role) == "" {
				return exitcode.Newf(exitcode.Usage, "--role needs both, post, or watch; it was given an empty value")
			}
			r, err := parseRegisterRole(role)
			if err != nil {
				return err
			}
			client, err := f.GraphQLClient()
			if err != nil {
				return err
			}
			var in gen.UpdateRegisterEntryInput
			if r != nil {
				in.Role = r
			}
			if cmd.Flags().Changed("mention-only") {
				in.MentionOnly = &mentionOnly
			}
			if cmd.Flags().Changed("description") {
				in.Description = &description
			}
			resp, err := gen.UpdateRegisterEntry(cmd.Context(), client, args[0], &in)
			if err != nil {
				return api.MapError(err)
			}
			if resp == nil || resp.UpdateRegisterEntry == nil {
				return exitcode.Newf(exitcode.Unavailable, "the server accepted the change but returned no entry")
			}
			dto := dtoFromRegisterEntry(resp.UpdateRegisterEntry.RegisterEntryFields)
			return output.Write(f.IOStreams, f.JSON, dto, func(w io.Writer) error {
				_, err := fmt.Fprintf(w, "✓ %s is now %s on channel %s (mention-only: %t)\n",
					attendeeLabel(dto.AttendeeURN), dto.Role, dto.ChannelID, dto.MentionOnly)
				return err
			})
		},
	}
	cmd.Flags().StringVar(&role, "role", "", "both, post, or watch")
	cmd.Flags().BoolVar(&mentionOnly, "mention-only", false, "only messages mentioning the attendee count as new")
	cmd.Flags().StringVar(&description, "description", "", "what this registration is for")
	return cmd
}

func newCmdRegisterRm(f *cmdutil.Factory) *cobra.Command {
	var yes bool
	cmd := &cobra.Command{
		Use:     "rm <entry-id>",
		Aliases: []string{"delete"},
		Short:   "Remove a register entry",
		Long: `Remove a register entry: the attendee no longer DECLARES a part in
that Channel.

THIS DOES NOT REVOKE ACCESS. A register row is intent, never permission
(spec 049, D-2026-09-13-008) — what an attendee may read or post is the
Channel's host memory's decision. An attendee who can reach the host memory
can still read and post after this command reports success. To actually
revoke access, change access to the memory hosting the Channel
(see "hadron memory member" and "hadron memory share").

Where the entry was the wide --all-attendees one it covered every attendee in
the owner's context, so the prompt names which entry is going rather than
only its id.`,
		Example: `  hadron channel register rm reg_123 --yes`,
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := f.GraphQLClient()
			if err != nil {
				return err
			}
			// Read it first so the prompt can name the attendee and role being
			// withdrawn. An id alone does not tell an operator what they are
			// about to remove, and the wide entry is the one where that matters
			// most (review:confirm-prompt-tells-the-truth).
			subject := args[0]
			got, lookupErr := gen.GetRegisterEntry(cmd.Context(), client, args[0])
			if lookupErr != nil {
				// A transport or GraphQL failure is NOT "no such entry", and
				// swallowing it let a transient error be followed by a
				// confirmed delete of an entry we could not identify —
				// silently, under --yes (@copilot, #638). The server reports
				// an unreadable or missing entry as a SUCCESSFUL response
				// carrying a null; only that case is benign.
				return api.MapError(lookupErr)
			}
			if got != nil && got.RegisterEntry != nil {
				e := dtoFromRegisterEntry(got.RegisterEntry.RegisterEntryFields)
				subject = fmt.Sprintf("%s (%s on channel %s)", attendeeLabel(e.AttendeeURN), e.Role, e.ChannelID)
			}
			// NOT ConfirmDeletion: its prompt says "This cannot be undone",
			// which is false — the server sets deletedAt rather than removing
			// the row. Telling an operator a recoverable action is permanent
			// makes them refuse a safe change, the same call `channel rm` made.
			// The prompt must not promise revocation: the register NEVER
			// grants, so an operator told they revoked access would walk away
			// from a door that is still open (@codex, #638).
			if err := cmdutil.Confirm(f.IOStreams, yes,
				fmt.Sprintf("Remove register entry %s? This removes the declared participation only — it does NOT revoke access, which the host memory controls.", subject)); err != nil {
				return err
			}
			resp, err := gen.DeleteRegisterEntry(cmd.Context(), client, args[0])
			if err != nil {
				return api.MapError(err)
			}
			if resp == nil || !resp.DeleteRegisterEntry {
				return exitcode.Newf(exitcode.NotFound, "no register entry %q was removed — it is unreadable or already gone", args[0])
			}
			dto := registerRemovalDTO{Ref: args[0], Status: "removed"}
			return output.Write(f.IOStreams, f.JSON, dto, func(w io.Writer) error {
				_, err := fmt.Fprintf(w, "✓ removed register entry %s\n", args[0])
				return err
			})
		},
	}
	cmd.Flags().BoolVar(&yes, "yes", false, "skip the confirmation prompt")
	return cmd
}
