package org

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

// publicResourceDTO is the stable --json shape for a sanitized public resource
// ref (a discoverable memory or agent).
type publicResourceDTO struct {
	ID          string  `json:"id"`
	Name        string  `json:"name"`
	URN         string  `json:"urn"`
	Description *string `json:"description"`
}

// publicOrgDTO is the stable --json shape for `org public` — an org's sanitized,
// non-member-safe public view.
type publicOrgDTO struct {
	ID                    string              `json:"id"`
	URN                   string              `json:"urn"`
	Name                  string              `json:"name"`
	ListedOnMarketplace   bool                `json:"listedOnMarketplace"`
	PublicMemories        []publicResourceDTO `json:"publicMemories"`
	PublicAgents          []publicResourceDTO `json:"publicAgents"`
	PublicMemoriesTotal   int                 `json:"publicMemoriesTotal"`
	PublicMemoriesHasMore bool                `json:"publicMemoriesHasMore"`
	PublicAgentsTotal     int                 `json:"publicAgentsTotal"`
	PublicAgentsHasMore   bool                `json:"publicAgentsHasMore"`
}

func newCmdPublic(f *cmdutil.Factory) *cobra.Command {
	var limit, offset int
	cmd := &cobra.Command{
		Use:     "public <org-ref>",
		Aliases: []string{"discover"},
		Short:   "Show an organization's public (discoverable) view",
		Long: `Fetch the sanitized public view of a DISCOVERABLE organization — the public
memories and agents any signed-in user can see, without being a member.

An organization is discoverable when it is activated and has a public footprint
(a PUBLIC memory or agent, or it is listed on the marketplace). A
non-discoverable or non-existent org returns not-found — the server collapses
both cases to null for anti-enumeration. <org-ref> is an org id or URN.

Fetch one page of each list (20 rows each by default). --limit and --offset
apply to both lists; the server caps a page at 200. Zero limit returns counts
only; negative limits and offsets count as zero. No pages are fetched automatically.
Partial lists say "showing N of TOTAL". --json retains publicMemories and
publicAgents arrays and adds publicMemoriesTotal/publicMemoriesHasMore and
publicAgentsTotal/publicAgentsHasMore.`,
		Example: `  hadron org public acme.com
  hadron org public hrn:org:acme.com --json
  hadron org public acme.com --limit 20 --offset 20`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := f.GraphQLClient()
			if err != nil {
				return err
			}
			resp, err := gen.PublicOrganization(cmd.Context(), client, args[0], max(0, limit), max(0, offset))
			if err != nil {
				return api.MapError(err)
			}
			if resp.PublicOrganization == nil {
				return exitcode.Newf(exitcode.NotFound, "organization %q is not discoverable (or does not exist)", args[0])
			}
			if resp.PublicOrganization.PublicMemoriesPage == nil || resp.PublicOrganization.PublicAgentsPage == nil {
				return exitcode.Newf(exitcode.Error, "the server returned an incomplete public organization page")
			}
			dto := publicOrgDTOFrom(resp.PublicOrganization)
			return output.Write(f.IOStreams, f.JSON, dto, func(w io.Writer) error {
				listing := "not listed on marketplace"
				if dto.ListedOnMarketplace {
					listing = "listed on marketplace"
				}
				fmt.Fprintf(w, "%s\n  urn: %s\n  id: %s\n  %s\n", output.SafeCell(dto.Name), output.SafeCell(dto.URN), output.SafeCell(dto.ID), listing)
				printPublicResources(w, "public memories", dto.PublicMemories, dto.PublicMemoriesTotal, dto.PublicMemoriesHasMore)
				printPublicResources(w, "public agents", dto.PublicAgents, dto.PublicAgentsTotal, dto.PublicAgentsHasMore)
				return nil
			})
		},
	}
	cmd.Flags().IntVar(&limit, "limit", 20, "Rows per list (0 for counts only; server caps at 200)")
	cmd.Flags().IntVar(&offset, "offset", 0, "Rows to skip in each list (negative counts as 0)")
	return cmd
}

func publicOrgDTOFrom(o *gen.PublicOrganizationPublicOrganization) publicOrgDTO {
	mems := make([]publicResourceDTO, 0, len(o.PublicMemoriesPage.Items))
	for _, m := range o.PublicMemoriesPage.Items {
		if m == nil {
			continue
		}
		mems = append(mems, publicResourceFrom(m.PublicResourceRefFields))
	}
	agents := make([]publicResourceDTO, 0, len(o.PublicAgentsPage.Items))
	for _, a := range o.PublicAgentsPage.Items {
		if a == nil {
			continue
		}
		agents = append(agents, publicResourceFrom(a.PublicResourceRefFields))
	}
	return publicOrgDTO{
		ID:                    o.Id,
		URN:                   o.Urn,
		Name:                  o.Name,
		ListedOnMarketplace:   o.ListedOnMarketplace,
		PublicMemories:        mems,
		PublicAgents:          agents,
		PublicMemoriesTotal:   o.PublicMemoriesPage.Total,
		PublicMemoriesHasMore: o.PublicMemoriesPage.HasMore,
		PublicAgentsTotal:     o.PublicAgentsPage.Total,
		PublicAgentsHasMore:   o.PublicAgentsPage.HasMore,
	}
}

func publicResourceFrom(f gen.PublicResourceRefFields) publicResourceDTO {
	return publicResourceDTO{ID: f.Id, Name: f.Name, URN: f.Urn, Description: f.Description}
}

func printPublicResources(w io.Writer, label string, rs []publicResourceDTO, total int, hasMore bool) {
	if hasMore {
		fmt.Fprintf(w, "  %s: showing %d of %d\n", label, len(rs), total)
	}

	if len(rs) == 0 && !hasMore {
		fmt.Fprintf(w, "  %s: (none)\n", label)
		return
	}
	if !hasMore {
		fmt.Fprintf(w, "  %s:\n", label)
	}
	for _, r := range rs {
		if r.Description != nil && *r.Description != "" {
			fmt.Fprintf(w, "    - %s (%s) — %s\n", output.SafeCell(r.Name), output.SafeCell(r.URN), output.SafeCell(*r.Description))
		} else {
			fmt.Fprintf(w, "    - %s (%s)\n", output.SafeCell(r.Name), output.SafeCell(r.URN))
		}
	}
}
