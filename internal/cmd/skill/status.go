package skill

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"github.com/Khan/genqlient/graphql"
	"github.com/spf13/cobra"

	"github.com/hadron-memory/hadron-cli/internal/api"
	"github.com/hadron-memory/hadron-cli/internal/api/gen"
	"github.com/hadron-memory/hadron-cli/internal/cmdutil"
	"github.com/hadron-memory/hadron-cli/internal/exitcode"
	"github.com/hadron-memory/hadron-cli/internal/output"
	"github.com/hadron-memory/hadron-cli/internal/skilldoc"
)

// skillFileName is the one file a skill directory is known by; a directory
// without it is not a skill and is not reported.
const skillFileName = "SKILL.md"

// statusFindingDTO reuses lint's finding shape so a script consuming `skill
// lint --json` and `skill status --json` sees ONE vocabulary.
type statusFindingDTO = lintFindingDTO

// statusEntryDTO is one row per declared node — the stable --json shape.
//
// Class is a POINTER on purpose. The server returns null when the file could
// not be parsed ("a parse failure is reported as a FAILURE, not as a class"),
// and a string zero value would render that as an empty class beside
// ParseFailure — two fields disagreeing about the same file. Null is what the
// server said, so null is what a parsing agent gets.
type statusEntryDTO struct {
	Node         string             `json:"node"`
	NodeID       string             `json:"nodeId"`
	Name         string             `json:"name"`
	Class        *string            `json:"class"`
	ParseFailure bool               `json:"parseFailure"`
	MovedFrom    string             `json:"movedFrom,omitempty"`
	Findings     []statusFindingDTO `json:"findings"`
}

// statusOrphanDTO is a file on disk that paired with no declared node.
type statusOrphanDTO struct {
	Dir    string `json:"dir"`
	NodeID string `json:"nodeId,omitempty"`
	Source string `json:"source,omitempty"`
}

// statusUnreadableDTO is a file this client could not read off the disk — an
// I/O failure, not a parse failure. It is reported separately because the two
// are different facts and the operator needs the errno to act on it.
type statusUnreadableDTO struct {
	Dir   string `json:"dir"`
	Error string `json:"error"`
}

// statusDTO is the whole report. Every slice is initialized so an empty field
// renders as [] and never as null.
type statusDTO struct {
	Root       string                `json:"root"`
	Host       string                `json:"host"`
	Scanned    int                   `json:"scanned"`
	Judged     int                   `json:"judged"`
	Entries    []statusEntryDTO      `json:"entries"`
	Orphans    []statusOrphanDTO     `json:"orphans"`
	Unreadable []statusUnreadableDTO `json:"unreadable"`
	// Unparseable: on disk, frontmatter unreadable AND no provenance header to
	// attribute it by — so it is reported here rather than sent up, where it
	// would be classed an orphan on no evidence.
	Unparseable []statusUnreadableDTO `json:"unparseable"`
	// Excluded: memories left out of the selection by the TARGET rather than by
	// the selector — today only --to plugin, which may carry PUBLIC memories
	// only (D9). Reported rather than silently dropped, so a short result is
	// never mistaken for a clean corpus.
	Excluded []statusExcludedDTO `json:"excluded"`
}

// statusExcludedDTO is a memory the target's own rule removed from scope.
type statusExcludedDTO struct {
	Memory string `json:"memory"`
	Reason string `json:"reason"`
}

func newCmdStatus(f *cmdutil.Factory) *cobra.Command {
	var sel selectorFlags
	var strict bool
	var to, host string
	cmd := &cobra.Command{
		Use:   "status (-m <memory>... | --all)",
		Short: "Compare the skill files on disk against the corpus that declares them",
		Long: `Report how the skill files on disk compare with the nodes that declare
them — reading both and writing nothing.

The skills root is walked for <root>/*/SKILL.md, each file is paired to its
node by the id in its provenance header, and the server returns a drift CLASS
per declared node. A file with no Hadron header is somebody else's skill: it is
never listed, moved or removed by any hadron skill command.

THE CLASS IS THE SERVER'S WORD, NOT THIS CLIENT'S. The vocabulary lives in one
place so it cannot drift between the CLI, MCP and the portal, and nothing here
invents, defaults or rewrites one. A file that exists and does not PARSE gets
no class at all — it is reported as a parse FAILURE, which is a separate fact
from the ten classes. Its CLASS cell shows "—" (no class was returned) and the
DETAIL cell says why; in --json the class key is PRESENT and null, never
omitted, so "the server returned no class" stays distinguishable from "this
client never asked".

Selection is deliberately memory-wide: there is no --node here, because a
status over one node cannot answer "is my skill set fresh?" — the orphan and
collision classes are properties of a SET. Use skill lint --node to judge a
single node.

--to plugin keeps only PUBLIC memories (D9): the committed bundle lives in a
public repo, so a private memory's tasks cannot ship in it. What it left out is
REPORTED, never silently dropped. The symbolic roots (user/project/plugin) are
host-specific, so a --host other than claudeSkill must name a directory.

Exit codes: an ERROR finding exits 5, as in skill lint. Drift alone exits 0 —
so a CI gate is an explicit --strict, which exits 5 on any drift, any parse
failure, or any orphan.`,
		Example: `  hadron skill status -m hrn:mem:hadronmemory.com:core
  hadron skill status --all --json
  hadron skill status --all --to plugin --strict`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if err := sel.validateNoNode(); err != nil {
				return err
			}
			root, err := resolveSkillsRoot(cmd, to, host)
			if err != nil {
				return err
			}
			client, err := f.GraphQLClient()
			if err != nil {
				return err
			}

			// Resolve the selector to explicit memories even for --all.
			// SkillPlanInput treats an OMITTED `memories` as "every memory the
			// caller can read", which is the SERVER's definition of all; this
			// CLI already has one (the three listings --all documents, shared
			// with skill lint). Sending the explicit list keeps --all meaning
			// exactly one thing across both verbs rather than two definitions
			// that agree until they quietly do not.
			mems, err := resolveMemories(cmd, client, &sel)
			if err != nil {
				return err
			}
			// D9/§6: the plugin bundle is committed to a PUBLIC repo, so it can
			// only carry tasks from PUBLIC memories — a customer's private task
			// cannot ship in a public artifact. Without this filter the
			// documented CI gate (`status --all --to plugin --strict`) compares
			// private declarations against the public directory and fails on a
			// bundle that is perfectly current.
			mems, excluded := filterForTarget(mems, to)

			files, unreadable, unparseable, err := walkSkillFiles(root)
			if err != nil {
				return err
			}

			// An EMPTY selection must not be sent. `memories` carries
			// `omitempty`, so an empty slice is omitted from the wire — and the
			// server reads an omitted `memories` as EVERY memory the caller can
			// read. The one case where this client has decided the scope is
			// empty is exactly the case where it would silently widen to the
			// opposite, including the memories --all deliberately excludes.
			if len(mems) == 0 {
				dto := emptyStatusDTO(root, host, unreadable, unparseable, excluded)
				return finishStatus(f, dto, strict)
			}

			refs := make([]string, 0, len(mems))
			for _, m := range mems {
				refs = append(refs, m.URN)
			}
			input := &gen.SkillPlanInput{
				Intent:   gen.SkillPlanIntentStatus,
				Host:     &host,
				Memories: refs,
				Files:    files,
			}
			resp, err := gen.SkillPlan(cmd.Context(), client, input)
			if err != nil {
				return api.MapError(err)
			}
			if resp.SkillPlan == nil {
				return exitcode.Newf(exitcode.Error, "skillPlan returned no result")
			}
			dto := toStatusDTO(root, host, resp.SkillPlan, unreadable, unparseable)
			dto.Excluded = excluded

			return finishStatus(f, dto, strict)
		},
	}
	sel.registerNoNode(cmd)
	cmd.Flags().BoolVar(&strict, "strict", false, "exit 5 on any drift, parse failure or orphan")
	cmd.Flags().StringVar(&to, "to", "user", "skills root: user, project, plugin, or a directory")
	cmd.Flags().StringVar(&host, "host", skilldoc.HostClaudeSkill, "skill host, named by its property key (claudeSkill)")
	return cmd
}

// classCurrent is the one class this command compares against — to decide
// whether --strict has anything to complain about. It is NOT a class this
// client can assign: every class rendered comes from the server verbatim.
const classCurrent = "current"

// resolveMemories turns the selector into the memories the plan will scan.
// Both branches return a full memoryInfo — including VISIBILITY, which the
// plugin target filters on — so `-m` costs one lookup per ref and gains an
// early, local refusal of a ref the server would otherwise reject mid-request.
func resolveMemories(cmd *cobra.Command, client graphql.Client, sel *selectorFlags) ([]*memoryInfo, error) {
	if sel.all {
		return allMemories(cmd, client)
	}
	out := make([]*memoryInfo, 0, len(sel.memories))
	seen := map[string]bool{}
	for _, ref := range sel.memories {
		m, err := lookupMemory(cmd, client, ref)
		if err != nil {
			return nil, err
		}
		if seen[m.ID] { // one memory named twice must not be scanned twice
			continue
		}
		seen[m.ID] = true
		out = append(out, m)
	}
	return out, nil
}

// filterForTarget applies the TARGET's own scope rule, returning what survived
// and what it removed. Only `plugin` has one: the bundle is committed to a
// public repo, so it may carry PUBLIC memories only (D9 — a visibility rule,
// not a memory allowlist, so a PUBLIC memory in any org qualifies).
func filterForTarget(mems []*memoryInfo, to string) ([]*memoryInfo, []statusExcludedDTO) {
	excluded := []statusExcludedDTO{}
	if to != "plugin" {
		return mems, excluded
	}
	kept := make([]*memoryInfo, 0, len(mems))
	for _, m := range mems {
		if m.Visibility == memoryVisibilityPublic {
			kept = append(kept, m)
			continue
		}
		excluded = append(excluded, statusExcludedDTO{
			Memory: m.URN,
			Reason: "not PUBLIC — the committed plugin bundle cannot carry a private memory's tasks",
		})
	}
	return kept, excluded
}

// emptyStatusDTO is the report for a selection that resolved to NO memory. It
// is built locally rather than by asking the server, because an empty
// `memories` list is omitted on the wire and an omitted one means the opposite
// of empty (every memory the caller can read).
func emptyStatusDTO(root, host string, unreadable, unparseable []statusUnreadableDTO, excluded []statusExcludedDTO) statusDTO {
	return statusDTO{
		Root: root, Host: host,
		Entries: []statusEntryDTO{}, Orphans: []statusOrphanDTO{},
		Unreadable: unreadable, Unparseable: unparseable, Excluded: excluded,
	}
}

// finishStatus renders the report and returns the user-visible exit code.
func finishStatus(f *cmdutil.Factory, dto statusDTO, strict bool) error {
	hasError, drift := false, false
	for _, e := range dto.Entries {
		if e.ParseFailure || (e.Class != nil && *e.Class != classCurrent) {
			drift = true
		}
		for _, fnd := range e.Findings {
			if fnd.Severity == skilldoc.SevError {
				hasError = true
			}
		}
	}
	if len(dto.Orphans) > 0 || len(dto.Unreadable) > 0 || len(dto.Unparseable) > 0 {
		drift = true
	}
	if err := output.Write(f.IOStreams, f.JSON, dto, func(w io.Writer) error {
		return renderStatus(w, dto)
	}); err != nil {
		return err
	}
	if hasError || (strict && drift) {
		return exitcode.Silent(exitcode.Conflict)
	}
	return nil
}

// cmp returns s, or fallback when s is empty — so an omitted --to is named by
// its default in an error rather than as a blank.
func cmp(s, fallback string) string {
	if s == "" {
		return fallback
	}
	return s
}

// hostDirs maps a host to the directory name its skills live under. ONLY
// claudeSkill has one, because only claudeSkill has a renderer (D10) — and a
// host with no renderer has no root this command could be right about.
var hostDirs = map[string]string{skilldoc.HostClaudeSkill: ".claude"}

// resolveSkillsRoot maps --to onto a directory. `project` and `plugin` are
// anchored at the git toplevel and are a usage error outside a worktree —
// resolving them against the process's cwd would write a customer's skills
// into whatever directory the shell happened to be in.
//
// The symbolic roots are HOST-SPECIFIC: `--host` selects the renderer and the
// host's root (§3/D10). A host with no mapping is REFUSED for a symbolic
// destination rather than silently resolved to Claude's directory — which
// would compare one host's declarations against another host's files and
// report the difference as drift, with every orphan and every never-exported
// row an artifact of the mismatch. An explicit directory stays the override,
// so a second host is testable before it has a root of its own.
func resolveSkillsRoot(cmd *cobra.Command, to, host string) (string, error) {
	hostDir, known := hostDirs[host]
	if !known {
		switch to {
		case "", "user", "project", "plugin":
			return "", exitcode.Newf(exitcode.Usage,
				"--host %s has no skills root of its own (only %s does), so --to %s cannot be resolved for it — name a directory instead: --to <dir>",
				host, skilldoc.HostClaudeSkill, cmp(to, "user"))
		}
	}
	switch to {
	case "", "user":
		home, err := os.UserHomeDir()
		if err != nil {
			return "", exitcode.Newf(exitcode.Error, "cannot locate your home directory for --to user: %v", err)
		}
		return filepath.Join(home, hostDir, "skills"), nil
	case "project", "plugin":
		out, err := exec.CommandContext(cmd.Context(), "git", "rev-parse", "--show-toplevel").Output()
		if err != nil {
			return "", exitcode.Newf(exitcode.Usage,
				"--to %s needs a git worktree — run it inside a checkout, or name a directory with --to <dir>", to)
		}
		top := strings.TrimSpace(string(out))
		if to == "project" {
			return filepath.Join(top, hostDir, "skills"), nil
		}
		return filepath.Join(top, "plugins", "hadron-cli", "skills"), nil
	default:
		return to, nil
	}
}

// walkSkillFiles reads <root>/*/SKILL.md and projects each onto the file FACTS
// the seam takes. It never sends a path or any content: the server does not
// read the caller's disk and is not told where it is.
//
// FIVE outcomes per directory, and they are different facts. Only the two
// that can be ATTRIBUTED are sent: a claim about a file we cannot identify
// would be a claim the server has no way to check.
//
//   - no SKILL.md, or a file that PARSES and carries no Hadron provenance
//     header — INVISIBLE. Not sent, not reported. `hadron skill` never touches
//     a file it did not generate, and reporting one would invite somebody to
//     "fix" it.
//   - parsed, with provenance — SENT: the facts, including a fileHash
//     recomputed from the file alone (that recomputation is what makes
//     `locally-edited` detectable).
//   - does not parse, provenance RECOVERED — SENT as parseFailed with its
//     nodeId, so the failure is attributed to its node instead of arriving as
//     an orphan.
//   - does not parse and carries NO recoverable provenance — reported locally
//     under `unparseable`, NOT sent. We cannot tell it is ours, and an orphan
//     claim on no evidence is worse than silence; but it is still shown,
//     because silently skipping a broken file is the outcome to avoid.
//   - present but UNREADABLE (an I/O error) — reported locally under
//     `unreadable` with the errno, NOT sent. With no bytes there is no
//     provenance, so there is nothing to attribute it by.
//
// A missing root is zero files, not an error: "nothing exported yet" is a
// legitimate answer to "what does my disk look like", and the report names the
// root so an empty result is never mistaken for a wrong one.
func walkSkillFiles(root string) ([]*gen.SkillFileFactsInput, []statusUnreadableDTO, []statusUnreadableDTO, error) {
	files := []*gen.SkillFileFactsInput{}
	unreadable := []statusUnreadableDTO{}
	unparseable := []statusUnreadableDTO{}

	dirs, err := os.ReadDir(root)
	if err != nil {
		if os.IsNotExist(err) {
			return files, unreadable, unparseable, nil
		}
		return nil, nil, nil, exitcode.Newf(exitcode.Error, "reading skills root %s: %v", root, err)
	}
	for _, d := range dirs {
		if !d.IsDir() {
			continue
		}
		dirName := d.Name()
		data, err := os.ReadFile(filepath.Join(root, dirName, skillFileName)) // #nosec G304 — the root is the user's own skills directory
		if err != nil {
			if os.IsNotExist(err) {
				continue // a directory without a SKILL.md is not a skill
			}
			unreadable = append(unreadable, statusUnreadableDTO{Dir: dirName, Error: err.Error()})
			continue
		}
		parsed, err := skilldoc.ParseFile(data)
		if err != nil {
			// The frontmatter did not parse. The provenance header sits BELOW
			// it and usually still reads, so recover it: a parse failure that
			// travels with its node id is ATTRIBUTED (class null +
			// parseFailure true), and one that does not is reported by the
			// server as an ORPHAN — a claim that a real skill belongs to no
			// node, and precisely what `export --prune` removes.
			id, source, headerHash, found := skilldoc.ParseProvenance(data)
			if !found {
				// Unparseable AND unattributable: we cannot tell it is ours,
				// so it is not sent. Claiming an orphan here would assert
				// something about a file that may be nobody's business of
				// ours — but it is still reported, because working around a
				// broken file in silence is the one outcome to avoid.
				unparseable = append(unparseable, statusUnreadableDTO{Dir: dirName, Error: err.Error()})
				continue
			}
			yes := true
			facts := &gen.SkillFileFactsInput{DirName: dirName, ParseFailed: &yes}
			if id != "" {
				facts.NodeId = &id
			}
			if source != "" {
				facts.SourceUrn = &source
			}
			if headerHash != "" {
				facts.HeaderHash = &headerHash
			}
			// No fileHash: it cannot be recomputed from a file whose
			// frontmatter is unreadable, and inventing one would answer a
			// staleness question nobody can actually answer.
			files = append(files, facts)
			continue
		}
		if parsed.Source == "" {
			continue // somebody else's skill — invisible to every verb
		}
		facts := &gen.SkillFileFactsInput{DirName: dirName}
		// The hash recomputed from the file's own bytes. An empty id hashes as
		// the EMPTY STRING rather than being skipped, which is what keeps a
		// pre-§4a file self-consistent against the header it carries.
		fileHash := skilldoc.Hash(parsed.ID, parsed.Source, parsed.Name, parsed.Description, parsed.Body)
		facts.FileHash = &fileHash
		facts.SourceUrn = &parsed.Source
		if parsed.ID != "" {
			facts.NodeId = &parsed.ID
		}
		if parsed.Hash != "" {
			facts.HeaderHash = &parsed.Hash
		}
		if len(parsed.Extra) > 0 {
			// Render writes no frontmatter key but name/description, so an
			// extra key IS a local edit — one the header hash cannot see,
			// because the hash covers the rendered inputs and not the file's
			// other keys.
			yes := true
			facts.HasExtraFrontmatter = &yes
		}
		files = append(files, facts)
	}
	sort.Slice(files, func(i, j int) bool { return files[i].DirName < files[j].DirName })
	return files, unreadable, unparseable, nil
}

// toStatusDTO projects the server's plan onto the command package's own stable
// shape — never the genqlient structs, which regeneration may rename.
func toStatusDTO(root, host string, plan *gen.SkillPlanSkillPlan, unreadable, unparseable []statusUnreadableDTO) statusDTO {
	dto := statusDTO{
		Root:        root,
		Host:        host,
		Scanned:     plan.Scanned,
		Judged:      plan.Judged,
		Entries:     []statusEntryDTO{},
		Orphans:     []statusOrphanDTO{},
		Unreadable:  unreadable,
		Unparseable: unparseable,
	}
	for _, e := range plan.Entries {
		if e == nil {
			continue
		}
		row := statusEntryDTO{
			Node:         e.Urn,
			NodeID:       e.NodeId,
			Name:         e.Name,
			Class:        e.Class,
			ParseFailure: e.ParseFailure,
			Findings:     []statusFindingDTO{},
		}
		if e.MovedFrom != nil {
			row.MovedFrom = *e.MovedFrom
		}
		for _, fnd := range e.Findings {
			if fnd == nil {
				continue
			}
			row.Findings = append(row.Findings, statusFindingDTO{
				Node: fnd.Urn, Memory: fnd.Memory, Rule: fnd.Rule, Severity: fnd.Severity, Message: fnd.Message,
			})
		}
		dto.Entries = append(dto.Entries, row)
	}
	for _, o := range plan.Orphans {
		if o == nil {
			continue
		}
		row := statusOrphanDTO{Dir: o.DirName}
		if o.NodeId != nil {
			row.NodeID = *o.NodeId
		}
		if o.SourceUrn != nil {
			row.Source = *o.SourceUrn
		}
		dto.Orphans = append(dto.Orphans, row)
	}
	return dto
}

// renderStatus writes the human view: one row per declared node, then the
// files that paired with nothing, then the counts.
func renderStatus(w io.Writer, dto statusDTO) error {
	fmt.Fprintf(w, "%s (host %s)\n\n", dto.Root, dto.Host)
	if len(dto.Excluded) > 0 {
		fmt.Fprintf(w, "Excluded from scope by the target:\n")
		for _, e := range dto.Excluded {
			fmt.Fprintf(w, "  %s — %s\n", e.Memory, e.Reason)
		}
		fmt.Fprintln(w)
	}
	if len(dto.Entries) == 0 && len(dto.Orphans) == 0 && len(dto.Unreadable) == 0 && len(dto.Unparseable) == 0 {
		// scanned distinguishes an empty corpus from one the caller cannot
		// see; without it both render as "nothing to do" and a permission
		// problem reads as a clean bill of health.
		fmt.Fprintf(w, "no skill declarations judged for this host (%d node(s) carrying a declaration were in scope)\n", dto.Scanned)
		if dto.Judged == 0 && dto.Scanned == 0 {
			fmt.Fprintf(w, "no memory was in scope — nothing was asked of the server, so this is not an all-clear\n")
		}
		return nil
	}
	t := output.NewTable(w, "NAME", "CLASS", "NODE", "DETAIL")
	for _, e := range dto.Entries {
		t.Row(e.Name, classCell(e), e.Node, detailCell(e))
	}
	if err := t.Flush(); err != nil {
		return err
	}
	if len(dto.Orphans) > 0 {
		fmt.Fprintf(w, "\nOrphaned — on disk, paired with no declared node (removed only by export --prune):\n")
		for _, o := range dto.Orphans {
			ref := o.Source
			if ref == "" {
				ref = "no source in header"
			}
			fmt.Fprintf(w, "  %s (%s)\n", o.Dir, ref)
		}
	}
	if len(dto.Unreadable) > 0 {
		fmt.Fprintf(w, "\nUnreadable — the file is there and this client could not read it:\n")
		for _, u := range dto.Unreadable {
			fmt.Fprintf(w, "  %s: %s\n", u.Dir, u.Error)
		}
	}
	if len(dto.Unparseable) > 0 {
		fmt.Fprintf(w, "\nUnparseable and unattributable — does not parse and carries no provenance header,\nso it is not reported to the server as anything (it may not be ours):\n")
		for _, u := range dto.Unparseable {
			fmt.Fprintf(w, "  %s: %s\n", u.Dir, u.Error)
		}
	}
	for _, e := range dto.Entries {
		for _, fnd := range e.Findings {
			fmt.Fprintf(w, "\n%s %s: %s\n  %s", fnd.Severity, fnd.Rule, fnd.Node, fnd.Message)
		}
	}
	fmt.Fprintf(w, "\n%d of %d declaring node(s) judged for host %s\n", dto.Judged, dto.Scanned, dto.Host)
	return nil
}

// noClass is what the CLASS cell shows when the server returned none. It is
// the repo's "no value" glyph (an em dash, as in `memory ls` and `spec
// register`) rather than a new symbol, because no OTHER column on this surface
// spells a definite answer with it — the hazard
// review:a-new-meaning-for-an-existing-glyph exists for is a reader learning
// the glyph from a neighbour that means "definitely no", and there is no such
// neighbour here. What removes the remaining ambiguity is the DETAIL cell,
// which states the one cause an absent class can have.
//
// It is deliberately NOT a word: a token in the CLASS column reads as an
// eleventh class, which is precisely the thing the server declined to mint.
const noClass = "—"

// classCell renders the server's class, or the absence of one. A parse failure
// has NO class, and printing a Go zero value here would show it as a blank
// cell beside a clean-looking row — the failure rendering as success.
func classCell(e statusEntryDTO) string {
	if e.Class == nil {
		return noClass
	}
	return *e.Class
}

func detailCell(e statusEntryDTO) string {
	switch {
	case e.ParseFailure:
		return "file does not parse"
	case e.MovedFrom != "":
		return "was " + e.MovedFrom
	default:
		return ""
	}
}
