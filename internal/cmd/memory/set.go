package memory

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

func newCmdSet(f *cmdutil.Factory) *cobra.Command {
	var (
		org         string
		name        string
		class       string
		short       string
		description string
		visibility  string
		slug        string
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
"Project KB" → project-kb) unless you pass --slug to set it explicitly;
the resulting URN, class, and visibility are echoed in the output. On
update, --slug renames the memory — its URN, and therefore the URNs of
every node under it, change.`,
		Example: `  hadron memory set --org acme.com --name "Project KB"
  hadron memory set --org acme.com --name "Hadron PDF Tool" --slug hadrontool-pdf
  hadron memory set --org acme.com --name "Notes" --class personal
  hadron memory set acme.com:project-kb --description "Long-form description"`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			// Pure, so a bad slug is a usage error even offline.
			if slug != "" {
				if err := cmdutil.ValidateURNSlug("--slug", slug); err != nil {
					return err
				}
			}
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
			// Set on create when the memory was made but the follow-up --slug
			// rename failed — a partial write we report, then exit non-zero.
			var slugErr error
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
				// createMemory has no slug input — it derives the slug from
				// --name. Honor an explicit --slug by renaming to it when it
				// differs (a second call). If that fails, the memory still exists
				// under its derived slug: keep m as-is, record the error, and
				// report it as a partial write below.
				if slug != "" && !memorySlugIs(m.URN, slug) {
					if resp, rerr := gen.UpdateMemory(cmd.Context(), client, m.ID, nil, nil, nil, nil, nil, nil, &slug); rerr != nil {
						slugErr = api.MapError(rerr)
					} else {
						m = dtoFromUpdatedMemory(resp.UpdateMemory)
					}
				}
			} else {
				if org != "" || class != "" {
					return exitcode.Newf(exitcode.Usage, "--org and --class only apply when creating (no positional argument)")
				}
				if name == "" && short == "" && description == "" && visibility == "" && tagsArg == nil && slug == "" {
					return exitcode.Newf(exitcode.Usage, "nothing to update — pass at least one field flag")
				}
				memID, err := resolveMemoryID(cmd, client, args[0])
				if err != nil {
					return err
				}
				var urnArg *string
				if slug != "" {
					urnArg = &slug
				}
				resp, err := gen.UpdateMemory(cmd.Context(), client, memID, optional(name), optional(short), optional(description), tagsArg, visArg, nil, urnArg)
				if err != nil {
					return api.MapError(err)
				}
				m = dtoFromUpdatedMemory(resp.UpdateMemory)
				verb = "updated"
			}

			if err := output.Write(f.IOStreams, f.JSON, m, func(w io.Writer) error {
				t := output.NewTable(w)
				t.Row("✓ "+verb, m.URN, m.Name)
				if err := t.Flush(); err != nil {
					return err
				}
				if verb == "created" {
					// Echo the effective class/visibility so a server-assigned
					// default (when --class/--visibility were omitted) isn't
					// invisible (#108). A nil visibility is genuinely unset — not
					// applicable to the owner-scoped personal/private classes — so
					// label it honestly rather than inventing a value.
					vis := "<unset>"
					if m.Visibility != nil && *m.Visibility != "" {
						vis = *m.Visibility
					}
					_, err := fmt.Fprintf(w, "  class: %s   visibility: %s\n", m.Class, vis)
					return err
				}
				return nil
			}); err != nil {
				return err
			}
			// The memory was created but couldn't be renamed to --slug: honest
			// partial-write reporting — surface it and exit non-zero so a script
			// doesn't read a clean success (matches node import --with-edges).
			if slugErr != nil {
				return exitcode.Newf(exitcode.Error,
					"memory created as %s, but setting slug %q failed: %v — retry with: hadron memory set %s --slug %s",
					m.URN, slug, slugErr, m.URN, slug)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&org, "org", "", "organization ID or URN (create only)")
	cmd.Flags().StringVar(&name, "name", "", "memory name")
	cmd.Flags().StringVar(&class, "class", "", "memory class: knowledge|group|personal|private (create only; server default: knowledge)")
	cmd.Flags().StringVar(&short, "short", "", "short description")
	cmd.Flags().StringVar(&description, "description", "", "long description")
	cmd.Flags().StringVar(&visibility, "visibility", "", "visibility: PUBLIC|ORGANIZATION|GROUP (unset uses the server default; the create output echoes the effective value)")
	cmd.Flags().StringVar(&slug, "slug", "", "URN slug (bare, e.g. hadrontool-pdf): set it explicitly on create instead of deriving from --name; renames the memory on update")
	cmd.Flags().StringArrayVar(&tags, "tag", nil, "tag (repeatable; replaces tags on update)")
	return cmd
}

// dtoFromUpdatedMemory maps an updateMemory result into the stable --json DTO.
func dtoFromUpdatedMemory(u *gen.UpdateMemoryUpdateMemory) memoryDTO {
	m := memoryDTO{
		ID: u.Id, URN: u.Urn, Name: u.Name,
		ShortDescription: u.ShortDescription, Class: string(u.Class),
		OrganizationID: u.OrganizationId, IsEncrypted: u.IsEncrypted,
		UpdatedAt: u.UpdatedAt,
	}
	if u.Visibility != nil {
		v := string(*u.Visibility)
		m.Visibility = &v
	}
	return m
}

// memorySlugIs reports whether urn's slug (the segment after the final "::")
// equals slug, case-insensitively — the server lower-cases derived slugs, so a
// case-only difference isn't a real rename worth a second call.
func memorySlugIs(urn, slug string) bool {
	i := strings.LastIndex(urn, "::")
	if i < 0 {
		return false
	}
	return strings.EqualFold(urn[i+2:], slug)
}
