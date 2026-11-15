package team

// `team host import` — hadron-cli#2061 (C6): read an existing host.yaml and
// plan, or apply, its S1a assignments (hadron-server#4241) on the server. The
// default is a DRY RUN that writes nothing; it reports, per entry, what an
// apply would do, plus the roster workers the file does not name.

import (
	"context"
	"errors"
	"fmt"
	"io"
	"path"
	"sort"
	"strings"

	"github.com/Khan/genqlient/graphql"
	"github.com/spf13/cobra"

	"github.com/hadron-memory/hadron-cli/internal/api"
	"github.com/hadron-memory/hadron-cli/internal/api/gen"
	"github.com/hadron-memory/hadron-cli/internal/cmdutil"
	"github.com/hadron-memory/hadron-cli/internal/exitcode"
	"github.com/hadron-memory/hadron-cli/internal/hostpoller"
	"github.com/hadron-memory/hadron-cli/internal/output"
)

// Import actions, one per host.yaml entry.
const (
	importCreate   = "create"    // no assignment on the server: an apply creates it
	importExisting = "unchanged" // the server already holds exactly this assignment
	importDiffers  = "differs"   // the server holds another one: needs --replace, never forced
	importSkip     = "skip"      // the entry cannot become an assignment as written
	// importConflict: two entries of the file resolve to the SAME roster worker
	// (a name and its id or URN) and ask for different things; both are
	// reported and neither is written.
	importConflict = "conflict"
	// importUnchecked: the entry could become an assignment, but the server
	// lacks hostSetup (it predates hadron-server#4241), so what it holds is
	// unknown. Reported, never an error: a dry run works against today's server.
	importUnchecked = "unchecked"
)

type importLaunchDTO struct {
	// Program is the base name of the first launch argument and Args the
	// number of the rest. The arguments themselves are never printed: a launch
	// line can carry paths and tokens.
	Program string `json:"program"`
	Args    int    `json:"args"`
}

type importEntryDTO struct {
	Ref     string          `json:"ref"`
	Worker  *string         `json:"worker"` // the roster worker's name; null when none matches
	Action  string          `json:"action"`
	Reason  *string         `json:"reason"`
	Workdir *string         `json:"workdir"`
	Family  string          `json:"family"`
	Launch  importLaunchDTO `json:"launch"`
	// Profile is the launcher profile proposed for the launch line; null when
	// none is known. Launcher profiles (S1b, hadron-server#4230) are not
	// served yet, so no line is mapped and none is guessed.
	Profile       *string  `json:"profile"`
	ProfileStatus string   `json:"profileStatus"`
	Existing      *string  `json:"existing"` // for "differs": the server's workdir/family
	Warnings      []string `json:"warnings"`
	// Applied is set by an apply, on "create" entries only.
	Applied  *importAppliedDTO `json:"applied"`
	workerID string            // the roster worker this entry resolved to (not serialized)
}

type importReportDTO struct {
	AppRef string `json:"appRef"`
	HostID string `json:"hostId"`
	// HostSetup is "available", or "missing" when the server lacks the
	// hostSetup read (before hadron-server#4241): the assignments are then
	// unknown and entries read "unchecked".
	HostSetup string `json:"hostSetup"`
	// FileApp is the App the host file names (its app key); the report reads
	// AppRef. A mismatch is warned about, never silently trusted.
	FileApp  string           `json:"fileApp"`
	Warnings []string         `json:"warnings"`
	Applied  bool             `json:"applied"`
	Entries  []importEntryDTO `json:"entries"`
	// Missing are the roster workers the file has no entry for.
	Missing []string       `json:"missing"`
	Counts  map[string]int `json:"counts"`
}

type importRoster struct{ ID, Name, URN string }

type importExisting1 struct{ WorkerID, Workdir, Family string }

// launcherProfileFor is the S1b seam: it maps one launch line to a launcher
// profile, or reports that it cannot. Until the profile catalog exists
// nothing maps; a line is reported, never guessed.
var launcherProfileFor = func(argv []string) (string, bool) { return "", false }

func launchShape(argv []string) importLaunchDTO {
	if len(argv) == 0 {
		return importLaunchDTO{}
	}
	return importLaunchDTO{Program: path.Base(strings.ReplaceAll(argv[0], `\`, "/")), Args: len(argv) - 1}
}

func matchRoster(ref string, roster []importRoster) *importRoster {
	// An explicit ID or URN wins over a name, whatever the roster order: a
	// worker may be NAMED like another worker's id (cli#2116).
	for i := range roster {
		if ref == roster[i].ID || (roster[i].URN != "" && ref == roster[i].URN) {
			return &roster[i]
		}
	}
	for i := range roster {
		if strings.EqualFold(ref, roster[i].Name) {
			return &roster[i]
		}
	}
	return nil
}

// planHostImport turns a host configuration into one entry per worker. Pure:
// no clock, no network. Entries are ordered by ref.
func planHostImport(cfg *hostpoller.Config, roster []importRoster, existing []importExisting1, setupKnown bool) ([]importEntryDTO, []string) {
	haveByWorker := map[string]importExisting1{}
	for _, e := range existing {
		haveByWorker[e.WorkerID] = e
	}
	var entries []importEntryDTO
	matched := map[string]bool{}
	for _, ref := range cfg.WorkerRefs() {
		w := cfg.Workers[ref]
		e := importEntryDTO{Ref: ref, Family: hostpoller.CanonicalFamily(w.Family), Launch: launchShape(w.Launch), Warnings: []string{}}
		if p, ok := launcherProfileFor(w.Launch); ok {
			e.Profile, e.ProfileStatus = &p, "mapped"
		} else {
			e.ProfileStatus = "unmapped"
		}
		skip := func(reason string) {
			e.Action, e.Reason = importSkip, &reason
			entries = append(entries, e)
		}
		r := matchRoster(ref, roster)
		if r == nil {
			skip("no roster worker matches this entry")
			continue
		}
		name := r.Name
		e.Worker = &name
		e.workerID = r.ID
		matched[r.ID] = true
		switch len(w.Worktrees) {
		case 0:
			skip("no worktree listed: nothing to assign a workdir from")
			continue
		case 1:
		default:
			skip(fmt.Sprintf("%d worktrees listed: one assignment holds one workdir, so the choice is yours", len(w.Worktrees)))
			continue
		}
		wd := w.Worktrees[0]
		if !hostSetupToken(wd) {
			skip("the worktree name is not a workdir token")
			continue
		}
		if !hostSetupFamilyRe.MatchString(e.Family) {
			skip("the family is not a family token")
			continue
		}
		e.Workdir = &wd
		if !setupKnown {
			e.Action = importUnchecked
			reason := "server lacks host setup: what it holds for this worker is unknown"
			e.Reason = &reason
			entries = append(entries, e)
			continue
		}
		if have, ok := haveByWorker[r.ID]; ok {
			if have.Workdir == wd && have.Family == e.Family {
				e.Action = importExisting
			} else {
				e.Action = importDiffers
				s := have.Workdir + "/" + have.Family
				e.Existing = &s
				reason := "the server already holds a different assignment; --replace is the operator's decision, never made by an import"
				e.Reason = &reason
			}
		} else {
			e.Action = importCreate
		}
		entries = append(entries, e)
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Ref < entries[j].Ref })
	markDuplicateWorkers(entries)
	var missing []string
	for _, r := range roster {
		if !matched[r.ID] {
			missing = append(missing, r.Name)
		}
	}
	sort.Strings(missing)
	return entries, missing
}

// markDuplicateWorkers handles two file entries that resolve to one roster
// worker. Asking for the same thing twice reads "skip: duplicate" on the later
// one; asking for different things marks BOTH "conflict". Neither is written.
func markDuplicateWorkers(entries []importEntryDTO) {
	byWorker := map[string][]int{}
	for i, e := range entries {
		if e.workerID != "" && e.Workdir != nil {
			byWorker[e.workerID] = append(byWorker[e.workerID], i)
		}
	}
	for _, idx := range byWorker {
		if len(idx) < 2 {
			continue
		}
		first := entries[idx[0]]
		same := true
		for _, i := range idx[1:] {
			if *entries[i].Workdir != *first.Workdir || entries[i].Family != first.Family {
				same = false
			}
		}
		for n, i := range idx {
			var reason string
			if same {
				if n == 0 {
					continue
				}
				reason = "the same worker as entry " + first.Ref + " (identical request): written once, from that entry"
				entries[i].Action = importSkip
			} else {
				others := []string{}
				for _, j := range idx {
					if j != i {
						others = append(others, entries[j].Ref)
					}
				}
				reason = "names the same worker as " + strings.Join(others, ", ") + " with a different workdir or family: nothing is written for it"
				entries[i].Action = importConflict
			}
			entries[i].Reason = &reason
		}
	}
}

// hostImportWrites are the writes an apply makes for one worker, by the
// roster worker's ID (never a name), injected so the flow is testable.
// LeaveAloneSet reads whether the worker is ALREADY marked leave-alone (the
// import never overwrites a mark it did not make). Refused says an error is the
// server's definite refusal (nothing took effect); any other failure is an
// UNKNOWN outcome. Systemic says a failure will repeat for every entry (no
// authority, server unreachable), so the apply stops.
type hostImportWrites struct {
	Assign        func(ctx context.Context, workerID, workdir, family string) error
	LeaveAlone    func(ctx context.Context, workerID string) error
	LeaveAloneSet func(ctx context.Context, workerID string) (bool, error)
	Refused       func(error) bool
	Systemic      func(error) bool
}

// importAppliedDTO is what an apply did for one "create" entry. LeaveAlone
// true with Assigned false is the SAFE half-state (marked, not assigned): the
// sweep leaves that worker alone, and a re-run completes it. LeaveAloneKept
// means the worker was already marked, so the import wrote no mark and a
// rollback must NOT clear it. Unknown means the failed step's outcome is not
// known (a lost answer can follow a commit): check "team host setup --host"
// and "team worker leave-alone" before any rollback.
type importAppliedDTO struct {
	LeaveAlone     bool    `json:"leaveAlone"`
	LeaveAloneKept bool    `json:"leaveAloneKept"`
	Assigned       bool    `json:"assigned"`
	Unknown        bool    `json:"unknown"`
	Error          *string `json:"error"`
}

var errApplyIncomplete = errors.New("some entries were not applied")

// applyHostImport performs the plan's "create" entries, one worker at a time:
// leave-alone FIRST, then the assignment, so a worker is never assigned while
// still eligible for the sweep. A refusal on one entry is recorded on it and
// the others still run, unless the failure is systemic. Re-running is
// idempotent: it plans from the server's state, where finished entries read
// "unchanged" and are not written again. It never replaces a "differs".
func applyHostImport(ctx context.Context, entries []importEntryDTO, w hostImportWrites) error {
	if w.LeaveAlone == nil || w.Assign == nil || w.LeaveAloneSet == nil {
		return errors.New("an apply needs its writes and the leave-alone read")
	}
	failed := 0
	for i := range entries {
		e := &entries[i]
		if e.Action != importCreate || e.Workdir == nil || e.workerID == "" {
			continue
		}
		res := &importAppliedDTO{}
		e.Applied = res
		// fail records one failed step. A READ wrote nothing, so it is never an
		// unknown outcome; a write that was not definitely refused is.
		fail := func(step string, wrote bool, err error) bool {
			res.Unknown = wrote && (w.Refused == nil || !w.Refused(err))
			msg := step + ": " + err.Error()
			if res.Unknown {
				msg = step + " outcome unknown (" + err.Error() + "): check the server's state before any rollback"
			}
			res.Error = &msg
			failed++
			return w.Systemic != nil && w.Systemic(err)
		}
		already, err := w.LeaveAloneSet(ctx, e.workerID)
		if err != nil {
			if fail("reading leave-alone", false, err) {
				break
			}
			continue
		}
		if already {
			res.LeaveAlone, res.LeaveAloneKept = true, true
		} else {
			if err := w.LeaveAlone(ctx, e.workerID); err != nil {
				if fail("leave-alone", true, err) {
					break
				}
				continue
			}
			res.LeaveAlone = true
		}
		if err := w.Assign(ctx, e.workerID, *e.Workdir, e.Family); err != nil {
			if fail("assignment", true, err) {
				break
			}
			continue
		}
		res.Assigned = true
	}
	if failed > 0 {
		return errApplyIncomplete
	}
	return nil
}

const importLeaveAloneReason = "imported from host.yaml (hadron-cli#2061); the sweep leaves it alone until its lane is released"

// hostImportWriter is the production writer set: the signed-in manager's own
// writes (assignHostWorker, setWorkerLeaveAlone), by worker id. A write counts
// only when the server's answer confirms the very worker and values asked for;
// an empty or mismatched answer is an unconfirmed outcome, never a success.
var hostImportWriter = func(client graphql.Client, appRef, hostID string) hostImportWrites {
	unconfirmed := errors.New("the server's answer did not confirm the write")
	return hostImportWrites{
		LeaveAloneSet: func(ctx context.Context, workerID string) (bool, error) {
			r, err := gen.GetWorkerLeaveAlone(ctx, client, workerID, nil)
			if err != nil {
				return false, api.MapErrorKeepingCode(err)
			}
			if r == nil || r.Worker == nil || r.Worker.Id != workerID || r.Worker.LeaveAlone == nil {
				return false, unconfirmed
			}
			return *r.Worker.LeaveAlone, nil
		},
		LeaveAlone: func(ctx context.Context, workerID string) error {
			reason := importLeaveAloneReason
			r, err := gen.SetWorkerLeaveAlone(ctx, client, &gen.SetWorkerLeaveAloneInput{Worker: workerID, LeaveAlone: true, Reason: &reason}, nil)
			if err != nil {
				return api.MapErrorKeepingCode(err)
			}
			if r == nil || r.SetWorkerLeaveAlone == nil || r.SetWorkerLeaveAlone.WorkerId != workerID || !r.SetWorkerLeaveAlone.LeaveAlone {
				return unconfirmed
			}
			return nil
		},
		Assign: func(ctx context.Context, workerID, workdir, family string) error {
			r, err := gen.TeamHostAssign(ctx, client, &gen.AssignHostWorkerInput{AppRef: appRef, Worker: workerID, HostId: hostID, Workdir: workdir, Family: family})
			if err != nil {
				return api.MapErrorKeepingCode(err)
			}
			a := (*gen.HostSetupAssignmentFields)(nil)
			if r != nil && r.AssignHostWorker != nil {
				a = &r.AssignHostWorker.HostSetupAssignmentFields
			}
			if a == nil || a.WorkerId != workerID || a.HostId != hostID || a.Workdir != workdir || a.Family != family {
				return unconfirmed
			}
			return nil
		},
		Refused: api.IsDefiniteRefusal,
		Systemic: func(err error) bool {
			switch exitcode.FromError(err) {
			case exitcode.AuthRequired, exitcode.Forbidden, exitcode.Unavailable:
				return true
			}
			return false
		},
	}
}

// importAppWarning says so when the App the file names is not the App being
// read: the roster and assignments come from the active App, not from the
// file. Both are resolved on the server and compared by identity, never by
// text (hrn:app:t:team is a prefix of hrn:app:t:team-two).
func importAppWarning(ctx context.Context, client graphql.Client, fileApp, readRef string) string {
	if strings.TrimSpace(fileApp) == "" {
		return ""
	}
	file, err := gen.TeamAppIdentity(ctx, client, fileApp)
	if err != nil || file == nil || file.App == nil {
		return fmt.Sprintf("the file names App %s, which this server does not resolve: the report reads the active App (--app chooses it)", fileApp)
	}
	readID := readRef
	if read, err := gen.TeamAppIdentity(ctx, client, readRef); err == nil && read != nil && read.App != nil {
		readID = read.App.Id
	}
	if file.App.Id == readID {
		return ""
	}
	return fmt.Sprintf("the file names App %s (%s) but this report reads another App (%s): the roster and assignments come from the active App (--app chooses it)", fileApp, file.App.Urn, readID)
}

func countActions(entries []importEntryDTO) map[string]int {
	c := map[string]int{importCreate: 0, importExisting: 0, importDiffers: 0, importSkip: 0, importUnchecked: 0, importConflict: 0}
	for _, e := range entries {
		c[e.Action]++
	}
	return c
}

func newCmdTeamHostImport(f *cmdutil.Factory) *cobra.Command {
	var from, hostID string
	var apply, yes bool
	cmd := &cobra.Command{
		Use:   "import --from <host.yaml> --host <host-id> [--apply [--yes]]",
		Short: "Plan, or with --apply write, the server assignments a host.yaml implies",
		Long: `Read an existing host YAML and report the S1a assignments it implies for one
host of the App. WITHOUT --apply nothing is written, ever: the report says,
per entry, what an apply would do:

  create     no assignment on the server; an apply creates it
  unchanged  the server already holds exactly this assignment
  differs    the server holds another one; an import never replaces it
  unchecked  the server lacks hostSetup (before hadron-server#4241), so what it
             holds is unknown; the dry run still works and says so
  conflict   two entries name the same worker (a name and its id or URN) and
             ask for different things; neither is written
  skip       the entry cannot become an assignment as written (no roster
             worker, no or several worktrees, a malformed token), with why

An entry's workdir is its single listed worktree, its family the configured
one; the model is not in the file, so none is sent. The launch line is reported
as program and argument count only, never its arguments; its launcher profile
(hadron-server#4230, not served yet) reads "unmapped": a line is never guessed.
"missing" lists the roster workers the file has no entry for. Read the copy of
the file, not the one a live host reads. The cutover and rollback steps are in
the runbook node coordination:team-on-orca-cutover-runbook.

--apply writes the "create" entries, one worker at a time: the worker is marked
leave-alone FIRST (so the sweep never acts on it), then the assignment is
created. These are the signed-in manager's own writes. It asks before writing
(--yes when not interactive) and never replaces a "differs". A refusal on one
entry is recorded on it (applied.error) and the others still run, except a
failure that would repeat for all of them (no authority, server unreachable),
which stops the run. Exit 1 when any entry failed; a re-run completes them and
writes nothing for the entries now "unchanged" (idempotent). Marked but not
assigned is the safe half-state. --apply is refused, nothing written, when the
server lacks host setup. The report's applied.assigned entries are what a
rollback undoes: "team host unassign <worker> --host <id>", and "team worker
leave-alone <worker> --off" ONLY where applied.leaveAlone is true and
applied.leaveAloneKept is false (a kept mark was not the import's). An
applied.unknown entry may have been written: check "team host setup --host <id>"
first (see the runbook). Never run it against a live
host without the runbook: the orca1 import is the host manager's step.`,
		Example: `  hadron team host import --from ./host.yaml.copy --host orca1
  hadron team host import --from ./host.yaml.copy --host orca1 --json`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			from, hostID = strings.TrimSpace(from), strings.TrimSpace(hostID)
			if from == "" || hostID == "" {
				return exitcode.Newf(exitcode.Usage, "--from and --host are required")
			}
			if !hostSetupHostIDRe.MatchString(hostID) {
				return exitcode.Newf(exitcode.Usage, "--host is a host id: letters, digits, \".\", \"_\", \":\" or \"-\", up to 128 characters; got %q", hostID)
			}
			cfg, err := hostpoller.LoadConfig(from)
			if err != nil {
				return exitcode.Newf(exitcode.Usage, "%v", err)
			}
			scope, client, err := hostSetupScope(cmd, f)
			if err != nil {
				return err
			}
			ctx := cmd.Context()
			rr, err := gen.TeamHostImportRoster(ctx, client, scope.Ref)
			if err != nil {
				return api.MapErrorKeepingCode(err)
			}
			if rr == nil || rr.TeamRoster == nil {
				// An empty roster would plan every entry as "no roster worker":
				// refuse an unknown roster instead (cli#2094 LOCAL F2).
				return exitcode.Newf(exitcode.Error, "server returned no roster; nothing planned")
			}
			var roster []importRoster
			for _, w := range rr.TeamRoster.Workers {
				if w != nil && w.Worker != nil {
					ir := importRoster{ID: w.Worker.Id}
					if w.Worker.Name != nil {
						ir.Name = *w.Worker.Name
					}
					if w.Worker.Urn != nil {
						ir.URN = *w.Worker.Urn
					}
					roster = append(roster, ir)
				}
			}
			setupKnown := true
			hs, err := gen.TeamHostSetup(ctx, client, scope.Ref, &hostID)
			if err != nil {
				// A server from before hadron-server#4241 has no hostSetup: say
				// so and plan without it, as a dry run must still work there.
				if !api.IsFrameworkValidationOnlyFor(err, "hostSetup") {
					return api.MapErrorKeepingCode(err)
				}
				setupKnown = false
			}
			if setupKnown && (hs == nil || hs.HostSetup == nil) {
				// A successful answer without the setup is not "no assignments":
				// planning from it would report false creates (cli#2094 QA F1).
				return exitcode.Newf(exitcode.Error, "server returned no host setup; nothing planned")
			}
			var existing []importExisting1
			if hs != nil && hs.HostSetup != nil {
				for _, a := range hs.HostSetup.Assignments {
					if a != nil {
						existing = append(existing, importExisting1{WorkerID: a.WorkerId, Workdir: a.Workdir, Family: a.Family})
					}
				}
			}
			entries, missing := planHostImport(cfg, roster, existing, setupKnown)
			dto := importReportDTO{AppRef: scope.Ref, HostID: hostID, HostSetup: map[bool]string{true: "available", false: "missing"}[setupKnown], Entries: entries, Missing: missing, Counts: countActions(entries)}
			dto.FileApp, dto.Warnings = cfg.App, []string{}
			appWarning := importAppWarning(ctx, client, cfg.App, scope.Ref)
			if appWarning != "" {
				dto.Warnings = append(dto.Warnings, appWarning)
			}
			if dto.Entries == nil {
				dto.Entries = []importEntryDTO{}
			}
			if dto.Missing == nil {
				dto.Missing = []string{}
			}
			var applyErr error
			if apply {
				if !setupKnown {
					return exitcode.Newf(exitcode.Usage, "--apply refused, nothing written: the server lacks host setup (it predates hadron-server#4241)")
				}
				if appWarning != "" {
					// The dry run's check is a gate for a write: a wrong --app or
					// binding would assign another App's same-named workers.
					return exitcode.Newf(exitcode.Usage, "--apply refused, nothing written: %s", appWarning)
				}
				if dto.Counts[importCreate] > 0 {
					if err := cmdutil.Confirm(f.IOStreams, yes, fmt.Sprintf("In App %s, assign %d workers to host %s and mark each leave-alone?", output.SafeCell(describeApp(ctx, f, scope.Ref)), dto.Counts[importCreate], output.SafeCell(hostID))); err != nil {
						return err
					}
					applyErr = applyHostImport(ctx, entries, hostImportWriter(client, scope.Ref, hostID))
					if applyErr != nil && !errors.Is(applyErr, errApplyIncomplete) {
						return exitcode.Newf(exitcode.Error, "%v", applyErr)
					}
				}
				dto.Applied = true
			}
			werr := output.Write(f.IOStreams, f.JSON, dto, func(w io.Writer) error {
				if dto.HostSetup == "missing" {
					if _, err := fmt.Fprintln(w, "server lacks host setup: existing assignments are unknown, so entries read unchecked"); err != nil {
						return err
					}
				}
				for _, warn := range dto.Warnings {
					if _, err := fmt.Fprintf(w, "warning: %s\n", output.SafeCell(warn)); err != nil {
						return err
					}
				}
				verb := "dry run"
				if dto.Applied {
					verb = "applied"
				}
				if _, err := fmt.Fprintf(w, "host %s (%s): %d create, %d unchanged, %d differs, %d unchecked, %d conflict, %d skip; %d roster workers have no entry\n",
					output.SafeCell(hostID), verb, dto.Counts[importCreate], dto.Counts[importExisting], dto.Counts[importDiffers], dto.Counts[importUnchecked], dto.Counts[importConflict], dto.Counts[importSkip], len(dto.Missing)); err != nil {
					return err
				}
				t := output.NewTable(w, "REF", "ACTION", "WORKDIR", "FAMILY", "LAUNCH", "PROFILE", "WHY")
				for _, e := range dto.Entries {
					why, wd, prof := "", "-", "unmapped"
					if e.Reason != nil {
						why = *e.Reason
					}
					if e.Existing != nil {
						why += " (server: " + *e.Existing + ")"
					}
					if e.Workdir != nil {
						wd = *e.Workdir
					}
					if e.Profile != nil {
						prof = *e.Profile
					}
					t.Row(output.SafeCell(e.Ref), e.Action, output.SafeCell(wd), output.SafeCell(e.Family), output.SafeCell(fmt.Sprintf("%s +%d", e.Launch.Program, e.Launch.Args)), prof, output.SafeCell(why))
				}
				if err := t.Flush(); err != nil {
					return err
				}
				for _, e := range dto.Entries {
					if e.Applied == nil {
						continue
					}
					res := "assigned and leave-alone"
					switch {
					case e.Applied.Error != nil && e.Applied.Unknown:
						// The step may have been written: never say "NOT".
						res = "UNKNOWN: " + *e.Applied.Error
					case e.Applied.Error != nil && e.Applied.LeaveAloneKept:
						res = "already leave-alone (kept, not the import's mark), NOT assigned: " + *e.Applied.Error
					case e.Applied.Error != nil && e.Applied.LeaveAlone:
						res = "leave-alone set, NOT assigned: " + *e.Applied.Error
					case e.Applied.Error != nil:
						res = "NOT written: " + *e.Applied.Error
					case e.Applied.LeaveAloneKept:
						res = "assigned; it was already leave-alone (kept, not the import's mark)"
					}
					if _, err := fmt.Fprintf(w, "applied %s: %s\n", output.SafeCell(e.Ref), output.SafeCell(res)); err != nil {
						return err
					}
				}
				for _, m := range dto.Missing {
					if _, err := fmt.Fprintf(w, "no entry: %s\n", output.SafeCell(m)); err != nil {
						return err
					}
				}
				return nil
			})
			if werr != nil {
				return werr
			}
			if applyErr != nil {
				return exitcode.Newf(exitcode.Error, "apply incomplete: some entries failed (see the report); a re-run completes them")
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&from, "from", "", "the host YAML to read (required; use a copy)")
	cmd.Flags().StringVar(&hostID, "host", "", "the host's id within the App (required)")
	cmd.Flags().BoolVar(&apply, "apply", false, "write the assignments and mark each worker leave-alone (default: dry run)")
	cmd.Flags().BoolVar(&yes, "yes", false, "do not prompt before --apply (required when not interactive)")
	return cmd
}
