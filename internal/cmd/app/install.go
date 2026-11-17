package app

import (
	"encoding/json"
	"fmt"
	"io"
	"slices"

	"github.com/spf13/cobra"

	"github.com/hadron-memory/hadron-cli/internal/api"
	"github.com/hadron-memory/hadron-cli/internal/api/gen"
	"github.com/hadron-memory/hadron-cli/internal/cmdutil"
	"github.com/hadron-memory/hadron-cli/internal/exitcode"
	"github.com/hadron-memory/hadron-cli/internal/output"
)

func newCmdInstall(f *cmdutil.Factory) *cobra.Command {
	var settings settingsFlags
	var yes bool
	var (
		org         string
		ownerMe     bool
		agent       string
		name        string
		urn         string
		appType     string
		description string
		policyFile  string
	)
	cmd := &cobra.Command{
		Use:   "install (--org <id> | --owner-me) --agent <ref> --name <n>",
		Short: "Install an Agent as an App — into an organization, or as a personal App (--owner-me)",
		Long: `Install an Agent as an App. Pass --org to install into an organization (you
must be an org ADMIN), or --owner-me to create a user-owned (personal) App in
your own @handle namespace (spec 047). A personal App can install a PUBLIC
(marketplace) agent or your own user-owned agent.

Only passed settings are sent. Omitted surfaces use the provider default ['chat'];
--surface and --agent-tool are repeatable whole-list inputs. Surfaces describe
UI availability (the portal shows chat iff chat is present), not authorization.
--type does not alter surfaces. Numeric values and policy shape are server
validated. AI configuration uses ai-config.

--policy-file <path> sets the App's action-policy link, the App layer of the
standing policy chain (cor:acl:040:02): one JSON value read from the file, for
example {"allow":["run.execute"]}. The App layer can only narrow what the other
layers allow; {"allow":[]} allows nothing. An empty file, invalid JSON and a
bare null are local usage errors and nothing is sent. Leave the flag out for no
link. Without it the request never mentions policy, so an older server keeps
working. Change it later with app update --policy-file (or --clear-policy).
New settings require confirmation, or --yes without a terminal; legacy-only
installs retain their existing request document and no-prompt path. On every
path, --type is validated against this CLI's schema enum; an explicit empty or
unknown value is a local usage error. Confirmation names fields, never values.
Success prints the installed App; --json retains its seven-field App receipt.
memberCount stays present as null when withheld, distinct from a known 0.`,
		Example: `  hadron app install --org acme.com --agent agent_123 --name "Support Bot"
  hadron app install --owner-me --agent hrn:agent:acme.com:support-bot --name "My Bot"`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			// spec 047: an App is owned by EXACTLY ONE of an org or the caller.
			if ownerMe && org != "" {
				return exitcode.Newf(exitcode.Usage, "--owner-me creates an App you own in your own namespace; drop --org (or drop --owner-me to install into an organization)")
			}
			if !ownerMe && org == "" {
				return exitcode.Newf(exitcode.Usage, "specify --org <id> to install into an organization, or --owner-me to create a personal App")
			}
			// --urn is an optional slug; validate it only when supplied.
			if urn != "" {
				if err := cmdutil.ValidateURNSlug("--urn", urn); err != nil {
					return err
				}
			}
			prepared, err := settings.prepare(cmd)
			if err != nil {
				return err
			}
			// cli#1628: the App-layer policy link, read before anything is sent;
			// every refusal is local (the reader is shared with app update, #1640).
			var policyJSON json.RawMessage
			if cmd.Flags().Changed("policy-file") {
				if policyJSON, err = readPolicyFile(policyFile); err != nil {
					return err
				}
				prepared.changed = append(prepared.changed, "policy")
			}
			if cmd.Flags().Changed("type") && !slices.Contains(gen.AllAppType, gen.AppType(appType)) {
				return exitcode.Newf(exitcode.Usage, "invalid --type")
			}
			if cmd.Flags().Changed("session-timeout-seconds") {
				prepared.timeout = &settings.timeout
				prepared.changed = append(prepared.changed, "sessionTimeoutSeconds")
			}
			if len(prepared.changed) > 0 {
				fields := []string{"agentRef", "name"}
				if !ownerMe {
					fields = append(fields, "orgRef")
				}
				for _, pair := range []struct{ flag, field string }{{"urn", "urn"}, {"type", "appType"}, {"description", "description"}} {
					if cmd.Flags().Changed(pair.flag) {
						fields = append(fields, pair.field)
					}
				}
				fields = append(fields, prepared.changed...)
				if err := cmdutil.Confirm(f.IOStreams, yes, fmt.Sprintf("Install App with settings: %v?", fields)); err != nil {
					return err
				}
			}
			client, err := f.GraphQLClient()
			if err != nil {
				return err
			}

			optional := func(s string) *string {
				if s == "" {
					return nil
				}
				return &s
			}
			var typeArg *gen.AppType
			if appType != "" {
				t := gen.AppType(appType)
				typeArg = &t
			}

			// optional(org) is nil for --owner-me (org == "") — the server reads an
			// omitted orgId as the personal-create signal (an empty string is
			// rejected), so nil-omitted is exactly right.
			var dto appDTO
			if policyJSON != nil {
				resp, err := gen.CreateAppSettingsPolicy(cmd.Context(), client, optional(org), agent, name, optional(urn), typeArg, optional(description), prepared.timeout, prepared.expires, prepared.role, prepared.permission, prepared.identify, prepared.ttl, prepared.training, prepared.tools, prepared.surfaces, prepared.optional, policyJSON)
				if err != nil {
					return api.MapError(err)
				}
				if resp.CreateApp == nil {
					return exitcode.Newf(exitcode.Error, "server returned no App for the install")
				}
				a := resp.CreateApp
				dto = appDTO{ID: a.Id, URN: a.Urn, Name: a.Name, AppType: string(a.AppType), AgentID: a.AgentId, MemberCount: a.MemberCount, CreatedAt: a.CreatedAt}
			} else if len(prepared.changed) > 0 {
				resp, err := gen.CreateAppSettings(cmd.Context(), client, optional(org), agent, name, optional(urn), typeArg, optional(description), prepared.timeout, prepared.expires, prepared.role, prepared.permission, prepared.identify, prepared.ttl, prepared.training, prepared.tools, prepared.surfaces, prepared.optional)
				if err != nil {
					return api.MapError(err)
				}
				if resp.CreateApp == nil {
					return exitcode.Newf(exitcode.Error, "server returned no App for the install")
				}
				a := resp.CreateApp
				dto = appDTO{ID: a.Id, URN: a.Urn, Name: a.Name, AppType: string(a.AppType), AgentID: a.AgentId, MemberCount: a.MemberCount, CreatedAt: a.CreatedAt}
			} else {
				resp, err := gen.CreateApp(cmd.Context(), client, optional(org), agent, name, optional(urn), typeArg, optional(description))
				if err != nil {
					return api.MapError(err)
				}
				if resp.CreateApp == nil {
					return exitcode.Newf(exitcode.Error, "server returned no App for the install")
				}
				a := resp.CreateApp
				dto = appDTO{ID: a.Id, URN: a.Urn, Name: a.Name, AppType: string(a.AppType), AgentID: a.AgentId, MemberCount: a.MemberCount, CreatedAt: a.CreatedAt}
			}

			return output.Write(f.IOStreams, f.JSON, dto, func(w io.Writer) error {
				t := output.NewTable(w)
				t.Row("✓ installed", output.SafeCell(dto.Name), output.SafeCell(dto.URN))
				return t.Flush()
			})
		},
	}
	cmd.Flags().StringVar(&org, "org", "", "organization ID or URN to install into (or use --owner-me for a personal App)")
	cmd.Flags().BoolVar(&ownerMe, "owner-me", false, "create a user-owned (personal) App in your own @handle namespace (org-less)")
	cmd.Flags().StringVar(&agent, "agent", "", "Agent ID or URN to install (required)")
	cmd.Flags().StringVar(&name, "name", "", "App name (required)")
	cmd.Flags().StringVar(&policyFile, "policy-file", "", "JSON file with the App's action-policy link (the App layer; it can only narrow)")
	cmd.Flags().StringVar(&urn, "urn", "", "App URN slug")
	cmd.Flags().StringVar(&appType, "type", "", "App type: AGENT|AUTOMATION|CHATBOT|CLOUD|IOT|WORKSTATION")
	cmd.Flags().StringVar(&description, "description", "", "App description")
	settings.flags(cmd, true)
	cmd.Flags().BoolVar(&yes, "yes", false, "skip settings confirmation")
	_ = cmd.MarkFlagRequired("agent")
	_ = cmd.MarkFlagRequired("name")
	return cmd
}
