package org

import (
	"encoding/json"
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

func newCmdUpdate(f *cmdutil.Factory) *cobra.Command {
	var name, urn, policy string
	var marketplace, clearPolicy bool
	cmd := &cobra.Command{
		Use:   "update <org>",
		Short: "Update an organization",
		Long: `Update an organization; only the fields you pass change. Needs ADMIN or
OWNER in the org (else exit 8). <org> is its id, URN or root.

--name changes the display name.

--urn renames the org's root, which changes its URN and the URNs of its
memories, Apps and agents. The old URNs keep working as aliases and the old
root stays reserved to the org. A root that is already taken is refused, and
so is a rename that would clash with an existing URN inside the org.

--marketplace (or --marketplace=true) lists the org on the marketplace
catalogue and --marketplace=false unlists it. Listing advertises the org; it
grants nobody access.

--policy sets the org's action-policy link, the org layer of the chain every
run of the org's Apps must pass (a run may do an action only if every layer
allows it). Pass a JSON allow-list, '{"allow":["run.execute","comm.outbound"]}';
'{"allow":[]}' allows nothing. --clear-policy removes the link, so this layer no longer
restricts anything. A blank --policy (an unset variable, say) or a JSON null
is refused (exit 2) rather than read as "clear". See which layer denies an
action with hadron policy explain.`,
		Example: `  hadron org update acme.com --name "Acme Inc"
  hadron org update acme.com --urn acme-inc.com
  hadron org update acme.com --marketplace         # list on the marketplace
  hadron org update acme.com --marketplace=false   # unlist
  hadron org update acme.com --policy '{"allow":["run.execute"]}'
  hadron org update acme.com --clear-policy        # remove the org policy link`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			changed := cmd.Flags().Changed
			if !changed("name") && !changed("urn") && !changed("marketplace") && !changed("policy") && !clearPolicy {
				return exitcode.Newf(exitcode.Usage, "nothing to update — pass --name, --urn, --marketplace, --policy, or --clear-policy")
			}
			// Clearing widens what every run in the org may do, so it takes
			// the explicit --clear-policy: a blank --policy (an unset
			// variable) or a JSON null is refused, never read as "clear"
			// (#1489 §10). Not passed, policy is omitted and the server
			// preserves the link.
			var policyArg *json.RawMessage
			switch {
			case clearPolicy && changed("policy"):
				return exitcode.Newf(exitcode.Usage, "--policy and --clear-policy are mutually exclusive")
			case clearPolicy:
				clear := json.RawMessage("null")
				policyArg = &clear
			case changed("policy"):
				if strings.TrimSpace(policy) == "" {
					return exitcode.Newf(exitcode.Usage, "--policy is empty (an unset variable?); pass the policy JSON, or --clear-policy to remove the org link")
				}
				parsed, err := cmdutil.ParseJSONArg(policy, "policy")
				if err != nil {
					return err
				}
				if strings.TrimSpace(string(*parsed)) == "null" {
					return exitcode.Newf(exitcode.Usage, "--policy null would remove the org link; use --clear-policy to do that")
				}
				policyArg = parsed
			}
			if changed("urn") {
				if err := cmdutil.ValidateOrgSlug("--urn", urn); err != nil {
					return err
				}
			}
			client, err := f.GraphQLClient()
			if err != nil {
				return err
			}
			var nameArg, urnArg *string
			var marketplaceArg *bool
			if changed("name") {
				nameArg = &name
			}
			if changed("urn") {
				urnArg = &urn
			}
			if changed("marketplace") {
				marketplaceArg = &marketplace
			}
			resp, err := gen.UpdateOrganization(cmd.Context(), client, args[0], nameArg, urnArg, marketplaceArg, policyArg)
			if err != nil {
				return api.MapError(err)
			}
			if resp.UpdateOrganization == nil {
				return exitcode.Newf(exitcode.Error, "server returned no organization")
			}
			dto := orgDTOFromFields(resp.UpdateOrganization.OrgFields)
			return output.Write(f.IOStreams, f.JSON, dto, func(w io.Writer) error {
				_, err := fmt.Fprintf(w, "✓ updated org %s (%s)\n", dto.URN, dto.ID)
				return err
			})
		},
	}
	cmd.Flags().StringVar(&name, "name", "", "new display name")
	cmd.Flags().StringVar(&urn, "urn", "", "new URN: a lowercase slug, dotted or not (e.g. acme.com or acme)")
	cmd.Flags().BoolVar(&marketplace, "marketplace", false, "list this org on the marketplace catalogue: --marketplace to list, --marketplace=false to unlist (advertisement, not access)")
	cmd.Flags().StringVar(&policy, "policy", "", `org action-policy link as JSON, e.g. '{"allow":["run.execute"]}'`)
	cmd.Flags().BoolVar(&clearPolicy, "clear-policy", false, "remove the org action-policy link (no org-layer restriction)")
	return cmd
}
