package cmdutil

import (
	"context"
	"fmt"
	"github.com/hadron-memory/hadron-cli/internal/api"
	"github.com/hadron-memory/hadron-cli/internal/api/gen"
	"github.com/hadron-memory/hadron-cli/internal/output"
)

type authoredWithKey struct{}

// WithAuthoredWith carries only an explicit caller claim through multi-write commands.
func WithAuthoredWith(ctx context.Context, claim *gen.AuthoredWithInput) context.Context {
	return context.WithValue(ctx, authoredWithKey{}, claim)
}

// AuthoredWith returns the caller's claim, never inferred from a binding or role.
func AuthoredWith(ctx context.Context) *gen.AuthoredWithInput {
	claim, _ := ctx.Value(authoredWithKey{}).(*gen.AuthoredWithInput)
	return claim
}

// AuthoredWithRecovery supplies bounded recovery on both fatal refusals and
// best-effort write warnings. Call only from a command that supports the flags.
func AuthoredWithRecovery(err error) string {
	d := api.AuthoringAttestationDetails(err)
	if d == nil || d["taskAccess"] != "READABLE" {
		return ""
	}
	task, _ := d["task"].(string)
	if task == "" {
		task, _ = d["taskId"].(string)
	}
	text := fmt.Sprintf("Required task: %s, revision %v", output.SafeLine(task, 512), d["taskRevision"])
	if load, ok := d["load"].(map[string]any); ok {
		if cli, ok := load["cli"].(string); ok && cli != "" {
			text += "\nLoad: " + output.SafeLine(cli, 2048)
		}
	}
	if task != "" && output.SafeLine(task, 512) == task && d["taskRevision"] != nil {
		text += fmt.Sprintf("\nRetry with: --authored-with %s --authored-revision %v", ShellQuote(task), d["taskRevision"])
	}
	return text
}
