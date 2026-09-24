package skill

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/spf13/cobra"

	"github.com/hadron-memory/hadron-cli/internal/api"
	"github.com/hadron-memory/hadron-cli/internal/api/gen"
	"github.com/hadron-memory/hadron-cli/internal/cmdutil"
	"github.com/hadron-memory/hadron-cli/internal/config"
	"github.com/hadron-memory/hadron-cli/internal/exitcode"
	"github.com/hadron-memory/hadron-cli/internal/output"
	"github.com/hadron-memory/hadron-cli/internal/skilldoc"
)

// exportRoots is the EXPORT host table (#621): where each host's user-level
// skills live, as path components below $HOME (cor:agt:030:02: export writes
// user-level, for every known host, with no detection and no prompt).
//
// It is export's own table, deliberately NOT status's hostDirs. Adding Codex
// there would let `status --host codexSkill --to user` compare only
// ~/.agents/skills and report an all-clear wider than what it read — Codex also
// reads the deprecated ~/.codex/skills (plan §3, plan-only Q4). The deprecated
// root is never written, moved or cleaned here either (matrix P20/P21, Q7).
//
// Every host in skilldoc.Hosts must have a row: TestExportRootsCoverEveryHost
// pins it, and at run time a host without one fails its items rather than
// being skipped (matrix P07).
var exportRoots = map[string][]string{
	skilldoc.HostClaudeSkill: {".claude", "skills"},
	skilldoc.HostCodexSkill:  {".agents", "skills"},
}

// Reason origins. A server reason is the planner's verbatim {code, message};
// a client reason is an I/O fact only this client can see (a link on the
// path, a failed write). The client never authors policy.
const (
	originServer = "server"
	originClient = "client"
)

// Client reason codes: facts about the local filesystem, never policy.
const (
	reasonHostNoRoot        = "host-has-no-root"
	reasonRootIsLink        = "root-is-link"
	reasonRootNotDir        = "root-not-a-directory"
	reasonDirIsLink         = "skill-dir-is-link"
	reasonNotDir            = "skill-dir-not-a-directory"
	reasonUnsafeName        = "unsafe-directory-name"
	reasonNoBody            = "no-rendered-body"
	reasonNoSource          = "no-source-directory"
	reasonNoExportPlan      = "no-export-plan"
	reasonUnknownAction     = "unknown-action"
	reasonPlanContradiction = "plan-contradiction"
	reasonIOError           = "io-error"
	reasonPlanRefused       = "plan-refused"
	reasonDirKept           = "directory-kept"
	reasonFileIsLink        = "skill-file-is-link"
	reasonForeignFile       = "foreign-skill-file"
	reasonNotRegular        = "skill-file-not-regular"
)

type exportReasonDTO struct {
	Code    string `json:"code"`
	Message string `json:"message"`
	// Origin is "server" (the planner's verbatim reason) or "client" (an I/O
	// fact about this machine).
	Origin string `json:"origin"`
}

// exportItemDTO is one skill for one host. Kept lists files a removal left in
// place because they are not SKILL.md: a skill directory may carry scripts or
// assets, and deleting them is not this command's call.
type exportItemDTO struct {
	Node    string            `json:"node"`
	NodeID  string            `json:"nodeId"`
	Name    string            `json:"name"`
	Reasons []exportReasonDTO `json:"reasons"`
	Kept    []string          `json:"kept"`
}

type exportMovedDTO struct {
	exportItemDTO
	From string `json:"from"`
}

// exportHostDTO is one host's part of the report. Failure is set when the
// whole host could not be written (no root row, a link on its root's path, a
// refused plan) — its items are then in Failed and each is named.
type exportHostDTO struct {
	Host        string                `json:"host"`
	Root        string                `json:"root"`
	Failure     *exportReasonDTO      `json:"failure"`
	Scanned     int                   `json:"scanned"`
	Judged      int                   `json:"judged"`
	Written     []exportItemDTO       `json:"written"`
	Moved       []exportMovedDTO      `json:"moved"`
	Removed     []exportItemDTO       `json:"removed"`
	Skipped     []exportItemDTO       `json:"skipped"`
	Refused     []exportItemDTO       `json:"refused"`
	Failed      []exportItemDTO       `json:"failed"`
	Orphaned    []statusOrphanDTO     `json:"orphaned"`
	Pruned      []statusOrphanDTO     `json:"pruned"`
	Unreadable  []statusUnreadableDTO `json:"unreadable"`
	Unparseable []statusUnreadableDTO `json:"unparseable"`
}

// exportUnrecognizedDTO is a node whose `exports` names no host (P16). It is
// host-free: the server returns the same list for every host, and it is
// reported once, attributed to no host.
type exportUnrecognizedDTO struct {
	Node     string             `json:"node"`
	NodeID   string             `json:"nodeId"`
	Name     string             `json:"name"`
	Memory   string             `json:"memory"`
	KeyCount int                `json:"keyCount"`
	Keys     []string           `json:"keys"`
	Findings []statusFindingDTO `json:"findings"`
}

// exportDTO is the whole report. Every slice is initialised, so an empty
// field renders as [] and never as null.
type exportDTO struct {
	DryRun       bool                    `json:"dryRun"`
	Hosts        []exportHostDTO         `json:"hosts"`
	Unrecognized []exportUnrecognizedDTO `json:"unrecognized"`
}

func newExportHost(host, root string) exportHostDTO {
	return exportHostDTO{
		Host: host, Root: root,
		Written: []exportItemDTO{}, Moved: []exportMovedDTO{}, Removed: []exportItemDTO{},
		Skipped: []exportItemDTO{}, Refused: []exportItemDTO{}, Failed: []exportItemDTO{},
		Orphaned: []statusOrphanDTO{}, Pruned: []statusOrphanDTO{},
		Unreadable: []statusUnreadableDTO{}, Unparseable: []statusUnreadableDTO{},
	}
}

type exportOpts struct {
	dryRun bool
	prune  bool
}

func newCmdExport(f *cmdutil.Factory) *cobra.Command {
	var opts exportOpts
	var force bool
	cmd := &cobra.Command{
		Use:   "export",
		Short: "Write the skill files for every enabled task you can read",
		Long: `Write the skill file for every enabled skill declaration you can read, for
every known host, into your user-level skills directories:

  claudeSkill  ~/.claude/skills/<name>/SKILL.md
  codexSkill   ~/.agents/skills/<name>/SKILL.md

This is the only hadron skill command that writes into your skills
directories ("hadron skill plugin" builds bundles elsewhere, under --out).
The server decides what
each file should be and what to do with it; this command does the I/O and
reports what actually happened. A missing directory is created. Nothing is
detected and nothing is prompted for: a host that is not installed still gets
its files, ready for when it is.

What it does per skill, per host:
  written  a new or stale file is (re)written from the node
  moved    the skill was renamed; the file moves to the new directory
  removed  the declaration is disabled; the file it wrote is removed
  skipped  already current, or nothing to do (the reason says which)
  refused  the file was edited by hand, or two skills claim one name
  failed   the item could not be done; every other item still runs

A removal deletes SKILL.md and then the directory only if it is empty: other
files in a skill directory are kept and listed.

A hand-edited file is someone's work, so it is REFUSED unless --force is
given, including when its declaration was switched off. --force also
regenerates a file written by an older CLI that carries no node id.

LINKS ARE REFUSED. If a host's skills directory, or any directory above it
inside your home directory, is a symbolic link, nothing is written for that
host and each of its skills is reported as failed. A skill directory that is a
link is refused the same way. Writing through a link could put one host's files
into another host's directory.

Files on disk that no enabled declaration claims are reported as orphaned and
left alone. --prune removes them.

Hosts load skills when a session starts, so restart a running session to pick
up changes.

Exit codes: 0 when every item was written, moved, removed or skipped. 5 after
the full report when any item was refused or failed. A run that cannot start
(not signed in, server unreachable) exits with that error's code.`,
		Example: `  hadron skill export --dry-run
  hadron skill export
  hadron skill export --force --prune --json`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			client, err := f.GraphQLClient()
			if err != nil {
				return err
			}
			home, err := os.UserHomeDir()
			if err != nil {
				return exitcode.Newf(exitcode.Error, "cannot locate your home directory: %v", err)
			}
			// $HOME itself is RESOLVED, not refused: on macOS every temp dir and
			// some home directories sit under a symlinked system path (/var ->
			// /private/var). The refusal covers the part this command owns —
			// every component BELOW home (Holger's Q2/Q3 ruling, #621).
			home, err = filepath.EvalSymlinks(home)
			if err != nil {
				return exitcode.Newf(exitcode.Error, "cannot resolve your home directory: %v", err)
			}
			var forcePtr *bool
			if force {
				forcePtr = &force
			}

			dto := exportDTO{DryRun: opts.dryRun, Hosts: []exportHostDTO{}, Unrecognized: []exportUnrecognizedDTO{}}
			var unrecognized []*gen.SkillExportPlanSkillPlanUnrecognized
			ioStarted := false
			for _, h := range skilldoc.Hosts {
				plan := func(files []*gen.SkillFileFactsInput) (*gen.SkillExportPlanSkillPlan, error) {
					host := h.Key
					resp, err := gen.SkillExportPlan(cmd.Context(), client, &gen.SkillPlanInput{
						Intent: gen.SkillPlanIntentExport,
						Host:   &host,
						Files:  files,
						Force:  forcePtr,
					})
					if err != nil {
						return nil, err
					}
					if resp.SkillPlan == nil {
						return nil, errors.New("skillPlan returned no result")
					}
					return resp.SkillPlan, nil
				}
				hd, p, err := exportHost(home, h.Key, plan, opts)
				if err != nil {
					// A run that cannot START is not an item failure: before any
					// host did I/O, an auth or transport error is the run's error.
					mapped := api.MapError(err)
					if code := exitcode.FromError(mapped); !ioStarted && (code == exitcode.AuthRequired || code == exitcode.Unavailable) {
						return mapped
					}
					hd.Failure = &exportReasonDTO{Code: reasonPlanRefused, Message: mapped.Error(), Origin: originClient}
				}
				ioStarted = true
				if p != nil && unrecognized == nil {
					unrecognized = p.Unrecognized
				}
				dto.Hosts = append(dto.Hosts, hd)
			}
			dto.Unrecognized = toUnrecognizedDTO(unrecognized)

			if err := output.Write(f.IOStreams, f.JSON, dto, func(w io.Writer) error {
				return renderExport(w, dto)
			}); err != nil {
				return err
			}
			if exportHasFailures(dto) {
				return exitcode.Silent(exitcode.Conflict)
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&opts.dryRun, "dry-run", false, "report what would happen, and change nothing on disk")
	cmd.Flags().BoolVar(&force, "force", false, "overwrite, move or remove a hand-edited file, or one with no node id")
	cmd.Flags().BoolVar(&opts.prune, "prune", false, "remove orphaned skill files that no enabled declaration claims")
	return cmd
}

// planFunc asks the server for one host's export plan, given the files found
// under that host's root.
type planFunc func(files []*gen.SkillFileFactsInput) (*gen.SkillExportPlanSkillPlan, error)

// exportHost runs one host: guard its root, walk it, fetch the plan, act. It
// returns the plan too (for the host-free `unrecognized` list). An error is a
// plan error; everything local is reported in the DTO instead.
func exportHost(home, host string, plan planFunc, opts exportOpts) (exportHostDTO, *gen.SkillExportPlanSkillPlan, error) {
	parts, ok := exportRoots[host]
	if !ok {
		hd := newExportHost(host, "")
		return blockHost(hd, plan, exportReasonDTO{
			Code:    reasonHostNoRoot,
			Message: fmt.Sprintf("this CLI has no skills directory for host %s, so nothing was written for it; upgrade the CLI", host),
			Origin:  originClient,
		})
	}
	root := filepath.Join(append([]string{home}, parts...)...)
	hd := newExportHost(host, root)

	exists, reason := checkRoot(home, parts)
	if reason != nil {
		return blockHost(hd, plan, *reason)
	}

	var files []*gen.SkillFileFactsInput
	linked := map[string]string{}
	if exists {
		var err error
		if linked, err = linkedSkillDirs(root); err != nil {
			return blockHost(hd, plan, ioReason(err))
		}
		all, unreadable, unparseable, err := walkSkillFiles(root)
		if err != nil {
			return blockHost(hd, plan, exportReasonDTO{Code: reasonIOError, Message: err.Error(), Origin: originClient})
		}
		// A linked skill directory is not this host's file to judge (P06):
		// the shared walk reads THROUGH links on purpose (status must see
		// them), but submitting one here would let the server call another
		// host's copy "current" for this one. It is withheld, and any entry
		// that would touch it is refused below.
		for _, fct := range all {
			if _, isLink := linked[fct.DirName]; !isLink {
				files = append(files, fct)
			}
		}
		for _, u := range unreadable {
			if _, isLink := linked[u.Dir]; !isLink {
				hd.Unreadable = append(hd.Unreadable, u)
			}
		}
		for _, u := range unparseable {
			if _, isLink := linked[u.Dir]; !isLink {
				hd.Unparseable = append(hd.Unparseable, u)
			}
		}
	}
	// Directories holding a SKILL.md that was NOT submitted: somebody else's
	// skill (it parses and carries no Hadron header, so the walk leaves it
	// invisible on purpose), or a file that could not be read or attributed.
	// The plan cannot know about them, so it may plan a WRITE onto one; that
	// write is refused here, never allowed to replace the file (#692 review).
	submitted := map[string]bool{}
	for _, fct := range files {
		submitted[fct.DirName] = true
	}
	foreign, err := foreignSkillDirs(root, submitted, linked)
	if err != nil {
		return blockHost(hd, plan, ioReason(err))
	}
	p, err := plan(files)
	if err != nil {
		return hd, nil, err
	}
	hd.Scanned, hd.Judged = p.Scanned, p.Judged
	fsys := hostFS{root: root, dryRun: opts.dryRun, guard: func() *exportReasonDTO {
		_, r := checkRoot(home, parts)
		return r
	}}
	for _, e := range p.Entries {
		if e == nil {
			continue
		}
		if dir, target, isLink := touchesLink(e, linked); isLink {
			it := itemFor(e)
			it.Reasons = append(it.Reasons, linkReason(filepath.Join(root, dir), target))
			hd.Refused = append(hd.Refused, it)
			continue
		}
		if writesInto(e) && foreign[e.Name] {
			it := itemFor(e)
			it.Reasons = append(it.Reasons, exportReasonDTO{
				Code: reasonForeignFile,
				Message: fmt.Sprintf("%s holds a SKILL.md this command did not write (no Hadron header, or unreadable), so it was left alone; rename or remove it to export this skill here",
					filepath.Join(root, e.Name)),
				Origin: originClient,
			})
			hd.Refused = append(hd.Refused, it)
			continue
		}
		applyEntry(&hd, fsys, e)
	}
	for _, o := range p.Orphans {
		if o == nil {
			continue
		}
		od := statusOrphanDTO{Dir: o.DirName, NodeID: deref(o.NodeId), Source: deref(o.SourceUrn)}
		if !opts.prune {
			hd.Orphaned = append(hd.Orphaned, od)
			continue
		}
		if !safeDirName(o.DirName) {
			hd.Failed = append(hd.Failed, exportItemDTO{Name: o.DirName, NodeID: od.NodeID, Reasons: []exportReasonDTO{unsafeName(o.DirName)}, Kept: []string{}})
			continue
		}
		if _, r := fsys.remove(o.DirName); r != nil {
			hd.Failed = append(hd.Failed, exportItemDTO{Name: o.DirName, NodeID: od.NodeID, Reasons: []exportReasonDTO{*r}, Kept: []string{}})
			continue
		}
		hd.Pruned = append(hd.Pruned, od)
	}
	return hd, p, nil
}

// blockHost reports a host that cannot be written at all. The plan is still
// fetched — with no files, so nothing on a linked path is read — because the
// report must NAME every skill that was not written, not just say the host
// failed (matrix P07/P17/P18).
func blockHost(hd exportHostDTO, plan planFunc, reason exportReasonDTO) (exportHostDTO, *gen.SkillExportPlanSkillPlan, error) {
	hd.Failure = &reason
	p, err := plan(nil)
	if err != nil {
		return hd, nil, err
	}
	hd.Scanned, hd.Judged = p.Scanned, p.Judged
	for _, e := range p.Entries {
		if e == nil {
			continue
		}
		it := itemFor(e)
		it.Reasons = append([]exportReasonDTO{reason}, it.Reasons...)
		hd.Failed = append(hd.Failed, it)
	}
	return hd, p, nil
}

// checkRoot walks the root's components below home with Lstat, one at a time.
// Any symbolic link on that path refuses the host: a link anywhere — the root
// itself (P17) or an ancestor such as ~/.agents (P18) — could put this host's
// files into another host's directory, or anywhere else. Because every
// component below the resolved home is checked, two hosts' roots cannot
// resolve to one directory without a link being found, which is the
// cross-host comparison the plan (§3) asks for.
//
// A missing component is not a failure: the first export creates the root
// (P03). Everything that does exist up to that point has been checked, so the
// nearest existing ancestor is known to be safe.
func checkRoot(home string, parts []string) (exists bool, reason *exportReasonDTO) {
	p := home
	for _, part := range parts {
		p = filepath.Join(p, part)
		fi, err := os.Lstat(p)
		if errors.Is(err, os.ErrNotExist) {
			return false, nil
		}
		if err != nil {
			r := ioReason(err)
			return false, &r
		}
		if fi.Mode()&os.ModeSymlink != 0 {
			target, _ := os.Readlink(p)
			return false, &exportReasonDTO{
				Code: reasonRootIsLink,
				Message: fmt.Sprintf("%s is a symbolic link (to %s), so nothing was written for this host: writing through it could put these skills in another host's directory. Replace the link with a real directory to export here",
					p, cmp(target, "an unreadable target")),
				Origin: originClient,
			}
		}
		if !fi.IsDir() {
			return false, &exportReasonDTO{Code: reasonRootNotDir, Message: fmt.Sprintf("%s is not a directory", p), Origin: originClient}
		}
	}
	return true, nil
}

// checkSkillDir guards one skill directory before anything is written into or
// removed from it. A link is refused (P06); so is a non-directory.
func checkSkillDir(root, name string) *exportReasonDTO {
	p := filepath.Join(root, name)
	fi, err := os.Lstat(p)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		r := ioReason(err)
		return &r
	}
	if fi.Mode()&os.ModeSymlink != 0 {
		target, _ := os.Readlink(p)
		r := linkReason(p, target)
		return &r
	}
	if !fi.IsDir() {
		return &exportReasonDTO{Code: reasonNotDir, Message: fmt.Sprintf("%s is not a directory", p), Origin: originClient}
	}
	return nil
}

// linkedSkillDirs lists the entries directly under root that are symbolic
// links, with their targets.
func linkedSkillDirs(root string) (map[string]string, error) {
	out := map[string]string{}
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil, err
	}
	for _, en := range entries {
		if en.Type()&os.ModeSymlink != 0 {
			target, _ := os.Readlink(filepath.Join(root, en.Name()))
			out[en.Name()] = target
		}
	}
	return out, nil
}

// touchesLink reports whether an entry's action would write into, or remove,
// a linked skill directory — whatever action the server planned, since even a
// SKIP "current" would be a claim about another host's file.
func touchesLink(e *gen.SkillExportPlanSkillPlanEntriesSkillPlanEntry, linked map[string]string) (dir, target string, ok bool) {
	for _, d := range []string{e.Name, deref(e.MovedFrom)} {
		if t, isLink := linked[d]; d != "" && isLink {
			return d, t, true
		}
	}
	return "", "", false
}

func linkReason(p, target string) exportReasonDTO {
	return exportReasonDTO{
		Code:    reasonDirIsLink,
		Message: fmt.Sprintf("%s is a symbolic link (to %s), so nothing was written or removed there; remove the link to export this skill here", p, cmp(target, "an unreadable target")),
		Origin:  originClient,
	}
}

// applyEntry performs one planned action and records what actually happened.
// Every path out of it records the item exactly once, so one failing item
// never stops the others (cor:agt:030:00: an export runs to the end).
func applyEntry(hd *exportHostDTO, fsys hostFS, e *gen.SkillExportPlanSkillPlanEntriesSkillPlanEntry) {
	root := fsys.root
	it := itemFor(e)
	fail := func(r exportReasonDTO) {
		it.Reasons = append(it.Reasons, r)
		if r.Code == reasonForeignFile {
			// Not a failure: a file that is not ours was protected.
			hd.Refused = append(hd.Refused, it)
			return
		}
		hd.Failed = append(hd.Failed, it)
	}
	ep := e.ExportPlan
	if ep == nil {
		fail(exportReasonDTO{Code: reasonNoExportPlan, Message: "the server returned no export plan for this skill", Origin: originClient})
		return
	}
	switch ep.Action {
	case gen.SkillExportActionSkip:
		hd.Skipped = append(hd.Skipped, it)
		return
	case gen.SkillExportActionRefuse:
		hd.Refused = append(hd.Refused, it)
		return
	case gen.SkillExportActionFail:
		hd.Failed = append(hd.Failed, it)
		return
	case gen.SkillExportActionWrite, gen.SkillExportActionMove, gen.SkillExportActionRemove:
	default:
		fail(exportReasonDTO{Code: reasonUnknownAction, Message: fmt.Sprintf("the server planned %q, which this CLI does not know; nothing was done — upgrade the CLI", ep.Action), Origin: originClient})
		return
	}
	// A destructive action on a file the server said to preserve is a
	// contradiction; the file wins.
	if ep.PreservesExistingFile {
		fail(exportReasonDTO{Code: reasonPlanContradiction, Message: fmt.Sprintf("the server planned %s but also asked to preserve the existing file; nothing was done", ep.Action), Origin: originClient})
		return
	}

	switch ep.Action {
	case gen.SkillExportActionWrite:
		if r := fsys.write(e); r != nil {
			fail(*r)
			return
		}
		hd.Written = append(hd.Written, it)

	case gen.SkillExportActionMove:
		from := deref(e.MovedFrom)
		if from == "" {
			fail(exportReasonDTO{Code: reasonNoSource, Message: "the server planned a move with no source directory; nothing was done", Origin: originClient})
			return
		}
		if !safeDirName(from) {
			fail(unsafeName(from))
			return
		}
		if r := checkSkillDir(root, from); r != nil {
			fail(*r)
			return
		}
		// Preflight the SOURCE before touching the destination, so a source
		// that remove() would refuse (a linked SKILL.md) fails the item with
		// nothing written (#694 review).
		if r := checkNotLink(filepath.Join(root, from, skillFileName)); r != nil {
			fail(*r)
			return
		}
		// On a case-insensitive or normalising filesystem, "Demo" and "demo"
		// can be ONE directory. Writing the new name then removing the old
		// one would delete the file just written (#696 review). Such a
		// rename is done in place: rewrite the file, then rename the
		// directory to its new spelling. Nothing is removed.
		if sameDirectory(filepath.Join(root, from), filepath.Join(root, e.Name)) {
			if r := fsys.write(e); r != nil {
				fail(*r)
				return
			}
			if r := fsys.renameDir(from, e.Name); r != nil {
				fail(*r)
				return
			}
			hd.Moved = append(hd.Moved, exportMovedDTO{exportItemDTO: it, From: from})
			return
		}
		if r := fsys.write(e); r != nil {
			fail(*r)
			return
		}
		// The new file is in place. The old one goes last, so a failure here
		// leaves two copies (visible, fixable) rather than none. Under
		// --dry-run the same checks run and the same files are listed as kept.
		if from != e.Name {
			kept, r := fsys.remove(from)
			if r != nil {
				it.Reasons = append(it.Reasons, *r)
				hd.Failed = append(hd.Failed, it)
				return
			}
			it.Kept = kept
			if len(kept) > 0 {
				it.Reasons = append(it.Reasons, keptReason(root, from, kept))
			}
		}
		hd.Moved = append(hd.Moved, exportMovedDTO{exportItemDTO: it, From: from})

	case gen.SkillExportActionRemove:
		dir := deref(e.MovedFrom)
		if dir == "" {
			fail(exportReasonDTO{Code: reasonNoSource, Message: "the server planned a removal with no directory; nothing was done", Origin: originClient})
			return
		}
		if !safeDirName(dir) {
			fail(unsafeName(dir))
			return
		}
		if r := checkSkillDir(root, dir); r != nil {
			fail(*r)
			return
		}
		kept, r := fsys.remove(dir)
		if r != nil {
			fail(*r)
			return
		}
		it.Kept = kept
		if len(kept) > 0 {
			it.Reasons = append(it.Reasons, keptReason(root, dir, kept))
		}
		hd.Removed = append(hd.Removed, it)
	}
}

// hostFS is where one host's I/O happens. Every write and removal goes
// through it, so --dry-run runs exactly the same checks as a real run and
// differs only in not mutating (#692 review).
//
// guard re-checks the WHOLE root path (every component below $HOME) right
// before each mutation. That narrows the window in which an ancestor swapped
// for a link after the first check would be followed; it does not close it.
// Closing it needs directory-handle-relative calls (openat and friends), which
// are Unix-only while this CLI also ships for Windows. The refusal it enforces
// is about links a user CONFIGURED (a dotfiles setup), not a process racing the
// export inside the user's own home — which could write there directly anyway.
type hostFS struct {
	root   string
	dryRun bool
	guard  func() *exportReasonDTO
}

// write puts the server's rendered file, byte for byte, at
// <root>/<name>/SKILL.md, creating the directories it needs.
func (fs hostFS) write(e *gen.SkillExportPlanSkillPlanEntriesSkillPlanEntry) *exportReasonDTO {
	if !safeDirName(e.Name) {
		r := unsafeName(e.Name)
		return &r
	}
	if e.RenderedBody == nil {
		return &exportReasonDTO{Code: reasonNoBody, Message: "the server planned a write but sent no file to write; nothing was done", Origin: originClient}
	}
	if r := fs.guard(); r != nil {
		return r
	}
	if r := checkSkillDir(fs.root, e.Name); r != nil {
		return r
	}
	dir := filepath.Join(fs.root, e.Name)
	target := filepath.Join(dir, skillFileName)
	if r := checkNotLink(target); r != nil {
		return r
	}
	// OWNERSHIP at the destination (#694 review, Codex P1): only a SKILL.md
	// that carries a Hadron provenance header may be replaced. Checked on the
	// path the filesystem resolves — not by comparing names — so a foreign
	// "Demo/" on a case-insensitive filesystem (macOS, Windows) cannot be
	// reached as "demo/" and overwritten.
	if r := checkOwned(target); r != nil {
		return r
	}
	if fs.dryRun {
		return nil
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		r := ioReason(err)
		return &r
	}
	// Re-check after creating, immediately before the write: MkdirAll follows
	// a link it finds, and a link or a foreign file may have appeared since the
	// first checks (#696 review). The window narrows; see hostFS on why it
	// cannot close portably.
	if r := fs.recheck(e.Name); r != nil {
		return r
	}
	if r := checkNotLink(target); r != nil {
		return r
	}
	if r := checkOwned(target); r != nil {
		return r
	}
	if err := config.WriteFileAtomic(target, []byte(*e.RenderedBody), 0o644); err != nil {
		r := ioReason(err)
		return &r
	}
	return nil
}

// remove deletes <root>/<dir>/SKILL.md and then the directory, but only if
// nothing else is in it. It never follows a link. It returns the names left
// behind; under --dry-run, the names that WOULD be left, after the same checks.
func (fs hostFS) remove(dir string) ([]string, *exportReasonDTO) {
	if !safeDirName(dir) {
		r := unsafeName(dir)
		return nil, &r
	}
	if r := fs.guard(); r != nil {
		return nil, r
	}
	p := filepath.Join(fs.root, dir)
	fi, err := os.Lstat(p)
	if errors.Is(err, os.ErrNotExist) {
		return []string{}, nil
	}
	if err != nil {
		r := ioReason(err)
		return nil, &r
	}
	if fi.Mode()&os.ModeSymlink != 0 {
		target, _ := os.Readlink(p)
		r := linkReason(p, target)
		return nil, &r
	}
	if !fi.IsDir() {
		return nil, &exportReasonDTO{Code: reasonNotDir, Message: fmt.Sprintf("%s is not a directory; not removed", p), Origin: originClient}
	}
	file := filepath.Join(p, skillFileName)
	if r := checkNotLink(file); r != nil {
		return nil, r
	}
	entries, err := os.ReadDir(p)
	if err != nil {
		r := ioReason(err)
		return nil, &r
	}
	kept := []string{}
	for _, en := range entries {
		if en.Name() != skillFileName {
			kept = append(kept, en.Name())
		}
	}
	sort.Strings(kept)
	if fs.dryRun {
		return kept, nil
	}
	// Re-check right before each removal (#694 review): the root path, the
	// directory, and the file itself.
	if r := fs.recheck(dir); r != nil {
		return nil, r
	}
	if r := checkNotLink(file); r != nil {
		return nil, r
	}
	if err := os.Remove(file); err != nil && !errors.Is(err, os.ErrNotExist) {
		r := ioReason(err)
		return nil, &r
	}
	if len(kept) == 0 {
		if r := fs.recheck(dir); r != nil {
			return nil, r
		}
		if err := os.Remove(p); err != nil {
			r := ioReason(err)
			return nil, &r
		}
	}
	return kept, nil
}

// sameDirectory reports whether two paths are one existing directory — as on a
// case-insensitive filesystem, where "Demo" and "demo" resolve alike. Neither
// path is followed: both are Lstat'd, and links are refused elsewhere.
func sameDirectory(a, b string) bool {
	fa, errA := os.Lstat(a)
	fb, errB := os.Lstat(b)
	return errA == nil && errB == nil && fa.IsDir() && fb.IsDir() && os.SameFile(fa, fb)
}

// renameDir renames a skill directory in place (a case-only rename). Under
// --dry-run it only runs the checks.
func (fs hostFS) renameDir(from, to string) *exportReasonDTO {
	if r := fs.recheck(from); r != nil {
		return r
	}
	if fs.dryRun || from == to {
		return nil
	}
	if err := os.Rename(filepath.Join(fs.root, from), filepath.Join(fs.root, to)); err != nil {
		r := ioReason(err)
		return &r
	}
	return nil
}

// recheck re-runs the root-path guard and the skill-directory check, for use
// immediately before a mutation.
func (fs hostFS) recheck(dir string) *exportReasonDTO {
	if r := fs.guard(); r != nil {
		return r
	}
	return checkSkillDir(fs.root, dir)
}

// checkOwned refuses to replace an existing SKILL.md that carries no Hadron
// provenance header: that file was not written by this command. An absent
// file is fine; an unreadable one is an I/O failure.
func checkOwned(p string) *exportReasonDTO {
	// Only a REGULAR file is read: a FIFO or device named SKILL.md would block
	// the read (a dry run included), and is not ours to replace either way
	// (#696 review).
	fi, err := os.Lstat(p)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		r := ioReason(err)
		return &r
	}
	if !fi.Mode().IsRegular() {
		return &exportReasonDTO{Code: reasonNotRegular, Message: fmt.Sprintf("%s is not a regular file, so it was left alone", p), Origin: originClient}
	}
	data, err := os.ReadFile(p)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		r := ioReason(err)
		return &r
	}
	if _, _, _, ok := skilldoc.ParseProvenance(data); ok {
		return nil
	}
	return &exportReasonDTO{
		Code: reasonForeignFile,
		Message: fmt.Sprintf("%s was not written by hadron (no provenance header), so it was left alone; rename or remove it to export this skill here",
			p),
		Origin: originClient,
	}
}

// checkNotLink refuses a SKILL.md that is itself a symbolic link: writing or
// removing through it would act on whatever it points at.
func checkNotLink(p string) *exportReasonDTO {
	fi, err := os.Lstat(p)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		// Only "absent" is absent: an EACCES here would pass a dry run that
		// the real run then fails (#694 review).
		r := ioReason(err)
		return &r
	}
	if fi.Mode()&os.ModeSymlink == 0 {
		return nil
	}
	target, _ := os.Readlink(p)
	return &exportReasonDTO{
		Code:    reasonFileIsLink,
		Message: fmt.Sprintf("%s is a symbolic link (to %s), so it was left alone", p, cmp(target, "an unreadable target")),
		Origin:  originClient,
	}
}

// foreignSkillDirs lists the directories under root that hold a SKILL.md which
// was not submitted to the planner, excluding linked directories (handled on
// their own). Those files are not this command's to replace.
func foreignSkillDirs(root string, submitted map[string]bool, linked map[string]string) (map[string]bool, error) {
	out := map[string]bool{}
	entries, err := os.ReadDir(root)
	if errors.Is(err, os.ErrNotExist) {
		return out, nil
	}
	if err != nil {
		return nil, err
	}
	for _, en := range entries {
		name := en.Name()
		if _, isLink := linked[name]; isLink || !en.IsDir() || submitted[name] {
			continue
		}
		fi, err := os.Lstat(filepath.Join(root, name, skillFileName))
		// A linked SKILL.md is left to the link check, which reports it as
		// skill-file-is-link rather than as foreign (#694 review).
		// A non-regular SKILL.md (a FIFO, a device) is left to the write path,
		// which reports it precisely as skill-file-not-regular.
		if err == nil && fi.Mode().IsRegular() {
			out[name] = true
		}
	}
	return out, nil
}

// writesInto reports whether an entry's planned action writes into the
// directory named by the entry: WRITE, or MOVE (whose destination it is).
func writesInto(e *gen.SkillExportPlanSkillPlanEntriesSkillPlanEntry) bool {
	if e.ExportPlan == nil {
		return false
	}
	return e.ExportPlan.Action == gen.SkillExportActionWrite || e.ExportPlan.Action == gen.SkillExportActionMove
}

// safeDirName reports whether a name from the server can be joined onto a
// root without escaping it. The server validates names too; this is the
// client's own guard, since it is the client that touches the disk.
func safeDirName(name string) bool {
	return name != "" && name != "." && name != ".." &&
		!strings.ContainsAny(name, `/\`) && !strings.ContainsRune(name, 0)
}

func itemFor(e *gen.SkillExportPlanSkillPlanEntriesSkillPlanEntry) exportItemDTO {
	it := exportItemDTO{Node: e.Urn, NodeID: e.NodeId, Name: e.Name, Reasons: []exportReasonDTO{}, Kept: []string{}}
	if e.ExportPlan != nil {
		for _, r := range e.ExportPlan.Reasons {
			if r != nil {
				it.Reasons = append(it.Reasons, exportReasonDTO{Code: r.Code, Message: r.Message, Origin: originServer})
			}
		}
	}
	return it
}

func unsafeName(name string) exportReasonDTO {
	return exportReasonDTO{Code: reasonUnsafeName, Message: fmt.Sprintf("%q is not a safe directory name; nothing was done", name), Origin: originClient}
}

func ioReason(err error) exportReasonDTO {
	return exportReasonDTO{Code: reasonIOError, Message: err.Error(), Origin: originClient}
}

func keptReason(root, dir string, kept []string) exportReasonDTO {
	return exportReasonDTO{
		Code:    reasonDirKept,
		Message: fmt.Sprintf("%s holds other files (%s), so the directory was kept", filepath.Join(root, dir), strings.Join(kept, ", ")),
		Origin:  originClient,
	}
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

func toUnrecognizedDTO(in []*gen.SkillExportPlanSkillPlanUnrecognized) []exportUnrecognizedDTO {
	out := []exportUnrecognizedDTO{}
	for _, u := range in {
		if u == nil {
			continue
		}
		d := exportUnrecognizedDTO{
			Node: u.Urn, NodeID: u.NodeId, Name: u.Name, Memory: u.Memory,
			KeyCount: u.KeyCount, Keys: []string{}, Findings: []statusFindingDTO{},
		}
		d.Keys = append(d.Keys, u.Keys...)
		for _, f := range u.Findings {
			if f != nil {
				d.Findings = append(d.Findings, statusFindingDTO{Node: f.Urn, Memory: f.Memory, Rule: f.Rule, Severity: f.Severity, Message: f.Message, Hosts: []string{}})
			}
		}
		out = append(out, d)
	}
	return out
}

// exportHasFailures is the exit-code rule: any refused or failed item, or a
// host that could not be written, exits 5 AFTER the full report.
func exportHasFailures(dto exportDTO) bool {
	for _, h := range dto.Hosts {
		if h.Failure != nil || len(h.Refused) > 0 || len(h.Failed) > 0 {
			return true
		}
	}
	return false
}

func renderExport(w io.Writer, dto exportDTO) error {
	verb := func(done, would string) string {
		if dto.DryRun {
			return would
		}
		return done
	}
	if dto.DryRun {
		if _, err := fmt.Fprintln(w, "Dry run: nothing on disk was changed."); err != nil {
			return err
		}
	}
	for _, h := range dto.Hosts {
		if _, err := fmt.Fprintf(w, "\n%s → %s\n", h.Host, cmp(h.Root, "(no directory)")); err != nil {
			return err
		}
		if h.Failure != nil {
			if _, err := fmt.Fprintf(w, "  ✗ nothing written for this host: %s\n", h.Failure.Message); err != nil {
				return err
			}
		}
		rows := len(h.Written) + len(h.Moved) + len(h.Removed) + len(h.Skipped) + len(h.Refused) +
			len(h.Failed) + len(h.Pruned) + len(h.Orphaned) + len(h.Unreadable) + len(h.Unparseable)
		if rows == 0 {
			if _, err := fmt.Fprintln(w, "  nothing to export for this host"); err != nil {
				return err
			}
			continue
		}
		// Two nodes may declare one name (only one can be enabled for a host,
		// but a disabled or broken one is still reported). The table would
		// then show two identical SKILL cells, so a repeated name is qualified
		// by its node.
		seen := map[string]int{}
		for _, list := range [][]exportItemDTO{h.Written, h.Removed, h.Skipped, h.Refused, h.Failed} {
			for _, it := range list {
				seen[it.Name]++
			}
		}
		for _, it := range h.Moved {
			seen[it.Name]++
		}
		label := func(it exportItemDTO) string {
			name := cmp(it.Name, it.Node)
			if seen[it.Name] > 1 && it.Node != "" {
				return name + " (" + it.Node + ")"
			}
			return name
		}
		t := output.NewTable(w, "STATE", "SKILL", "DETAIL")
		for _, it := range h.Written {
			t.Row(verb("written", "would write"), label(it), reasonText(it.Reasons))
		}
		for _, it := range h.Moved {
			t.Row(verb("moved", "would move"), label(it.exportItemDTO), "from "+it.From+joinDetail(reasonText(it.Reasons)))
		}
		for _, it := range h.Removed {
			t.Row(verb("removed", "would remove"), label(it), reasonText(it.Reasons))
		}
		for _, it := range h.Skipped {
			t.Row("skipped", label(it), reasonText(it.Reasons))
		}
		for _, it := range h.Refused {
			t.Row("refused", label(it), reasonText(it.Reasons))
		}
		for _, it := range h.Failed {
			t.Row("failed", label(it), reasonText(it.Reasons))
		}
		for _, o := range h.Pruned {
			t.Row(verb("pruned", "would prune"), o.Dir, "orphaned")
		}
		for _, o := range h.Orphaned {
			t.Row("orphaned", o.Dir, "no enabled declaration claims it; --prune removes it")
		}
		for _, u := range h.Unreadable {
			t.Row("unreadable", u.Dir, u.Error)
		}
		for _, u := range h.Unparseable {
			t.Row("unparseable", u.Dir, u.Error)
		}
		if err := t.Flush(); err != nil {
			return err
		}
	}
	if len(dto.Unrecognized) > 0 {
		if _, err := fmt.Fprintf(w, "\nNot exported for any host: these nodes declare an exports key that names no known host.\n"); err != nil {
			return err
		}
		for _, u := range dto.Unrecognized {
			if _, err := fmt.Fprintf(w, "  %s (%s): %s\n", cmp(u.Name, u.Node), u.Node, strings.Join(u.Keys, ", ")); err != nil {
				return err
			}
		}
	}
	if !dto.DryRun {
		if _, err := fmt.Fprintln(w, "\nHosts load skills when a session starts: restart a running session to pick up changes."); err != nil {
			return err
		}
	}
	return nil
}

func reasonText(rs []exportReasonDTO) string {
	parts := make([]string, 0, len(rs))
	for _, r := range rs {
		if r.Message != "" {
			parts = append(parts, r.Message)
		} else {
			parts = append(parts, r.Code)
		}
	}
	return strings.Join(parts, "; ")
}

func joinDetail(s string) string {
	if s == "" {
		return ""
	}
	return "; " + s
}
