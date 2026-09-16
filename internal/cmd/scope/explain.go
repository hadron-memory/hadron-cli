package scope

import (
	"io"

	"github.com/spf13/cobra"

	"github.com/hadron-memory/hadron-cli/internal/api"
	"github.com/hadron-memory/hadron-cli/internal/api/gen"
	"github.com/hadron-memory/hadron-cli/internal/cmdutil"
	"github.com/hadron-memory/hadron-cli/internal/exitcode"
	"github.com/hadron-memory/hadron-cli/internal/output"
)

// explanationDTO is the stable --json shape of `scope explain`.
//
// droppedCount and shadowed are the two disclosures that make this command
// worth having: the first says how much of the scope you cannot see, the
// second says your NAME matched more than one scope and which one lost.
type explanationDTO struct {
	Scope        scopeDTO       `json:"scope"`
	ResolvedVia  string         `json:"resolvedVia"`
	DroppedCount int            `json:"droppedCount"`
	Memories     []memoryRefDTO `json:"memories"`
	Winner       *memoryRefDTO  `json:"winner"`
	Shadowed     []scopeDTO     `json:"shadowed"`
}

type memoryRefDTO struct {
	ID   string `json:"id"`
	URN  string `json:"urn"`
	Name string `json:"name"`
}

func newCmdExplain(f *cmdutil.Factory) *cobra.Command {
	var loc string
	cmd := &cobra.Command{
		Use:   "explain <name|id>",
		Short: "Show how a scope resolves for you, and what it hides",
		Long: `Explain a scope AS IT APPLIES TO YOU.

Shows which memories the scope resolves to in order, how it was resolved
(App > Agent > organization, never unioned), how many of its memories you may
not read, and any lower-precedence scopes of the same name that it shadowed.

With --loc, also shows which memory would WIN for that address under this
scope — the question "if I ask for this loc, where does it come from?".

A name that two installed Agents both own is reported as ambiguous by the
server; answer it by passing the scope's id instead.`,
		Example: `  hadron scope explain research
  hadron scope explain research --loc findings:retrieval`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := f.GraphQLClient()
			if err != nil {
				return err
			}
			appRef, err := f.App()
			if err != nil {
				return err
			}

			// Unlike the other verbs this does NOT pre-resolve: scopeExplain
			// is itself the resolver, so passing the name straight through
			// keeps it to one round trip and lets the server raise
			// SCOPE_NAME_AMBIGUOUS in its own words.
			var scopeRef, name, appPtr, locPtr *string
			if cmdutil.IsBareID(args[0]) {
				scopeRef = &args[0]
			} else {
				// Same guard as resolveScopeID: without an App context the
				// server refuses by naming `appRef`, a GraphQL field with no
				// flag behind it, leaving the reader nothing to type.
				if appRef == "" {
					return exitcode.Newf(exitcode.Usage,
						"a scope name resolves in an App's context — pass --app <ref>, run `hadron app set-active <ref>`, or give the scope's id instead")
				}
				name, appPtr = &args[0], &appRef
			}
			if loc != "" {
				locPtr = &loc
			}

			resp, err := gen.ScopeExplain(cmd.Context(), client, scopeRef, name, appPtr, locPtr)
			if err != nil {
				return api.MapError(err)
			}
			if resp == nil || resp.ScopeExplain == nil || resp.ScopeExplain.Scope == nil {
				return exitcode.Newf(exitcode.NotFound, "no scope %q is readable here", args[0])
			}
			ex := resp.ScopeExplain

			d := explanationDTO{
				Scope:        dtoFromFields(ex.Scope.ScopeFields),
				ResolvedVia:  string(ex.ResolvedVia),
				DroppedCount: ex.DroppedCount,
				Memories:     []memoryRefDTO{},
				Shadowed:     []scopeDTO{},
			}
			for _, m := range ex.Memories {
				if m == nil {
					continue
				}
				d.Memories = append(d.Memories, memoryRefDTO{ID: m.Id, URN: m.Urn, Name: m.Name})
			}
			if ex.Winner != nil {
				d.Winner = &memoryRefDTO{ID: ex.Winner.Id, URN: ex.Winner.Urn, Name: ex.Winner.Name}
			}
			for _, s := range ex.Shadowed {
				if s == nil {
					continue
				}
				d.Shadowed = append(d.Shadowed, dtoFromFields(s.ScopeFields))
			}

			return output.Write(f.IOStreams, f.JSON, d, func(w io.Writer) error {
				t := output.NewTable(w, "FIELD", "VALUE")
				t.Row("scope", d.Scope.Name)
				t.Row("id", d.Scope.ID)
				t.Row("resolved via", d.ResolvedVia)
				if d.Winner != nil {
					t.Row("winner for "+loc, d.Winner.URN)
				}
				if err := t.Flush(); err != nil {
					return err
				}
				if len(d.Memories) > 0 {
					mt := output.NewTable(w, "#", "URN", "NAME")
					for i, m := range d.Memories {
						mt.Row(itoa(i), m.URN, m.Name)
					}
					if err := mt.Flush(); err != nil {
						return err
					}
				}
				// droppedCount is the whole point of "explain" — a resolved
				// list that silently omits what you cannot read is exactly the
				// thing this command exists to make visible.
				if note := hiddenNote(d.DroppedCount); note != "" {
					if _, err := io.WriteString(w, note+"\n"); err != nil {
						return err
					}
				}
				if len(d.Shadowed) > 0 {
					if _, err := io.WriteString(w, "\nthis name also matched, at lower precedence:\n"); err != nil {
						return err
					}
					st := output.NewTable(w, "NAME", "OWNER", "ID")
					for _, s := range d.Shadowed {
						st.Row(s.Name, s.OwnerType+" "+ownerLabel(s), s.ID)
					}
					if err := st.Flush(); err != nil {
						return err
					}
				}
				return nil
			})
		},
	}
	cmd.Flags().StringVar(&loc, "loc", "", "also report which memory would win for this address")
	return cmd
}
