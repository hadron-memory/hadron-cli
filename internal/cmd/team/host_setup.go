package team

// `team host assign|unassign|setup` — the platform's record of which workers
// each host of a team App runs, and the per-App switch that makes
// registration and starts follow it (hadron-cli#2080 over hadron-server#4241,
// cor:agt:070:01, the change Proposed). Every field a host acts on is a name
// token, never a path, a command or a credential (cor:agt:070:03).
//
// Reads carry no worker session. The three writes are the App manager's
// (cor:dmo:050:12): without --instruction they go out as the signed-in person
// even from a bound worktree; with it they claim the holder's instruction
// through the worktree's session (the C9 / staffing pattern), and are never
// retried as the person after a refusal.

import (
	"fmt"
	"io"
	"regexp"
	"strings"

	"github.com/Khan/genqlient/graphql"
	"github.com/spf13/cobra"

	"github.com/hadron-memory/hadron-cli/internal/api"
	"github.com/hadron-memory/hadron-cli/internal/api/gen"
	"github.com/hadron-memory/hadron-cli/internal/cmdutil"
	"github.com/hadron-memory/hadron-cli/internal/exitcode"
	"github.com/hadron-memory/hadron-cli/internal/output"
)

// The server's shapes (hadron-server src/lib/hostOperations, #4241), checked
// here only to refuse before a request what the server would refuse anyway.
// The server's refusal is still mapped when it disagrees.
var (
	hostSetupTokenRe  = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._/@+-]{0,255}$`)
	hostSetupHostIDRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:-]{0,127}$`)
	hostSetupFamilyRe = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,63}$`)
)

// hostSetupProfileTokenRe is the server's launcher-profile value rule (S1b,
// hadron-server#4230, LAUNCHER_PROFILE_TOKEN): letters, digits and . _ -,
// starting with a letter or digit, at most 128 characters. It applies to the
// model, the effort and the account profile alike: no slash, space or shell
// character, because the host builds a launch command from them. A value the
// server would refuse BAD_LAUNCHER_PROFILE is refused here before any request.
var hostSetupProfileTokenRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$`)

const hostSetupProfileTokenRule = "a profile token: letters, digits, \".\", \"_\" or \"-\", starting with a letter or digit, at most 128 characters"

func hostSetupToken(s string) bool {
	return hostSetupTokenRe.MatchString(s) && !strings.Contains(s, "..") && !strings.Contains(s, "//") && !strings.HasSuffix(s, "/")
}

const hostSetupTokenRule = "a name token: letters, digits and . _ / @ + -, starting with a letter or digit, no \"..\", \"//\" or trailing \"/\""

const hostSetupWriteHelp = `WHO: whoever manages the App. Without --instruction the write is the
signed-in person's, even in a bound worktree. A worker session is never an
App key's: a key is refused.

` + instructionHelp

type hostAssignmentDTO struct {
	ID         string  `json:"id"`
	HostID     string  `json:"hostId"`
	WorkerID   string  `json:"workerId"`
	WorkerName *string `json:"workerName"`
	Workdir    string  `json:"workdir"`
	Family     string  `json:"family"`
	Model      *string `json:"model"`
	// Effort and AccountProfile are the launcher profile the assignment records
	// (S1b, hadron-server#4230): a profile token, or null when unset. The keys are
	// always present, so a consumer can tell "unset" from "an older CLI".
	Effort         *string `json:"effort"`
	AccountProfile *string `json:"accountProfile"`
	CreatedAt      string  `json:"createdAt"`
	UpdatedAt      string  `json:"updatedAt"`
}

func hostAssignmentDTOFrom(a *gen.HostSetupAssignmentFields) hostAssignmentDTO {
	d := hostAssignmentDTO{ID: a.Id, HostID: a.HostId, WorkerID: a.WorkerId, Workdir: a.Workdir, Family: a.Family, Model: a.Model, CreatedAt: a.CreatedAt, UpdatedAt: a.UpdatedAt}
	if a.Worker != nil {
		n := a.Worker.Name
		d.WorkerName = &n
	}
	return d
}

// hostProfileAvailable and hostProfileUnavailable say whether `team host setup`
// could read the S1b launcher profile: a server that predates it refuses the
// profile-bearing document at validation, and the read then falls back to the
// S1a document (assignments without effort and accountProfile, which read as
// null in --json), saying so once.
const (
	hostProfileAvailable   = "available"
	hostProfileUnavailable = "unavailable"
)

type hostSetupDTO struct {
	AppRef string `json:"appRef"`
	Source string `json:"source"`
	// Profile is "available", or "unavailable" when the server predates S1b
	// (hadron-server#4230) and the effort and accountProfile of every assignment
	// below are therefore UNKNOWN (null), not unset.
	Profile     string              `json:"profile"`
	HostID      *string             `json:"hostId"`
	Assignments []hostAssignmentDTO `json:"assignments"`
}

type hostSetupSourceDTO struct {
	AppRef string `json:"appRef"`
	Source string `json:"source"`
}

type hostUnassignDTO struct {
	AppRef  string `json:"appRef"`
	HostID  string `json:"hostId"`
	Worker  string `json:"worker"`
	Removed bool   `json:"removed"`
}

func assignmentWho(d hostAssignmentDTO) string {
	if d.WorkerName != nil {
		return output.SafeCell(*d.WorkerName)
	}
	return output.SafeCell(d.WorkerID)
}

func newCmdTeamHostAssign(f *cmdutil.Factory) *cobra.Command {
	var hostID, workdir, family, model, effort, accountProfile, instructionRaw string
	var replace bool
	cmd := &cobra.Command{
		Use:   "assign <worker> --host <host-id> --workdir <token> --family <family> [--model <id>] [--effort <token>] [--account-profile <token>] [--replace] [--instruction <kind>:<ref>]",
		Short: "Assign a worker to a host of the App, as its manager",
		Long: `Record that a host runs a worker: the workdir it runs in there, its model
family and, optionally, its launcher profile: --model, --effort and
--account-profile (S1b, hadron-server#4230). The workdir is the NAME the host
knows the directory by (an Orca worktree name), ` + hostSetupTokenRule + `;
never a path. The three profile values are ` + hostSetupProfileTokenRule + `:
the host builds its launch command from a fixed template for the family, so a
value is never a path, a command or a credential. While the App's switch is
SERVER a worker start carries the ones that are set. A worker may be assigned
to several hosts. The record changes nothing until the App's switch is SERVER
(` + "`team host setup --source SERVER`" + `).

--effort and --account-profile need a server with the S1b launcher profile; on
one that predates it the command refuses by name (exit 2) and writes nothing,
never an assignment that quietly lacks them. Without them the command works on
any server.

A profile flag you leave out is OMITTED from the request, never sent as null.
On a new assignment that leaves the value unset; with --replace the server
rewrites the WHOLE assignment, so a profile value you do not give again is
cleared (the model included). Pass every value you want kept.

--replace changes an existing assignment of this worker to this host; without
it that is refused HOST_ASSIGNMENT_EXISTS (exit 5). The worker is an id, URN
or name within the App (--app, the App context or the worktree binding). A
retired worker is refused WORKER_RETIRED (5), an unknown one
WORKER_NOT_FOUND (4), a field that is not its token shape
BAD_HOST_ASSIGNMENT_ARGS (2), a profile value or a family with no launcher
BAD_LAUNCHER_PROFILE (2), and a caller who does not manage the App
HOST_ASSIGNMENT_NOT_PERMITTED (8). A refusal writes nothing. --json prints the
assignment.

` + hostSetupWriteHelp,
		Example: `  hadron team host assign Jonas --host orca1 --workdir jonas-3 --family claude --model opus-5.5
  hadron team host assign Jonas --host orca1 --workdir jonas-3 --family codex --model gpt-5 --effort high --account-profile team-a
  hadron team host assign Jonas --host orca1 --workdir jonas-4 --family claude --model opus-5.5 --replace`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			worker := strings.TrimSpace(args[0])
			hostID, workdir, family = strings.TrimSpace(hostID), strings.TrimSpace(workdir), strings.TrimSpace(family)
			if worker == "" {
				return exitcode.Newf(exitcode.Usage, "worker reference is empty")
			}
			if hostID == "" || workdir == "" || family == "" {
				return exitcode.Newf(exitcode.Usage, "--host, --workdir and --family are required")
			}
			if !hostSetupHostIDRe.MatchString(hostID) {
				return exitcode.Newf(exitcode.Usage, "--host is a host id: letters, digits, \".\", \"_\", \":\" or \"-\", up to 128 characters; got %q", hostID)
			}
			if !hostSetupToken(workdir) {
				return exitcode.Newf(exitcode.Usage, "--workdir is %s, the name the host knows the directory by, never a path; got %q", hostSetupTokenRule, workdir)
			}
			// The server's family is a lowercase token (it normalizes case); send
			// exactly the value that was validated.
			family = strings.ToLower(family)
			if !hostSetupFamilyRe.MatchString(family) {
				return exitcode.Newf(exitcode.Usage, "--family is a model family token such as claude or codex; got %q", family)
			}
			input := gen.AssignHostWorkerInput{HostId: hostID, Workdir: workdir, Family: family, Worker: worker}
			if cmd.Flags().Changed("model") {
				model = strings.TrimSpace(model)
				if !hostSetupProfileTokenRe.MatchString(model) {
					return exitcode.Newf(exitcode.Usage, "--model is %s; got %q", hostSetupProfileTokenRule, model)
				}
				input.Model = &model
			}
			// A profile flag that was not given leaves its input OMITTED (a pointer
			// that stays nil); a given one is checked with the server's own rule first.
			for _, p := range []struct {
				flag string
				val  *string
				dst  **string
			}{{"effort", &effort, &input.Effort}, {"account-profile", &accountProfile, &input.AccountProfile}} {
				if !cmd.Flags().Changed(p.flag) {
					continue
				}
				v := strings.TrimSpace(*p.val)
				if !hostSetupProfileTokenRe.MatchString(v) {
					return exitcode.Newf(exitcode.Usage, "--%s is %s; got %q", p.flag, hostSetupProfileTokenRule, v)
				}
				*p.dst = &v
			}
			if replace {
				input.Replace = &replace
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
			ctx := cmd.Context()
			// Without a profile flag the S1a documents are used, which a server that
			// predates S1b still validates (the answer then has no profile, and
			// none is claimed: a new or replaced assignment without profile flags
			// has none). With one, the profile-bearing documents are used, and a
			// server that cannot take them is refused by name, writing nothing.
			wantsProfile := input.Effort != nil || input.AccountProfile != nil
			var a *gen.HostSetupAssignmentFields
			var effortOut, accountOut *string
			var callErr error
			switch {
			case claim == nil && !wantsProfile:
				resp, err := gen.TeamHostAssign(ctx, client, &input)
				callErr = err
				if err == nil && resp != nil && resp.AssignHostWorker != nil {
					a = &resp.AssignHostWorker.HostSetupAssignmentFields
				}
			case claim == nil:
				resp, err := gen.TeamHostAssignWithProfile(ctx, client, &input)
				callErr = err
				if err == nil && resp != nil && resp.AssignHostWorker != nil {
					a, effortOut, accountOut = &resp.AssignHostWorker.HostSetupAssignmentFields, resp.AssignHostWorker.Effort, resp.AssignHostWorker.AccountProfile
				}
			default:
				ictx, _, err := instructed(ctx, "", scope.Ref)
				if err != nil {
					return err
				}
				if wantsProfile {
					resp, err := gen.TeamHostAssignWithProfileInstructed(ictx, client, &input, claim)
					callErr = err
					if err == nil && resp != nil && resp.AssignHostWorker != nil {
						a, effortOut, accountOut = &resp.AssignHostWorker.HostSetupAssignmentFields, resp.AssignHostWorker.Effort, resp.AssignHostWorker.AccountProfile
					}
				} else {
					resp, err := gen.TeamHostAssignInstructed(ictx, client, &input, claim)
					callErr = err
					if err == nil && resp != nil && resp.AssignHostWorker != nil {
						a = &resp.AssignHostWorker.HostSetupAssignmentFields
					}
				}
			}
			if callErr != nil {
				if wantsProfile && serverLacksProfile(callErr) {
					return exitcode.Newf(exitcode.Usage, "--effort and --account-profile need a server with the S1b launcher profile (hadron-server#4230); this server predates it. Nothing was written. Assign without them, or ask for the server to be upgraded")
				}
				return api.MapErrorKeepingCode(callErr)
			}
			if a == nil {
				return exitcode.Newf(exitcode.Error, "server returned no assignment; outcome unknown, run `hadron team host setup` before retrying")
			}
			d := hostAssignmentDTOFrom(a)
			d.Effort, d.AccountProfile = effortOut, accountOut
			label := lazyAppLabel(ctx, f, scope)
			return output.Write(f.IOStreams, f.JSON, d, func(w io.Writer) error {
				_, err := fmt.Fprintf(w, "app: %s\n✓ assigned %s to host %s: workdir %s, family %s, model %s, effort %s, account profile %s\n", label(), assignmentWho(d), output.SafeCell(d.HostID), output.SafeCell(d.Workdir), output.SafeCell(d.Family), output.SafeCell(stopValue(d.Model)), output.SafeCell(stopValue(d.Effort)), output.SafeCell(stopValue(d.AccountProfile)))
				return err
			})
		},
	}
	cmd.Flags().StringVar(&hostID, "host", "", "the host's id within the App (required)")
	cmd.Flags().StringVar(&workdir, "workdir", "", "the name the host knows the working directory by (required; a token, never a path)")
	cmd.Flags().StringVar(&family, "family", "", "the model family it runs as, e.g. claude or codex (required)")
	cmd.Flags().StringVar(&model, "model", "", "the model, a profile token (optional)")
	cmd.Flags().StringVar(&effort, "effort", "", "the effort the launcher runs the model at, a profile token (optional)")
	cmd.Flags().StringVar(&accountProfile, "account-profile", "", "the host-local account the launcher runs under, a profile token (optional; never a path or a credential)")
	cmd.Flags().BoolVar(&replace, "replace", false, "change an existing assignment of this worker to this host")
	cmd.Flags().StringVar(&instructionRaw, "instruction", "", instructionFlagUsage)
	return cmd
}

// serverLacksProfile reports whether err is a server that predates the S1b
// launcher profile refusing it at validation: the profile fields are not on its
// assignment type (a selection) or not on its input type (an argument). It reads
// the structured GraphQL envelope; any other failure is not this one.
func serverLacksProfile(err error) bool {
	return api.IsFrameworkValidationOnlyFor(err, "effort", "accountProfile") ||
		api.IsUnknownInputFieldOnly(err, "AssignHostWorkerInput", "effort", "accountProfile")
}

func newCmdTeamHostUnassign(f *cmdutil.Factory) *cobra.Command {
	var hostID, instructionRaw string
	cmd := &cobra.Command{
		Use:   "unassign <worker> --host <host-id> [--instruction <kind>:<ref>]",
		Short: "Remove a worker's assignment to a host, as the App's manager",
		Long: `Remove the record that a host runs a worker. It answers "removed" or "there
was none": neither is an error, so a repeat is safe. A retired worker's
assignment is removable. --json prints {appRef, hostId, worker, removed}.

` + hostSetupWriteHelp,
		Example: "  hadron team host unassign Jonas --host orca1",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			worker, hostID := strings.TrimSpace(args[0]), strings.TrimSpace(hostID)
			if worker == "" {
				return exitcode.Newf(exitcode.Usage, "worker reference is empty")
			}
			if hostID == "" {
				return exitcode.Newf(exitcode.Usage, "--host is required")
			}
			if !hostSetupHostIDRe.MatchString(hostID) {
				return exitcode.Newf(exitcode.Usage, "--host is a host id: letters, digits, \".\", \"_\", \":\" or \"-\", up to 128 characters; got %q", hostID)
			}
			claim, instructed, err := staffingInstruction(cmd, f, instructionRaw)
			if err != nil {
				return err
			}
			scope, client, err := hostSetupScope(cmd, f)
			if err != nil {
				return err
			}
			input := gen.UnassignHostWorkerInput{AppRef: scope.Ref, HostId: hostID, Worker: worker}
			ctx := cmd.Context()
			var removed bool
			if claim == nil {
				resp, err := gen.TeamHostUnassign(ctx, client, &input)
				if err != nil {
					return api.MapErrorKeepingCode(err)
				}
				// A missing or null answer is not "there was none": the
				// outcome is unknown (hadron-cli#2090).
				if resp == nil || resp.UnassignHostWorker == nil {
					return exitcode.Newf(exitcode.Error, "server returned no answer; outcome unknown, run `hadron team host setup` before retrying")
				}
				removed = *resp.UnassignHostWorker
			} else {
				ictx, _, err := instructed(ctx, "", scope.Ref)
				if err != nil {
					return err
				}
				resp, err := gen.TeamHostUnassignInstructed(ictx, client, &input, claim)
				if err != nil {
					return api.MapErrorKeepingCode(err)
				}
				// A missing or null answer is not "there was none": the
				// outcome is unknown (hadron-cli#2090).
				if resp == nil || resp.UnassignHostWorker == nil {
					return exitcode.Newf(exitcode.Error, "server returned no answer; outcome unknown, run `hadron team host setup` before retrying")
				}
				removed = *resp.UnassignHostWorker
			}
			d := hostUnassignDTO{AppRef: scope.Ref, HostID: hostID, Worker: worker, Removed: removed}
			label := lazyAppLabel(ctx, f, scope)
			return output.Write(f.IOStreams, f.JSON, d, func(w io.Writer) error {
				verb := "there was no assignment of %s to host %s to remove"
				if removed {
					verb = "✓ removed the assignment of %s to host %s"
				}
				_, err := fmt.Fprintf(w, "app: %s\n"+verb+"\n", label(), output.SafeCell(worker), output.SafeCell(hostID))
				return err
			})
		},
	}
	cmd.Flags().StringVar(&hostID, "host", "", "the host's id within the App (required)")
	cmd.Flags().StringVar(&instructionRaw, "instruction", "", instructionFlagUsage)
	return cmd
}

func newCmdTeamHostSetup(f *cmdutil.Factory) *cobra.Command {
	var hostID, source, instructionRaw string
	cmd := &cobra.Command{
		Use:   "setup [--host <host-id>] [--source HOST|SERVER [--instruction <kind>:<ref>]]",
		Short: "Show which workers each host runs, or set who states it",
		Long: `Without --source: read the App's host setup — the switch (who states which
workers a host runs) and the assignments in effect, ordered by host then
worker; --host narrows to one host. A retired worker's assignments are left
out. Any participant who reads the App's tasks and events may read; a read
carries no worker session. --json prints {appRef, source, hostId,
assignments[]}, an assignment being {id, hostId, workerId, workerName,
workdir, family, model, effort, accountProfile, createdAt, updatedAt}
(effort and accountProfile are the S1b launcher profile: a profile token, or null
when unset; the keys are always present). "profile" is "available", or
"unavailable" when the server predates S1b: then the assignments are shown
without the profile, effort and accountProfile read null meaning UNKNOWN (not
unset), and the text says so once.

With --source HOST|SERVER: set the switch (the App manager's write). HOST, the
default, keeps today's behaviour: the host states its workers at registration.
SERVER: a registration is checked against the assignments (an unassigned
worker is left out, reported per row, never failing the registration), and a
worker is started only on a host it is assigned to whose registration lists
the assigned workdir, in that workdir and as the assigned family. It takes
effect at the next registration and start decision. --json prints
{appRef, source}.

` + hostSetupWriteHelp,
		Example: `  hadron team host setup
  hadron team host setup --host orca1 --json
  hadron team host setup --source SERVER`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			hostID = strings.TrimSpace(hostID)
			writing := cmd.Flags().Changed("source")
			if cmd.Flags().Changed("host") && !hostSetupHostIDRe.MatchString(hostID) {
				return exitcode.Newf(exitcode.Usage, "--host is a host id: letters, digits, \".\", \"_\", \":\" or \"-\", up to 128 characters; got %q", hostID)
			}
			if !writing {
				if cmd.Flags().Changed("instruction") {
					return exitcode.Newf(exitcode.Usage, "--instruction goes with --source: a read carries no instruction")
				}
				return runHostSetupRead(cmd, f, hostID)
			}
			if cmd.Flags().Changed("host") {
				return exitcode.Newf(exitcode.Usage, "--host narrows a read; --source sets the switch for the whole App")
			}
			src := gen.HostSetupSource(strings.ToUpper(strings.TrimSpace(source)))
			if src != gen.HostSetupSourceHost && src != gen.HostSetupSourceServer {
				return exitcode.Newf(exitcode.Usage, "--source is HOST or SERVER; got %q", source)
			}
			claim, instructed, err := staffingInstruction(cmd, f, instructionRaw)
			if err != nil {
				return err
			}
			scope, client, err := hostSetupScope(cmd, f)
			if err != nil {
				return err
			}
			ctx := cmd.Context()
			var got gen.HostSetupSource
			if claim == nil {
				resp, err := gen.TeamHostSetSource(ctx, client, scope.Ref, src)
				if err != nil {
					return api.MapErrorKeepingCode(err)
				}
				if resp == nil || resp.SetHostSetupSource == "" {
					return exitcode.Newf(exitcode.Error, "server returned no switch; outcome unknown, run `hadron team host setup` before retrying")
				}
				got = resp.SetHostSetupSource
			} else {
				ictx, _, err := instructed(ctx, "", scope.Ref)
				if err != nil {
					return err
				}
				resp, err := gen.TeamHostSetSourceInstructed(ictx, client, scope.Ref, src, claim)
				if err != nil {
					return api.MapErrorKeepingCode(err)
				}
				if resp == nil || resp.SetHostSetupSource == "" {
					return exitcode.Newf(exitcode.Error, "server returned no switch; outcome unknown, run `hadron team host setup` before retrying")
				}
				got = resp.SetHostSetupSource
			}
			d := hostSetupSourceDTO{AppRef: scope.Ref, Source: string(got)}
			label := lazyAppLabel(ctx, f, scope)
			return output.Write(f.IOStreams, f.JSON, d, func(w io.Writer) error {
				_, err := fmt.Fprintf(w, "app: %s\n✓ host setup source is now %s\n", label(), output.SafeCell(d.Source))
				return err
			})
		},
	}
	cmd.Flags().StringVar(&hostID, "host", "", "show only this host's assignments")
	cmd.Flags().StringVar(&source, "source", "", "set who states which workers the hosts run: HOST or SERVER")
	cmd.Flags().StringVar(&instructionRaw, "instruction", "", instructionFlagUsage)
	return cmd
}

func runHostSetupRead(cmd *cobra.Command, f *cmdutil.Factory, hostID string) error {
	scope, client, err := hostSetupScope(cmd, f)
	if err != nil {
		return err
	}
	// A read never carries the worker session: api.WithSession is opt-in.
	// The profile-bearing document first; a server that predates S1b refuses it
	// at validation and the S1a document answers instead, without the profile.
	var d hostSetupDTO
	if presp, perr := gen.TeamHostSetupWithProfile(cmd.Context(), client, scope.Ref, optStr(hostID)); perr == nil {
		if presp == nil || presp.HostSetup == nil {
			return exitcode.Newf(exitcode.Error, "server returned no host setup")
		}
		d = hostSetupDTO{AppRef: scope.Ref, Source: string(presp.HostSetup.Source), Profile: hostProfileAvailable, Assignments: []hostAssignmentDTO{}}
		for _, a := range presp.HostSetup.Assignments {
			if a == nil {
				continue
			}
			x := hostAssignmentDTOFrom(&a.HostSetupAssignmentFields)
			x.Effort, x.AccountProfile = a.Effort, a.AccountProfile
			d.Assignments = append(d.Assignments, x)
		}
	} else if serverLacksProfile(perr) {
		resp, err := gen.TeamHostSetup(cmd.Context(), client, scope.Ref, optStr(hostID))
		if err != nil {
			return api.MapErrorKeepingCode(err)
		}
		if resp == nil || resp.HostSetup == nil {
			return exitcode.Newf(exitcode.Error, "server returned no host setup")
		}
		d = hostSetupDTO{AppRef: scope.Ref, Source: string(resp.HostSetup.Source), Profile: hostProfileUnavailable, Assignments: []hostAssignmentDTO{}}
		for _, a := range resp.HostSetup.Assignments {
			if a == nil {
				continue
			}
			d.Assignments = append(d.Assignments, hostAssignmentDTO{
				ID: a.Id, HostID: a.HostId, WorkerID: a.WorkerId, Workdir: a.Workdir, Family: a.Family, Model: a.Model,
				CreatedAt: a.CreatedAt, UpdatedAt: a.UpdatedAt,
			})
			if a.Worker != nil {
				n := a.Worker.Name
				d.Assignments[len(d.Assignments)-1].WorkerName = &n
			}
		}
	} else {
		return api.MapErrorKeepingCode(perr)
	}
	if hostID != "" {
		d.HostID = &hostID
	}
	label := lazyAppLabel(cmd.Context(), f, scope)
	return output.Write(f.IOStreams, f.JSON, d, func(w io.Writer) error {
		if _, err := fmt.Fprintf(w, "app: %s\nsource: %s\n", label(), output.SafeCell(d.Source)); err != nil {
			return err
		}
		if d.Profile == hostProfileUnavailable {
			if _, err := fmt.Fprintln(w, "launcher profile: unavailable (this server predates S1b, hadron-server#4230); effort and account profile are unknown, not unset"); err != nil {
				return err
			}
		}
		if len(d.Assignments) == 0 {
			_, err := fmt.Fprintln(w, "no assignments")
			return err
		}
		if d.Profile == hostProfileUnavailable {
			t := output.NewTable(w, "HOST", "WORKER", "WORKDIR", "FAMILY", "MODEL")
			for _, a := range d.Assignments {
				t.Row(output.SafeCell(a.HostID), assignmentWho(a), output.SafeCell(a.Workdir), output.SafeCell(a.Family), output.SafeCell(stopValue(a.Model)))
			}
			return t.Flush()
		}
		t := output.NewTable(w, "HOST", "WORKER", "WORKDIR", "FAMILY", "MODEL", "EFFORT", "ACCOUNT")
		for _, a := range d.Assignments {
			t.Row(output.SafeCell(a.HostID), assignmentWho(a), output.SafeCell(a.Workdir), output.SafeCell(a.Family), output.SafeCell(stopValue(a.Model)), output.SafeCell(stopValue(a.Effort)), output.SafeCell(stopValue(a.AccountProfile)))
		}
		return t.Flush()
	})
}

func hostSetupScope(cmd *cobra.Command, f *cmdutil.Factory) (appScope, graphql.Client, error) {
	b, err := readBindingOrNilWithApp(cmd.Context(), f)
	if err != nil {
		return appScope{}, nil, err
	}
	scope, err := resolveTeamAppScope(cmd.Context(), f, b)
	if err != nil {
		return scope, nil, err
	}
	client, err := f.GraphQLClient()
	return scope, client, err
}
