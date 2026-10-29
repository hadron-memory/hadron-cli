package app

import (
	"fmt"
	"io"
	"strings"

	"github.com/spf13/cobra"

	"github.com/hadron-memory/hadron-cli/internal/api"
	"github.com/hadron-memory/hadron-cli/internal/api/gen"
	"github.com/hadron-memory/hadron-cli/internal/cmdutil"
	"github.com/hadron-memory/hadron-cli/internal/exitcode"
	"github.com/hadron-memory/hadron-cli/internal/output"
)

const (
	// memoriesDefaultLimit is the server's own default for appMemoryScope,
	// sent explicitly so the continuation line can name it.
	memoriesDefaultLimit = 20
	memoriesMaxLimit     = 200
)

// appScopedMemoryDTO is the stable --json row of `app memories` (cli#1209):
// the server's AppScopedMemory as returned, plus writeTarget, the SDL's
// advisory rule already applied (viewerCanWrite AND a source other than
// INHERITED_BASE).
type appScopedMemoryDTO struct {
	MemoryID       string               `json:"memoryId"`
	MemoryURN      string               `json:"memoryUrn"`
	Name           string               `json:"name"`
	Class          string               `json:"class"`
	Sources        []appMemorySourceDTO `json:"sources"`
	ViewerCanWrite bool                 `json:"viewerCanWrite"`
	WriteTarget    bool                 `json:"writeTarget"`
}

// appMemorySourceDTO is one reason a memory is in scope. agentName is added
// by the CLI: null when agentId is null or names an Agent the caller may not
// read.
type appMemorySourceDTO struct {
	Kind      string  `json:"kind"`
	AgentID   *string `json:"agentId"`
	AgentName *string `json:"agentName"`
}

type appMemoryScopePageDTO struct {
	Items []appScopedMemoryDTO `json:"items"`
	Total int                  `json:"total"`
}

// writeLabel renders the advisory write column: an inherited-only row is
// never a write target, whatever viewerCanWrite says.
func (m appScopedMemoryDTO) writeLabel() string {
	switch {
	case m.WriteTarget:
		return "yes"
	case m.ViewerCanWrite:
		return "inherited"
	default:
		return "no"
	}
}

func (m appScopedMemoryDTO) sourcesLabel() string {
	parts := make([]string, 0, len(m.Sources))
	for _, s := range m.Sources {
		if s.AgentName != nil {
			parts = append(parts, fmt.Sprintf("%s (%s)", s.Kind, *s.AgentName))
		} else {
			parts = append(parts, s.Kind)
		}
	}
	return strings.Join(parts, ", ")
}

func newCmdMemories(f *cmdutil.Factory) *cobra.Command {
	var limit, offset int
	cmd := &cobra.Command{
		Use:   "memories [<app-ref>] [--limit N] [--offset N]",
		Short: "List the memories in an App's scope, and why each is there",
		Long: fmt.Sprintf(`List the memories an App reads for you: the memories that a search across the
whole App covers. One row per memory, with every reason it is in scope:

  AGENT_SYSTEM    the system memory of an Agent installed in the App
  AGENT_LINK      a memory linked to an installed Agent
  INHERITED_BASE  a base Agent's system memory, inherited by an installed one
  TEAM_SPACE      the App's own team space

An Agent's name is shown after its kind when you may read that Agent;
otherwise the kind is shown alone.

Who sees what: App participants also get the team space; reading the App's
organization does not, by itself, add it.

The WRITE (ADVISORY) column says whether you may create nodes there: yes, no,
or "inherited" for a memory you could write that is in scope only as an
inherited base. That memory belongs to the base Agent and other Apps read it,
so it is not a write target here. The column is advice: the server still
decides every write.

The App is the positional ref (ID or URN); omit it to use --app or the App
set with 'hadron app set-active'. Exit 8 when you may not read the App's
scope, 4 when the App is not found. Returns ONE page, ordered by memory URN: --limit
1..%d (default %d) and --offset N. When more rows exist, stderr says how to
fetch the next page. --json prints the page ({items, total}); each source
adds agentName and each row adds writeTarget. The rule is in the Hadron specs
corpus at cor:dmo:050:17.`, memoriesMaxLimit, memoriesDefaultLimit),
		Example: `  hadron app memories hrn:app:acme.com:eng-team
  hadron app memories --app hrn:app:acme.com:eng-team --json
  hadron app memories hrn:app:acme.com:eng-team --limit 50 --offset 50`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if limit < 1 || limit > memoriesMaxLimit {
				return exitcode.Newf(exitcode.Usage, "--limit must be between 1 and %d", memoriesMaxLimit)
			}
			if offset < 0 {
				return exitcode.Newf(exitcode.Usage, "--offset must be non-negative")
			}
			appRef := ""
			if len(args) == 1 {
				var err error
				if appRef, err = cmdutil.CanonicalAppRef("<app-ref>", args[0]); err != nil {
					return err
				}
			} else {
				ambient, err := f.App()
				if err != nil {
					return err
				}
				appRef = ambient
			}
			if appRef == "" {
				return exitcode.Newf(exitcode.Usage,
					"no App — pass one as the argument, use --app <ref>, or set a default with `hadron app set-active`")
			}
			client, err := f.GraphQLClient()
			if err != nil {
				return err
			}
			// limit is always sent; offset 0 is omitted (a nil pointer).
			var offsetArg *int
			if offset > 0 {
				offsetArg = &offset
			}
			resp, err := gen.AppMemoryScope(cmd.Context(), client, appRef, &limit, offsetArg)
			if err != nil {
				return api.MapError(err)
			}
			// The page, its rows and their sources are all non-null in the SDL,
			// and every row has at least one source. A missing piece is a broken
			// answer, not an empty scope: refuse it rather than print a success.
			if resp == nil || resp.AppMemoryScope == nil {
				return exitcode.Newf(exitcode.Error, "the server returned no appMemoryScope page for %s; nothing is shown", appRef)
			}
			page := appMemoryScopePageDTO{Items: []appScopedMemoryDTO{}, Total: resp.AppMemoryScope.Total}
			for i, it := range resp.AppMemoryScope.Items {
				if it == nil {
					return exitcode.Newf(exitcode.Error, "the server returned a null row %d in the appMemoryScope page; nothing is shown", offset+i+1)
				}
				if len(it.Sources) == 0 {
					return exitcode.Newf(exitcode.Error, "the server returned no sources for %s in the appMemoryScope page; nothing is shown", it.MemoryUrn)
				}
				row := appScopedMemoryDTO{
					MemoryID:       it.MemoryId,
					MemoryURN:      it.MemoryUrn,
					Name:           it.Name,
					Class:          string(it.Class),
					Sources:        []appMemorySourceDTO{},
					ViewerCanWrite: it.ViewerCanWrite,
				}
				ownSource := false
				for _, s := range it.Sources {
					if s == nil {
						return exitcode.Newf(exitcode.Error, "the server returned a null source for %s in the appMemoryScope page; nothing is shown", it.MemoryUrn)
					}
					row.Sources = append(row.Sources, appMemorySourceDTO{Kind: string(s.Kind), AgentID: s.AgentId})
					if s.Kind != gen.AppMemorySourceKindInheritedBase {
						ownSource = true
					}
				}
				row.WriteTarget = row.ViewerCanWrite && ownSource
				page.Items = append(page.Items, row)
			}
			// One lookup per distinct agent, in first-appearance order. A null
			// answer is an Agent the caller may not read: the kind stands alone.
			names := map[string]*string{}
			for i := range page.Items {
				for j := range page.Items[i].Sources {
					src := &page.Items[i].Sources[j]
					if src.AgentID == nil {
						continue
					}
					name, seen := names[*src.AgentID]
					if !seen {
						a, err := gen.AppScopeAgentName(cmd.Context(), client, *src.AgentID)
						if err != nil {
							return api.MapError(err)
						}
						if a != nil && a.Agent != nil {
							n := a.Agent.Name
							name = &n
						}
						names[*src.AgentID] = name
					}
					src.AgentName = name
				}
			}
			if next := offset + len(page.Items); len(page.Items) > 0 && next < page.Total {
				fmt.Fprintf(f.IOStreams.ErrOut, "showing %d of %d; next page: --offset %d --limit %d\n", len(page.Items), page.Total, next, limit)
			}
			return output.Write(f.IOStreams, f.JSON, page, func(w io.Writer) error {
				table := output.NewTable(w, "MEMORY", "NAME", "CLASS", "SOURCES", "WRITE (ADVISORY)")
				for _, m := range page.Items {
					table.Row(output.SafeCell(m.MemoryURN), output.SafeCell(m.Name), output.SafeCell(m.Class), output.SafeCell(m.sourcesLabel()), m.writeLabel())
				}
				return table.Flush()
			})
		},
	}
	cmd.Flags().IntVar(&limit, "limit", memoriesDefaultLimit, fmt.Sprintf("page size, 1..%d", memoriesMaxLimit))
	cmd.Flags().IntVar(&offset, "offset", 0, "rows to skip")
	return cmd
}
