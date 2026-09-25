package skill

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"syscall"

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
	// ScopeEmpty is true when the selection resolved to NO memory. Nothing was
	// then asked of the server, so this report verified nothing — it is the one
	// state that must never read as an all-clear, and --strict treats it as
	// drift for exactly that reason.
	ScopeEmpty bool `json:"scopeEmpty"`
	// Unchecked: provenance-bearing files found on disk that were never
	// submitted, because there was no memory to compare them against.
	Unchecked []statusUncheckedDTO `json:"unchecked"`
}

// statusUncheckedDTO is a generated file nothing was able to judge.
type statusUncheckedDTO struct {
	Dir    string `json:"dir"`
	Source string `json:"source,omitempty"`
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
per declared node. A file that PARSES and carries no Hadron header is somebody
else's skill: it is never listed, moved or removed by any hadron skill command.

A file that does NOT parse is reported either way, because "not ours" is a
claim its own header would have to support and a broken file cannot: when the
provenance below the frontmatter is still readable the failure is attributed to
its node, and otherwise the file is listed as unparseable — which --strict
counts as drift even though such a file may be third-party.

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

The symbolic roots (user/project/plugin) are host-specific, so a --host other
than claudeSkill must name a directory.

Exit codes: an ERROR finding exits 5, as in skill lint. Drift and warnings
alone exit 0 — so a CI gate is an explicit --strict, which exits 5 on any
drift, any parse failure, any orphan, a scope that resolved to no memory, and
(as skill lint does) any WARNING finding.`,
		Example: `  hadron skill status -m hrn:mem:hadronmemory.com:core
  hadron skill status --all --json
  hadron skill status -m hrn:mem:hadronmemory.com:core --to plugin --strict   # a CI gate names its memories; --all makes the token's reach the selection`,
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
				dto := emptyStatusDTO(root, host, files, unreadable, unparseable)
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

			return finishStatus(f, dto, strict)
		},
	}
	sel.registerNoNode(cmd)
	cmd.Flags().BoolVar(&strict, "strict", false, "exit 5 on any drift, parse failure, orphan, warning finding, or an empty scope")
	cmd.Flags().StringVar(&to, "to", "user", "skills root: user, project, plugin, or a directory")
	cmd.Flags().StringVar(&host, "host", skilldoc.HostClaudeSkill, "skill host, named by its property key (claudeSkill)")
	return cmd
}

// classCurrent is the one class this command compares against — to decide
// whether --strict has anything to complain about. It is NOT a class this
// client can assign: every class rendered comes from the server verbatim.
const classCurrent = "current"

// resolveMemories turns the selector into the memories the plan will scan. No
// target filters them — D9 was ruled the other way, so every target sees every
// accessible memory; do not reintroduce a filter here (TestSkillStatusPlugin-
// TargetFiltersNothing pins that).
//
// `-m` resolves each ref through a lookup rather than passing the string
// through: it costs one round trip per ref and buys an early, LOCAL refusal of
// a bad ref, instead of one that fails mid-request with a message naming a
// GraphQL field rather than the flag.
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

// emptyStatusDTO is the report for a selection that resolved to NO memory. It
// is built locally rather than by asking the server, because an empty
// `memories` list is omitted on the wire and an omitted one means the opposite
// of empty (every memory the caller can read).
// @codex on #652: the files already found on disk must travel into this
// report. Discarding them let a `--strict` run exit 0 while generated skills
// sat in the target unpaired and unexamined — a gate passing on a result
// nothing checked. (The example then read `--all --to plugin --strict`; the
// documented gate names its memories with -m now, for a separate reason given
// in the plan's §6 — but an empty scope can still arise, so this still bites.)
func emptyStatusDTO(root, host string, files []*gen.SkillFileFactsInput, unreadable, unparseable []statusUnreadableDTO) statusDTO {
	unchecked := []statusUncheckedDTO{}
	for _, f := range files {
		row := statusUncheckedDTO{Dir: f.DirName}
		if f.SourceUrn != nil {
			row.Source = *f.SourceUrn
		}
		unchecked = append(unchecked, row)
	}
	return statusDTO{
		Root: root, Host: host, ScopeEmpty: true,
		Entries: []statusEntryDTO{}, Orphans: []statusOrphanDTO{},
		Unchecked:  unchecked,
		Unreadable: unreadable, Unparseable: unparseable,
	}
}

// finishStatus renders the report and returns the user-visible exit code.
func finishStatus(f *cmdutil.Factory, dto statusDTO, strict bool) error {
	hasError, hasWarning, drift := false, false, false
	for _, e := range dto.Entries {
		if e.ParseFailure || (e.Class != nil && *e.Class != classCurrent) {
			drift = true
		}
		for _, fnd := range e.Findings {
			switch fnd.Severity {
			case skilldoc.SevError:
				hasError = true
			case skilldoc.SevWarning:
				hasWarning = true
			}
		}
	}
	// @codex on #652: --strict PROMOTES warnings, which is the group's contract
	// (§3) and what `skill lint --strict` already does. Without this a `current`
	// entry carrying only a warning — skill-description-no-trigger, say — left
	// both flags false and the CI gate passed a warned corpus, so the same
	// finding changed exit code depending on which verb reported it.
	if strict && hasWarning {
		drift = true
		// Promote the rendered SEVERITY as well, not just the exit code
		// (@copilot on #652). `skill lint --strict` rewrites the finding to
		// "error", so leaving it "warning" here would have the two verbs
		// disagree about the same finding in --json — the exit code says
		// promoted and the row says not. One contract or the other, not both.
		for i := range dto.Entries {
			for j := range dto.Entries[i].Findings {
				if dto.Entries[i].Findings[j].Severity == skilldoc.SevWarning {
					dto.Entries[i].Findings[j].Severity = skilldoc.SevError
				}
			}
		}
	}
	if len(dto.Orphans) > 0 || len(dto.Unreadable) > 0 || len(dto.Unparseable) > 0 {
		drift = true
	}
	// An empty scope verified NOTHING. A gate must not pass on a result no
	// query produced, so --strict fails here whether or not files were found:
	// "nothing to compare against" and "everything matches" are the same empty
	// report, and only one of them is good news.
	if dto.ScopeEmpty {
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
// claudeSkill has one today. One renderer serves both hosts (cli#622), so the
// gap is the host TABLE, not rendering: Codex has more than one read root and
// a root that may itself be a symlink, which a host → dir map cannot express
// (acceptance audit, docs/plans/skill-command-group.md §3: matrix P17/P18/
// P20/P21, plan-only Q1–Q8).
// The Codex row lands with the #621 writer.
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
		// NOT gated on d.IsDir(): os.ReadDir does not follow symlinks, so a
		// SYMLINKED skill directory reports IsDir() == false (measured: type
		// L---------) while its SKILL.md reads perfectly well through the link.
		// Skipping it made an installed skill invisible, and an invisible file
		// is reported by the server as `never-exported` — which on the export
		// path means overwriting somebody's linked file rather than leaving it
		// (@codex on #652). Reading unconditionally handles the link and costs
		// one failed open on an entry that is not a directory at all.
		dirName := d.Name()
		// Only a REGULAR file is read (Stat follows a link, as the read would):
		// a FIFO or device named SKILL.md would block the read forever, for
		// status and export alike (#696 review). It is reported, never read.
		if fi, err := os.Stat(filepath.Join(root, dirName, skillFileName)); err == nil && !fi.Mode().IsRegular() {
			unreadable = append(unreadable, statusUnreadableDTO{Dir: dirName, Error: "SKILL.md is not a regular file, so it was not read"})
			continue
		}
		data, err := os.ReadFile(filepath.Join(root, dirName, skillFileName)) // #nosec G304 — the root is the user's own skills directory
		if err != nil {
			if os.IsNotExist(err) || errors.Is(err, syscall.ENOTDIR) {
				// No SKILL.md, or the entry is a plain file and not a skill
				// directory at all. Neither is a skill; neither is an error.
				continue
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
			if source != "" {
				facts.SourceUrn = &source
			}
			// The SAME no-id rule as the parsed path below, repeated because
			// this branch reaches the facts by a different route (@copilot on
			// #663 — I fixed one branch and not the other). A header hash sent
			// WITHOUT a file hash makes the server's
			// `headerHash !== '' && fileHash !== headerHash` test vacuously
			// true, so an unparseable pre-§4a file would be reported as
			// hand-edited rather than as the older generation it is. No
			// fileHash can be computed here either way: the frontmatter did not
			// parse, so three of the five inputs are unavailable.
			if id != "" {
				facts.NodeId = &id
				if headerHash != "" {
					facts.HeaderHash = &headerHash
				}
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
		// The hash recomputed from the file's own bytes, with the id hashed
		// unconditionally — one form, matching hadron-server's exactly.
		facts.SourceUrn = &parsed.Source

		// A file with NO `id=` was never written by the current exporter, so
		// there is nothing to validate and NOTHING IS HASHED FOR IT (ruled
		// 2026-09-23). Two fields are deliberately withheld, and the second is
		// the one that matters:
		//
		//   - fileHash — it cannot be computed. The digest covers the id, and
		//     this file has none, so any number we produced would compare
		//     against a header written under a different formula.
		//   - headerHash — withheld even when the file HAS one. The server's
		//     `locally-edited` test is `headerHash !== '' && fileHash !==
		//     headerHash`, so a header hash arriving WITHOUT its counterpart
		//     makes that mismatch vacuously true and the file is reported as
		//     hand-edited on no evidence. Sending neither yields `unhashed`,
		//     which is the truthful answer: an older header generation.
		//
		// @Dara: the server could state this directly — an absent nodeId could
		// short-circuit to `unhashed` before the locally-edited test — which
		// would let this client send every fact it has instead of withholding
		// one to avoid a false positive. Raised, not assumed.
		if parsed.ID == "" {
			// Extra frontmatter still travels (@copilot on #663): it is an
			// independent local-edit signal rather than part of the hash, and
			// withholding a true fact is not this rule's business.
			//
			// Stated plainly because it is a LIMIT of the rule, not of this
			// code: with headerHash withheld, the server's locally-edited test
			// is false whatever this flag says, so a no-id file a human edited
			// cannot presently be protected AS `locally-edited`. It classifies
			// `unhashed`, whose action is skip-and-report — which refuses to
			// touch it anyway, so the outcome is safe and only the reason shown
			// is wrong. The fix belongs with the server-side nodeId
			// short-circuit raised below.
			if len(parsed.Extra) > 0 {
				yes := true
				facts.HasExtraFrontmatter = &yes
			}
			files = append(files, facts)
			continue
		}
		facts.NodeId = &parsed.ID
		fileHash := skilldoc.Hash(parsed.ID, parsed.Source, parsed.Name, parsed.Description, parsed.Body)
		facts.FileHash = &fileHash
		if parsed.Hash != "" {
			facts.HeaderHash = &parsed.Hash
		}
		// The header's rev=N (#1323), sent only when present: a nil pointer
		// is omitted from the wire, so an older server that has no such field
		// still accepts the request, and an older file with no rev= claims no
		// revision. The server compares it with the node's live revision and
		// classifies a mismatch `stale`, even when the content hash agrees.
		if parsed.Revision > 0 {
			rev := parsed.Revision
			facts.Revision = &rev
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
		// EMPTY here, not absent: this path did ask the server, so nothing went
		// unjudged. It must still be an ARRAY — a field that is [] on one code
		// path and null on another is a shape an agent cannot iterate
		// unconditionally, and both review bots caught exactly that.
		// TestStatusDTOHasNoNilSlices pins the whole class.
		Unchecked: []statusUncheckedDTO{},
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
			// A status plan judges exactly one host, so each finding's hosts
			// is that host — the same field `skill lint` fills (#665), never null.
			row.Findings = append(row.Findings, statusFindingDTO{
				Node: fnd.Urn, Memory: fnd.Memory, Rule: fnd.Rule, Severity: fnd.Severity, Message: fnd.Message,
				Hosts: []string{host},
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
	if len(dto.Entries) == 0 && len(dto.Orphans) == 0 && len(dto.Unreadable) == 0 &&
		len(dto.Unparseable) == 0 && len(dto.Unchecked) == 0 {
		// scanned distinguishes an empty corpus from one the caller cannot
		// see; without it both render as "nothing to do" and a permission
		// problem reads as a clean bill of health.
		fmt.Fprintf(w, "no skill declarations judged for this host (%d node(s) carrying a declaration were in scope)\n", dto.Scanned)
		if dto.ScopeEmpty {
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
	if len(dto.Unchecked) > 0 {
		fmt.Fprintf(w, "\nUnchecked — generated files in this root that nothing judged, because no\nmemory was in scope to compare them against:\n")
		for _, u := range dto.Unchecked {
			ref := u.Source
			if ref == "" {
				ref = "no source in header"
			}
			fmt.Fprintf(w, "  %s (%s)\n", u.Dir, ref)
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
