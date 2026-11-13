package team

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/hadron-memory/hadron-cli/internal/api"
	"github.com/hadron-memory/hadron-cli/internal/api/gen"
	"github.com/hadron-memory/hadron-cli/internal/cmdutil"
	"github.com/hadron-memory/hadron-cli/internal/exitcode"
	"github.com/hadron-memory/hadron-cli/internal/output"
	"github.com/spf13/cobra"
)

const grantActionsHelp = `Read an App's registered grant actions: the actions a grant can allow
(cor:acl:140:02). Anyone in the App may read the list: a participating person
with their own credential, or a bound worker of that App through its session.
No worker binding is required.

The App is named with --app (or the App context) and goes to the server exactly
as given; nothing resolves or looks it up first. Every refused caller (not in
the App, an unknown or malformed App, a retired worker, an App key, a run)
gets the same answer as an empty list, so an empty result is not proof the App
has no actions.

enforcement is the level the action DECLARES: what the platform promises, not
an active guarantee that a gate enforces it today.`

type grantActionParamDTO struct {
	Name            string `json:"name"`
	Type            string `json:"type"`
	RefusesWildcard bool   `json:"refusesWildcard"`
}

type grantActionDTO struct {
	Name        string                `json:"name"`
	Parameters  []grantActionParamDTO `json:"parameters"`
	Tool        *string               `json:"tool"`
	Enforcement string                `json:"enforcement"`
	IssuerRule  string                `json:"issuerRule"`
	Source      string                `json:"source"`
}

type grantActionPage struct {
	Items []grantActionDTO `json:"items"`
	Total int              `json:"total"`
}

func grantActionFrom(a *gen.GrantActionFields) grantActionDTO {
	d := grantActionDTO{Name: a.Name, Parameters: []grantActionParamDTO{}, Tool: a.Tool, Enforcement: a.Enforcement, IssuerRule: a.IssuerRule, Source: a.Source}
	for _, p := range a.Parameters {
		d.Parameters = append(d.Parameters, grantActionParamDTO{p.Name, p.Type, p.RefusesWildcard})
	}
	return d
}

func (d grantActionDTO) paramText() string {
	parts := make([]string, 0, len(d.Parameters))
	for _, p := range d.Parameters {
		s := p.Name + ":" + p.Type
		if p.RefusesWildcard {
			s += " (no wildcard)"
		}
		parts = append(parts, s)
	}
	if len(parts) == 0 {
		return "—"
	}
	return strings.Join(parts, ", ")
}

// grantActionsAppRef is the App to read, as given: --app, else the configured
// context. It is deliberately NOT canonicalized or resolved: the server answers
// every refusal arm the same way, and a local verdict on "no such App" or
// "malformed" would be a signal of the CLI's own (cli#1996). Only an absent
// ref is refused here, on syntax alone.
func grantActionsAppRef(f *cmdutil.Factory) (ref, source string, err error) {
	if ref = strings.TrimSpace(f.AppFlag); ref != "" {
		return ref, "from --app", nil
	}
	cfg, err := f.Config()
	if err != nil {
		return "", "", err
	}
	if ref = strings.TrimSpace(cfg.App()); ref == "" {
		return "", "", exitcode.Newf(exitcode.Usage, "an App is required: pass --app <ref> or set a default App context")
	}
	return ref, "from the App context", nil
}

// grantActionsClient reads with the worker session when this worktree has a
// binding for this server, and with the caller's own credential otherwise.
// Unlike workerGrantClient it never requires a binding and never compares the
// binding's App with --app: who may read is the server's call, not the CLI's.
func grantActionsClient(ctx context.Context, f *cmdutil.Factory) (context.Context, error) {
	b, _, err := readBinding(ctx)
	if err != nil && !errors.Is(err, errNoWorktree) {
		return ctx, err
	}
	if b != nil && b.SessionID != "" && bindingServerMatches(f, b) {
		ctx = api.WithSession(ctx, b.SessionID)
	}
	return ctx, nil
}

func newCmdTeamGrantActions(f *cmdutil.Factory) *cobra.Command {
	var limit, offset int
	cmd := &cobra.Command{Use: "actions", Short: "List an App's registered grant actions", Long: grantActionsHelp + `

--limit 0 prints only the total count; --offset selects the page start. The
default page limit is 20. --json returns {items:[],total:N}, or {total:N} for
count-only, with the server's values unchanged.`, Example: "  hadron team grant actions --app hrn:app:acme.com:team\n  hadron team grant actions --app hrn:app:acme.com:team --limit 0 --json", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, args []string) error {
		if limit < 0 || limit > 200 || offset < 0 || offset > 2147483647 {
			return exitcode.Newf(exitcode.Usage, "--limit takes 0..200 and --offset takes 0..2147483647")
		}
		ref, source, err := grantActionsAppRef(f)
		if err != nil {
			return err
		}
		ctx, err := grantActionsClient(cmd.Context(), f)
		if err != nil {
			return err
		}
		client, err := f.GraphQLClient()
		if err != nil {
			return err
		}
		resp, err := gen.GrantActions(ctx, client, ref, limit, offset)
		if err != nil {
			return api.MapError(err)
		}
		if resp == nil || resp.GrantActions == nil {
			return exitcode.Newf(exitcode.Error, "server returned no grant actions page")
		}
		if limit == 0 {
			return output.Write(f.IOStreams, f.JSON, struct {
				Total int `json:"total"`
			}{resp.GrantActions.Total}, func(w io.Writer) error {
				_, e := fmt.Fprintf(w, "total: %d\n", resp.GrantActions.Total)
				return e
			})
		}
		dto := grantActionPage{Items: []grantActionDTO{}, Total: resp.GrantActions.Total}
		for i := range resp.GrantActions.Items {
			dto.Items = append(dto.Items, grantActionFrom(&resp.GrantActions.Items[i].GrantActionFields))
		}
		return output.Write(f.IOStreams, f.JSON, dto, func(w io.Writer) error {
			// The ref is client-composed (what the caller typed), so it prints
			// raw; the source is a local fact, never a server lookup (cli#1996).
			if _, e := fmt.Fprintf(w, "App: %s (%s)\n", ref, source); e != nil {
				return e
			}
			if len(dto.Items) == 0 {
				_, e := fmt.Fprintln(w, "No grant actions returned.")
				return e
			}
			table := output.NewTable(w, "NAME", "ENFORCEMENT (declared)", "ISSUER RULE", "SOURCE", "TOOL", "PARAMETERS")
			for _, a := range dto.Items {
				tool := "—"
				if a.Tool != nil {
					tool = *a.Tool
				}
				table.Row(output.SafeCell(a.Name), output.SafeCell(a.Enforcement), output.SafeCell(a.IssuerRule), output.SafeCell(a.Source), output.SafeCell(tool), output.SafeCell(a.paramText()))
			}
			if e := table.Flush(); e != nil {
				return e
			}
			_, e := fmt.Fprintf(w, "total: %d; offset: %d; limit: %d\nenforcement is the declared level, not a guarantee that a gate enforces it today.\n", dto.Total, offset, limit)
			return e
		})
	}}
	cmd.Flags().IntVar(&limit, "limit", 20, "page size, 0..200; 0 prints only the total count")
	cmd.Flags().IntVar(&offset, "offset", 0, "page offset, 0..2147483647")
	return cmd
}

func newCmdTeamGrantAction(f *cmdutil.Factory) *cobra.Command {
	return &cobra.Command{Use: "action <name>", Short: "Read one registered grant action by exact name", Long: grantActionsHelp + `

<name> is the action's exact dotted code, for example schedule.create. An
unknown name and every refused caller get the same not-found line and exit 4.
--json returns the action's fields as the server sent them.`, Example: "  hadron team grant action schedule.create --app hrn:app:acme.com:team --json", Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
		ref, source, err := grantActionsAppRef(f)
		if err != nil {
			return err
		}
		ctx, err := grantActionsClient(cmd.Context(), f)
		if err != nil {
			return err
		}
		client, err := f.GraphQLClient()
		if err != nil {
			return err
		}
		resp, err := gen.GrantAction(ctx, client, ref, args[0])
		if err != nil {
			return api.MapError(err)
		}
		if resp == nil || resp.Action == nil {
			return exitcode.Newf(exitcode.NotFound, "grant action %q not found in App %q (or not visible to this caller)", args[0], ref)
		}
		dto := grantActionFrom(&resp.Action.GrantActionFields)
		return output.Write(f.IOStreams, f.JSON, dto, func(w io.Writer) error {
			tool := "—"
			if dto.Tool != nil {
				tool = *dto.Tool
			}
			_, e := fmt.Fprintf(w, "App: %s (%s)\nname: %s\nenforcement (declared, not a guarantee): %s\nissuerRule: %s\nsource: %s\ntool: %s\nparameters: %s\n",
				ref, source, output.SafeCell(dto.Name), output.SafeCell(dto.Enforcement), output.SafeCell(dto.IssuerRule), output.SafeCell(dto.Source), output.SafeCell(tool), output.SafeCell(dto.paramText()))
			return e
		})
	}}
}
