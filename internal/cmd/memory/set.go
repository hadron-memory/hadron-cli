package memory

import (
	"fmt"
	"io"

	"github.com/spf13/cobra"

	"github.com/hadron-memory/hadron-cli/internal/api"
	"github.com/hadron-memory/hadron-cli/internal/api/gen"
	"github.com/hadron-memory/hadron-cli/internal/cmdutil"
	"github.com/hadron-memory/hadron-cli/internal/exitcode"
	"github.com/hadron-memory/hadron-cli/internal/output"
)

func newCmdSet(f *cmdutil.Factory) *cobra.Command {
	var (
		org         string
		name        string
		class       string
		short       string
		description string
		visibility  string
		tags        []string
	)
	cmd := &cobra.Command{
		Use:   "set [<memory-id-or-urn>]",
		Short: "Create or update a memory",
		Long: `Create or update a memory.

With a positional memory ID or URN, updates that memory (only the
fields you pass change). Without one, creates a new memory — --org
and --name are then required.

On create, the URN slug is derived from --name (kebab-cased, e.g.
"Project KB" → project-kb); the resulting URN, class, and visibility
are echoed in the output.`,
		Example: `  hadron memory set --org acme.com --name "Project KB"
  hadron memory set --org acme.com --name "Notes" --class personal
  hadron memory set acme.com:project-kb --description "Long-form description"`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := f.GraphQLClient()
			if err != nil {
				return err
			}

			var visArg *gen.MemoryVisibility
			if visibility != "" {
				v := gen.MemoryVisibility(visibility)
				visArg = &v
			}
			optional := func(s string) *string {
				if s == "" {
					return nil
				}
				return &s
			}
			var tagsArg *[]string
			if cmd.Flags().Changed("tag") {
				tagsArg = &tags
			}

			var m memoryDTO
			var verb string
			if len(args) == 0 {
				if org == "" || name == "" {
					return exitcode.Newf(exitcode.Usage, "creating a memory requires --org and --name")
				}
				var classArg *gen.MemoryClass
				if class != "" {
					c := gen.MemoryClass(class)
					classArg = &c
				}
				resp, err := gen.CreateMemory(cmd.Context(), client, org, name, classArg, optional(short), optional(description), tagsArg, visArg)
				if err != nil {
					return api.MapError(err)
				}
				created := resp.CreateMemory
				m = memoryDTO{
					ID: created.Id, URN: created.Urn, Name: created.Name,
					ShortDescription: created.ShortDescription, Class: string(created.Class),
					OrganizationID: created.OrganizationId, IsEncrypted: created.IsEncrypted,
					UpdatedAt: created.UpdatedAt,
				}
				if created.Visibility != nil {
					v := string(*created.Visibility)
					m.Visibility = &v
				}
				verb = "created"
			} else {
				if org != "" || class != "" {
					return exitcode.Newf(exitcode.Usage, "--org and --class only apply when creating (no positional argument)")
				}
				if name == "" && short == "" && description == "" && visibility == "" && tagsArg == nil {
					return exitcode.Newf(exitcode.Usage, "nothing to update — pass at least one field flag")
				}
				memID, err := resolveMemoryID(cmd, client, args[0])
				if err != nil {
					return err
				}
				resp, err := gen.UpdateMemory(cmd.Context(), client, memID, optional(name), optional(short), optional(description), tagsArg, visArg, nil)
				if err != nil {
					return api.MapError(err)
				}
				updated := resp.UpdateMemory
				m = memoryDTO{
					ID: updated.Id, URN: updated.Urn, Name: updated.Name,
					ShortDescription: updated.ShortDescription, Class: string(updated.Class),
					OrganizationID: updated.OrganizationId, IsEncrypted: updated.IsEncrypted,
					UpdatedAt: updated.UpdatedAt,
				}
				if updated.Visibility != nil {
					v := string(*updated.Visibility)
					m.Visibility = &v
				}
				verb = "updated"
			}

			return output.Write(f.IOStreams, f.JSON, m, func(w io.Writer) error {
				t := output.NewTable(w)
				t.Row("✓ "+verb, m.URN, m.Name)
				if err := t.Flush(); err != nil {
					return err
				}
				if verb == "created" {
					// Echo the effective class/visibility so a server-assigned
					// default (when --class/--visibility were omitted) isn't
					// invisible (#108).
					vis := "default"
					if m.Visibility != nil && *m.Visibility != "" {
						vis = *m.Visibility
					}
					_, err := fmt.Fprintf(w, "  class: %s   visibility: %s\n", m.Class, vis)
					return err
				}
				return nil
			})
		},
	}
	cmd.Flags().StringVar(&org, "org", "", "organization ID or URN (create only)")
	cmd.Flags().StringVar(&name, "name", "", "memory name")
	cmd.Flags().StringVar(&class, "class", "", "memory class: knowledge|group|personal|private (create only; server default: knowledge)")
	cmd.Flags().StringVar(&short, "short", "", "short description")
	cmd.Flags().StringVar(&description, "description", "", "long description")
	cmd.Flags().StringVar(&visibility, "visibility", "", "visibility: PUBLIC|ORGANIZATION|GROUP (unset uses the server default; the create output echoes the effective value)")
	cmd.Flags().StringArrayVar(&tags, "tag", nil, "tag (repeatable; replaces tags on update)")
	return cmd
}
