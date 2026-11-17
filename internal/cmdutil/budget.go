package cmdutil

import (
	"encoding/json"
	"strconv"

	"github.com/spf13/cobra"

	"github.com/hadron-memory/hadron-cli/internal/exitcode"
)

// BudgetFlags carries the per-trigger run limits (--budget-tokens /
// --budget-actions, server#2479) shared by schedule create|update, webhook
// create and run trigger. Above-default values are NOT checked here: the server
// refuses them (BAD_USER_INPUT) and that refusal is passed through.
type BudgetFlags struct {
	Tokens, Actions           int
	ClearTokens, ClearActions bool
}

// Add registers --budget-tokens and --budget-actions; withClear also registers
// --clear-budget-tokens / --clear-budget-actions (update only).
func (b *BudgetFlags) Add(cmd *cobra.Command, withClear bool) {
	cmd.Flags().IntVar(&b.Tokens, "budget-tokens", 0, "per-run LLM-token limit, a positive whole number no greater than the platform default (requires a server with trigger budgets)")
	cmd.Flags().IntVar(&b.Actions, "budget-actions", 0, "per-run action limit, a positive whole number no greater than the platform default (requires a server with trigger budgets)")
	if withClear {
		cmd.Flags().BoolVar(&b.ClearTokens, "clear-budget-tokens", false, "clear the stored token limit (back to the platform default)")
		cmd.Flags().BoolVar(&b.ClearActions, "clear-budget-actions", false, "clear the stored action limit (back to the platform default)")
	}
}

func (b *BudgetFlags) positive(cmd *cobra.Command, flag string, v int) (*int, error) {
	if !cmd.Flags().Changed(flag) {
		return nil, nil
	}
	if v <= 0 {
		return nil, exitcode.Newf(exitcode.Usage, "--%s must be a positive whole number (got %d)", flag, v)
	}
	return &v, nil
}

// Values returns the limits to send on create/trigger: nil (omitted) when the
// flag was not passed, so the platform default applies.
func (b *BudgetFlags) Values(cmd *cobra.Command) (tokens, actions *int, err error) {
	if tokens, err = b.positive(cmd, "budget-tokens", b.Tokens); err != nil {
		return nil, nil, err
	}
	if actions, err = b.positive(cmd, "budget-actions", b.Actions); err != nil {
		return nil, nil, err
	}
	return tokens, actions, nil
}

// UpdateValues returns the update payloads: nil omits (preserve), a number sets,
// and the literal null (from --clear-budget-*) clears back to the default.
// changed reports whether either limit is part of the update.
func (b *BudgetFlags) UpdateValues(cmd *cobra.Command) (tokens, actions json.RawMessage, changed bool, err error) {
	one := func(flag, clearFlag string, v int, clear bool) (json.RawMessage, error) {
		set := cmd.Flags().Changed(flag)
		if set && clear {
			return nil, exitcode.Newf(exitcode.Usage, "--%s and --%s are mutually exclusive", flag, clearFlag)
		}
		if clear {
			return json.RawMessage("null"), nil
		}
		p, err := b.positive(cmd, flag, v)
		if err != nil || p == nil {
			return nil, err
		}
		return json.RawMessage(strconv.Itoa(*p)), nil
	}
	if tokens, err = one("budget-tokens", "clear-budget-tokens", b.Tokens, b.ClearTokens); err != nil {
		return nil, nil, false, err
	}
	if actions, err = one("budget-actions", "clear-budget-actions", b.Actions, b.ClearActions); err != nil {
		return nil, nil, false, err
	}
	return tokens, actions, tokens != nil || actions != nil, nil
}

// BudgetCell renders a stored limit for a table: the number, or "default" when
// the trigger carries none.
func BudgetCell(v *int) string {
	if v == nil {
		return "default"
	}
	return strconv.Itoa(*v)
}
