package skill

import (
	"fmt"
	"io"
	"strings"

	"github.com/spf13/cobra"

	"github.com/hadron-memory/hadron-cli/internal/cmdutil"
	"github.com/hadron-memory/hadron-cli/internal/exitcode"
	"github.com/hadron-memory/hadron-cli/internal/output"
	"github.com/hadron-memory/hadron-cli/internal/skilldoc"
)

// lintFindingDTO is one lint result — the stable --json row. The vocabulary
// (severity strings, the shape) matches `spec lint` so a script consuming
// both sees one contract.
type lintFindingDTO struct {
	Node     string `json:"node"`
	Memory   string `json:"memory"`
	Rule     string `json:"rule"`
	Severity string `json:"severity"` // error | warning
	Message  string `json:"message"`
	// Hosts are the skill hosts whose judgment produced this finding, in
	// host-table order (#665). A finding every host reports identically — a
	// node-level rule, a malformed `exports` entry — is ONE row naming them
	// all, not one row per host. Empty for a finding about no declaration,
	// such as an unreadable node.
	Hosts []string `json:"hosts"`
}

func newCmdLint(f *cmdutil.Factory) *cobra.Command {
	var sel selectorFlags
	var strict bool
	cmd := &cobra.Command{
		Use:   "lint (-m <memory>... | --all | --node <ref>...)",
		Short: "Check skill-declaring task nodes against the corpus rules",
		Long: `Check every node that declares a skill against the rules an export
needs to hold — reading the corpus only; nothing on disk is touched.

A declaration is an object at properties.exports.<host>, carrying
{name, description, enable}; the retired top-level properties.skill and
properties.claudeSkill are read as aliases for the claudeSkill host, so nothing
has to be migrated to keep working.

Every host is checked by the server — claudeSkill and codexSkill — each declaration against
its OWN host's caps, and each finding names the host(s) it came from. The
retired keys alias to claudeSkill only.

Rules (errors unless noted): the name is present, kebab-case and at most the
host's cap (64 for both hosts) — it is STORED at properties.exports.<host>.name,
not derived, so that is what to change — and not a name the host reserves
(claudeSkill: synced, in any capitalization, Claude Code's folder for
claude.ai-synced skills); the description is present and at most
the host's cap (1024 for both) — the host does not refuse a longer one, it
TRUNCATES it in the skill listing, so trigger phrases past the cut silently
never fire — and, for claudeSkill, holds nothing its upload validator
refuses as an XML tag (a "<" followed later by a ">" with at least one
character between, such as <memory>); isRunnable is true; the body is non-empty and carries no
frontmatter of its own; no two nodes in the server's corpus ENABLE the same name for the same
host (a declaration that is not enabled claims no name, cor:agt:030:06). Warnings: the declaration still uses a retired key; the description never
says when to use the skill; the stored body contains a {{…}} placeholder that ships
as literal text (an include tag {{> part}} is resolved on export, so it is not flagged); a key under exports names no host (reported once, with no
host — it is never read as an alias and never blocks a known host beside it).

Note what cannot be checked: with no prefix source, a name's PREFIX is
unverifiable — "hadon-foo" lints clean. Shape is checkable, correctness is not.

The same server judgment checks include resolution using your access and reports
skill-include-broken warnings. Missing/deleted/unreadable include targets share
a neutral warning; this client never diagnoses them by searching body text.
The client does not fetch rendered skill bodies or write files; provider failure
does not become a clean lint result or fall back to a local verdict.

A skill-node-not-judged warning names a selected declaration the server did not
judge. The result is not clean, but the warning exits 0 unless --strict promotes
it. Retry once in case the corpus changed between host plans; if a known-host
omission persists, report a server skillPlan defect with the node and host. A
node whose unknown exports key has no host-free judgment gets ONE such warning
(per node, not per key) without blocking the corpus.

Errors exit 5; warnings alone exit 0 unless --strict promotes them to errors.

--all covers what the server LISTS for you — your orgs' memories, memories
shared with you, and other orgs' PUBLIC memories, every class. A per-user
agent memory (an agent's working memory for one user) is excluded from that
listing by the server; lint it by naming it with -m.`,
		Example: `  hadron skill lint -m hrn:mem:hadronmemory.com:core
  hadron skill lint --all --json
  hadron skill lint --node hrn:node:hadronmemory.com:core:tasks:create-release-tag --strict`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if err := sel.validate(); err != nil {
				return err
			}
			client, err := f.GraphQLClient()
			if err != nil {
				return err
			}
			s, err := selectNodes(cmd, client, &sel)
			if err != nil {
				return err
			}

			declared := 0
			for _, n := range s.nodes {
				memURN := ""
				if m := s.memories[n.MemoryId]; m != nil {
					memURN = m.URN
				}
				if hasSkillLintSubject(toSkillNode(n, memURN).Properties) {
					declared++
				}
			}
			findings, err := providerLintFindings(cmd.Context(), client, s.nodes, s.memories)
			if err != nil {
				return err
			}
			for _, ref := range s.unavailable {
				findings = append(findings, lintFindingDTO{Node: ref, Memory: "-", Rule: "skill-node-unavailable", Severity: skilldoc.SevWarning, Message: describeUnavailable(ref), Hosts: []string{}})
			}
			if strict {
				for i := range findings {
					if findings[i].Severity == skilldoc.SevWarning {
						findings[i].Severity = skilldoc.SevError
					}
				}
			}

			hasError := false
			for _, fnd := range findings {
				if fnd.Severity == skilldoc.SevError {
					hasError = true
					break
				}
			}
			if err := output.Write(f.IOStreams, f.JSON, findings, func(w io.Writer) error {
				if len(findings) == 0 {
					fmt.Fprintf(w, "✓ %d skill-declaring node(s) OK across %d memor%s\n", declared, len(s.memories), plural(len(s.memories)))
					return nil
				}
				t := output.NewTable(w, "NODE", "HOSTS", "SEVERITY", "RULE", "MESSAGE")
				for _, fnd := range findings {
					hosts := strings.Join(fnd.Hosts, ",")
					if hosts == "" {
						hosts = "-"
					}
					t.Row(output.SafeCell(fnd.Node), output.SafeCell(hosts), output.SafeCell(fnd.Severity), output.SafeCell(fnd.Rule), output.SafeCell(fnd.Message))
				}
				if err := t.Flush(); err != nil {
					return err
				}
				fmt.Fprintf(w, "\n%d finding(s) across %d skill-declaring node(s)\n", len(findings), declared)
				return nil
			}); err != nil {
				return err
			}
			if hasError {
				return exitcode.Silent(exitcode.Conflict)
			}
			return nil
		},
	}
	sel.register(cmd)
	cmd.Flags().BoolVar(&strict, "strict", false, "treat warnings as errors")
	return cmd
}

// hasSkillLintSubject reports whether a node carries a valid, malformed, or
// unknown-host declaration shape. These nodes belong in the human summary even
// when no known host can read a valid declaration from them.
func hasSkillLintSubject(props map[string]any) bool {
	if len(skilldoc.UnknownHostKeys(props)) > 0 {
		return true
	}
	for _, h := range skilldoc.Hosts {
		if skilldoc.HasDeclarationFor(props, h) {
			return true
		}
	}
	return false
}

func plural(n int) string {
	if n == 1 {
		return "y"
	}
	return "ies"
}
