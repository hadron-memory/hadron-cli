package policy

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

// explainDTO is the stable --json shape for effectiveActionPolicy.
type explainDTO struct {
	Allowed bool       `json:"allowed"`
	Layers  []layerDTO `json:"layers"`
}

// layerDTO is one layer's verdict. Allow is deliberately nullable rather than
// [] when empty: null means the layer has no link (no restriction), while []
// is a link that allows nothing. Collapsing the two would invert the meaning.
type layerDTO struct {
	Layer   string    `json:"layer"`
	Present bool      `json:"present"`
	Allowed bool      `json:"allowed"`
	Allow   *[]string `json:"allow"`
}

func newCmdExplain(f *cmdutil.Factory) *cobra.Command {
	var action, app, user string
	cmd := &cobra.Command{
		Use:   "explain --action <action> --app <app> [--user <user>]",
		Short: "Show which policy layer allows or denies an action",
		Long: `Explain the standing policy chain for one action: would a run of this App
(acting for --user, if given) be allowed to do it, and which layer says no.
The layers are org, user (only with --user) and app, in that order.

Needs ADMIN or OWNER in the App's org (else exit 8). An unknown App or user
exits 4. --app takes an App id or URN; --user a user id, email, handle or URN.

The table shows each layer, whether it has a link ("none" = no restriction),
whether it allows the action, and its allow-list. --json gives allowed and
layers, each with layer, present, allowed and allow; allow is null when the
layer has no link and [] when its link allows nothing.

Per-trigger links (schedule and webhook --policy) are not part of this chain;
hadron run get shows the policy a finished run actually applied.`,
		Example: `  hadron policy explain --action comm.outbound --app hrn:app:acme.com:assistant
  hadron policy explain --action run.execute --app hrn:app:acme.com:assistant --user jane --json`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if strings.TrimSpace(action) == "" || strings.TrimSpace(app) == "" {
				return exitcode.Newf(exitcode.Usage, "--action and --app are required")
			}
			var userRef *string
			if cmd.Flags().Changed("user") {
				if strings.TrimSpace(user) == "" {
					return exitcode.Newf(exitcode.Usage, "--user is empty; pass a user, or leave --user out to explain without the user layer")
				}
				userRef = &user
			}
			client, err := f.GraphQLClient()
			if err != nil {
				return err
			}
			resp, err := gen.EffectiveActionPolicy(cmd.Context(), client, action, app, userRef)
			if err != nil {
				return api.MapError(err)
			}
			if resp == nil || resp.EffectiveActionPolicy == nil {
				return exitcode.Newf(exitcode.Error, "server returned no policy explanation")
			}
			dto := explainDTO{Allowed: resp.EffectiveActionPolicy.Allowed, Layers: []layerDTO{}}
			for _, l := range resp.EffectiveActionPolicy.Layers {
				if l == nil {
					continue
				}
				ld := layerDTO{Layer: l.Layer, Present: l.Present, Allowed: l.Allowed}
				if l.Allow != nil {
					allow := append([]string{}, l.Allow...)
					ld.Allow = &allow
				}
				dto.Layers = append(dto.Layers, ld)
			}
			return output.Write(f.IOStreams, f.JSON, dto, func(w io.Writer) error {
				verdict := "ALLOWED"
				if !dto.Allowed {
					var deniers []string
					for _, l := range dto.Layers {
						if !l.Allowed {
							deniers = append(deniers, l.Layer)
						}
					}
					verdict = "DENIED by " + strings.Join(deniers, ", ")
				}
				if _, err := fmt.Fprintf(w, "%s: %s\n", output.SafeCell(action), output.SafeCell(verdict)); err != nil {
					return err
				}
				t := output.NewTable(w, "LAYER", "LINK", "ALLOWS", "ALLOW-LIST")
				for _, l := range dto.Layers {
					link, list := "none", "-"
					if l.Present {
						link = "yes"
					}
					if l.Allow != nil {
						list = strings.Join(*l.Allow, ",")
						if list == "" {
							list = "(nothing)"
						}
					}
					t.Row(output.SafeCell(l.Layer), link, yesNo(l.Allowed), output.SafeCell(list))
				}
				return t.Flush()
			})
		},
	}
	cmd.Flags().StringVar(&action, "action", "", "the action to explain, e.g. run.execute or comm.outbound (required)")
	cmd.Flags().StringVar(&app, "app", "", "App id or URN whose chain to explain (required)")
	cmd.Flags().StringVar(&user, "user", "", "include this user's layer (id, email, handle or URN)")
	return cmd
}

func yesNo(b bool) string {
	if b {
		return "yes"
	}
	return "no"
}
