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
			root, err := resolveSkillsRoot(cmd, to)
			if err != nil {
				return err
			}
			client, err := f.GraphQLClient()
			if err != nil {
				return err
			}

			// Resolve the selector to explicit memory refs even for --all.
			// SkillPlanInput treats an OMITTED `memories` as "every memory the
			// caller can read", which is the SERVER's definition of all; this
			// CLI already has one (the three listings --all documents, shared
			// with skill lint). Sending the explicit list keeps --all meaning
			// exactly one thing across both verbs rather than two definitions
			// that agree until they quietly do not.
			memRefs, err := resolveMemoryRefs(cmd, client, &sel)
			if err != nil {
				return err
			}

			files, unreadable, unparseable, err := walkSkillFiles(root)
			if err != nil {
				return err
			}

			input := &gen.SkillPlanInput{
				Intent:   gen.SkillPlanIntentStatus,
				Host:     &host,
				Memories: memRefs,
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

// resolveMemoryRefs turns the selector into the explicit memory refs the plan
// input takes. `-m` passes the user's refs through canonicalized (the server
// resolves every accepted grammar); `--all` expands to the same three listings
// skill lint means by it.
func resolveMemoryRefs(cmd *cobra.Command, client graphql.Client, sel *selectorFlags) ([]string, error) {
	if sel.all {
		mems, err := allMemories(cmd, client)
		if err != nil {
			return nil, err
		}
		refs := make([]string, 0, len(mems))
		for _, m := range mems {
			refs = append(refs, m.URN)
		}
		return refs, nil
	}
	refs := make([]string, 0, len(sel.memories))
	for _, m := range sel.memories {
		refs = append(refs, cmdutil.CanonicalMemoryRef(m))
	}
	return dedupe(refs), nil
}

// resolveSkillsRoot maps --to onto a directory. `project` and `plugin` are
// anchored at the git toplevel and are a usage error outside a worktree —
// resolving them against the process's cwd would write a customer's skills
// into whatever directory the shell happened to be in.
func resolveSkillsRoot(cmd *cobra.Command, to string) (string, error) {
	switch to {
	case "", "user":
		home, err := os.UserHomeDir()
		if err != nil {
			return "", exitcode.Newf(exitcode.Error, "cannot locate your home directory for --to user: %v", err)
		}
		return filepath.Join(home, ".claude", "skills"), nil
	case "project", "plugin":
		out, err := exec.CommandContext(cmd.Context(), "git", "rev-parse", "--show-toplevel").Output()
		if err != nil {
			return "", exitcode.Newf(exitcode.Usage,
				"--to %s needs a git worktree — run it inside a checkout, or name a directory with --to <dir>", to)
		}
		top := strings.TrimSpace(string(out))
		if to == "project" {
			return filepath.Join(top, ".claude", "skills"), nil
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
// Three outcomes per directory, and they are different facts:
//
//   - no SKILL.md, or a file with no Hadron provenance header — INVISIBLE.
//     Not sent, not reported. `hadron skill` never touches a file it did not
//     generate, and reporting one would invite somebody to "fix" it.
//   - parsed — the facts, including a fileHash recomputed from the file alone
//     (that recomputation is what makes `locally-edited` detectable).
//   - present but UNREADABLE — sent as parseFailed, and also returned
//     separately with the OS error. Omitting it would make the server say
//     `never-exported`, a false statement about a disk it cannot see; calling
//     it a parse failure is true about the only thing we know (it is there and
//     we could not turn it into facts), and the errno is preserved for the
//     human rather than dissolved into a class.
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
	if len(dto.Entries) == 0 && len(dto.Orphans) == 0 && len(dto.Unreadable) == 0 && len(dto.Unparseable) == 0 {
		// scanned distinguishes an empty corpus from one the caller cannot
		// see; without it both render as "nothing to do" and a permission
		// problem reads as a clean bill of health.
		fmt.Fprintf(w, "no skill declarations judged for this host (%d node(s) carrying a declaration were in scope)\n", dto.Scanned)
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
