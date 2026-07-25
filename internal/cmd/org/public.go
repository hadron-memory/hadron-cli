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
	ID                  string              `json:"id"`
	URN                 string              `json:"urn"`
	Name                string              `json:"name"`
	ListedOnMarketplace bool                `json:"listedOnMarketplace"`
	PublicMemories      []publicResourceDTO `json:"publicMemories"`
	PublicAgents        []publicResourceDTO `json:"publicAgents"`
}

func newCmdPublic(f *cmdutil.Factory) *cobra.Command {
	return &cobra.Command{
		Use:     "public <org-ref>",
		Aliases: []string{"discover"},
		Short:   "Show an organization's public (discoverable) view",
		Long: `Fetch the sanitized public view of a DISCOVERABLE organization — the public
memories and agents any signed-in user can see, without being a member.

An organization is discoverable when it is activated and has a public footprint
(a PUBLIC memory or agent, or it is listed on the marketplace). A
non-discoverable or non-existent org returns not-found — the server collapses
both cases to null for anti-enumeration. <org-ref> is an org id or URN.`,
		Example: `  hadron org public acme.com
  hadron org public hrn:org:acme.com --json`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := f.GraphQLClient()
			if err != nil {
				return err
			}
			resp, err := gen.PublicOrganization(cmd.Context(), client, args[0])
			if err != nil {
				return api.MapError(err)
			}
			if resp.PublicOrganization == nil {
				return exitcode.Newf(exitcode.NotFound, "organization %q is not discoverable (or does not exist)", args[0])
			}
			dto := publicOrgDTOFrom(resp.PublicOrganization)
			return output.Write(f.IOStreams, f.JSON, dto, func(w io.Writer) error {
				listing := "not listed on marketplace"
				if dto.ListedOnMarketplace {
					listing = "listed on marketplace"
				}
				fmt.Fprintf(w, "%s\n  urn: %s\n  id: %s\n  %s\n", dto.Name, dto.URN, dto.ID, listing)
				printPublicResources(w, "public memories", dto.PublicMemories)
				printPublicResources(w, "public agents", dto.PublicAgents)
				return nil
			})
		},
	}
}

func publicOrgDTOFrom(o *gen.PublicOrganizationPublicOrganization) publicOrgDTO {
	mems := make([]publicResourceDTO, 0, len(o.PublicMemories))
	for _, m := range o.PublicMemories {
		if m == nil {
			continue
		}
		mems = append(mems, publicResourceFrom(m.PublicResourceRefFields))
	}
	agents := make([]publicResourceDTO, 0, len(o.PublicAgents))
	for _, a := range o.PublicAgents {
		if a == nil {
			continue
		}
		agents = append(agents, publicResourceFrom(a.PublicResourceRefFields))
	}
	return publicOrgDTO{
		ID:                  o.Id,
		URN:                 o.Urn,
		Name:                o.Name,
		ListedOnMarketplace: o.ListedOnMarketplace,
		PublicMemories:      mems,
		PublicAgents:        agents,
	}
}

func publicResourceFrom(f gen.PublicResourceRefFields) publicResourceDTO {
	return publicResourceDTO{ID: f.Id, Name: f.Name, URN: f.Urn, Description: f.Description}
}

func printPublicResources(w io.Writer, label string, rs []publicResourceDTO) {
	if len(rs) == 0 {
		fmt.Fprintf(w, "  %s: (none)\n", label)
		return
	}
	fmt.Fprintf(w, "  %s:\n", label)
	for _, r := range rs {
		if r.Description != nil && *r.Description != "" {
			fmt.Fprintf(w, "    - %s (%s) — %s\n", r.Name, r.URN, *r.Description)
		} else {
			fmt.Fprintf(w, "    - %s (%s)\n", r.Name, r.URN)
		}
	}
}
