package skill

import (
	"fmt"
	"io"

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
}

func newCmdLint(f *cmdutil.Factory) *cobra.Command {
	var sel selectorFlags
	var prefix string
	var strict bool
	cmd := &cobra.Command{
		Use:   "lint (-m <memory>... | --all | --node <ref>...)",
		Short: "Check skill-declaring task nodes against the corpus rules",
		Long: `Check every node that declares a skill (properties.skill, or the legacy
properties.claudeSkill) against the rules an export needs to hold — reading
the corpus only; nothing on disk is touched.

Rules (errors unless noted): the description is present and at most 1024
characters — the host does not refuse a longer one, it TRUNCATES it in the
skill listing, so trigger phrases past the cut silently never fire; the
derived name (<prefix> + the loc below "tasks:") is a valid skill name of at
most 64 characters; a hand-set "name" in the declaration, if any, equals the
derived one (the stored name is retired); isRunnable is true; the body is
non-empty and carries no frontmatter of its own; no two selected nodes derive
the same name. Warnings: the declaration still uses the legacy key; the
description never says when to use the skill; the body contains a {{…}}
placeholder, which export ships verbatim.

An org-owned memory whose org has chosen no Organization.skillPrefix is
itself a finding — set it (org admin) or pass --prefix; a user-owned memory
takes "hadron-". Errors exit 5; warnings alone exit 0 unless --strict promotes
them to errors.

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
			if err := validatePrefixFlag(cmd, prefix); err != nil {
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

			findings := []lintFindingDTO{}
			toDTO := func(f skilldoc.Finding) lintFindingDTO {
				return lintFindingDTO{Node: f.URN, Memory: f.Memory, Rule: f.Rule, Severity: f.Severity, Message: f.Message}
			}
			prefixes := map[string]skilldoc.Prefix{} // memory URN → resolved prefix
			for _, m := range s.memories {
				prefixes[m.URN] = resolvePrefix(m, prefix)
			}

			nodes := make([]skilldoc.Node, 0, len(s.nodes))
			declared := 0
			for _, n := range s.nodes {
				memURN := ""
				if m := s.memories[n.MemoryId]; m != nil {
					memURN = m.URN
				}
				sn := toSkillNode(n, memURN)
				nodes = append(nodes, sn)
				if _, ok := skilldoc.Declared(sn.Properties); ok {
					declared++
				}
				for _, fnd := range skilldoc.Lint(sn, prefixes[memURN]) {
					findings = append(findings, toDTO(fnd))
				}
			}
			for _, fnd := range skilldoc.LintPrefixes(nodes, prefixes) {
				findings = append(findings, toDTO(fnd))
			}
			for _, fnd := range skilldoc.LintCollisions(nodes, prefixes) {
				findings = append(findings, toDTO(fnd))
			}
			for _, ref := range s.unavailable {
				findings = append(findings, lintFindingDTO{Node: ref, Memory: "-", Rule: "skill-node-unavailable", Severity: skilldoc.SevWarning, Message: describeUnavailable(ref)})
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
				t := output.NewTable(w, "NODE", "SEVERITY", "RULE", "MESSAGE")
				for _, fnd := range findings {
					t.Row(fnd.Node, fnd.Severity, fnd.Rule, fnd.Message)
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
	cmd.Flags().StringVar(&prefix, "prefix", "", "export prefix to derive names with, overriding the org's (lowercase, trailing hyphen: hadron-, mm-)")
	cmd.Flags().BoolVar(&strict, "strict", false, "treat warnings as errors")
	return cmd
}

func plural(n int) string {
	if n == 1 {
		return "y"
	}
	return "ies"
}
