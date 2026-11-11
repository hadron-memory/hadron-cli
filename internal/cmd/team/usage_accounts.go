package team

// `team usage accounts` (hadron-cli#1875): a VIEW of the server's list of an
// App's usage accounts (hadron-server#3853, query accountUsageAccounts,
// cor:agt:140:05). The list, its order, its bound and its completeness are the
// server's; nothing here filters, dedupes, re-orders or validates
// (cor:api:240:01).

import (
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

// usageAccountDTO is one entry, field for field the server's AccountUsageAccount.
type usageAccountDTO struct {
	Account           string   `json:"account"`
	Host              string   `json:"host"`
	Family            string   `json:"family"`
	NewestSnapshotAt  string   `json:"newestSnapshotAt"`
	Workers           []string `json:"workers"`
	WorkersConfigured bool     `json:"workersConfigured"`
}

// usageAccountsDTO is the server's AccountUsageAccounts envelope, verbatim.
type usageAccountsDTO struct {
	AsOf     string            `json:"asOf"`
	Accounts []usageAccountDTO `json:"accounts"`
	Complete bool              `json:"complete"`
}

// usageAccountsIncompleteNote is the stderr line for complete: false.
const usageAccountsIncompleteNote = "note: the server's account list is incomplete (more than 50 accounts exist, or its search stopped short); some accounts are not listed"

func newUsageAccounts(f *cmdutil.Factory) *cobra.Command {
	return &cobra.Command{
		Use:   "accounts",
		Short: "List the team App's usage accounts, as the server returns them",
		Long: `List the team App's subscription accounts that have usage history
(accountUsageAccounts), exactly as the server returns them: one row per
account, host and family, in the server's order (label, then host, then
family), with the newest snapshot time and the workers configured on it. A
label with history from two hosts is two rows. "not configured" means the
account's records name no workers list, which is different from an empty one
("none"). Read-only.

The server lists at most 50 accounts. When more exist, or its search stopped
short, the list is incomplete: a note goes to stderr (stdout carries only the
rows), --json carries "complete": false, and the exit code stays 0. --json
prints the server's envelope {"asOf", "accounts", "complete"}; an App with no
history is "accounts": [] with "complete": true.

APP_NOT_FOUND is printed as the server gives it, the same answer for an App
that does not exist and one you may not read.

The team App is --app, the App context, or the worktree binding.`,
		Example: "  hadron team usage accounts\n  hadron team usage accounts --app acme.com:eng-team --json",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx := cmd.Context()
			b, err := readBindingOrNilWithApp(ctx, f)
			if err != nil {
				return err
			}
			scope, err := resolveTeamAppScope(ctx, f, b)
			if err != nil {
				return err
			}
			if scope.Ref == "" {
				return exitcode.Newf(exitcode.Usage, "no team App: pass --app, set an App context, or bind a worker session")
			}
			client, err := f.GraphQLClient()
			if err != nil {
				return err
			}
			resp, err := gen.AccountUsageAccounts(ctx, client, scope.Ref)
			if err != nil {
				return mapAccountsError(err)
			}
			if resp.AccountUsageAccounts == nil {
				return exitcode.Newf(exitcode.Error, "server returned no account list")
			}
			dto := usageAccountsFrom(*resp.AccountUsageAccounts)
			if !f.JSON && !dto.Complete {
				_, _ = fmt.Fprintln(f.IOStreams.ErrOut, usageAccountsIncompleteNote)
			}
			return output.Write(f.IOStreams, f.JSON, dto, func(w io.Writer) error { return writeUsageAccounts(w, dto) })
		},
	}
}

// mapAccountsError turns a server without the field into a plain "too old"
// message and routes everything else through api.MapError.
func mapAccountsError(err error) error {
	if strings.Contains(err.Error(), "accountUsageAccounts") && (strings.Contains(err.Error(), "Cannot query field") || strings.Contains(err.Error(), "Unknown argument")) {
		return exitcode.Newf(exitcode.Error, "this hadron-server is too old to list usage accounts (no accountUsageAccounts field; needs hadron-server #3853) — upgrade the server")
	}
	return api.MapError(err)
}

// usageAccountsFrom copies the server's answer into the command's own DTO
// (never the generated structs), in the server's order, every list [] not null.
func usageAccountsFrom(r gen.AccountUsageAccountsAccountUsageAccounts) usageAccountsDTO {
	d := usageAccountsDTO{AsOf: r.AsOf, Accounts: []usageAccountDTO{}, Complete: r.Complete}
	for _, a := range r.Accounts {
		if a == nil {
			continue
		}
		d.Accounts = append(d.Accounts, usageAccountDTO{
			Account: a.Account, Host: a.Host, Family: a.Family, NewestSnapshotAt: a.NewestSnapshotAt,
			Workers: append([]string{}, a.Workers...), WorkersConfigured: a.WorkersConfigured,
		})
	}
	return d
}

func workersCell(a usageAccountDTO) string {
	switch {
	case !a.WorkersConfigured:
		return "not configured"
	case len(a.Workers) == 0:
		return "none"
	}
	return strings.Join(a.Workers, ", ")
}

func writeUsageAccounts(w io.Writer, dto usageAccountsDTO) error {
	if len(dto.Accounts) == 0 {
		_, err := fmt.Fprintln(w, "no usage accounts listed for this App")
		return err
	}
	t := output.NewTable(w, "ACCOUNT", "HOST", "FAMILY", "NEWEST SNAPSHOT", "WORKERS")
	for _, a := range dto.Accounts {
		t.Row(output.SafeCell(a.Account), output.SafeCell(a.Host), output.SafeCell(a.Family), output.SafeCell(a.NewestSnapshotAt), output.SafeCell(workersCell(a)))
	}
	return t.Flush()
}
