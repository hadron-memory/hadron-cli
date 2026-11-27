package team

// `team host join|leave|set|hosts|show` (hadron-cli#3239, slice C1 of
// hadron-server#5209, over hadron-server#5259 V1, first in hadron-server
// 0.47.0): make this machine a host of a team App and keep its credential in
// the host credential store (internal/hostcred), take it away again, and read
// or change the platform's record of the App's hosts.
//
// Every request is the signed-in person's, sent without the worker session
// (api.WithoutSession): join and remove are person-only acts the server
// refuses to a worker session (cor:agt:070:07), and none of these commands
// ever sends the stored host key (cor:agt:070:04). The one exception is
// leave's dead-key check (ruling RC3), which presents the stored key to ask
// the server whether it still authenticates, and nothing else.
//
// The key is never printed: not on stdout, stderr, in --json or in an error.
// Server and transport text is never echoed on these paths either; a failure
// is named by its exit code (failureClassHost), as `app key create` does.

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/Khan/genqlient/graphql"
	"github.com/spf13/cobra"

	"github.com/hadron-memory/hadron-cli/internal/api"
	"github.com/hadron-memory/hadron-cli/internal/api/gen"
	"github.com/hadron-memory/hadron-cli/internal/api/gqltypes"
	"github.com/hadron-memory/hadron-cli/internal/cmdutil"
	"github.com/hadron-memory/hadron-cli/internal/exitcode"
	"github.com/hadron-memory/hadron-cli/internal/hostcred"
	"github.com/hadron-memory/hadron-cli/internal/hostid"
	"github.com/hadron-memory/hadron-cli/internal/output"
)

// agentTeamHostsMissing names the server these commands need.
const agentTeamHostsMissing = "this server does not have team App hosts (they need hadron-server 0.47.0 or later, hadron-server#5259)"

const (
	hostLabelMax       = 128
	hostPollIntervalHi = 86400
)

const hostIDRule = "a host id: letters, digits, \".\", \"_\", \":\" or \"-\", starting with a letter or digit, up to 128 characters"

// hostJoinDTO is join's --json. It has no key field, by design.
type hostJoinDTO struct {
	Server   string   `json:"server"`
	AppID    string   `json:"appId"`
	HostID   string   `json:"hostId"`
	Label    string   `json:"label"`
	Replaced bool     `json:"replaced"`
	Next     []string `json:"next"`
}

type hostLeaveDTO struct {
	Server          string `json:"server"`
	AppID           string `json:"appId"`
	HostID          string `json:"hostId"`
	RemovedOnServer bool   `json:"removedOnServer"`
	RemovedLocally  bool   `json:"removedLocally"`
}

// agentTeamHostDTO is one of the platform's AgentTeamHost rows. A null
// pollIntervalSeconds or deliveryEnabled is the default (the host's own
// interval; delivery on).
type agentTeamHostDTO struct {
	ID                  string `json:"id"`
	HostID              string `json:"hostId"`
	Label               string `json:"label"`
	PollIntervalSeconds *int   `json:"pollIntervalSeconds"`
	DeliveryEnabled     *bool  `json:"deliveryEnabled"`
	CreatedAt           string `json:"createdAt"`
	UpdatedAt           string `json:"updatedAt"`
}

func agentTeamHostFrom(h gen.AgentTeamHostFields) agentTeamHostDTO {
	return agentTeamHostDTO{ID: h.Id, HostID: h.HostId, Label: h.Label, PollIntervalSeconds: h.PollIntervalSeconds,
		DeliveryEnabled: h.DeliveryEnabled, CreatedAt: h.CreatedAt, UpdatedAt: h.UpdatedAt}
}

// failureClassHost names a failure by its exit code. Server and transport
// text is never echoed here: a refusal's message could quote a key.
func failureClassHost(code int) string {
	switch code {
	case exitcode.AuthRequired:
		return "not signed in or credentials rejected"
	case exitcode.Forbidden:
		return "not permitted: managing a team App's hosts is for whoever manages the App, signed in as themselves"
	case exitcode.NotFound:
		return "App or host not found or not visible"
	case exitcode.Conflict:
		return "state conflict"
	case exitcode.Usage:
		return "the server refused the input"
	case exitcode.Unavailable:
		return "server or network unavailable"
	}
	return "server error"
}

// hostRefusal maps a failed request to its exit code with fixed text. An old
// server without the operation is named as such.
func hostRefusal(err error, op string) error {
	if api.IsFrameworkValidationOnlyFor(err, op) {
		return exitcode.Newf(exitcode.Error, "%s", agentTeamHostsMissing)
	}
	code := exitcode.FromError(api.MapError(err))
	return exitcode.Newf(code, "%s refused: %s", op, failureClassHost(code))
}

// lostAnswer reports whether a failure may have reached the server with its
// answer lost: a transport-class failure or an internal fault.
func lostAnswer(code int) bool { return code == exitcode.Unavailable || code == exitcode.Error }

// hostApp resolves the App the command acts on, the person's client and the
// App's id. The id is the store's key, with the server.
func hostApp(cmd *cobra.Command, f *cmdutil.Factory) (appScope, graphql.Client, string, string, error) {
	scope, client, err := hostSetupScope(cmd, f)
	if err != nil {
		return scope, nil, "", "", err
	}
	server, err := f.Server()
	if err != nil {
		return scope, nil, "", "", err
	}
	resp, err := gen.TeamAppIdentity(personCtx(cmd.Context()), client, scope.Ref)
	if err != nil {
		return scope, nil, "", "", api.MapError(err)
	}
	if resp == nil || resp.App == nil || resp.App.Id == "" {
		return scope, nil, "", "", exitcode.Newf(exitcode.NotFound, "App not found or not visible")
	}
	return scope, client, server, resp.App.Id, nil
}

// personCtx is the context every request here is sent in: no worker
// session, and marked the person's own write so the worktree attribution
// policy (AuthoredWriteContext) never adds one, even if these mutations are
// ever added to its list. A session header would make the server refuse the
// person-only join and remove (cor:agt:070:07).
func personCtx(ctx context.Context) context.Context {
	return withAsPerson(api.WithoutSession(ctx))
}

// storeErr reports a refusal of the credential store. Its text names a path
// and a fixed reason, never content (hostcred.Error).
func storeErr(err error) error {
	var se *hostcred.Error
	if errors.As(err, &se) {
		return exitcode.Newf(exitcode.Error, "%s", se.Error())
	}
	return exitcode.Newf(exitcode.Error, "host credential store: %v", err)
}

// rejoinHint is the join that recovers a key nobody holds, from a machine that holds nothing for the App: it
// must name the same host, because a bare --replace would re-key whichever host the server derives. The host
// id when one is known; else the label the server derives it from (its value is not shown); else the bare form.
func rejoinHint(hostID string, labelGiven bool) string {
	switch {
	case hostID != "" && hostid.Valid(hostID):
		return "`hadron team host join --replace --host-id " + hostID + "`"
	case labelGiven:
		return "`hadron team host join --replace` with the same --label (value not shown)"
	default:
		return "`hadron team host join --replace`"
	}
}

func newCmdTeamHostJoin(f *cmdutil.Factory) *cobra.Command {
	var label, hostID string
	var replace bool
	cmd := &cobra.Command{
		Use:   "join [--app <ref>] [--label <label>] [--host-id <id>] [--replace]",
		Short: "Make this machine a host of a team App and store its key",
		Long: `Make this machine a host of the App: the server records the host, mints its
App key with host:operate for this host, and the CLI stores the key in this
machine's host credential store, <config dir>/host-credentials.json (mode
0600 in a 0700 directory; on Windows an owner-only access list, and the mode
checks are skipped). The key is never printed: not on stdout, stderr or in
--json. Prints the host id and the commands to run next.

--host-id names the host (default: a slug of --label); --label names it for
people (default: the host id). A machine holds one host per server and App:
while it holds one, join exits 5 before the join request. --replace re-keys the
host this machine holds: the server revokes its other keys and the new key
replaces the stored one; to become another host, ` + "`leave`" + ` first. With
nothing held here, --replace re-keys the host named by --host-id or --label
wherever its keys are, so another machine operating it loses its key.

WHO: whoever manages the App, signed in as themselves (` + "`hadron auth login`" + `). The
request never carries a worker session, so a bound worktree is fine.

A join whose answer is lost is not retried, and may have minted a key
nobody holds: run join --replace with the same --host-id (or --label), which
revokes it; the error names the command. --json prints {server,
appId, hostId, label, replaced, next[]}. Exit codes: 2 bad flags, 3 not
signed in, 4 App not found, 5 already a host (or the host is held by a live
key elsewhere), 7 answer lost, 8 not permitted.`,
		Example: `  hadron team host join --app acme.com:eng-team --label "build box"
  hadron team host join --app acme.com:eng-team --replace`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if cmd.Flags().Changed("label") {
				label = strings.TrimSpace(label)
				if label == "" || utf8.RuneCountInString(label) > hostLabelMax {
					return exitcode.Newf(exitcode.Usage, "--label must be 1 to %d characters", hostLabelMax)
				}
			}
			hostID = strings.TrimSpace(hostID)
			if cmd.Flags().Changed("host-id") && !hostid.Valid(hostID) {
				return exitcode.Newf(exitcode.Usage, "--host-id is %s; the value given is not shown", hostIDRule)
			}
			scope, client, server, appID, err := hostApp(cmd, f)
			if err != nil {
				return err
			}
			store, err := hostcred.Lock()
			if err != nil {
				return storeErr(err)
			}
			defer store.Unlock()
			if held, ok := store.Find(server, appID); ok {
				if !replace {
					return exitcode.Newf(exitcode.Conflict, "this machine already holds host %s for this App on %s; `hadron team host join --replace` re-keys it, `hadron team host leave` gives it up", output.SafeCell(held.HostID), output.SafeCell(server))
				}
				if hostID != "" && hostID != held.HostID {
					return exitcode.Newf(exitcode.Conflict, "this machine holds host %s for this App; --replace re-keys that host only: run `hadron team host leave` before joining as another", output.SafeCell(held.HostID))
				}
				hostID = held.HostID
			}
			var replacePtr *bool
			if replace {
				t := true
				replacePtr = &t
			}
			resp, err := gen.JoinAgentTeamHost(personCtx(cmd.Context()), client, scope.Ref, optStr(hostID), optStr(label), replacePtr)
			if err != nil {
				if api.IsFrameworkValidationOnlyFor(err, "joinAgentTeamHost") {
					return exitcode.Newf(exitcode.Error, "%s", agentTeamHostsMissing)
				}
				code := exitcode.FromError(api.MapError(err))
				if lostAnswer(code) {
					return exitcode.Newf(code, "joinAgentTeamHost was sent and its answer was lost, so a key may exist that nobody holds. It was not retried. Run %s (it revokes the host's other keys). Cause: %s", rejoinHint(hostID, cmd.Flags().Changed("label")), failureClassHost(code))
				}
				return exitcode.Newf(code, "joinAgentTeamHost refused: %s; no key was minted", failureClassHost(code))
			}
			if resp == nil || resp.JoinAgentTeamHost == nil || resp.JoinAgentTeamHost.Host == nil || resp.JoinAgentTeamHost.RawKey == "" {
				return exitcode.Newf(exitcode.Error, "the server's answer carried no key or no host, so a key may exist that nobody holds; run %s", rejoinHint(hostID, cmd.Flags().Changed("label")))
			}
			host := resp.JoinAgentTeamHost.Host.AgentTeamHostFields
			if err := store.Put(hostcred.Entry{Server: server, AppID: appID, HostID: host.HostId, Key: resp.JoinAgentTeamHost.RawKey}); err != nil {
				if errors.Is(err, hostcred.ErrBadEntry) {
					// The answer itself is one this CLI cannot store: the same join gets the same answer, so a
					// newer hadron comes first (blind review of #3283, P2 2).
					return exitcode.Newf(exitcode.Error, "the server answered for host %s with a host id or key this version of hadron cannot store, so the key it minted is held by nobody, and the same join would get the same answer. Install a newer hadron, then run `hadron team host join --replace --host-id <that id>`, which revokes it", output.SafeCell(host.HostId))
				}
				return exitcode.Newf(exitcode.Error, "the key minted for host %s could not be stored, so nobody holds it (%s); fix that, then run %s, which revokes it", output.SafeCell(host.HostId), storeErr(err).Error(), rejoinHint(host.HostId, false))
			}
			appLabel := lazyAppLabel(cmd.Context(), f, scope)
			d := hostJoinDTO{Server: server, AppID: appID, HostID: host.HostId, Label: host.Label, Replaced: replace,
				Next: []string{
					fmt.Sprintf("hadron team host show %s --app %s", host.HostId, scope.Ref),
					fmt.Sprintf("hadron team host assign <worker> --host %s --app %s", host.HostId, scope.Ref),
				}}
			return output.Write(f.IOStreams, f.JSON, d, func(w io.Writer) error {
				verb := "joined"
				if replace {
					verb = "re-keyed"
				}
				if _, err := fmt.Fprintf(w, "✓ %s as host %s (%s) of App %s on %s; its key is stored in %s\nnext:\n", verb, output.SafeCell(d.HostID), output.SafeCell(d.Label), appLabel(), output.SafeCell(server), output.SafeCell(store.Path())); err != nil {
					return err
				}
				for _, n := range d.Next {
					if _, err := fmt.Fprintf(w, "  %s\n", output.SafeCell(n)); err != nil {
						return err
					}
				}
				return nil
			})
		},
	}
	cmd.Flags().StringVar(&label, "label", "", "the host's label for people, 1 to 128 characters (default: the host id)")
	cmd.Flags().StringVar(&hostID, "host-id", "", "the host's id within the App (default: a slug of --label)")
	cmd.Flags().BoolVar(&replace, "replace", false, "re-key the host this machine holds: revoke its other keys and store the new one")
	return cmd
}

func newCmdTeamHostLeave(f *cmdutil.Factory) *cobra.Command {
	var yes bool
	cmd := &cobra.Command{
		Use:   "leave [--app <ref>] [--yes]",
		Short: "Stop this machine being a host of a team App and delete its key",
		Long: `Remove the host this machine holds for the App: the server revokes its keys,
ends its registration, removes its assignments and settles its pending
operations as failed; then the CLI deletes the stored key. Prompts on a
terminal; --yes is required otherwise.

The server goes first. When it refuses (not permitted, a worker of the host
has an open session) or cannot be reached, the stored key is kept and the
command exits with the refusal's code: a host the platform still holds must
not lose its only credential. A host with an open session is refused with
the sessions' workers named; end those sessions first (there is no force).
When the server no longer has the host, the stored key is presented once to
ask whether it still authenticates: only a key the server refuses is deleted
(exit 0); otherwise the entry is kept (exit 4). With the key, the host's
local state directory is deleted, so a later host of the same id starts
clean.

WHO: whoever manages the App, signed in as themselves; the request never
carries a worker session or the stored key. --json prints {server, appId,
hostId, removedOnServer, removedLocally}. Exit codes: 2 no --yes off a
terminal, 3 not signed in, 4 no host held here or on the server, 5 open
sessions, 7 server unreachable or answer lost, 8 not permitted.`,
		Example: "  hadron team host leave --app acme.com:eng-team --yes",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			scope, client, server, appID, err := hostApp(cmd, f)
			if err != nil {
				return err
			}
			store, err := hostcred.Lock()
			if err != nil {
				return storeErr(err)
			}
			defer store.Unlock()
			entry, ok := store.Find(server, appID)
			if !ok {
				return exitcode.Newf(exitcode.NotFound, "this machine holds no host of this App on %s", output.SafeCell(server))
			}
			if err := cmdutil.ConfirmDeletion(f.IOStreams, yes, fmt.Sprintf("host %s of this App (the server revokes its keys)", output.SafeCell(entry.HostID))); err != nil {
				return err
			}
			ctx := personCtx(cmd.Context())
			d := hostLeaveDTO{Server: server, AppID: appID, HostID: entry.HostID}
			resp, err := gen.RemoveAgentTeamHost(ctx, client, scope.Ref, entry.HostID)
			switch {
			case err != nil && api.HasErrorCode(err, "HOST_NOT_FOUND"):
				if !storedKeyRevoked(ctx, f, entry) {
					return exitcode.Newf(exitcode.NotFound, "the server has no host %s for this App, but the stored key still authenticates or could not be checked, so it is kept", output.SafeCell(entry.HostID))
				}
			case err != nil && api.HasErrorCode(err, "AGENT_TEAM_HOST_HAS_OPEN_SESSIONS"):
				return exitcode.Newf(exitcode.Conflict, "host %s has a worker with an open session%s; end it first (there is no force). The stored key is kept", output.SafeCell(entry.HostID), openSessionNames(ctx, client, scope.Ref, entry.HostID))
			case err != nil:
				if api.IsFrameworkValidationOnlyFor(err, "removeAgentTeamHost") {
					return exitcode.Newf(exitcode.Error, "%s; the stored key is kept", agentTeamHostsMissing)
				}
				code := exitcode.FromError(api.MapError(err))
				if lostAnswer(code) {
					return exitcode.Newf(code, "removeAgentTeamHost was sent and its answer was lost, so host %s may or may not be removed; the stored key is kept. Run `hadron team host leave` again. Cause: %s", output.SafeCell(entry.HostID), failureClassHost(code))
				}
				return exitcode.Newf(code, "removeAgentTeamHost refused: %s; the stored key is kept", failureClassHost(code))
			case resp == nil || !resp.RemoveAgentTeamHost:
				return exitcode.Newf(exitcode.Error, "the server did not confirm removing host %s; the stored key is kept", output.SafeCell(entry.HostID))
			default:
				d.RemovedOnServer = true
			}
			if _, err := store.Remove(server, appID); err != nil {
				return exitcode.Newf(exitcode.Error, "host %s is off the server but its stored key, now revoked, could not be deleted (%s); run `hadron team host leave` again", output.SafeCell(entry.HostID), storeErr(err).Error())
			}
			d.RemovedLocally = true
			// The host's state directory goes with it, so a later host of this id is not refused by its claim.
			// The leave has already happened; a directory that cannot be removed is said, not an exit.
			if _, err := releaseHostState(entry); err != nil {
				_, _ = fmt.Fprintf(f.IOStreams.ErrOut, "warning: host %s is left, but its state directory could not be removed (%v); remove it before another host of this id runs here\n", output.SafeCell(entry.HostID), err)
			}
			appLabel := lazyAppLabel(cmd.Context(), f, scope)
			return output.Write(f.IOStreams, f.JSON, d, func(w io.Writer) error {
				if _, err := fmt.Fprintf(w, "app: %s\n", appLabel()); err != nil {
					return err
				}
				if d.RemovedOnServer {
					_, err := fmt.Fprintf(w, "✓ removed host %s from the App on %s and deleted its stored key\n", output.SafeCell(d.HostID), output.SafeCell(server))
					return err
				}
				_, err := fmt.Fprintf(w, "✓ the server had no host %s any more and refuses its key; deleted the stored key\n", output.SafeCell(d.HostID))
				return err
			})
		},
	}
	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "skip the confirmation prompt (required when not on a terminal)")
	return cmd
}

// storedKeyRevoked presents the stored key, once, to ask whether it still
// authenticates (ruling RC3). Only REFUSED proves it revoked; an error, an
// authenticated answer or anything else keeps the entry.
func storedKeyRevoked(ctx context.Context, f *cmdutil.Factory, e hostcred.Entry) bool {
	keyClient, err := api.NewClient(e.Server, e.Key, f.HTTPClient, api.WithCredentialSent(f.Credentials(), api.CredentialHostKey))
	if err != nil {
		return false
	}
	resp, err := gen.HostCredentialStatus(api.WithoutSession(ctx), keyClient)
	if err != nil || resp == nil {
		return false
	}
	return resp.AuthenticationStatus == gen.AuthenticationStatusRefused
}

// openSessionNames names, best effort, the host's workers with an open
// session (ruling RC4: the refusal itself carries no names).
func openSessionNames(ctx context.Context, client graphql.Client, appRef, hostID string) string {
	resp, err := gen.HostDesiredState(ctx, client, appRef, hostID)
	if err != nil || resp == nil || resp.HostDesiredState == nil {
		return ""
	}
	var names []string
	for _, w := range resp.HostDesiredState.Workers {
		if w != nil && w.OpenSessionId != nil {
			names = append(names, output.SafeCell(w.Name))
		}
	}
	if len(names) == 0 {
		return ""
	}
	sort.Strings(names)
	return " (" + strings.Join(names, ", ") + ")"
}

func newCmdTeamHostSet(f *cmdutil.Factory) *cobra.Command {
	var label, instructionRaw string
	var poll int
	var defaultPoll, delivery, noDelivery, defaultDelivery bool
	cmd := &cobra.Command{
		Use:   "set <host> [--label <label>] [--poll-interval <s> | --default-poll-interval] [--delivery | --no-delivery | --default-delivery] [--instruction <kind>:<ref>]",
		Short: "Change a team App host's label, check-in interval or wake delivery",
		Long: `Change the platform's settings for one of the App's hosts. A flag left out
keeps the setting. --poll-interval sets the seconds between the host's
check-ins (1 to 86400) and --default-poll-interval clears it to the host's
own default; --delivery and --no-delivery turn the host's wake delivery on
or off and --default-delivery clears it to the default (on). Contradicting
flags exit 2 before any request. --json prints the host as ` + "`show`" + ` does.

` + hostSetupWriteHelp,
		Example: `  hadron team host set orca1 --poll-interval 30
  hadron team host set orca1 --no-delivery --label "orca1 (paused)"`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			hostID := strings.TrimSpace(args[0])
			if !hostid.Valid(hostID) {
				return exitcode.Newf(exitcode.Usage, "<host> is %s; the value given is not shown", hostIDRule)
			}
			fl := cmd.Flags()
			input := gqltypes.AgentTeamHostUpdateInput{HostID: hostID}
			if fl.Changed("label") {
				label = strings.TrimSpace(label)
				if label == "" || utf8.RuneCountInString(label) > hostLabelMax {
					return exitcode.Newf(exitcode.Usage, "--label must be 1 to %d characters", hostLabelMax)
				}
				input.Label = &label
			}
			switch {
			case fl.Changed("poll-interval") && defaultPoll:
				return exitcode.Newf(exitcode.Usage, "--poll-interval and --default-poll-interval contradict each other")
			case fl.Changed("poll-interval"):
				if poll < 1 || poll > hostPollIntervalHi {
					return exitcode.Newf(exitcode.Usage, "--poll-interval must be 1 to %d seconds", hostPollIntervalHi)
				}
				input.PollIntervalSeconds = &poll
			case defaultPoll:
				input.ClearPollInterval = true
			}
			n := 0
			for _, b := range []bool{delivery, noDelivery, defaultDelivery} {
				if b {
					n++
				}
			}
			switch {
			case n > 1:
				return exitcode.Newf(exitcode.Usage, "--delivery, --no-delivery and --default-delivery contradict each other: give one")
			case delivery || noDelivery:
				on := delivery
				input.DeliveryEnabled = &on
			case defaultDelivery:
				input.ClearDelivery = true
			}
			if input.Label == nil && input.PollIntervalSeconds == nil && !input.ClearPollInterval && input.DeliveryEnabled == nil && !input.ClearDelivery {
				return exitcode.Newf(exitcode.Usage, "nothing to change: give --label, a poll-interval flag or a delivery flag")
			}
			claim, instructed, err := staffingInstruction(cmd, f, instructionRaw)
			if err != nil {
				return err
			}
			scope, client, err := hostSetupScope(cmd, f)
			if err != nil {
				return err
			}
			input.AppRef = scope.Ref
			var host *gen.AgentTeamHostFields
			if claim == nil {
				resp, err := gen.UpdateAgentTeamHost(personCtx(cmd.Context()), client, &input)
				if err != nil {
					return hostRefusal(err, "updateAgentTeamHost")
				}
				if resp != nil && resp.UpdateAgentTeamHost != nil {
					host = &resp.UpdateAgentTeamHost.AgentTeamHostFields
				}
			} else {
				ictx, _, err := instructed(cmd.Context(), "", scope.Ref)
				if err != nil {
					return err
				}
				resp, err := gen.UpdateAgentTeamHostInstructed(ictx, client, &input, claim)
				if err != nil {
					return hostRefusal(err, "updateAgentTeamHost")
				}
				if resp != nil && resp.UpdateAgentTeamHost != nil {
					host = &resp.UpdateAgentTeamHost.AgentTeamHostFields
				}
			}
			if host == nil {
				return exitcode.Newf(exitcode.Error, "server returned no host; outcome unknown, run `hadron team host show <host>` before retrying (the host given is not shown)")
			}
			return writeAgentTeamHost(f, lazyAppLabel(cmd.Context(), f, scope), agentTeamHostFrom(*host))
		},
	}
	cmd.Flags().StringVar(&label, "label", "", "the host's label for people, 1 to 128 characters")
	cmd.Flags().IntVar(&poll, "poll-interval", 0, "seconds between the host's check-ins, 1 to 86400")
	cmd.Flags().BoolVar(&defaultPoll, "default-poll-interval", false, "clear the check-in interval to the host's own default")
	cmd.Flags().BoolVar(&delivery, "delivery", false, "turn the host's wake delivery on")
	cmd.Flags().BoolVar(&noDelivery, "no-delivery", false, "turn the host's wake delivery off")
	cmd.Flags().BoolVar(&defaultDelivery, "default-delivery", false, "clear wake delivery to the default (on)")
	cmd.Flags().StringVar(&instructionRaw, "instruction", "", instructionFlagUsage)
	return cmd
}

func writeAgentTeamHost(f *cmdutil.Factory, appLabel func() string, d agentTeamHostDTO) error {
	return output.Write(f.IOStreams, f.JSON, d, func(w io.Writer) error {
		_, err := fmt.Fprintf(w, "app: %s\nhost: %s\nlabel: %s\npoll interval: %s\ndelivery: %s\ncreated: %s\nupdated: %s\n",
			appLabel(), output.SafeCell(d.HostID), output.SafeCell(d.Label), pollText(d.PollIntervalSeconds), deliveryText(d.DeliveryEnabled),
			output.SafeCell(d.CreatedAt), output.SafeCell(d.UpdatedAt))
		return err
	})
}

func pollText(p *int) string {
	if p == nil {
		return "default"
	}
	return fmt.Sprintf("%ds", *p)
}

func deliveryText(b *bool) string {
	switch {
	case b == nil:
		return "default (on)"
	case *b:
		return "on"
	}
	return "off"
}

func newCmdTeamHostHosts(f *cmdutil.Factory) *cobra.Command {
	return &cobra.Command{
		Use:   "hosts [--app <ref>]",
		Short: "List the platform's record of a team App's hosts and their settings",
		Long: `List the App's hosts as the platform records them (made by ` + "`join`" + `): each
host's id, label, check-in interval and wake delivery, by host id. A removed
host is left out. This is the platform's record, not the hosts'
registrations: ` + "`list`" + ` shows those (which workers each host can run, and
whether it is live). Whoever reads the App's tasks and events may; the
request carries the signed-in person's credential and no worker session.
--json prints an array of {id, hostId, label, pollIntervalSeconds,
deliveryEnabled, createdAt, updatedAt} ([] when there are none; a null
setting is the default).`,
		Example: "  hadron team host hosts --app acme.com:eng-team --json",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			scope, client, err := hostSetupScope(cmd, f)
			if err != nil {
				return err
			}
			resp, err := gen.AgentTeamHosts(personCtx(cmd.Context()), client, scope.Ref)
			if err != nil {
				return hostRefusal(err, "agentTeamHosts")
			}
			rows := []agentTeamHostDTO{}
			if resp != nil {
				for _, h := range resp.AgentTeamHosts {
					if h != nil {
						rows = append(rows, agentTeamHostFrom(h.AgentTeamHostFields))
					}
				}
			}
			label := lazyAppLabel(cmd.Context(), f, scope)
			return output.Write(f.IOStreams, f.JSON, rows, func(w io.Writer) error {
				if _, err := fmt.Fprintf(w, "app: %s\n", label()); err != nil {
					return err
				}
				if len(rows) == 0 {
					_, err := fmt.Fprintln(w, "no hosts (`hadron team host join` makes this machine one)")
					return err
				}
				t := output.NewTable(w, "HOST", "LABEL", "POLL", "DELIVERY", "UPDATED")
				for _, r := range rows {
					t.Row(output.SafeCell(r.HostID), output.SafeCell(r.Label), pollText(r.PollIntervalSeconds), deliveryText(r.DeliveryEnabled), output.SafeCell(r.UpdatedAt))
				}
				return t.Flush()
			})
		},
	}
}

func newCmdTeamHostShow(f *cmdutil.Factory) *cobra.Command {
	return &cobra.Command{
		Use:   "show <host> [--app <ref>]",
		Short: "Show one of a team App's hosts and its settings",
		Long: `Show the platform's record of one of the App's hosts: its id, label,
check-in interval and wake delivery. A host the App does not have, or has
removed, exits 4. Whoever reads the App's tasks and events may; the request
carries the signed-in person's credential and no worker session. --json
prints {id, hostId, label, pollIntervalSeconds, deliveryEnabled, createdAt,
updatedAt} (a null setting is the default).`,
		Example: "  hadron team host show orca1 --app acme.com:eng-team",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			hostID := strings.TrimSpace(args[0])
			if !hostid.Valid(hostID) {
				return exitcode.Newf(exitcode.Usage, "<host> is %s; the value given is not shown", hostIDRule)
			}
			scope, client, err := hostSetupScope(cmd, f)
			if err != nil {
				return err
			}
			resp, err := gen.AgentTeamHost(personCtx(cmd.Context()), client, scope.Ref, hostID)
			if err != nil {
				return hostRefusal(err, "agentTeamHost")
			}
			if resp == nil || resp.AgentTeamHost == nil {
				return exitcode.Newf(exitcode.NotFound, "host not found")
			}
			return writeAgentTeamHost(f, lazyAppLabel(cmd.Context(), f, scope), agentTeamHostFrom(resp.AgentTeamHost.AgentTeamHostFields))
		},
	}
}
