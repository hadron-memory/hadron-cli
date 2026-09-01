// Package team implements `hadron team ...` — the team-coordination
// surface (#369, re-keyed to the Worker model in #428 / hadron-server#974).
// A persona is pure DRESSING on an Agent (personaRole + a {{name}}-templated
// personaPrompt — no behavior forks on "is a persona"); the named identity
// ("Iris") is a WORKER, the casting of an installed Agent into an App
// (cor:dmo:050:11).
//
// TWO facts about a name, and they are not the same one (cor:agt:020:09,
// hadron-cli#487). HELD is whose name it is: held by a PERSON, freed only by
// an explicit release, and what decides whether you may bind. LIVE is whether
// a worker session is open on it right now — derived from an unended Session,
// only ever a question about a name already yours, and never a claim that
// anybody is at the keyboard, since a worker session outlives the chat session
// that started it. An earlier version of this comment called the second one
// "taken right now"; that word named both facts at once, which is the defect
// #487 was filed for.
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

       Adding a role to a team App you ALREADY have? Pass
       --install-into <app> here and skip step 2 — a new agent is in no
       App's cast pool, so without an install step 4 cannot reach it.

  2. hadron app install --org <org> --agent <agent> --name "<Team>"
       The team APP. ` + "`app agent add`" + ` installs further role agents into
       it; the AppAgent join is the cast pool (` + "`app agent list`" + `).

  3. hadron team role create <role> [--description <d>]
       (optional) A role definition — a label and a description. It used
       to carry a name register the cast allocated from; that is gone
       (hadron-server#1050), so this step no longer affects step 4 and is
       purely documentation of what the role is for.

  4. hadron team worker cast --app <app> --role <role> --name <Name>
       The named identity. The server resolves the agent (--role picks the
       single installed agent with that persona role; --agent names one
       directly), takes the name, binds the template, and provisions the
       worker's working memory. --name is REQUIRED: the name is PERMANENT
       per App (cor:agt:020:02), so it is chosen rather than derived —
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
	URN *string `json:"urn"`
	// PortalURL is the server-built link that opens this worker
	// (hadron-server#1026, cor:api:230:01) — the one to COPY when signing
	// anything published outside the team, rather than assembling
	// <origin>/app/u/<urn> by hand. Null means the deployment has no usable web
	// origin, or there is no URN to build from; both render as nothing and
	// neither is a cue to compose one.
	PortalURL      *string `json:"portalUrl"`
	Slug           string  `json:"slug"`
	AppID          string  `json:"appId"`
	AgentID        string  `json:"agentId"`
	Name           string  `json:"name"`
	Role           *string `json:"role"`
	Prompt         *string `json:"prompt"`
	PromptOverride *string `json:"promptOverride"`
	// Repos is the ROLE's repo affinity, resolved (#1024). A SOFT signal —
	// `session start` warns on a mismatch and never refuses. Empty is the
	// answer to every uncertainty (no role, no definition, unreadable memory,
	// or a denied read), so it means "never warn" and NOT "unconfigured";
	// there is deliberately no way to tell those apart, since both mean the
	// same thing to a caller. Initialized to []string{} so it renders as []
	// rather than null.
	Repos    []string `json:"repos"`
	MemoryID *string  `json:"memoryId"`
	// HeldByUserID / HeldAt are the HOLD (cor:agt:020:09): whose name this is.
	// The user ID rather than a rendered name, per
	// review:entity-fields-not-display-labels — a label drops the actionable
	// ref, and this one addresses a person a caller may need to contact.
	//
	// Both are masked to null on deny, so absence means "unheld OR not visible
	// to you". There is deliberately NO `held bool` convenience field beside
	// them: it would read as a settled fact and silently answer "no" to a
	// caller who simply cannot see.
	HeldByUserID *string `json:"heldByUserId,omitempty"`
	HeldAt       *string `json:"heldAt,omitempty"`
	// hadron-cli#487 / hadron-server#1086. NOT omitempty, deliberately, and
	// unlike the two above: null on hasLiveSession is the load-bearing answer
	// on this type — it is the signal that says every other working-state null
	// on the row is a mask rather than an absence. A key that vanishes cannot
	// carry that, because an absent key is indistinguishable from a client that
	// did not ask. lastActiveAt keeps its company for the same reason: the pair
	// is read together or not at all.
	//
	// null      → not permitted to know (the read gate masked it)
	// false     → no worker session is open on this name right now
	// true      → one is; which is NOT a claim that anyone is at the keyboard
	HasLiveSession *bool `json:"hasLiveSession"`
	// null on a PERMITTED read (hasLiveSession != null) means never driven —
	// the state hadron-cli#487 was filed for. Under-reports on a reaped
	// session, on purpose and in the safe direction; see the fragment comment
	// in team.graphql, which is read from the resolver rather than from the
	// field's SDL description.
	LastActiveAt *string `json:"lastActiveAt"`
	RetiredAt    *string `json:"retiredAt"`
	RetiredBy    *string `json:"retiredBy"`
	CreatedAt    string  `json:"createdAt"`
	CreatedBy    *string `json:"createdBy"`
	// Retired is the convenience read of retiredAt — a retired worker stops
	// authoring and takes no new sessions; its name stays reserved forever.
	// (Contrast HeldByUserID above, which gets no such convenience and says
	// why: retiredAt is never masked, so its absence is unambiguous.)
	Retired bool `json:"retired"`
}

func workerDTOFromFields(w gen.WorkerFields) workerDTO {
	return workerDTO{
		ID: w.Id, URN: w.Urn, PortalURL: w.PortalUrl, Slug: w.Slug, AppID: w.AppId, AgentID: w.AgentId,
		Name: w.Name, Role: w.Role,
		Prompt: w.Prompt, PromptOverride: w.PromptOverride,
		Repos: nonNilRepos(w.Repos), MemoryID: w.MemoryId,
		HeldByUserID: w.HeldByUserId, HeldAt: w.HeldAt,
		HasLiveSession: w.HasLiveSession, LastActiveAt: w.LastActiveAt,
		RetiredAt: w.RetiredAt, RetiredBy: w.RetiredBy,
		CreatedAt: w.CreatedAt, CreatedBy: w.CreatedBy,
		Retired: w.RetiredAt != nil,
	}
}

// workerRosterDTO is the stable --json shape of `worker list` (#459): every
// workerDTO field EXCEPT prompt.
//
// The key is OMITTED, not nulled. Nulling it would preserve the shape while
// silently handing a reader who wanted the briefing nothing at all — a wrong
// answer that looks like an answer, which is worse than an honest absence.
// `worker get` is the prompt surface, as the shipped task nodes already say.
type workerRosterDTO struct {
	ID  string  `json:"id"`
	URN *string `json:"urn"`
	// PortalURL is the server-built link that opens this worker
	// (cor:api:230:01). Null is a defined answer — no usable web origin on the
	// deployment, or no URN to build one from — and it stays null: the CLI
	// never composes a replacement, which is the defect the field exists to
	// remove. URN beside it is unaffected by the link's absence.
	PortalURL      *string `json:"portalUrl"`
	Slug           string  `json:"slug"`
	AppID          string  `json:"appId"`
	AgentID        string  `json:"agentId"`
	Name           string  `json:"name"`
	Role           *string `json:"role"`
	PromptOverride *string `json:"promptOverride"`
	// Same field and same [] semantics as workerDTO.Repos.
	Repos    []string `json:"repos"`
	MemoryID *string  `json:"memoryId"`
	// hadron-cli#487 — the four the roster needs to answer "is anyone actually
	// driving these names". Same keys, same spellings and same semantics as
	// workerDTO above, including the omitempty split: an entity that answers
	// one shape on `worker get` and another on `worker list` is what an agent
	// has to special-case, which is the rule this projection already follows
	// for portalUrl.
	HeldByUserID   *string `json:"heldByUserId,omitempty"`
	HeldAt         *string `json:"heldAt,omitempty"`
	HasLiveSession *bool   `json:"hasLiveSession"`
	LastActiveAt   *string `json:"lastActiveAt"`
	RetiredAt      *string `json:"retiredAt"`
	RetiredBy      *string `json:"retiredBy"`
	CreatedAt      string  `json:"createdAt"`
	CreatedBy      *string `json:"createdBy"`
	Retired        bool    `json:"retired"`
}

func workerRosterDTOFromFields(w gen.WorkerRosterFields) workerRosterDTO {
	return workerRosterDTO{
		ID: w.Id, URN: w.Urn, PortalURL: w.PortalUrl, Slug: w.Slug, AppID: w.AppId, AgentID: w.AgentId,
		Name: w.Name, Role: w.Role, PromptOverride: w.PromptOverride,
		Repos:          nonNilRepos(w.Repos),
		MemoryID:       w.MemoryId,
		HeldByUserID:   w.HeldByUserId,
		HeldAt:         w.HeldAt,
		HasLiveSession: w.HasLiveSession,
		LastActiveAt:   w.LastActiveAt,
		RetiredAt:      w.RetiredAt, RetiredBy: w.RetiredBy,
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

// scanWorkerRoster is scanWorkers' projection twin for `worker list` (#459) —
// same App, same includeRetired posture, same exhaustion loop, no prompt.
// Kept separate rather than parameterised: the two return different generated
// types, and a shared helper would have to erase that difference to a
// lowest-common shape, which is what dragged the prompt into the roster in the
// first place.
func scanWorkerRoster(ctx context.Context, client graphql.Client, appRef string) ([]gen.WorkerRosterFields, error) {
	includeRetired := true
	workers := []gen.WorkerRosterFields{}
	limit := workerPageSize
	for offset := 0; ; {
		off := offset
		resp, err := gen.WorkersRoster(ctx, client, appRef, &includeRetired, &limit, &off)
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
			workers = append(workers, w.WorkerRosterFields)
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
		// The id lookup was the ONLY lookup, so its failure is the answer — but
		// WHICH failure decides what to say, so branch on the CODE rather than
		// on err != nil (#464). A name-shaped argument is never a valid id, so
		// this lookup always errors for the very input the message below was
		// written for; keying on presence made that message unreachable and
		// leaked `input:3: worker Worker not found.` instead.
		//
		// WORKER_NOT_FOUND is what the server returns for every "cannot resolve
		// this ref" shape — verified live against a bare name, a malformed ref,
		// and a well-formed URN that does not exist. Anything else (auth,
		// transport, 5xx) still surfaces as itself, which is the outage-honesty
		// the original comment protects: an outage must not read as "the worker
		// does not exist".
		if err != nil && !api.HasErrorCode(err, "WORKER_NOT_FOUND") {
			return zero, api.MapError(err)
		}
		// Covers BOTH readings of the argument rather than guessing which was
		// meant (PR #483 review). An id that does not exist returns the same
		// WORKER_NOT_FOUND as a name with no scope, and --app cannot make a
		// nonexistent id resolve — so advising it alone would misdirect every
		// id-based caller. resolveWorker is shared by worker get/retire/rm and
		// `session start --as`, all of which take name-or-id, so one message
		// serves all of them.
		//
		// Deliberately NOT shape-sniffing the argument to pick a branch: worker
		// names are free-form, so no name-vs-id test is safe, and guessing is
		// the smell review:no-hand-parsed-ref-labels exists to catch.
		return zero, exitcode.Newf(exitcode.NotFound,
			"no worker %q — if that is a NAME it resolves only within an App, so pass --app <ref> (or set an App context); an id or URN (hrn:worker:…) needs no App scope, so if you passed one, nothing by that ref exists", arg)
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

// isBindingsApp answers "is the App this command resolved to the SAME App the
// worktree is bound to?" — the question a per-binding cursor has to get right
// before it writes (PR #493 review).
//
// A raw `scope.Ref == b.AppID` is not that question. `--app <urn>` and the
// documented `hadron app set-active <app-urn>` both put a URN in scope.Ref
// while the binding stores the server id, so the strings differ for the same
// App. Answering "different team" there is not a safe conservative default: it
// pins the watermark forever, and `session log` then claims on every run that
// this worktree has never read the chat. A nudge that is always wrong is
// ignored, which costs more than the case it was guarding.
//
// One round trip, and only in the ambiguous case — the ids match outright for
// a binding-sourced scope and for `--app <id>`. Unresolvable means unknown,
// and unknown must not write.
func isBindingsApp(ctx context.Context, f *cmdutil.Factory, ref, bindingAppID string) bool {
	if ref == "" || bindingAppID == "" {
		return false
	}
	if ref == bindingAppID {
		return true
	}
	client, err := f.GraphQLClient()
	if err != nil {
		return false
	}
	resp, err := gen.TeamAppIdentity(ctx, client, ref)
	if err != nil || resp.App == nil {
		return false
	}
	return resp.App.Id == bindingAppID
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
	return lazyOnce(func() string {
		return describeApp(ctx, f, scope.Ref) + " (" + scope.Source + ")"
	})
}

// lazyOnce defers read until the first call and caches the result. Extracted
// from lazyAppLabel so the laziness itself is testable without a Factory or a
// GraphQL stub (PR #471 review): the test that used to cover this
// reimplemented the caching inline, so it would have passed even if the real
// helper stopped caching.
func lazyOnce(read func() string) func() string {
	var cached string
	var done bool
	return func() string {
		if !done {
			cached, done = read(), true
		}
		return cached
	}
}

// nonNilRepos keeps an absent affinity rendering as [] rather than null — the
// slice rule from conventions:output-contract. It matters more than usual
// here: [] is the field's MEANINGFUL value ("never warn"), so a null would
// read as a third state the contract does not have.
func nonNilRepos(r []string) []string {
	if r == nil {
		return []string{}
	}
	return r
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
