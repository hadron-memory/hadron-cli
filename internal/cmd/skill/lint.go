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

Every host is checked — claudeSkill and codexSkill — each declaration against
its OWN host's caps, and each finding names the host(s) it came from. The
retired keys alias to claudeSkill only.

Rules (errors unless noted): the name is present, kebab-case and at most the
host's cap (64 for both hosts) — it is STORED at properties.exports.<host>.name,
not derived, so that is what to change; the description is present and at most
the host's cap (1024 for both) — the host does not refuse a longer one, it
TRUNCATES it in the skill listing, so trigger phrases past the cut silently
never fire; isRunnable is true; the body is non-empty and carries no
frontmatter of its own; no two selected nodes store the same name for the same
host. Warnings: the declaration still uses a retired key; the description never
says when to use the skill; the body contains a {{…}} placeholder, which export
ships verbatim.

Note what cannot be checked: with no prefix source, a name's PREFIX is
unverifiable — "hadon-foo" lints clean. Shape is checkable, correctness is not.

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

			var acc findingSet
			nodes := make([]skilldoc.Node, 0, len(s.nodes))
			declared := 0
			for _, n := range s.nodes {
				memURN := ""
				if m := s.memories[n.MemoryId]; m != nil {
					memURN = m.URN
				}
				sn := toSkillNode(n, memURN)
				nodes = append(nodes, sn)
				if declaresAnyHost(sn.Properties) {
					declared++
				}
				// #665: every host, not only Claude. A Codex declaration over
				// its cap was an all-clear before, because nothing judged it.
				for _, h := range skilldoc.Hosts {
					for _, fnd := range skilldoc.LintFor(sn, h) {
						acc.add(fnd, h.Key)
					}
				}
			}
			for _, h := range skilldoc.Hosts {
				for _, fnd := range skilldoc.LintCollisionsFor(nodes, h) {
					acc.add(fnd, h.Key)
				}
			}
			// Initialized, never nil: a clean corpus is `[]` on --json, not `null`.
			findings := append([]lintFindingDTO{}, acc.rows...)
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
					t.Row(fnd.Node, hosts, fnd.Severity, fnd.Rule, fnd.Message)
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

// findingSet accumulates lint findings across hosts, in first-seen order. A
// finding identical under several hosts (same node, rule, severity and
// message) is one row carrying every host that reported it: node-level rules
// and a malformed `exports` entry are judged under each host, and printing
// them once per host would double every such report.
type findingSet struct {
	rows  []lintFindingDTO
	index map[[4]string]int
}

func (s *findingSet) add(f skilldoc.Finding, host string) {
	if s.index == nil {
		s.index = map[[4]string]int{}
	}
	k := [4]string{f.URN, f.Rule, f.Severity, f.Message}
	if i, ok := s.index[k]; ok {
		s.rows[i].Hosts = append(s.rows[i].Hosts, host)
		return
	}
	s.index[k] = len(s.rows)
	s.rows = append(s.rows, lintFindingDTO{Node: f.URN, Memory: f.Memory, Rule: f.Rule, Severity: f.Severity, Message: f.Message, Hosts: []string{host}})
}

// declaresAnyHost reports whether a node carries a declaration for at least
// one host, which is what "skill-declaring node" counts.
func declaresAnyHost(props map[string]any) bool {
	for _, h := range skilldoc.Hosts {
		if _, ok := skilldoc.DeclaredFor(props, h); ok {
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
