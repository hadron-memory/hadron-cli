package automation

import (
	"context"
	"strconv"
	"strings"

	"github.com/Khan/genqlient/graphql"
	"github.com/spf13/cobra"

	"github.com/hadron-memory/hadron-cli/internal/api/gen"
	"github.com/hadron-memory/hadron-cli/internal/exitcode"
)

// selection is the one trigger a command acts on.
type selection struct {
	Kind    gen.AutomationTriggerKind
	Trigger triggerDTO
	Display string // what a person reads: schedule/nightly, or "the manual run <id>"
	State   string // the list's state for a listed trigger; "" for a MANUAL origin
}

const selectHelp = "name an automation (`schedule/<name>`, `webhook/<name>` or a bare <name>) or pass --run <run-id> for a manual run's approval"

// addSelectionArgs wires the optional <name> positional and --run.
func addSelectionArgs(cmd *cobra.Command, run *string) {
	cmd.Args = cobra.MaximumNArgs(1)
	cmd.Flags().StringVar(run, "run", "", "the manual run whose approval this is (the run id printed by the refused run); instead of a name; the run is found by its id alone (which App it belongs to is not checked), but an App must still be given (--app, else the active App)")
}

// resolveSelection turns what the person typed into one listed trigger. A
// <name> is matched against the App's whole list; nothing is guessed: no match
// refuses (4), several refuse (2) listing only the matches. A MANUAL origin is
// the run id exactly as given — never looked up, listed or substituted.
func resolveSelection(ctx context.Context, client graphql.Client, app string, args []string, run string, runSet bool) (*selection, error) {
	name := ""
	if len(args) > 0 {
		name = strings.TrimSpace(args[0])
	}
	// An explicit blank --run is not an absent one: it would silently fall back
	// to the name (or to the "no name" refusal), so it refuses on its own.
	if runSet && strings.TrimSpace(run) == "" {
		return nil, exitcode.Newf(exitcode.Usage, "--run was given a blank value — pass the run id, or omit --run and name an automation")
	}
	hasRun := strings.TrimSpace(run) != ""
	switch {
	case name != "" && hasRun:
		return nil, exitcode.Newf(exitcode.Usage, "give an automation name or --run, not both: %s", selectHelp)
	case name == "" && !hasRun:
		return nil, exitcode.Newf(exitcode.Usage, "no automation named: %s", selectHelp)
	}
	if hasRun {
		run = strings.TrimSpace(run)
		return &selection{
			Kind:    gen.AutomationTriggerKindManual,
			Trigger: triggerDTO{Kind: string(gen.AutomationTriggerKindManual), Ref: run},
			Display: "the manual run " + safeText(run, maxName),
		}, nil
	}

	l, err := collectListing(ctx, client, app)
	if err != nil {
		return nil, err
	}
	kind, bare := splitQualified(name)
	var matches []rowDTO
	for _, r := range l.Rows {
		if r.Name != bare || (kind != "" && !strings.EqualFold(r.TriggerKind, kind)) {
			continue
		}
		matches = append(matches, r)
	}
	switch len(matches) {
	case 0:
		return nil, exitcode.Newf(exitcode.NotFound, "no automation named %s in %s, or it is not visible to you; run `hadron automation list`", strconv.Quote(safeText(name, maxName)), app)
	case 1:
		r := matches[0]
		return &selection{
			Kind:    gen.AutomationTriggerKind(r.TriggerKind),
			Trigger: triggerDTO{Kind: r.TriggerKind, Ref: r.TriggerRef, Name: r.Name},
			Display: displayName(r),
			State:   r.State,
		}, nil
	}
	var b strings.Builder
	b.WriteString("several automations match " + strconv.Quote(safeText(name, maxName)) + " in " + app + ":\n")
	sameKind := false
	seen := map[string]bool{}
	for _, m := range matches {
		b.WriteString("  " + displayName(m) + " — " + safeText(m.Headline, maxLine) + "\n")
		if seen[m.TriggerKind] {
			sameKind = true
		}
		seen[m.TriggerKind] = true
	}
	if sameKind {
		b.WriteString("two of them share a kind and a name, so a name cannot tell them apart: rename one, then retry")
	} else {
		b.WriteString("name one of them with its kind, e.g. " + displayName(matches[0]))
	}
	return nil, exitcode.Newf(exitcode.Usage, "%s", b.String())
}

// splitQualified splits `schedule/<name>` or `webhook/<name>` (kind
// case-insensitive). Anything else is a bare name, slashes included.
func splitQualified(name string) (kind, bare string) {
	if i := strings.IndexByte(name, '/'); i > 0 {
		switch strings.ToLower(name[:i]) {
		case "schedule":
			return string(gen.AutomationTriggerKindSchedule), name[i+1:]
		case "webhook":
			return string(gen.AutomationTriggerKindWebhook), name[i+1:]
		}
	}
	return "", name
}

func displayName(r rowDTO) string {
	return strings.ToLower(r.TriggerKind) + "/" + safeText(r.Name, maxName)
}
