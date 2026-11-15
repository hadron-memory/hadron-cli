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
}

type importReportDTO struct {
	AppRef string `json:"appRef"`
	HostID string `json:"hostId"`
	// HostSetup is "available", or "missing" when the server lacks the
	// hostSetup read (before hadron-server#4241): the assignments are then
	// unknown and entries read "unchecked".
	HostSetup string           `json:"hostSetup"`
	Applied   bool             `json:"applied"`
	Entries   []importEntryDTO `json:"entries"`
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
	for i := range roster {
		r := roster[i]
		if ref == r.ID || (r.URN != "" && ref == r.URN) || strings.EqualFold(ref, r.Name) {
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
	var missing []string
	for _, r := range roster {
		if !matched[r.ID] {
			missing = append(missing, r.Name)
		}
	}
	sort.Strings(missing)
	return entries, missing
}

// hostImportWrites are the two writes an apply makes, injected so the flow is
// testable without a server.
type hostImportWrites struct {
	Assign     func(ctx context.Context, workerRef, workdir, family string) error
	LeaveAlone func(ctx context.Context, workerRef string) error
}

var errLeaveAloneUnavailable = errors.New("marking imported workers leave-alone needs the lifecycle write from hadron-cli#2072 (C9), which is not in this build")

// applyHostImport performs the plan's "create" entries. It first proves the
// leave-alone write exists: without it a worker would become assigned while
// still eligible for the sweep, so nothing is written at all. Re-running it
// is idempotent: only entries that are still "create" are written.
func applyHostImport(ctx context.Context, entries []importEntryDTO, w hostImportWrites) error {
	if w.LeaveAlone == nil || w.Assign == nil {
		return errLeaveAloneUnavailable
	}
	for _, e := range entries {
		if e.Action != importCreate || e.Workdir == nil {
			continue
		}
		if err := w.LeaveAlone(ctx, e.Ref); err != nil {
			return fmt.Errorf("leave-alone for %s: %w", e.Ref, err)
		}
		if err := w.Assign(ctx, e.Ref, *e.Workdir, e.Family); err != nil {
			return fmt.Errorf("assign %s: %w", e.Ref, err)
		}
	}
	return nil
}

// hostImportWriter is the production writer set; the leave-alone write is
// absent until C9 (#2072) merges, so an apply refuses before writing.
var hostImportWriter = func(cmd *cobra.Command, f *cmdutil.Factory, appRef, hostID string) hostImportWrites {
	return hostImportWrites{} // LeaveAlone nil: refused by applyHostImport
}

func countActions(entries []importEntryDTO) map[string]int {
	c := map[string]int{importCreate: 0, importExisting: 0, importDiffers: 0, importSkip: 0, importUnchecked: 0}
	for _, e := range entries {
		c[e.Action]++
	}
	return c
}

func newCmdTeamHostImport(f *cmdutil.Factory) *cobra.Command {
	var from, hostID string
	var apply bool
	cmd := &cobra.Command{
		Use:   "import --from <host.yaml> --host <host-id> [--apply]",
		Short: "Plan (dry run) the server assignments a host.yaml implies",
		Long: `Read an existing host YAML and report the S1a assignments it implies for one
host of the App. WITHOUT --apply nothing is written, ever: the report says,
per entry, what an apply would do:

  create     no assignment on the server; an apply creates it
  unchanged  the server already holds exactly this assignment
  differs    the server holds another one; an import never replaces it
  unchecked  the server lacks hostSetup (before hadron-server#4241), so what it
             holds is unknown; the dry run still works and says so
  skip       the entry cannot become an assignment as written (no roster
             worker, no or several worktrees, a malformed token), with why

An entry's workdir is its single listed worktree, its family the configured
one; the model is not in the file, so none is sent. The launch line is reported
as program and argument count only, never its arguments; its launcher profile
(hadron-server#4230, not served yet) reads "unmapped": a line is never guessed.
"missing" lists the roster workers the file has no entry for. Read the copy of
the file, not the one a live host reads. The cutover and rollback steps are in
the runbook node coordination:team-on-orca-cutover-runbook.

--apply is refused until the leave-alone write (hadron-cli#2072) is in this
build: assigning a worker without marking it leave-alone would make it
eligible for the sweep. Nothing is written by the refusal.`,
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
			rr, err := gen.TeamRoster(ctx, client, scope.Ref)
			if err != nil {
				return api.MapErrorKeepingCode(err)
			}
			var roster []importRoster
			if rr != nil && rr.TeamRoster != nil {
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
			if dto.Entries == nil {
				dto.Entries = []importEntryDTO{}
			}
			if dto.Missing == nil {
				dto.Missing = []string{}
			}
			if apply {
				if err := applyHostImport(ctx, entries, hostImportWriter(cmd, f, scope.Ref, hostID)); err != nil {
					if errors.Is(err, errLeaveAloneUnavailable) {
						return exitcode.Newf(exitcode.Usage, "--apply refused, nothing written: %v", err)
					}
					return exitcode.Newf(exitcode.Error, "%v", err)
				}
				dto.Applied = true
			}
			return output.Write(f.IOStreams, f.JSON, dto, func(w io.Writer) error {
				if dto.HostSetup == "missing" {
					if _, err := fmt.Fprintln(w, "server lacks host setup: existing assignments are unknown, so entries read unchecked"); err != nil {
						return err
					}
				}
				verb := "dry run"
				if dto.Applied {
					verb = "applied"
				}
				if _, err := fmt.Fprintf(w, "host %s (%s): %d create, %d unchanged, %d differs, %d unchecked, %d skip; %d roster workers have no entry\n",
					output.SafeCell(hostID), verb, dto.Counts[importCreate], dto.Counts[importExisting], dto.Counts[importDiffers], dto.Counts[importUnchecked], dto.Counts[importSkip], len(dto.Missing)); err != nil {
					return err
				}
				t := output.NewTable(w, "REF", "ACTION", "WORKDIR", "FAMILY", "LAUNCH", "PROFILE", "WHY")
				for _, e := range dto.Entries {
					why, wd, prof := "", "-", "unmapped"
					if e.Reason != nil {
						why = *e.Reason
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
				for _, m := range dto.Missing {
					if _, err := fmt.Fprintf(w, "no entry: %s\n", output.SafeCell(m)); err != nil {
						return err
					}
				}
				return nil
			})
		},
	}
	cmd.Flags().StringVar(&from, "from", "", "the host YAML to read (required; use a copy)")
	cmd.Flags().StringVar(&hostID, "host", "", "the host's id within the App (required)")
	cmd.Flags().BoolVar(&apply, "apply", false, "write the assignments (refused until the leave-alone write is available)")
	return cmd
}
