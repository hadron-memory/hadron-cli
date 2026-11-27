package billing

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

// limitDTO is one owner limit: what is allowed (null means unlimited) and how much
// is held now. Both numbers are the server's base-10 strings, never parsed.
type limitDTO struct {
	Key   string  `json:"key"`
	Limit *string `json:"limit"`
	Used  string  `json:"used"`
}

func writeLimits(f *cmdutil.Factory, rows []limitDTO) error {
	return output.Write(f.IOStreams, f.JSON, rows, func(w io.Writer) error {
		t := output.NewTable(w, "KEY", "LIMIT", "USED")
		for _, r := range rows {
			limit := "unlimited"
			if r.Limit != nil {
				limit = *r.Limit
			}
			t.Row(output.SafeCell(r.Key), output.SafeCell(limit), output.SafeCell(r.Used))
		}
		return t.Flush()
	})
}

func newCmdLimits(f *cmdutil.Factory) *cobra.Command {
	var who ownerFlags
	cmd := &cobra.Command{
		Use:   "limits (<org> | --user <person> | --owner-me)",
		Short: "Show an owner's limits and how much of each is used; set or clear them",
		Long: `Show an organization's or a person's limits and how much of each is held now,
one row per limit the server reports (for example nodes, memories, Agents, Apps
and asset bytes). Name exactly one owner. LIMIT is the limit in force: the
owner's own row, else the deployment default for that kind of owner, and
"unlimited" when there is neither. USED is a total counted when you ask. The CLI
keeps no list of limits: it prints what the server returns.

Some rows are only reported and cannot be set (the server says which).
"billing limits set" and "billing limits clear" change them for platform admins
and the billing role.

Reading an organization's limits requires org ADMIN, a person's their own or a
platform admin; the server decides and the CLI checks none of it.`,
		Example: `  hadron billing limits hrn:org:acme.com
  hadron billing limits --user @ann --json
  hadron billing limits --owner-me
  hadron billing limits set hrn:org:acme.com --limit nodes=20000 --limit apps=unlimited --source-type manual --source-id t-1 --note "enterprise" --yes`,
		Args: cobra.RangeArgs(0, 1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := who.check(cmd, args); err != nil {
				return err
			}
			client, err := f.GraphQLClient()
			if err != nil {
				return err
			}
			o, err := who.resolve(cmd, f, args)
			if err != nil {
				return err
			}
			resp, err := gen.OwnerLimits(cmd.Context(), client, o.orgRef, o.userRef)
			if err != nil {
				return api.MapError(err)
			}
			if resp == nil {
				return exitcode.Newf(exitcode.Error, "the server returned no limits")
			}
			rows := make([]limitDTO, 0, len(resp.OwnerLimits))
			for _, r := range resp.OwnerLimits {
				if r == nil {
					return exitcode.Newf(exitcode.Error, "the server returned a null limit, which the schema forbids; no result is shown")
				}
				rows = append(rows, limitDTO{Key: r.Key, Limit: r.Limit, Used: r.Used})
			}
			return writeLimits(f, rows)
		},
	}
	who.register(cmd)
	cmd.AddCommand(newCmdLimitsSet(f))
	cmd.AddCommand(newCmdLimitsClear(f))
	return cmd
}

// parseLimit splits one --limit key=value on the first "=". "unlimited" becomes a
// nil limit, sent as null (which lifts the deployment default); every other value
// is opaque and goes byte for byte, the package's money rule. The server judges
// the key and the number.
func parseLimit(s string) (*gen.OwnerLimitInput, error) {
	key, value, ok := strings.Cut(s, "=")
	if !ok || key == "" || value == "" {
		return nil, exitcode.Newf(exitcode.Usage, "--limit takes key=value or key=unlimited (the value given is not shown)")
	}
	if value == "unlimited" {
		return &gen.OwnerLimitInput{Key: key}, nil
	}
	return &gen.OwnerLimitInput{Key: key, Limit: &value}, nil
}

func newCmdLimitsSet(f *cmdutil.Factory) *cobra.Command {
	var limits []string
	var sourceType, sourceID, note, onlySourceType string
	var replace, yes bool
	var who ownerFlags
	cmd := &cobra.Command{
		Use:   "set (<org> | --user <person> | --owner-me) --limit <key>=<n|unlimited> [--limit ...] --source-type <t> --source-id <i> --note <text>",
		Short: "Set an owner's limits (platform admin or billing role)",
		Long: `Write limits for an organization or a person: one --limit key=value for each,
where value is a whole number or "unlimited" (which lifts the deployment default
for that key). Name exactly one owner. Each key may be given once. It changes
what the owner may hold, so it asks before it sends (or pass --yes; without a
terminal --yes is required).

--replace also deletes the owner's limits for keys you did not name;
--only-source-type narrows that deletion to limits written with that source type
(the server refuses it without --replace). Numbers go to the server exactly as
typed and the CLI keeps no list of keys: the server refuses an unknown key, a
negative number and a key that is only reported.

--source-type and --source-id name what set these limits, for example manual
and a ticket id; they are logged, so they must not hold personal data. --note
says why: it is kept with the row and must not hold personal data. All three
are required. For platform admins and the billing role.`,
		Example: `  hadron billing limits set hrn:org:acme.com --limit nodes=20000 --limit apps=unlimited \
    --source-type manual --source-id t-1 --note "enterprise" --yes
  hadron billing limits set --user @ann --limit memories=50 --replace \
    --source-type manual --source-id t-2 --note "support case" --yes --json`,
		Args: cobra.RangeArgs(0, 1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := who.check(cmd, args); err != nil {
				return err
			}
			if len(limits) == 0 {
				return exitcode.Newf(exitcode.Usage, "give at least one --limit key=value")
			}
			parsed := make([]*gen.OwnerLimitInput, 0, len(limits))
			seen := map[string]bool{}
			for _, l := range limits {
				p, err := parseLimit(l)
				if err != nil {
					return err
				}
				if seen[p.Key] {
					return exitcode.Newf(exitcode.Usage, "--limit names %s twice; each key may be given once", promptValue(p.Key))
				}
				seen[p.Key] = true
				parsed = append(parsed, p)
			}
			for _, r := range []struct{ name, val string }{{"source-type", sourceType}, {"source-id", sourceID}, {"note", note}} {
				if err := requireFlag(cmd, r.name, r.val); err != nil {
					return err
				}
			}
			only, err := optionalFlag(cmd, "only-source-type", &onlySourceType)
			if err != nil {
				return err
			}
			// Every key and value goes into the prompt untruncated: it is what the
			// caller is approving.
			var shown []string
			for _, p := range parsed {
				v := "unlimited"
				if p.Limit != nil {
					v = *p.Limit
				}
				shown = append(shown, promptValue(p.Key)+"="+promptValue(v))
			}
			prompt := fmt.Sprintf("Set the limits of %s: %s", who.label(args), strings.Join(shown, ", "))
			if replace {
				prompt += " and delete every other limit it has"
			}
			if err := cmdutil.Confirm(f.IOStreams, yes, prompt+"?"); err != nil {
				return err
			}
			client, err := f.GraphQLClient()
			if err != nil {
				return err
			}
			o, err := who.resolve(cmd, f, args)
			if err != nil {
				return err
			}
			in := gen.SetOwnerLimitsInput{
				Limits: parsed, Note: note, SourceType: sourceType, SourceId: sourceID,
				OnlySourceType: only, OrgRef: o.orgRef, UserRef: o.userRef,
			}
			if replace {
				in.Replace = &replace
			}
			resp, err := gen.SetOwnerLimits(cmd.Context(), client, &in)
			if err != nil {
				return api.MapError(err)
			}
			if resp == nil {
				return exitcode.Newf(exitcode.Error, "the server returned no limits")
			}
			rows := make([]limitDTO, 0, len(resp.SetOwnerLimits))
			for _, r := range resp.SetOwnerLimits {
				if r == nil {
					return exitcode.Newf(exitcode.Error, "the server returned a null limit, which the schema forbids; no result is shown")
				}
				rows = append(rows, limitDTO{Key: r.Key, Limit: r.Limit, Used: r.Used})
			}
			return writeLimits(f, rows)
		},
	}
	cmd.Flags().StringArrayVar(&limits, "limit", nil, "key=value, value a whole number or unlimited (repeatable; required; each key once)")
	cmd.Flags().BoolVar(&replace, "replace", false, "also delete the owner's limits for keys not named here")
	cmd.Flags().StringVar(&onlySourceType, "only-source-type", "", "narrow --replace's deletion to limits written with this source type")
	cmd.Flags().StringVar(&sourceType, "source-type", "", "what set these limits, for example manual (required; logged, no personal data)")
	cmd.Flags().StringVar(&sourceID, "source-id", "", "which one, for example a ticket id (required; logged, no personal data)")
	cmd.Flags().StringVar(&note, "note", "", "why (required); kept with the row, must not hold personal data")
	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "skip the confirmation prompt")
	who.register(cmd)
	return cmd
}

func newCmdLimitsClear(f *cmdutil.Factory) *cobra.Command {
	var keys []string
	var sourceType, sourceID, note, onlySourceType string
	var yes bool
	var who ownerFlags
	cmd := &cobra.Command{
		Use:   "clear (<org> | --user <person> | --owner-me) [--key <key> ...] --source-type <t> --source-id <i> --note <text>",
		Short: "Clear an owner's limits so the deployment default applies (platform admin or billing role)",
		Long: `Delete an organization's or a person's own limits, so the deployment default
for that kind of owner applies again. Name exactly one owner. Without --key it
clears EVERY limit the owner has, and the prompt says so; with --key (repeatable)
only those. It asks before it sends (or pass --yes; without a terminal --yes is
required).

--only-source-type clears only limits written with that source type.
--source-type and --source-id name what cleared them and are logged. --note says
why; the row is deleted, so the server does not keep it with a row, but it must
not hold personal data. All three are required. For platform admins and the
billing role.`,
		Example: `  hadron billing limits clear hrn:org:acme.com --key nodes --source-type manual --source-id t-3 --note "downgrade" --yes
  hadron billing limits clear --user @ann --source-type manual --source-id t-4 --note "support case" --yes`,
		Args: cobra.RangeArgs(0, 1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := who.check(cmd, args); err != nil {
				return err
			}
			for _, k := range keys {
				if k == "" {
					return exitcode.Newf(exitcode.Usage, "--key is empty")
				}
			}
			for _, r := range []struct{ name, val string }{{"source-type", sourceType}, {"source-id", sourceID}, {"note", note}} {
				if err := requireFlag(cmd, r.name, r.val); err != nil {
					return err
				}
			}
			only, err := optionalFlag(cmd, "only-source-type", &onlySourceType)
			if err != nil {
				return err
			}
			what := "EVERY limit"
			if len(keys) > 0 {
				var shown []string
				for _, k := range keys {
					shown = append(shown, promptValue(k))
				}
				what = "the limits " + strings.Join(shown, ", ")
			}
			if err := cmdutil.Confirm(f.IOStreams, yes, fmt.Sprintf("Clear %s of %s?", what, who.label(args))); err != nil {
				return err
			}
			client, err := f.GraphQLClient()
			if err != nil {
				return err
			}
			o, err := who.resolve(cmd, f, args)
			if err != nil {
				return err
			}
			in := gen.ClearOwnerLimitsInput{
				Note: note, SourceType: sourceType, SourceId: sourceID,
				OnlySourceType: only, OrgRef: o.orgRef, UserRef: o.userRef,
			}
			if len(keys) > 0 {
				in.Keys = keys
			}
			resp, err := gen.ClearOwnerLimits(cmd.Context(), client, &in)
			if err != nil {
				return api.MapError(err)
			}
			if resp == nil {
				return exitcode.Newf(exitcode.Error, "the server returned no limits")
			}
			rows := make([]limitDTO, 0, len(resp.ClearOwnerLimits))
			for _, r := range resp.ClearOwnerLimits {
				if r == nil {
					return exitcode.Newf(exitcode.Error, "the server returned a null limit, which the schema forbids; no result is shown")
				}
				rows = append(rows, limitDTO{Key: r.Key, Limit: r.Limit, Used: r.Used})
			}
			return writeLimits(f, rows)
		},
	}
	cmd.Flags().StringArrayVar(&keys, "key", nil, "a limit to clear (repeatable); omit to clear every limit the owner has")
	cmd.Flags().StringVar(&onlySourceType, "only-source-type", "", "clear only limits written with this source type")
	cmd.Flags().StringVar(&sourceType, "source-type", "", "what cleared these limits (required; logged, no personal data)")
	cmd.Flags().StringVar(&sourceID, "source-id", "", "which one (required; logged, no personal data)")
	cmd.Flags().StringVar(&note, "note", "", "why (required); must not hold personal data (the row is deleted, so it is not kept with one)")
	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "skip the confirmation prompt")
	who.register(cmd)
	return cmd
}
