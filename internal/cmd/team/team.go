// Package team implements `hadron team ...` — the team-coordination
// surface (#369, re-keyed to the Worker model in #428 / hadron-server#974).
// A persona is pure DRESSING on an Agent (personaRole + a {{name}}-templated
// personaPrompt — no behavior forks on "is a persona"); the named identity
// ("Iris") is a WORKER, the casting of an installed Agent into an App
// (cor:dmo:050:11). "Taken right now" derives from an active Session bound
// to the worker.
package team

import (
	"context"
	"strings"

	"github.com/Khan/genqlient/graphql"
	"github.com/spf13/cobra"

	"github.com/hadron-memory/hadron-cli/internal/api"
	"github.com/hadron-memory/hadron-cli/internal/api/gen"
	"github.com/hadron-memory/hadron-cli/internal/cmdutil"
	"github.com/hadron-memory/hadron-cli/internal/exitcode"
)

// NewCmdTeam builds the `hadron team` command group.
func NewCmdTeam(f *cmdutil.Factory) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "team <command>",
		Short: "Team coordination: workers and coding sessions",
		Long: `Coordinate a team of humans and AI agents. A team IS an App
(cor:agt:020:01): the installed agents are its cast pool, and its staff are
WORKERS — named castings of an installed agent ("Iris", the backend-engineer
agent cast into this App). A worker is re-driven across many sessions — by
the same or a different human; a session binds the current git worktree to a
worker and records provenance (host, tool, transcript path) so a merged PR
traces back to the session that produced it.

GETTING STARTED — standing up a team, in order

Two of the steps are not in this group, so the sequence is not discoverable
from ` + "`hadron team --help`" + ` alone (#402).

  1. hadron agent create --org <org> --name backend-engineer \
       --persona-role backend-engineer \
       --persona-prompt 'You are {{name}}, a backend engineer ...'
       The ROLE AGENT, born with its persona dressing: a role plus an
       identity prompt TEMPLATE ({{name}}/{{role}} are bound at casting
       time; refine later with ` + "`agent update`" + `). The dressing is reusable —
       one backend-engineer agent can be cast into many Apps, under many
       names.

  2. hadron app install --org <org> --agent <agent> --name "<Team>"
       The team APP. ` + "`app agent add`" + ` installs further role agents into
       it; the AppAgent join is the cast pool (` + "`app agent list`" + `).

  3. hadron team role create <role> --names <a,b,c> [--name-range F-J]
       (optional) The cast-list register: the ordered names the
       register-mode cast allocates from. Skip it and pass --name at cast
       time instead; with a register, step 4 needs no --name and
       team role list shows which names are free.

  4. hadron team worker cast --app <app> --role <role> [--name <Name>]
       The named identity. The server resolves the agent (--role picks the
       single installed agent with that persona role; --agent names one
       directly), allocates the name (explicit --name, or the register
       from step 3), binds the template, and provisions the worker's
       working memory. The name is PERMANENT per App (cor:agt:020:02) —
       preview the irreversible part first with worker cast --dry-run.

  5. hadron team session start --as <worker>
       Binds this worktree. The session is App-bound through the worker,
       and the binding records the worker's App as the worklog home — so
       ` + "`session log`" + ` records milestones with no flags at all (#399).

  6. hadron team init                                 (optional)
       Asks the server to converge the collection schemas it owns — the
       App resolves its own team memory (#400). NOT a precondition for
       anything — useful to repair a memory declared by an older CLI.

Check any of it with ` + "`hadron team worker list --app <app>`" + ` (the staff)
and ` + "`hadron app agent list <app>`" + ` (the installed cast pool).`,
	}
	cmd.AddCommand(newCmdInit(f))
	cmd.AddCommand(newCmdWorker(f))
	cmd.AddCommand(newCmdRole(f))
	cmd.AddCommand(newCmdSession(f))
	cmd.AddCommand(newCmdTeamChat(f))
	return cmd
}

// workerDTO is the stable --json shape for a worker.
type workerDTO struct {
	ID string `json:"id"`
	// URN is the worker's address (#991), keyed on the permanent derived
	// slug. Null when the App's URN predates the flat grammar-v2 arity it
	// needs, so consumers must tolerate its absence.
	URN            *string `json:"urn"`
	Slug           string  `json:"slug"`
	AppID          string  `json:"appId"`
	AgentID        string  `json:"agentId"`
	Name           string  `json:"name"`
	Role           *string `json:"role"`
	Prompt         *string `json:"prompt"`
	PromptOverride *string `json:"promptOverride"`
	MemoryID       *string `json:"memoryId"`
	RetiredAt      *string `json:"retiredAt"`
	RetiredBy      *string `json:"retiredBy"`
	CreatedAt      string  `json:"createdAt"`
	CreatedBy      *string `json:"createdBy"`
	// Retired is the convenience read of retiredAt — a retired worker stops
	// authoring and takes no new sessions; its name stays reserved forever.
	Retired bool `json:"retired"`
}

func workerDTOFromFields(w gen.WorkerFields) workerDTO {
	return workerDTO{
		ID: w.Id, URN: w.Urn, Slug: w.Slug, AppID: w.AppId, AgentID: w.AgentId, Name: w.Name, Role: w.Role,
		Prompt: w.Prompt, PromptOverride: w.PromptOverride, MemoryID: w.MemoryId,
		RetiredAt: w.RetiredAt, RetiredBy: w.RetiredBy,
		CreatedAt: w.CreatedAt, CreatedBy: w.CreatedBy,
		Retired: w.RetiredAt != nil,
	}
}

const workerPageSize = 200

// scanWorkers pages one App's staff to exhaustion (the issue-#23 rule: an
// unbounded list is one default page). includeRetired is always true here —
// resolution and joins must see retired workers, since their names stay
// bound to history; commands that hide them filter client-side.
func scanWorkers(ctx context.Context, client graphql.Client, appRef string) ([]gen.WorkerFields, error) {
	includeRetired := true
	workers := []gen.WorkerFields{}
	limit := workerPageSize
	for offset := 0; ; {
		off := offset
		resp, err := gen.Workers(ctx, client, appRef, &includeRetired, &limit, &off)
		if err != nil {
			return nil, api.MapError(err)
		}
		if resp.Workers == nil {
			return workers, nil
		}
		for _, w := range resp.Workers.Items {
			if w == nil {
				continue
			}
			workers = append(workers, w.WorkerFields)
		}
		offset += len(resp.Workers.Items)
		if len(resp.Workers.Items) < workerPageSize || offset >= resp.Workers.Total {
			return workers, nil
		}
	}
}

// resolveWorker turns a worker name or a worker id into the worker row.
// Names are App-scoped (unique per App, case-insensitively — cor:agt:020:02),
// so a name needs an App to resolve in: appRef may be empty, in which case
// only the App-free spellings can succeed: the worker's URN (#991 — keyed on
// its derived permanent slug) or its id, both of which `worker(ref:)` takes.
// The name pass sees retired workers too — retiring must not make
// `worker get Iris` a not-found.
func resolveWorker(ctx context.Context, client graphql.Client, appRef, arg string) (gen.WorkerFields, error) {
	var zero gen.WorkerFields
	arg = strings.TrimSpace(arg)
	if arg == "" {
		return zero, exitcode.Newf(exitcode.Usage, "empty worker reference")
	}
	// A worker URN addresses ONE casting App-independently (#991/#445), so it
	// dispatches FIRST — before any ambient App scope. Scanning an App's staff
	// ahead of it would let a stale, uninstalled or unreadable --app/context
	// break a ref that is by construction self-contained, and would then bury
	// the real error under "no worker … in this App" (PR #446 review).
	if strings.HasPrefix(strings.ToLower(arg), "hrn:worker:") {
		resp, err := gen.GetWorker(ctx, client, arg)
		if err != nil {
			return zero, api.MapError(err)
		}
		if resp.Worker == nil {
			return zero, exitcode.Newf(exitcode.NotFound, "no worker %q", arg)
		}
		return resp.Worker.WorkerFields, nil
	}
	if appRef != "" {
		workers, err := scanWorkers(ctx, client, appRef)
		if err != nil {
			return zero, err
		}
		for _, w := range workers {
			if strings.EqualFold(w.Name, arg) || w.Id == arg {
				return w, nil
			}
		}
	}
	// Not a name in this App (or no App to look in) — try it as a worker id.
	resp, err := gen.GetWorker(ctx, client, arg)
	if err == nil && resp.Worker != nil {
		return resp.Worker.WorkerFields, nil
	}
	if appRef == "" {
		// The id lookup was the ONLY lookup, so its failure is the answer: an
		// auth or transport error must surface as itself, not as a fabricated
		// not-found (an outage would read as "the worker does not exist").
		if err != nil {
			return zero, api.MapError(err)
		}
		return zero, exitcode.Newf(exitcode.NotFound,
			"no worker %q — a worker NAME resolves within an App, so pass --app <ref> (or set an App context); a worker URN (hrn:worker:…) or id works without one", arg)
	}
	// With an App scope, the staff scan above already reached the server and
	// found no such name — the id try was a bonus, and a refusal of a
	// name-shaped ref (e.g. BAD_USER_INPUT) must not mask the honest answer.
	return zero, exitcode.Newf(exitcode.NotFound,
		"no worker %q in this App — `hadron team worker list` shows the staff", arg)
}

// describeApp renders a team App the way a reader can act on it: its URN,
// which carries the App slug in readable form, plus its display name. The
// ambient resolution answers with whatever ref won — often the binding's raw
// AppID — so the readable form takes one extra read.
//
// BEST-EFFORT BY DESIGN: this decorates a render, it never gates one. A
// failed or unreadable App lookup degrades to the ref we already have rather
// than turning a working `worker list` into an error, and callers pass a ref
// they have already used successfully, so a failure here means the App RECORD
// is unreadable, not that the scope was wrong.
func describeApp(ctx context.Context, f *cmdutil.Factory, ref string) string {
	if ref == "" {
		return "—"
	}
	client, err := f.GraphQLClient()
	if err != nil {
		return ref
	}
	resp, err := gen.TeamAppIdentity(ctx, client, ref)
	if err != nil || resp.App == nil {
		return ref
	}
	readable := resp.App.Urn
	if readable == "" {
		readable = ref
	}
	if resp.App.Name != "" {
		// An em dash, not parentheses: the list's scope line already ends in a
		// parenthesised source, and two bracketed groups in a row read as noise.
		return readable + " — " + resp.App.Name
	}
	return readable
}

// lazyAppLabel renders a resolved App scope as "<readable app> (<source>)",
// on first call only, and caches it (#468).
//
// Every site that prints this is GUARDED: `--json` never runs the human
// branch, and `--yes` skips a confirmation prompt entirely. So the read must
// not fire until a site that actually prints it fires — an agent running
// `role rm --yes --json` should pay nothing — and must not fire twice when a
// command both prompts and prints a receipt.
func lazyAppLabel(ctx context.Context, f *cmdutil.Factory, scope appScope) func() string {
	var cached string
	return func() string {
		if cached == "" {
			cached = describeApp(ctx, f, scope.Ref) + " (" + scope.Source + ")"
		}
		return cached
	}
}

func roleSuffix(role *string) string {
	if role == nil || *role == "" {
		return ""
	}
	return " (" + *role + ")"
}

func dash(s *string) string {
	if s == nil || *s == "" {
		return "—"
	}
	return *s
}

// optStr returns a pointer for a non-empty value, else nil (omitted on the wire).
func optStr(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

// changedStr returns a pointer only when the flag was explicitly set, so an
// unset flag is omitted (preserve) while an explicit "" is sent (clear).
func changedStr(cmd *cobra.Command, flag, val string) *string {
	if cmd.Flags().Changed(flag) {
		return &val
	}
	return nil
}
