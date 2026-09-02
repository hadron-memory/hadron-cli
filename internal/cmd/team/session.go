package team

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"runtime"
	"strings"

	"github.com/Khan/genqlient/graphql"
	"github.com/google/uuid"
	"github.com/spf13/cobra"

	"github.com/hadron-memory/hadron-cli/internal/api"
	"github.com/hadron-memory/hadron-cli/internal/api/gen"
	"github.com/hadron-memory/hadron-cli/internal/cmdutil"
	"github.com/hadron-memory/hadron-cli/internal/exitcode"
	"github.com/hadron-memory/hadron-cli/internal/output"
)

// newSessionID is a seam for tests; startSession takes a client-minted id.
var newSessionID = uuid.NewString

func newCmdSession(f *cmdutil.Factory) *cobra.Command {
	cmd := &cobra.Command{
		Use:     "session <command>",
		Aliases: []string{"sessions"},
		Short:   "Drive a worker from this worktree — worker sessions",
		Long: `A WORKER SESSION binds the current git worktree to a worker and records
who is driving it, from where, with which tool. The binding lives under the
worktree's git dir, so it survives a context compaction — ` + "`whoami`" + ` reads
it back.

TWO THINGS ARE CALLED A SESSION, AND ENDING ONE DOES NOT END THE OTHER
(hadron-server#1034). This group manages the first:

  worker session   the Hadron binding above — what makes your work
                   attributable, and what holds the worker
  chat session     the conversation you are in — the Claude Desktop window,
                   the Claude Code session, the IDE chat

Closing your CHAT SESSION does not end the worker session. It outlives the
chat, and since hadron-server#1114 NOTHING else ends it — no idle window,
no reap — so it stays open until you run ` + "`session end`" + `. Being TAKEN is
separate and does clear on its own: the server derives it from recent
driving. End the worker session deliberately when you stop, because that
is the only thing that ends it and the only chance to leave a handoff.

TAKEN is not HELD. Ending the session frees the SESSION; a PERSON binding a
worker also claims its name, and that hold stays yours until you run
` + "`worker release`" + ` — no session end, idle window, expiry or reap ever
clears one (cor:agt:020:09). An APP-KEY session claims no hold (an App key
holds nothing), so it has nothing to release.`,
	}
	cmd.AddCommand(newCmdSessionStart(f))
	cmd.AddCommand(newCmdSessionWhoami(f))
	cmd.AddCommand(newCmdSessionLog(f))
	cmd.AddCommand(newCmdSessionEnd(f))
	cmd.AddCommand(newCmdSessionList(f))
	return cmd
}

// sessionDTO is the stable --json shape for a session.
type sessionDTO struct {
	ID string `json:"id"`
	// AgentID is the role-agent driving the session; with workerId set it is
	// the agent behind the casting (stamped server-side, cor:agt:020:03).
	AgentID    *string `json:"agentId"`
	WorkerID   *string `json:"workerId"`
	WorkerName *string `json:"workerName"`
	// WorkerRole is the cast-list role behind the name — what a worker IS,
	// as opposed to what it is called (#486). Already nested on
	// TeamSessionFields and discarded here until now, so it costs no query
	// change and no round trip.
	//
	// Nil whenever the NESTED worker is absent — unreadable, or a session that
	// predates worker binding. Deliberately not phrased in terms of WorkerName,
	// which can be populated from fallbackName (a provenance stub's worklog
	// name) on exactly those rows, so the two are not nil together (PR #521
	// review, @copilot). Rendered as a dash rather than guessed at.
	WorkerRole     *string `json:"workerRole"`
	UserID         *string `json:"userId"`
	Type           string  `json:"type"`
	Repo           *string `json:"repo"`
	Branch         *string `json:"branch"`
	PRNumber       *int    `json:"prNumber"`
	StartedAt      string  `json:"startedAt"`
	EndedAt        *string `json:"endedAt"`
	Host           *string `json:"host"`
	Tool           *string `json:"tool"`
	TranscriptPath *string `json:"transcriptPath"`
	LLMModel       *string `json:"llmModel"`
	// Active means "not ended" (endedAt IS NULL) — an honest liveness
	// signal: hadron-server#1114 retired the inactivity reaper, so a
	// developer session ends only on an explicit endSession (hard expiry
	// still applies to the CHATBOT path, which stamps expiresAt).
	Active bool `json:"active"`
}

// sessionDTOFromFields maps a session row. The worker name comes from the
// nested Session.worker (#980 / #432 — resolves for retired workers too, no
// per-row round trip); fallbackName covers rows where the server nested none
// (a provenance stub's worklog name). A session whose worker is unreadable
// keeps a nil name and the caller renders the raw id — surfaced, not
// silently dropped (the visibility-gap rule).
func sessionDTOFromFields(s gen.TeamSessionFields, fallbackName *string) sessionDTO {
	name := fallbackName
	var role *string
	if s.Worker != nil {
		name = &s.Worker.Name
		// No fallback for the role: unlike the name, nothing else on the wire
		// carries it, so absent stays absent rather than being inferred.
		role = s.Worker.Role
	}
	return sessionDTO{
		ID: s.Id, AgentID: s.AgentId, WorkerID: s.WorkerId, WorkerName: name, WorkerRole: role,
		UserID: s.UserId,
		Type:   s.Type, Repo: s.Repo, Branch: s.Branch, PRNumber: s.PrNumber,
		StartedAt: s.StartedAt, EndedAt: s.EndedAt, Host: s.Host, Tool: s.Tool,
		TranscriptPath: s.TranscriptPath, LLMModel: s.LlmModel, Active: s.EndedAt == nil,
	}
}

const sessionPageSize = 200

// scanSessions pages the sessions list (ordered startedAt desc; the server
// has no active filter, issue-#23 style: an unbounded call is one default
// page) and calls visit per session until visit returns false or the list is
// exhausted. workerRef narrows server-side to one worker's sessions (#974).
func scanSessions(ctx context.Context, client graphql.Client, repo, workerRef *string, visit func(gen.TeamSessionFields) bool) error {
	limit := sessionPageSize
	for offset := 0; ; {
		off := offset
		resp, err := gen.TeamSessions(ctx, client, repo, workerRef, &limit, &off)
		if err != nil {
			return api.MapError(err)
		}
		for _, s := range resp.Sessions {
			if s == nil {
				continue
			}
			if !visit(s.TeamSessionFields) {
				return nil
			}
		}
		if len(resp.Sessions) < sessionPageSize {
			return nil
		}
		offset += len(resp.Sessions)
	}
}

// workerActivity reads the worker's most recent session and its most recent
// still-active one — the taken/last-driven-by check, narrowed server-side by
// sessions(workerRef:) (#974). The scan still runs to exhaustion unless an
// active session shows up: active sessions are not necessarily the newest
// rows, so absence can only be proven by reading the worker's whole list.
func workerActivity(ctx context.Context, client graphql.Client, workerID string) (last, active *gen.TeamSessionFields, err error) {
	err = scanSessions(ctx, client, nil, &workerID, func(s gen.TeamSessionFields) bool {
		if last == nil {
			cp := s
			last = &cp
		}
		if s.EndedAt == nil {
			cp := s
			active = &cp
			return false
		}
		return true
	})
	return last, active, err
}

func describeSession(s *gen.TeamSessionFields) string {
	who := "an unknown user"
	if s.UserId != nil && *s.UserId != "" {
		who = "user " + *s.UserId
	}
	parts := []string{who}
	if s.Tool != nil && *s.Tool != "" {
		parts = append(parts, "via "+*s.Tool)
	}
	if s.Host != nil && *s.Host != "" {
		parts = append(parts, "on "+*s.Host)
	}
	return fmt.Sprintf("%s since %s (session %s)", strings.Join(parts, " "), s.StartedAt, s.Id)
}

// alreadyBoundError refuses a second binding in one worktree, and picks the
// remedy by whether the bound session is still ALIVE (#472).
//
// The guard itself was always right; the remedy was not. Offering --force for
// a LIVE binding answers the wrong problem: it replaces the binding and
// relabels which worker gets blamed, while leaving two agents editing one
// index and one working tree. It is worse than neutral, because afterwards the
// second agent believes it is correctly bound — the one signal that something
// was off has been cleared.
//
// Scope, stated honestly because the message cannot: this catches a second
// BINDING. A second agent working UNBOUND in the same checkout does identical
// damage and nothing here fires. That is why hadron-docs#233 exists.
// Takes the FACTORY, not a client, and builds the client itself (PR #478
// review). Before #472 this guard ran before any client construction, so it
// answered for a signed-out caller too. Hoisting f.GraphQLClient() above it
// would have replaced the documented exit-5 conflict with an auth or config
// error — losing the safe worktree remedy at exactly the moment the caller
// cannot fix the situation any other way. A client that cannot be built is
// simply unknown liveness, which already leads with that remedy.
func alreadyBoundError(ctx context.Context, f *cmdutil.Factory, existing *binding) error {
	const separate = "give this worker its own checkout:\n" +
		"    git worktree add -b <new-branch> ../<name>     # new branch\n" +
		"    git worktree add ../<name> <existing-branch>   # a branch that already exists\n" +
		"(-b is load-bearing: a bare fresh name fails, and a tag or sha gives a detached HEAD.)\n" +
		"Two agents in one worktree share an index and a working tree: whichever commits with `git add -A`\n" +
		"absorbs the other's in-flight edits, and Session.branch is captured once at bind and never revisited,\n" +
		"so the provenance record goes false with no signal. A merged PR stops tracing back to the work that\n" +
		"produced it — which is the whole reason the binding exists."

	// One read, only on a path that is already refusing. `session start` has
	// to know whether the bound session is live to answer at all, and guessing
	// would pick the wrong remedy half the time.
	var resp *gen.GetTeamSessionResponse
	client, err := f.GraphQLClient()
	if err == nil {
		resp, err = gen.GetTeamSession(ctx, client, existing.SessionID)
	}
	switch {
	case err != nil || resp == nil || resp.Session == nil:
		// Cannot tell. Lead with the safe remedy rather than the convenient
		// one: separating the trees is never wrong, and --force is only right
		// for a binding nobody is driving.
		return exitcode.Newf(exitcode.Conflict,
			"this worktree is already bound to worker %s (session %s), and whether that session is still active could not be checked — %s\n"+
				"If you are certain the binding is abandoned, --force replaces it; it does NOT separate the working trees.",
			existing.WorkerName, existing.SessionID, separate)
	case resp.Session.EndedAt == nil:
		// LIVE. The worktree is the answer; --force is named only so the
		// reader knows it is the wrong tool here rather than wondering.
		return exitcode.Newf(exitcode.Conflict,
			"this worktree is already bound to worker %s (session %s, started %s and still active) — if another agent is working here, %s\n"+
				"`--force` replaces the binding WITHOUT separating the working trees; use it only to take over an abandoned binding.",
			existing.WorkerName, existing.SessionID, resp.Session.StartedAt, separate)
	default:
		// Ended: nobody is driving, so replacing the binding is exactly right
		// and the worktree advice would be noise.
		return exitcode.Newf(exitcode.Conflict,
			"this worktree is already bound to worker %s (session %s), whose session ended %s — --force replaces the abandoned binding.",
			existing.WorkerName, existing.SessionID, *resp.Session.EndedAt)
	}
}

// holdVerdict is what the CLI can honestly say about whose name this is
// (cor:agt:020:09). It has four values rather than a bool because
// `heldByUserId` masks to null on deny, so "no hold visible" is NOT "unheld",
// and a failed identity read is NOT "held by somebody else" — the two
// unknowns fall on opposite sides and collapsing either one produces a
// confident wrong sentence (review:a-claim-must-not-outrun-its-evidence).
type holdVerdict int

const (
	// holdNoneVisible — no hold on the row. Either nobody holds the name or
	// the caller cannot see who does; the CLI must not assert which, so it
	// asserts nothing and lets the server be the gate.
	holdNoneVisible holdVerdict = iota
	// holdByMe — the caller's own name. TAKEN is forceable here, and only
	// here, because the driver deciding is the driver affected.
	holdByMe
	// holdByOther — provably somebody else's. --force cannot help.
	holdByOther
	// holdUnknownWhose — held, but this CLI could not read its own identity
	// to say by whom. Held is a FACT; whether it is yours is the open part.
	holdUnknownWhose
)

// classifyHold answers "whose name is this?" for a worker row, returning the
// holder as a human label alongside the verdict. Best-effort by construction:
// both reads it makes are decoration-or-classification, never gates, and a
// failure downgrades the verdict rather than failing the command.
func classifyHold(ctx context.Context, client graphql.Client, w gen.WorkerFields) (string, holdVerdict) {
	if w.HeldByUserId == nil {
		return "", holdNoneVisible
	}
	holder := describeHolder(ctx, client, *w.HeldByUserId)
	me, known := currentUserID(ctx, client)
	switch {
	case !known:
		return holder, holdUnknownWhose
	case me == *w.HeldByUserId:
		// me == "" is an App-key caller, which never equals a real user id —
		// correct, since per cor:agt:020:09 an App key holds nothing and so
		// is never the holder.
		return holder, holdByMe
	default:
		return holder, holdByOther
	}
}

// repoInAffinity reports whether repo is one of the affinity entries.
//
// Case-insensitive, because GitHub repository paths are: `Acme/Widgets` and
// `acme/widgets` are one repo, and treating them as two would fire a mismatch
// warning at somebody who typed their own repo correctly. That is the rule
// canonicalRepo already applies to every artifact ref, applied here too rather
// than invented separately — but WITHOUT its validation, since an affinity
// entry the server stored is not ours to reject and a --repo we cannot parse
// should silently not match rather than error out of a nudge.
func repoInAffinity(repo string, affinity []string) bool {
	want := strings.ToLower(strings.TrimSpace(repo))
	for _, r := range affinity {
		if strings.ToLower(strings.TrimSpace(r)) == want {
			return true
		}
	}
	return false
}

// warnRepoAffinity prints the #456 nudge when --repo sits outside the bound
// worker's role affinity. Best-effort and side-effect-free by construction:
// every uncertainty resolves to SILENCE, never to a warning and never to an
// error, because the one thing a soft signal must not do is cost somebody a
// session they legitimately started.
//
// Silent when:
//   - --repo was not passed (nothing to compare);
//   - the affinity is empty, which per the server's contract is the answer to
//     "no role", "role has no definition", "system memory unreadable" AND
//     "you may not read this field" alike — so a denied caller simply gets no
//     warning, which is the safe direction;
//   - the repo IS in the affinity, the ordinary case.
func warnRepoAffinity(ctx context.Context, f *cmdutil.Factory, client graphql.Client, w gen.WorkerFields, repo string) {
	if strings.TrimSpace(repo) == "" || len(w.Repos) == 0 || repoInAffinity(repo, w.Repos) {
		return
	}
	fmt.Fprintf(f.IOStreams.ErrOut, "! %s%s normally works in %s, but --repo is %s\n",
		w.Name, roleSuffix(w.Role), strings.Join(w.Repos, ", "), repo)
	// The suggestion is the part that makes this actionable rather than
	// merely correct, and it is deliberately only offered when it is
	// UNAMBIGUOUS: exactly one non-retired teammate claims this repo. Two
	// candidates and a guess would be worse than none, since the whole nudge
	// rests on the reader trusting it.
	//
	// Reached only on the mismatch path, so the roster read costs nothing in
	// the ordinary case; and its failure is swallowed, because a decoration
	// that cannot be computed must not turn into noise on a session that
	// started fine.
	if name := soleWorkerForRepo(ctx, client, w, repo); name != "" {
		fmt.Fprintf(f.IOStreams.ErrOut, "  bind %s if that is what you meant.\n", name)
	}
	fmt.Fprintf(f.IOStreams.ErrOut, "  `hadron team worker list` shows the roster. The session started; this is a nudge, not a refusal.\n")
}

// soleWorkerForRepo returns the name of the ONE non-retired worker in this
// App whose role claims repo, or "" when there is no such worker or more than
// one. Errors resolve to "" — see warnRepoAffinity on why silence is the only
// safe failure here.
// scanWorkerRoster, NOT scanWorkers: the roster projection exists precisely to
// leave the resolved multi-KB boot briefing on the server (#459), and `repos`
// was added to it in this change FOR this lookup — then the first draft called
// the prompt-bearing scan anyway, which on a name-based start repeats a roster
// read `resolveWorker` has already done, briefings and all (PR #516 review,
// Codex P2 + Copilot). Everything this needs — id, name, role, repos,
// retiredAt — is in the trimmed projection.
func soleWorkerForRepo(ctx context.Context, client graphql.Client, bound gen.WorkerFields, repo string) string {
	workers, err := scanWorkerRoster(ctx, client, bound.AppId)
	if err != nil {
		return ""
	}
	found := ""
	for _, cand := range workers {
		// Never suggest a retired worker: `session start` refuses one
		// outright (WORKER_RETIRED), so naming it would hand the reader a
		// remedy that cannot be run — the same defect the held refusal had
		// before #511.
		if cand.RetiredAt != nil || cand.Id == bound.Id || !repoInAffinity(repo, cand.Repos) {
			continue
		}
		if found != "" {
			return "" // ambiguous: name the mismatch, suggest nothing
		}
		found = cand.Name + roleSuffix(cand.Role)
	}
	return found
}

// appIndependentRef picks the spelling of a worker that resolves with NO App
// scope: its URN, or its id when the App's URN predates the grammar-v2 arity
// a worker URN needs and there is none.
//
// Never the NAME. A name resolves only within an App (cor:agt:020:02), so a
// remedy command spelled with one fails not-found for precisely the caller
// most likely to be reading it — whoever reached this refusal through
// `--as hrn:worker:…` with no ambient App, which is a supported path and the
// one `--as`'s own help now recommends for scripts. (PR #511 review, Codex
// P2 + Copilot, independently.)
func appIndependentRef(w gen.WorkerFields) string {
	if w.Urn != nil && *w.Urn != "" {
		return *w.Urn
	}
	return w.Id
}

// heldRefusal is the WORKER_HELD refusal, in one place because two paths
// reach it — the pre-flight that reads the hold off the worker row, and the
// server's own refusal, which is the authority and also covers the race the
// pre-flight cannot close.
//
// The remedy is the load-bearing half. #492 recorded a driver meeting a
// refusal whose only advertised way forward was `--force`; the answer then was
// "the register holds a free name", and when the register went (#500) the
// refusal was left with no remedy at all. cor:agt:020:09 supplies the real
// one: cast your own worker. Naming the holder is what makes the other route —
// ask them to release — actionable rather than theoretical.
// A remedy is only a remedy if the caller can run it as written, so BOTH
// commands here name their App scope: `release` through an App-independent
// ref, and `cast`, which has no such spelling — a worker that does not exist
// yet cannot be addressed — through an explicit --app placeholder.
func heldRefusal(name, ref, holder string, heldAt *string) error {
	who := holder
	if who == "" {
		// Reached when the server sent no heldByName and no heldBy, or when
		// the holder's user record is unreadable. The refusal still stands;
		// only the label is missing.
		who = "another person"
	}
	since := ""
	if heldAt != nil && *heldAt != "" {
		since = fmt.Sprintf(" since %s", *heldAt)
	}
	return exitcode.Newf(exitcode.Conflict,
		"the name %s is held by %s%s — a HELD name is not a TAKEN one: no session ending, idle window, expiry or reap frees it, and --force does NOT apply (cor:agt:020:09). "+
			"Cast your own worker for the role (`hadron team worker cast --app <app> --name <n> --role <role>`), or ask the holder — or an App/org admin — to run `hadron team worker release %s`",
		name, who, since, ref)
}

func newCmdSessionStart(f *cmdutil.Factory) *cobra.Command {
	var as, repo, branch, transcript, host, tool, model, teamMemory string
	var force bool
	cmd := &cobra.Command{
		Use:   "start --as <worker> [--transcript <path>] [--tool <t>] [--force]",
		Short: "Start a worker session: bind this worktree to a worker",
		Long: `Start a coding session as a worker. The session is recorded server-side
(with the provenance fields: repo, branch, host, tool, transcript path,
model) and the binding is written under this worktree's git dir so
` + "`whoami`" + ` can recover it. The session binds the WORKER (cor:agt:020:03);
the server stamps the role-agent and the worker's App itself, so every
worker session is App-bound. On success the worker's resolved boot
briefing (its prompt) is printed — adopt it.

--as takes three spellings. The worker's NAME resolves within the team App,
from -m, the persistent --app flag, or the configured App context. Its URN
(` + "`hrn:worker:<root>:<app-slug>:<slug>`" + `) and its id need no App scope at all —
and the URN is tried FIRST, before any ambient App is even consulted, so it
is the spelling that still works when the App context is wrong, stale or
missing. Prefer it in anything scripted.

ONE WORKTREE PER WORKER (#472). Do not drive two workers from one
checkout. Two agents there share an index and a working tree: whichever
commits with ` + "`git add -A`" + ` absorbs the other's in-flight edits, and
Session.branch is captured once here and never revisited, so a session
keeps reporting a branch it left hours ago. Both failures are silent, and
the one that matters is the provenance: a merged PR stops tracing back to
the work that produced it, which is the whole reason binding a worker
exists. Give each worker its own tree with
` + "`git worktree add -b <new-branch> ../<name>`" + ` (or a bare
<existing-branch> in place of -b) — the binding lives under the
worktree's own git dir, so linked worktrees are already independent.

TWO REFUSALS, AND ONLY ONE OF THEM IS FORCEABLE (cor:agt:020:09). Read
this before reaching for --force, because the flag answers exactly one of
them:

  HELD    whose name it is. A PERSON holds a name from the moment they
          bind it, and only an explicit ` + "`worker release`" + ` ends the hold —
          no session end, idle window, expiry or reap ever does. NOT
          forceable, by design: the person who would force is not the
          person who would lose, so consent cannot be given by the party
          doing the taking. The remedy is to CAST YOUR OWN WORKER for the
          role, or to ask the holder (or an App/org admin) to release it.
  TAKEN   a live worker session already exists. Forceable — because a
          hold means this is now only ever a question about your OWN
          name, so the driver deciding is the driver affected.

A worker with a LIVE session is taken: the takeover requires --force, and
the last driver and start time are shown (informed override,
cor:agt:020:03 — never silent).

Live is DERIVED, not stored (hadron-server#1114): the server computes it
at read time from when the session was last driven, so a name whose
driver stopped stops being taken on its own. Nothing ends the session to
make that happen — it stays open, so the CHANCE to write a handoff is
still there for its driver to take. There is no handoff until somebody
writes one: leaving the row open preserves the opportunity the old
reaper foreclosed, not a record that does not exist.

So an OPEN session tells you nothing about whether the name is taken.
Liveness does — and it means DRIVEN RECENTLY, not that somebody is at the
keyboard: the window is generous. ` + "`worker list`'s" + ` LAST DRIVEN
column is where to read it. --force starts
your session alongside the taken-over one; it does not end another
driver's session. When this worktree already has a binding, --force
replaces it — first ending the session that binding names (best-effort),
so the old binding never leaves a session open. That makes --force the
remedy for an ABANDONED binding only: it never separates two live agents,
it just relabels which worker the shared tree is blamed on.

Binding a worker CLAIMS its name for you. Casting one does not: a roster
staffed by a coordinator is unheld until each person binds their own, and
an APP-KEY session claims nothing at all (an App key is not a person and
holds nothing).`,
		Example: `  hadron team session start --as Iris --tool claude-code \
      --transcript ~/.claude/projects/x/transcript.jsonl`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			existing, _, err := readBinding(ctx)
			if err != nil {
				return err
			}
			if existing != nil && !force {
				return alreadyBoundError(ctx, f, existing)
			}
			client, err := f.GraphQLClient()
			if err != nil {
				return err
			}
			// --force over an existing binding: best-effort end the session it
			// names before replacing it, so the overwritten binding does not
			// leave a session open. Since hadron-server#1114 there is no
			// backstop at all — nothing expires it, ever — so this end is
			// the ONLY thing that closes it, not merely the faster of two
			// routes. Do not read it as an optimisation. Best-effort
			// because that session may already be ended — or live on another
			// server, which `session end`'s server guard would catch but a
			// takeover deliberately steamrolls.
			if existing != nil {
				// No handoff: this ends SOMEBODY ELSE'S abandoned session, and a
				// continuity record is the departing driver's account of their
				// own work. Composing one on their behalf would put words in
				// the worker's handoff sequence that nobody wrote.
				if _, endErr := gen.EndTeamSession(ctx, client, existing.SessionID, nil, nil); endErr != nil {
					fmt.Fprintf(f.IOStreams.ErrOut, "note: could not end previously bound session %s (%v) — if it is still active, end it manually\n", existing.SessionID, endErr)
				} else {
					fmt.Fprintf(f.IOStreams.ErrOut, "ended previously bound session %s (worker %s)\n", existing.SessionID, existing.WorkerName)
				}
			}
			// The App scope for resolving a worker NAME: -m names the team
			// memory (its appId IS the team App), else the ambient --app /
			// configured context. A worker ID resolves without any of them.
			appScope := ""
			var appRefArg *string
			if teamMemory != "" {
				resolved, aerr := appForTeamMemory(ctx, client, cmdutil.CanonicalMemoryRef(teamMemory))
				if aerr != nil {
					return aerr
				}
				appScope = resolved
				// Passed through to startSession too: the server verifies it
				// matches the worker's App, so a `-m` naming another team's
				// memory fails loudly instead of silently binding elsewhere.
				appRefArg = &resolved
			} else if ambient, aerr := f.App(); aerr == nil && ambient != "" {
				appScope = ambient
			}
			w, err := resolveWorker(ctx, client, appScope, as)
			if err != nil {
				return err
			}
			if w.RetiredAt != nil {
				return exitcode.Newf(exitcode.Usage,
					"worker %s retired %s — a retired worker takes no new sessions (cast a new worker for the role)", w.Name, *w.RetiredAt)
			}
			last, active, err := workerActivity(ctx, client, w.Id)
			if err != nil {
				return err
			}
			if active != nil && !force {
				// --force is the remedy for a TAKEN worker, and cor:agt:020:09
				// says TAKEN is only ever a question about your OWN name: the
				// driver deciding is the driver affected. When the name is HELD
				// by somebody else the server refuses WORKER_HELD *before* it
				// weighs liveness at all, and no --force gets past it — so
				// offering the flag here would send the driver at the one door
				// the spec says is locked. That is #487's conflation arriving as
				// advice rather than as vocabulary.
				switch holder, verdict := classifyHold(ctx, client, w); verdict {
				case holdByOther:
					return heldRefusal(w.Name, appIndependentRef(w), holder, w.HeldAt)
				case holdUnknownWhose:
					return exitcode.Newf(exitcode.Conflict,
						"worker %s is being driven by %s, and the name is held by %s — this CLI could not read your own identity to tell whether that is you. If it is, --force takes over; if it is not, nothing does: a held name is not forceable, and the remedy is to cast your own worker (cor:agt:020:09)",
						w.Name, describeSession(active), holder)
				}
				return exitcode.Newf(exitcode.Conflict,
					"worker %s is being driven by %s — its worker session is still open, which a closed chat session does not end; --force takes over",
					w.Name, describeSession(active))
			}
			if active != nil {
				fmt.Fprintf(f.IOStreams.ErrOut, "taking over worker %s from %s\n", w.Name, describeSession(active))
			} else if last != nil {
				// Informational, not a conflict: the last stint ended.
				fmt.Fprintf(f.IOStreams.ErrOut, "worker %s was last driven by %s\n", w.Name, describeSession(last))
			}
			if host == "" {
				host, _ = os.Hostname()
			}
			input := &gen.SessionInput{
				Id:             newSessionID(),
				WorkerRef:      &w.Id,
				AppRef:         appRefArg,
				Repo:           optStr(repo),
				Branch:         optStr(branch),
				Host:           optStr(host),
				Tool:           optStr(tool),
				TranscriptPath: optStr(transcript),
				LlmModel:       optStr(model),
			}
			// #940/#432: the server refuses a taken worker atomically
			// (WORKER_TAKEN) unless force rides along — the activity check
			// above is the friendly pre-flight, this is the race-safe gate.
			// Sent only on --force so the override stays explicit.
			if force {
				input.Force = &force
			}
			resp, err := gen.StartTeamSession(ctx, client, input)
			if err != nil {
				// The race the pre-flight can't close: the worker was free a
				// moment ago and another driver bound it first. Render the
				// refusal from the WORKER_TAKEN EXTENSIONS payload (#940 —
				// the documented contract; the message narration is not),
				// with the override the pre-flight refusal already offers.
				// HELD is checked first because the server raises it first
				// (and harder): a name held by another person refuses every
				// binder but its holder, force or not. Rendering it as a
				// takeover would be the exact misdirection this branch exists
				// to remove.
				if detail, held := api.WorkerHeldDetail(err); held {
					return heldRefusal(w.Name, appIndependentRef(w), detail.Holder(), optStr(detail.HeldAt))
				}
				if detail, taken := api.WorkerTakenDetail(err); taken {
					who := detail.LastDriver
					if who == "" {
						who = "an unknown driver"
					}
					seen := detail.LastSeenAt
					if seen == "" {
						seen = "unknown"
					}
					return exitcode.Newf(exitcode.Conflict,
						"worker %s is being driven by %s, last seen %s (worker session %s) — that session is still open, which closing a chat session does not do; --force takes over (informed override, cor:agt:020:03)",
						w.Name, who, seen, detail.SessionID)
				}
				return api.MapError(err)
			}
			if resp.StartSession == nil {
				return exitcode.Newf(exitcode.Error, "server returned no session")
			}
			s := resp.StartSession.TeamSessionFields
			server, _ := f.Server()
			teamMem := ""
			if teamMemory != "" {
				teamMem = cmdutil.CanonicalMemoryRef(teamMemory)
			}
			// #399: the binding records the worker's App — the worklog home.
			// The worklog surface is App-addressed, so `session log` and the
			// provenance query need no -m at all; the old "no worklog home"
			// warning is gone because the condition it warned about is gone.
			path, err := writeBinding(ctx, &binding{
				AppBound:   true, // a worker session is always App-bound (cor:agt:020:03)
				SessionID:  s.Id,
				WorkerID:   w.Id,
				WorkerName: w.Name,
				WorkerRole: strOrEmpty(w.Role),
				AgentID:    w.AgentId,
				AppID:      w.AppId,
				Server:     server,
				StartedAt:  s.StartedAt,
				TeamMemory: teamMem,
				Tool:       tool,
				Repo:       repo,
				Model:      model,
			})
			if err != nil {
				// The server session already exists; without a binding this
				// worktree cannot end it, and since hadron-server#1114
				// nothing else will — it would stay open indefinitely, with
				// its handoff unwritable. Compensating here is mandatory,
				// not tidy.
				if _, endErr := gen.EndTeamSession(ctx, client, s.Id, nil, nil); endErr != nil {
					return fmt.Errorf("%w; additionally, rolling back session %s failed (%v) — end it with `hadron team session end --session %s`", err, s.Id, endErr, s.Id)
				}
				// NOT "is not held": ending a session never clears a hold
				// (cor:agt:020:09), so the rollback frees the SESSION and
				// leaves any name this bind claimed.
				//
				// CONDITIONAL, though, and that is the second correction here:
				// only a PERSON's bind claims a name — an App key holds nothing
				// — and this error path has no cheap way to know which
				// credential it ran under. Asserting "HELD by you" would send an
				// App-key caller to release a name it never took, which for
				// somebody else's hold is a force-release with a chat post.
				// Naming the condition costs one clause and cannot misdirect
				// (PR #504 review, twice).
				// DISCLOSE, do not prescribe — the third revision of this
				// sentence, and each earlier one was confidently wrong:
				//
				//   1. "worker %s is not held"      — ending a session never
				//      clears a hold, so this stranded one silently.
				//   2. "%s is now HELD by you, run worker release" — an App key
				//      claims no hold, so that sent it to release somebody
				//      else's name (a force-release, with a chat post).
				//   3. still prescribing release — but a person who ALREADY
				//      held this name and bound a second session acquired
				//      nothing new here, and releasing would discard the hold
				//      they had all along, handing the worker's memory and
				//      history to whoever takes the name next.
				//
				// This path cannot tell which of the three it is in without
				// reads it has no business making while reporting a failure. So
				// it states what is certain — the SESSION is gone, a hold is
				// not — and leaves the remedy to a caller who knows.
				return fmt.Errorf("%w (session %s was rolled back. Ending a session never clears a name HOLD, "+
					"so if this bind claimed one it is still yours — `hadron team worker get %s` shows the "+
					"current holder, and `worker release` gives the name up if that is what you want.)",
					err, s.Id, w.Name)
			}
			result := struct {
				Session sessionDTO `json:"session"`
				// sessionStartWorker, not workerDTO: this response is built from
				// the PRE-mutation read, so the fields the bind itself changes
				// are omitted rather than reported stale. See its doc.
				Worker      sessionStartWorker `json:"worker"`
				BindingPath string             `json:"bindingPath"`
				TookOver    bool               `json:"tookOver"`
			}{sessionDTOFromFields(s, &w.Name), sessionStartWorkerDTO(w), path, active != nil}
			if err := output.Write(f.IOStreams, f.JSON, result, func(out io.Writer) error {
				if _, err := fmt.Fprintf(out, "✓ started session %s as %s%s\n  binding: %s\n", s.Id, w.Name, roleSuffix(w.Role), path); err != nil {
					return err
				}
				// The worker's portal link, at the bind (#510, beyond that
				// issue's literal list of worker get/list — say so and it comes
				// out). Two reasons it belongs here more than anywhere:
				//
				// PARITY: hadron_start_session already returns this line, so a
				// desktop-track worker is handed its link at bind and a CLI
				// one was not — the same surface, two answers.
				//
				// ADJACENCY: the boot briefing printed immediately below tells
				// this worker to sign whatever it publishes with a CLICKABLE
				// URN (hadron-server#1008). This is the one place where that
				// instruction and the string it needs are on the same screen,
				// which is what lets the briefing's "the ONE portal link you
				// may construct yourself" carve-out be retired.
				//
				// Absent stays absent, as everywhere else (cor:api:230:01).
				if w.PortalUrl != nil && *w.PortalUrl != "" {
					if _, err := fmt.Fprintf(out, "  URL: %s\n", *w.PortalUrl); err != nil {
						return err
					}
				}
				// The resolved boot briefing (template bound + override) is
				// what the driver adopts — print it where they will see it.
				if w.Prompt != nil && *w.Prompt != "" {
					if _, err := fmt.Fprintf(out, "\n%s\n", *w.Prompt); err != nil {
						return err
					}
				}
				return nil
			}); err != nil {
				return err
			}
			// #456 — the role/repo mismatch nudge, and its PLACEMENT is the
			// feature. The bind receipt has always printed the role
			// (`roleSuffix`), so the information was never missing; it is
			// missable, because the several-hundred-word boot briefing prints
			// directly after it and pushes it off screen. A warning emitted
			// before the briefing would land in exactly the spot the issue
			// exists because nobody reads, which is why this sits AFTER
			// output.Write rather than inside its callback.
			//
			// stderr, not stdout: it is a nudge about the invocation rather
			// than part of the session record, so it must not enter a --json
			// consumer's parse. It still prints under --json for the same
			// reason — a wrong binding is worth telling an agent about too, and
			// stderr is not the JSON channel.
			//
			// WARN, NEVER REFUSE (cor:agt:020 / the server's own SDL note):
			// cross-repo work is legitimate — a coordinator does it by
			// definition and sibling repos share code — so the exit code is
			// untouched, the session is already started and the binding
			// already written by the time this runs.
			warnRepoAffinity(cmd.Context(), f, client, w, repo)
			return nil
		},
	}
	cmd.Flags().StringVar(&as, "as", "", "worker to drive: its URN or id (no App scope needed), or its name within the team App (required)")
	cmd.Flags().BoolVar(&force, "force", false, "take over a worker with an active session (never a name HELD by someone else); also replaces this worktree's binding, ending its session first (best-effort)")
	cmd.Flags().StringVar(&repo, "repo", "", "repository the session works on, e.g. owner/repo")
	cmd.Flags().StringVar(&branch, "branch", "", "branch the session works on")
	cmd.Flags().StringVar(&transcript, "transcript", "", "path of the driving tool's transcript on this host")
	cmd.Flags().StringVar(&host, "host", "", "machine identifier (defaults to this hostname)")
	cmd.Flags().StringVar(&tool, "tool", "", "driving tool, e.g. claude-code or codex")
	cmd.Flags().StringVar(&model, "model", "", "LLM model driving the session")
	cmd.Flags().StringVarP(&teamMemory, "memory", "m", "", "explicit team memory (optional since #399 — the worker's App is recorded as the worklog home; -m also cross-checks the App at start)")
	_ = cmd.MarkFlagRequired("as")
	return cmd
}

func newCmdSessionWhoami(f *cmdutil.Factory) *cobra.Command {
	return &cobra.Command{
		Use:   "whoami",
		Short: "Show which worker this worktree is bound to (worker session)",
		Long: `Read the worktree's WORKER SESSION binding back — the compaction-recovery
read. Local only: it reports what ` + "`session start`" + ` recorded, without asking
the server whether the worker session is still open.

If you are here because a chat session ended and you are not sure what you
are still driving: the worker session survived it. This tells you which
worker, and ` + "`session end`" + ` is what ends it. Ending the session does NOT
release the NAME: a person who binds a worker holds its name until
` + "`worker release`" + ` (cor:agt:020:09).`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			b, path, err := readBinding(cmd.Context())
			if err != nil {
				return err
			}
			if b == nil {
				return exitcode.Newf(exitcode.NotFound, "no worker is bound to this worktree — `hadron team session start --as <name>` opens a worker session")
			}
			result := struct {
				*binding
				BindingPath string `json:"bindingPath"`
			}{b, path}
			return output.Write(f.IOStreams, f.JSON, result, func(w io.Writer) error {
				if b.WorkerID == "" {
					// A binding written by a pre-Worker CLI: the session id is
					// still good (and `session end` is the recovery), but the
					// worker fields are unknown.
					fmt.Fprintf(w, "(binding predates the Worker model — end it with `hadron team session end` and start a new worker session)\n")
				}
				fmt.Fprintf(w, "%s%s\n  worker session: %s (started %s)\n  worker: %s\n", b.WorkerName, roleSuffix(optStr(b.WorkerRole)), b.SessionID, b.StartedAt, b.WorkerID)
				if b.AppID != "" {
					fmt.Fprintf(w, "  app: %s (the worklog home — `session log` needs no -m)\n", b.AppID)
				}
				if len(b.PRNumbers) > 0 {
					prs := make([]string, len(b.PRNumbers))
					for i, n := range b.PRNumbers {
						prs[i] = fmt.Sprintf("#%d", n)
					}
					fmt.Fprintf(w, "  prs: %s\n", strings.Join(prs, " "))
				}
				return nil
			})
		},
	}
}

// logResultDTO is the stable --json shape of `session log`. Recorded says
// where the milestone durably landed: "worklog" (the object-store row — the
// real provenance record) or "session" (only the Session.prNumber
// denormalization, when no team memory is configured). The pre-worklog
// stopgap value was "local".
type logResultDTO struct {
	SessionID string `json:"sessionId"`
	Kind      string `json:"kind"`
	Ref       string `json:"ref"`
	// PRNumber is set for kind "pr" (0 otherwise) — the value denormalized
	// onto Session.prNumber.
	PRNumber int    `json:"prNumber"`
	Recorded string `json:"recorded"`
}

// noteUnreadTeamChat tells a heads-down worker what landed in the team chat
// while they were not looking (#474, @Ada's option 2).
//
// WHY HERE. The role prompts already say "read everything, not just what
// mentions you" — the rule existed and did not hold, because nothing
// interrupts a focused worker to apply it. `session log` fires at exactly the
// moments a worker is about to publish something durable, and it already talks
// to the server, so it is the one place a signal costs nothing to deliver and
// arrives when it still changes a decision. That is the same argument as
// #468/#469/#470: put it in the reader's path.
//
// STDERR, always: the --json stdout contract is untouched, and an agent piping
// a milestone write is exactly the caller most likely to have missed a ruling.
//
// BEST-EFFORT, always: a milestone is already recorded server-side by the time
// this runs. A courtesy read that fails, or a worktree with no App, must not
// turn a successful write into a failure — so every path here returns silently.
func noteUnreadTeamChat(ctx context.Context, f *cmdutil.Factory, b *binding) {
	if b == nil || b.AppID == "" {
		return // pre-#399 binding: no App to address the chat with.
	}
	client, err := f.GraphQLClient()
	if err != nil {
		return
	}
	// Never read through this binding is a LOUDER state than "nothing new", and
	// a distinct one: it is what Gil was in when a ratified commit-trailer
	// change had been in the chat four hours before he merged with the retired
	// form. Say so plainly rather than reporting a count against seq 0, which
	// would read as ordinary backlog.
	if b.ChatSeenSeq == nil {
		// Says only what the CLI KNOWS. The watermark lives in this worktree's
		// binding, so a read performed through the MCP tools — the surface this
		// team predominantly works from — never reaches it, and asserting "you
		// have not read" to a worker who just did is a FALSE nudge. Reported
		// live against this feature before it merged (team chat seq 102) by a
		// worker in exactly that mixed mode.
		//
		// The distinction is not pedantry: a nudge that is sometimes wrong
		// trains people to ignore the one that is right, which is the whole
		// value being built here.
		fmt.Fprintf(f.IOStreams.ErrOut,
			"note: this worktree has no record of reading the team chat — `hadron team chat read --since 0` "+
				"(a read made through the MCP tools is not visible here)\n")
		return
	}
	seen := *b.ChatSeenSeq
	one := 1
	resp, err := gen.TeamChatMessages(ctx, client, b.AppID, &seen, nil, &one, nil)
	if err != nil || resp.TeamChatMessages == nil || resp.TeamChatMessages.Total == 0 {
		return // caught up, or unreadable — either way, nothing useful to say.
	}
	total := resp.TeamChatMessages.Total

	// The mentions count is a SECOND query rather than a client-side filter,
	// because the server owns mention resolution (hadron-server#979: a token
	// may match several workers, and matching is not ours to reimplement).
	// Cheap: total is exact under limit 1, verified against the live server —
	// and asked only once the total says there is something to qualify, so the
	// steady state (caught up) stays one round trip.
	//
	// Two ways the answer can be wrong, and both resolve to the same thing:
	//
	//   - the query FAILED, so the count is unknown;
	//   - the query SUCCEEDED but a message arrived between the two round
	//     trips, so mentions can exceed the earlier total — "1 new message
	//     (2 mentioning you)" is not merely stale, it is impossible, and an
	//     impossible receipt is the kind readers stop believing.
	//
	// Both print the count without the clause. "none mentioning you" is the
	// phrase that decides whether the reader stops to look, so it is only ever
	// said when it is actually known (PR #493 review).
	mentions, mentionsKnown := 0, false
	if b.WorkerID != "" {
		if m, merr := gen.TeamChatMessages(ctx, client, b.AppID, &seen, &b.WorkerID, &one, nil); merr == nil && m.TeamChatMessages != nil {
			mentions, mentionsKnown = m.TeamChatMessages.Total, m.TeamChatMessages.Total <= total
		}
	}
	detail := ""
	if mentionsKnown {
		detail = " (" + pluralMentions(mentions) + ")"
	}
	fmt.Fprintf(f.IOStreams.ErrOut,
		"note: %s in the team chat since you last read%s — `hadron team chat read --since %d`\n",
		pluralMessages(total), detail, seen)
}

func pluralMessages(n int) string {
	if n == 1 {
		return "1 new message"
	}
	return fmt.Sprintf("%d new messages", n)
}

func pluralMentions(n int) string {
	switch n {
	case 0:
		return "none mentioning you"
	case 1:
		return "1 mentioning you"
	default:
		return fmt.Sprintf("%d mentioning you", n)
	}
}

func newCmdSessionLog(f *cmdutil.Factory) *cobra.Command {
	var pr, issue, commit, branch, action, detail, memory string
	cmd := &cobra.Command{
		Use:   "log (--pr | --issue | --commit | --branch) <ref> [--action <a>]",
		Short: "Record an artifact milestone for the current worker session",
		Long: `Record an external-artifact milestone for this worktree's session in the
team worklog — the collection behind the provenance query
(` + "`session list --pr <ref>`" + `: which sessions produced this PR?).

No flag is needed (#399): the binding records the bound worker's App, the
worklog is App-addressed, and the collection schema is the server's — no
` + "`team init`" + ` first (#401), no -m. --memory stays as an explicit override;
it fails SESSION_NOT_IN_APP when it names a DIFFERENT App's memory than
the bound worker's (drop -m, or pass the worker's own team memory). Only
bindings written by OLDER CLIs differ: a pre-#399 worker binding falls
back to its recorded team memory and behaves as before; a pre-Worker
binding (no App recorded at all) degrades — --pr/--branch write only the
Session denormalization, --issue/--commit refuse — with a note naming the
remedy.

Refs are normalized to one canonical string per artifact (owner/repo#371,
owner/repo@sha, owner/repo:branch) — a URL, the short form, or a bare
number/sha/branch are all accepted; a bare value is qualified by the
session's recorded --repo (or the git remote). --pr and --branch
additionally denormalize onto Session.prNumber / Session.branch (latest
wins; display convenience only). Every logged milestone — issue and
commit included — is a heartbeat feeding the server's liveness
derivation, so logging keeps the worker taken while work is in flight.

It also tells you what landed in the TEAM CHAT while you were heads-down
(#474): a stderr note counting messages since you last ran
` + "`chat read`" + `, and how many mention you. This fires here because it is
the moment before you publish something durable, which is the last point a
decision you missed can still change what you do. Best-effort and on
stderr: the milestone is already recorded by the time it runs, so it never
fails the write, and --json is untouched.

The watermark is this WORKTREE's: a read made through the MCP tools does
not reach it, so the note reports what this worktree knows rather than
what you have read. A cross-surface watermark is a server-side question.`,
		Example: `  hadron team session log --pr 371
  hadron team session log --pr acme/widgets#7 --action merged
  hadron team session log --commit 93200b2 --action pushed
  hadron team session log --branch team-chat --action pushed`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			kind, raw := "", ""
			switch {
			case pr != "":
				kind, raw = "pr", pr
			case issue != "":
				kind, raw = "issue", issue
			case commit != "":
				kind, raw = "commit", commit
			case branch != "":
				kind, raw = "branch", branch
			}
			var detailRaw json.RawMessage
			if detail != "" {
				if !json.Valid([]byte(detail)) {
					return exitcode.Newf(exitcode.Usage, "--detail must be valid JSON")
				}
				detailRaw = json.RawMessage(detail)
			}
			b, _, err := readBinding(ctx)
			if err != nil {
				return err
			}
			if b == nil {
				return exitcode.Newf(exitcode.NotFound, "no active session in this worktree — `hadron team session start --as <name>` first")
			}
			if err := checkBindingServer(f, b); err != nil {
				return err
			}
			canonical, number, err := normalizeArtifactRef(kind, raw, defaultRepo(ctx, b))
			if err != nil {
				return exitcode.New(exitcode.Usage, err)
			}
			client, err := f.GraphQLClient()
			if err != nil {
				return err
			}
			// #399: the worklog surface is App-addressed, and the binding
			// carries the bound worker's App — so no flag is needed at all.
			// Explicit -m stays as the override (resolved to ITS App, so a
			// mismatch fails honestly); a pre-#399 binding falls back to its
			// recorded team memory, and a pre-Worker binding has neither.
			appRef := ""
			switch {
			case memory != "":
				appRef, err = appForTeamMemory(ctx, client, cmdutil.CanonicalMemoryRef(memory))
			case b.AppID != "":
				appRef = b.AppID
			case b.TeamMemory != "":
				appRef, err = appForTeamMemory(ctx, client, b.TeamMemory)
			}
			if err != nil {
				return err
			}
			if appRef == "" && kind != "pr" && kind != "branch" {
				return exitcode.Newf(exitcode.Usage,
					"an %s milestone lives only in the team worklog, and this binding predates the App-recording CLI — pass -m <team-memory> here, or `hadron team session end` and start a fresh session", kind)
			}
			// EVERY milestone touches the session: prNumber for PRs, branch
			// (the name, without the repo qualifier) for branches, and the
			// EMPTY update for issue/commit — the #932-designed liveness
			// touch (any authorized update bumps updatedAt), which is one of
			// the three inputs the server derives liveness from — so a
			// driver logging only commits keeps reading as live, and the
			// worker stays taken while the work is in flight.
			var prArg *int
			var branchArg *string
			switch kind {
			case "pr":
				prArg = &number
			case "branch":
				_, branchName, _ := strings.Cut(canonical, ":")
				branchArg = &branchName
			}
			if _, err := gen.UpdateTeamSession(ctx, client, b.SessionID, prArg, branchArg); err != nil {
				return api.MapError(err)
			}
			recorded := "session"
			if appRef != "" {
				// #396: the dedicated operation owns the record — its field set, the
				// `at` stamp, and the worker derivation (the server resolves the
				// session's worker, or the attributed user's handle, rather than
				// trusting the binding's copy). The CLI used to compose all of that
				// and write it through the generic object surface, which put the
				// record shape in two places with nothing keeping them in step.
				var detailArg *json.RawMessage
				if detailRaw != nil {
					detailArg = &detailRaw
				}
				logged, rerr := gen.RecordTeamWork(
					ctx, client, appRef, b.SessionID, b.Tool, kind, canonical, action, detailArg,
				)
				if rerr != nil {
					if api.HasErrorCode(rerr, "SESSION_NOT_IN_APP") {
						// A WORKER session binds to the worker's App, so for one
						// this means -m named a different App's memory — a
						// mismatch, not a rescue candidate. A binding written by
						// a pre-Worker CLI (no workerId) may instead name a
						// session that was never App-bound at all, and nothing
						// can bind it afterwards — the old end-and-restart
						// remedy still applies there.
						if b.WorkerID == "" {
							return exitcode.Newf(exitcode.Usage,
								"session %s is not a session of this App and this binding predates the Worker model, so it may never have been App-bound — nothing can bind it afterwards; `hadron team session end`, then `session start --as <worker>`",
								b.SessionID)
						}
						return exitcode.Newf(exitcode.Usage,
							"session %s is not a session of %s's App — the -m memory names a different App than the bound worker's; drop -m (the binding's App is the worklog home) or pass the worker's own team memory",
							b.SessionID, b.WorkerName)
					}
					return api.MapError(rerr)
				}
				// Prefer the server's canonical spelling for display and local state:
				// it is the equality key the provenance query matches on, so echoing
				// our own would let the two diverge.
				if logged.RecordTeamWork.Ref != "" {
					canonical = logged.RecordTeamWork.Ref
				}
				recorded = "worklog"
			} else {
				// #414/#399: reachable only by a binding that predates the
				// App-recording CLI (no appId, no teamMemory). The note must
				// name the real cause and the real remedy — for a pre-Worker
				// binding the session may not be App-bound at all, where only
				// end-and-restart helps.
				if b.WorkerID == "" {
					fmt.Fprintf(f.IOStreams.ErrOut,
						"note: this milestone went only to the Session field — not the worklog. "+
							"This binding predates the Worker model, so its session may not be App-bound at all; if `-m <team-memory>` "+
							"fails SESSION_NOT_IN_APP, run `hadron team session end`, then `session start --as <worker>`.\n")
				} else {
					fmt.Fprintf(f.IOStreams.ErrOut,
						"note: this milestone went only to the Session field — not the worklog. "+
							"This binding predates the App-recording CLI: pass `-m <team-memory>` here, or `hadron team session end` "+
							"and start a fresh session (new bindings need no -m).\n")
				}
			}
			if kind == "pr" {
				// #499: append under the binding lock, with BOTH checks INSIDE
				// the mutation. `b` is the snapshot this command read before it
				// talked to the server; the mutation runs later, under the
				// lock, against a fresh read — so anything decided from `b`
				// alone is stale by construction.
				//
				// SESSION FIRST (PR #519 review, @codex P2). A concurrent
				// `session start` can replace the binding in that gap, and
				// appending then would file this PR under a session that never
				// logged it — contaminating the NEW session's whoami history
				// with the old one's work. The watermark mutation already
				// guarded this; the append did not.
				//
				// Then membership, also against what is on disk now: a
				// concurrent agent may have appended this very number since,
				// and a dedupe checked against `b` would let the serialized
				// write faithfully preserve a duplicate.
				//
				// The server writes already succeeded, so a failed local append
				// only degrades whoami's history — report, don't fail. A
				// binding removed by a concurrent `session end` is the ordinary
				// shape of that, not an anomaly: the milestone is recorded
				// server-side either way.
				err := updateBinding(ctx, func(cur *binding) error {
					if cur.SessionID != b.SessionID {
						return errBindingChangedSession
					}
					for _, n := range cur.PRNumbers {
						if n == number {
							return errPRAlreadyKnown
						}
					}
					cur.PRNumbers = append(cur.PRNumbers, number)
					return nil
				})
				switch {
				case err == nil, errors.Is(err, errPRAlreadyKnown):
				case errors.Is(err, errBindingChangedSession):
					fmt.Fprintf(f.IOStreams.ErrOut, "note: recorded server-side for session %s, but this worktree is now bound to a different one — not adding it to that session's local history\n", b.SessionID)
				case errors.Is(err, errNoBinding):
					fmt.Fprintf(f.IOStreams.ErrOut, "note: recorded server-side, but this worktree's binding is gone — a concurrent `session end`?\n")
				default:
					fmt.Fprintf(f.IOStreams.ErrOut, "note: recorded server-side, but updating the local binding failed: %v\n", err)
				}
			}
			// #474: say what landed in the team chat while you were heads-down.
			// AFTER the milestone is recorded — this is a courtesy, and it must
			// never sit between the caller and their write.
			noteUnreadTeamChat(ctx, f, b)
			result := logResultDTO{SessionID: b.SessionID, Kind: kind, Ref: canonical, PRNumber: number, Recorded: recorded}
			return output.Write(f.IOStreams, f.JSON, result, func(w io.Writer) error {
				_, err := fmt.Fprintf(w, "✓ logged %s %s for session %s (%s)\n", kind, canonical, b.SessionID, recorded)
				return err
			})
		},
	}
	cmd.Flags().StringVar(&pr, "pr", "", "pull-request ref: number, owner/repo#N, or URL")
	cmd.Flags().StringVar(&issue, "issue", "", "issue ref: number, owner/repo#N, or URL")
	cmd.Flags().StringVar(&commit, "commit", "", "commit ref: sha, owner/repo@sha, or URL")
	cmd.Flags().StringVar(&branch, "branch", "", "branch ref: name, owner/repo:branch, or /tree/ URL")
	cmd.Flags().StringVar(&action, "action", "worked-on", "what happened to the artifact (e.g. opened, merged, pushed)")
	cmd.Flags().StringVar(&detail, "detail", "", "optional JSON bag of display extras stored with the milestone")
	cmd.Flags().StringVarP(&memory, "memory", "m", "", "explicit team-memory override (the binding's App is the default worklog home)")
	cmd.MarkFlagsMutuallyExclusive("pr", "issue", "commit", "branch")
	cmd.MarkFlagsOneRequired("pr", "issue", "commit", "branch")
	return cmd
}

// appForTeamMemory resolves the team App from the team MEMORY the caller
// named. Deliberately NOT resolveTeamApp: that prefers --app / the configured
// App context, so an explicit `-m <memory>` (on `session log` or the provenance
// query) could act against a different App than the memory it names — and with
// the session-to-App gate on the worklog write, fail confusingly as
// SESSION_NOT_IN_APP rather than honestly as a mismatch. The team memory IS the
// App's shared app-class memory, so Memory.appId is the answer.
func appForTeamMemory(ctx context.Context, client graphql.Client, teamMem string) (string, error) {
	resp, err := gen.TeamMemoryApp(ctx, client, teamMem)
	if err != nil {
		return "", api.MapError(err)
	}
	if resp.Memory == nil || resp.Memory.AppId == nil || *resp.Memory.AppId == "" {
		return "", exitcode.Newf(exitcode.Usage,
			"%s is not an App memory — the worklog lives in a team App's shared memory", teamMem)
	}
	return *resp.Memory.AppId, nil
}

// defaultRepo qualifies bare artifact numbers/shas: the binding's recorded
// --repo first (nil binding tolerated — the provenance query runs from
// unbound checkouts too), else the worktree's github origin remote. The env
// override (tests, exotic setups) suppresses the git call the same way
// gitDir's does.
func defaultRepo(ctx context.Context, b *binding) string {
	if b != nil && b.Repo != "" {
		return b.Repo
	}
	if os.Getenv(GitDirEnv) != "" {
		return ""
	}
	out, err := exec.CommandContext(ctx, "git", "config", "--get", "remote.origin.url").Output()
	if err != nil {
		return ""
	}
	return repoFromRemote(string(out))
}

// checkBindingServer refuses a session mutation when the binding records a
// different server than this invocation targets: the mutation would miss the
// real session (or hit an unrelated one) while the real session keeps
// holding its worker.
func checkBindingServer(f *cmdutil.Factory, b *binding) error {
	if bindingServerMatches(f, b) {
		return nil
	}
	server, _ := f.Server()
	return exitcode.Newf(exitcode.Usage,
		"this worktree's session was started against %s, but the current server is %s — rerun with `--server %s`",
		b.Server, server, b.Server)
}

// bindingServerMatches is the same comparison as a predicate, for the caller
// that must not REFUSE a cross-server invocation but must not act on it either
// (PR #493 review). `chat read --server <other>` against a second deployment is
// a legitimate read; writing that deployment's seq into this binding is not.
// App ids are not globally unique across deployments — a clone or a restore
// carries them over — so the id comparison alone cannot catch this.
func bindingServerMatches(f *cmdutil.Factory, b *binding) bool {
	if b == nil {
		return false
	}
	server, _ := f.Server()
	return b.Server == "" || server == "" || b.Server == server
}

// endResultDTO is the stable --json shape of `session end`.
type endResultDTO struct {
	SessionID  string `json:"sessionId"`
	WorkerName string `json:"workerName"`
	EndedAt    string `json:"endedAt"`
}

func newCmdSessionEnd(f *cmdutil.Factory) *cobra.Command {
	var summary, sessionID, handoff, handoffFile string
	cmd := &cobra.Command{
		Use:   "end [--handoff <text> | --handoff-file <path>] [--summary <text>] [--session <id>]",
		Short: "End this worktree's worker session — the session, not the name",
		Long: `End the WORKER SESSION this worktree is bound to and clear the binding.
This is the only thing that ends the session — unless another active worker
session still has the worker (e.g. after a --force takeover; check
` + "`session list --active`" + `).

IT DOES NOT RELEASE THE NAME. A person who binds a worker claims its name,
and no session end, idle window, expiry or reap ever clears that hold
(cor:agt:020:09) — only ` + "`worker release`" + ` does. So ending here frees the
worker to be BOUND by you again; it does not hand the name to anyone else.

Closing your CHAT SESSION does not do this. Archive the Desktop window or
quit the Claude Code session and the worker session stays open, holding the
worker until you end it here — since hadron-server#1114 nothing else does.
So end it deliberately when you stop working, not when you close the window.

--handoff IS WHAT THE NEXT DRIVER READS (hadron-server#1029). Prose about
what landed, what is open, what is blocked, and what they should not
repeat — the server files it in the worker's own memory, and the next bind
hands it over. Because handoffs follow the NAME rather than the holder
(cor:agt:020:09), it may well be a colleague who receives it.

Written BEFORE the session ends, and a failed write REFUSES the end
(HANDOFF_WRITE_FAILED, exit 1) rather than ending anyway: a still-bound
worker is recoverable — retry, or end without a handoff deliberately —
while an ended session whose handoff evaporated is not, because the
context that composed it is gone. A session with no worker has no sequence
to write to, so passing --handoff there is refused rather than dropped.
That ordering is a platform guarantee, not this client's choice
(cor:agt:020:10).

RETRY IS SAFE, and cannot double-write. One stint records one handoff,
and that is keyed on the STINT rather than on whether a write already
happened — so a crash between the handoff and the close does not leave a
second attempt appending a duplicate.

IF THE END FAILS WHILE CARRYING A HANDOFF, THIS SAVES THE PROSE. It is
written to a temp file and the path printed with the retry that recovers
it — a ready-to-run command line on POSIX, where single-quoting makes
every argument literal, and the same arguments listed as raw values on
Windows, where no quoting survives both cmd.exe and PowerShell.
The guarantee above protects the SESSION; it cannot protect text that
only ever existed in this process, which is the case whenever the handoff
came from stdin and the pipe has already been drained.

--handoff-file reads it from a file, and ` + "`--handoff -`" + ` from stdin. A
handoff is prose of real length, and putting a paragraph through shell
quoting is its own hazard.

--SUMMARY IS A DIFFERENT FIELD AND THE NEXT DRIVER NEVER SEES IT. It is a
short label on the session row; nothing reads it back. That is not a
quirk of today's build: cor:agt:020:10 makes --handoff the ONE field
carrying continuity, and any other free-text field on a session
display-only unless the corpus says otherwise — so writing continuity
into --summary is a contract violation rather than a naming preference,
and it fails silently, producing no error and no record. Both flags are
kept deliberately; if you are writing one thing for whoever comes next,
write --handoff.

--session ends an explicit worker session id instead — the recovery path
when the binding is gone or unusable (a lost worktree, a failed binding
write, a binding written by a pre-Worker CLI) but the server-side worker
session is still open.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			// THE HANDOFF IS TAKEN FIRST — before the binding preflight, not
			// merely before the client (PR #528 review, Codex, third round).
			//
			// The previous revision put this below `readBinding` and
			// `checkBindingServer`, and those return for the ordinary reasons:
			// no binding in this worktree, an unreadable one, a binding whose
			// server disagrees with --server. On a real pipe each of those
			// closes the consumer end with the prose still in it.
			//
			// So the invariant written one commit ago — "if you gave us a
			// handoff and we did not record it, we saved it and said where" —
			// was already untrue for three returns sitting above it. Taking the
			// text first is what makes it true, and it costs nothing: reading
			// stdin cannot fail in a way the binding preflight would have
			// prevented.
			text, src, err := resolveHandoff(cmd, handoff, handoffFile, f.IOStreams.In)
			if err != nil {
				return err // nothing was successfully taken; there is nothing to rescue
			}
			b, _, err := readBinding(ctx)
			if err != nil && sessionID == "" {
				return rescueHandoff(f, handoffRescue{text: text, src: src, sessionID: sessionID, summary: summary, answered: true}, err)
			}
			id := sessionID
			workerName := ""
			if id == "" {
				if b == nil {
					return rescueHandoff(f, handoffRescue{text: text, src: src, summary: summary, answered: true},
						exitcode.Newf(exitcode.NotFound,
							"no active session in this worktree — pass --session <id> to end one without a binding"))
				}
				id = b.SessionID
				workerName = b.WorkerName
				if err := checkBindingServer(f, b); err != nil {
					// The BINDING's server, not this invocation's: the retry
					// carries an explicit --session, which bypasses this very
					// check, so printing the rejected server would hand the
					// caller a command that does the thing just refused.
					return rescueHandoff(f, handoffRescue{text: text, src: src, sessionID: id, server: b.Server, summary: summary, answered: true}, err)
				}
			}
			// Why the ordering above is what it is (PR #528, Codex P1 + P2).
			// An earlier revision authenticated first, so a signed-out caller
			// was refused before their pipe was read — "never take custody of
			// prose we cannot deliver". Two objections killed it:
			//
			//  1. NOT READING IS NOT THE SAME AS NOT LOSING. On a real pipe,
			//     returning without reading closes the consumer end: buffered
			//     prose is discarded and the producer can take SIGPIPE. "The
			//     caller still has it" was an assumption about the producer,
			//     not a property of the pipe — and `echo … |` has already
			//     written and exited.
			//  2. IT MOVED THE EXIT CODES. Validation lives in resolveHandoff,
			//     so authenticating first meant an explicit empty --handoff or
			//     an unreadable --handoff-file reported AuthRequired instead of
			//     the documented Usage (exit 2) — a contract agents branch on,
			//     and invisible to a suite whose factory is always signed in.
			//
			// INVARIANT: past resolveHandoff, every error return carries the
			// text through rescueHandoff. A bare `return err` below silently
			// re-opens the hole this whole command exists to close.
			client, err := f.GraphQLClient()
			if err != nil {
				// The BINDING's server, when this session came from one (PR #528
				// review, Codex). If the config cannot be loaded and no --server
				// was passed, f.Server() fails the same way inside retryLine and
				// the printed command would omit --server entirely — repeating
				// the configuration failure, while the binding held the
				// deployment to use all along.
				bound := ""
				if b != nil && b.SessionID == id {
					bound = b.Server
				}
				return rescueHandoff(f, handoffRescue{
					text: text, src: src, sessionID: id, server: bound, summary: summary, answered: true,
				}, err)
			}
			resp, err := gen.EndTeamSession(ctx, client, id, optStr(summary), optStr(text))
			if err != nil {
				// The handoff does not evaporate with the call that carried it.
				// Only THIS path can be ambiguous: everything above either never
				// reached the server or was refused locally.
				//
				// The definite wording is earned by a SPEC GUARANTEE, not
				// inferred from the response shape (PR #528 review, Codex,
				// twice). `cor:agt:020:10` says a failed handoff write refuses
				// the end, so HANDOFF_WRITE_FAILED proves nothing committed.
				// Nothing else does: GraphQL returns data AND errors when a
				// nested resolver fails after the mutation ran, and null-
				// bubbling from a non-null child nulls the payload even though
				// the write happened — so neither an error's presence nor a
				// payload's absence is proof.
				return rescueHandoff(f, handoffRescue{
					text: text, src: src, sessionID: id, summary: summary,
					answered: api.EndRefusedBeforeCommit(err),
				}, api.MapError(err))
			}
			// Clear the binding when it names the session we just ended.
			if b != nil && b.SessionID == id {
				if err := clearBinding(ctx); err != nil {
					return err
				}
			}
			var endedAt string
			if resp.EndSession != nil && resp.EndSession.EndedAt != nil {
				endedAt = *resp.EndSession.EndedAt
			}
			result := endResultDTO{SessionID: id, WorkerName: workerName, EndedAt: endedAt}
			return output.Write(f.IOStreams, f.JSON, result, func(w io.Writer) error {
				who := ""
				if workerName != "" {
					who = " (worker " + workerName + ")"
				}
				_, err := fmt.Fprintf(w, "✓ ended session %s%s\n", id, who)
				return err
			})
		},
	}
	cmd.Flags().StringVar(&handoff, "handoff", "", "continuity record for the next driver — what landed, what is open, what is blocked (a lone - reads stdin)")
	cmd.Flags().StringVar(&handoffFile, "handoff-file", "", "read the handoff from a file (multi-line safe)")
	cmd.Flags().StringVar(&summary, "summary", "", "short session label — DISPLAY ONLY, the next driver never sees it (use --handoff for that)")
	cmd.MarkFlagsMutuallyExclusive("handoff", "handoff-file")
	cmd.Flags().StringVar(&sessionID, "session", "", "end this session id instead of the worktree's bound one (recovery)")
	return cmd
}

func newCmdSessionList(f *cmdutil.Factory) *cobra.Command {
	var as, repo, pr, issue, commit, branch, memory string
	var active bool
	var limit, offset int
	cmd := &cobra.Command{
		Use:     "list [--active] [--as <worker>] [--repo <r>] | (--pr | --issue | --commit | --branch) <ref> [-m <team-memory>]",
		Aliases: []string{"ls"},
		Short:   "List worker sessions — open sessions and provenance",
		Long: `List sessions, newest first, with each session's worker joined in.
--active narrows to sessions that never ENDED, which is not the same as
team presence: since hadron-server#1114 nothing ends a developer session
but an explicit ` + "`session end`" + `, so an abandoned one stays listed
indefinitely. For "was this name driven recently, and does the server still
count it as taken", read ` + "`worker list`'s" + ` LAST DRIVEN. --as narrows to one
worker's sessions (server-side, via the worker binding on the session).
--active narrows client-side over the full list; plain listings page
server-side.

--pr <ref> is THE provenance query: which sessions produced this PR? It
looks the normalized ref up in the team worklog (` + "`session log`" + ` writes it)
and resolves each recorded session — several rows per PR are expected and
desirable (a PR spanning three sessions yields three transcripts).
--issue/--commit/--branch ask the same question of the other artifact
kinds. The worklog's App comes from the worktree binding, or from --app /
-m when running unbound (#399). A recorded session that is no longer
visible to you still lists (id only), rather than being silently
dropped.`,
		Example: `  hadron team session list --active
  hadron team session list --pr 371 -m acme.com:eng-team
  hadron team session list --commit 93200b2`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			if limit < 0 || offset < 0 {
				return exitcode.Newf(exitcode.Usage, "--limit and --offset must be non-negative")
			}
			kind, ref := "", ""
			switch {
			case pr != "":
				kind, ref = "pr", pr
			case issue != "":
				kind, ref = "issue", issue
			case commit != "":
				kind, ref = "commit", commit
			case branch != "":
				kind, ref = "branch", branch
			}
			if ref != "" && (active || as != "" || repo != "" ||
				cmd.Flags().Changed("limit") || cmd.Flags().Changed("offset")) {
				return exitcode.Newf(exitcode.Usage, "--%s is the worklog provenance query — it does not combine with --active/--as/--repo/--limit/--offset", kind)
			}
			client, err := f.GraphQLClient()
			if err != nil {
				return err
			}
			if ref != "" {
				return runProvenanceQuery(cmd, f, client, kind, ref, memory)
			}
			var asWorkerRef *string
			if as != "" {
				// The App scope for a worker NAME comes from --app / the App
				// context / the binding, like the other worker-taking commands.
				b, bErr := readBindingOrNilWithApp(ctx, f)
				if bErr != nil {
					b = nil
				}
				appScope, aErr := resolveTeamApp(ctx, f, b)
				if aErr != nil {
					appScope = ""
				}
				w, wErr := resolveWorker(ctx, client, appScope, as)
				if wErr != nil {
					return wErr
				}
				asWorkerRef = &w.Id
			}

			sessions := []sessionDTO{}
			if active {
				// The active filter is client-side and needs the whole list;
				// --limit/--offset then slice the filtered result.
				err = scanSessions(ctx, client, optStr(repo), asWorkerRef, func(s gen.TeamSessionFields) bool {
					if s.EndedAt != nil {
						return true
					}
					sessions = append(sessions, sessionDTOFromFields(s, nil))
					return true
				})
				if err != nil {
					return err
				}
				if offset > 0 {
					if offset >= len(sessions) {
						sessions = []sessionDTO{}
					} else {
						sessions = sessions[offset:]
					}
				}
				if cmd.Flags().Changed("limit") && limit < len(sessions) {
					sessions = sessions[:limit]
				}
			} else {
				var lim, off *int
				if cmd.Flags().Changed("limit") {
					lim = &limit
				}
				if cmd.Flags().Changed("offset") {
					off = &offset
				}
				resp, err := gen.TeamSessions(ctx, client, optStr(repo), asWorkerRef, lim, off)
				if err != nil {
					return api.MapError(err)
				}
				for _, s := range resp.Sessions {
					if s == nil {
						continue
					}
					sessions = append(sessions, sessionDTOFromFields(s.TeamSessionFields, nil))
				}
			}
			return output.Write(f.IOStreams, f.JSON, sessions, func(w io.Writer) error {
				// WORKER first, ROLE beside it, SESSION last (#486). The id
				// anchored the reader's eye while being the one value a human
				// cannot act on; it stays because `session end --session <id>`
				// and the worklog join need it, and stays FULL because a
				// truncated id breaks copy-paste into that flag — shortening is
				// only safe once --session learns prefix resolution, which is a
				// deliberate change rather than an assumption.
				t := output.NewTable(w, "WORKER", "ROLE", "USER", "REPO", "PR", "STARTED", "ENDED", "TOOL", "SESSION")
				for _, s := range sessions {
					pr := "—"
					if s.PRNumber != nil {
						pr = fmt.Sprintf("#%d", *s.PRNumber)
					}
					// A session whose worker is unreadable (or that predates
					// worker binding) shows the raw ids rather than nothing.
					worker := s.WorkerName
					if worker == nil {
						worker = s.WorkerID
					}
					if worker == nil {
						worker = s.AgentID
					}
					t.Row(dash(worker), dash(s.WorkerRole), dash(s.UserID), dash(s.Repo), pr, s.StartedAt, dash(s.EndedAt), dash(s.Tool), s.ID)
				}
				return t.Flush()
			})
		},
	}
	// NO BACKTICKS: cobra reads a backticked word in a usage string as the
	// flag's PLACEHOLDER name, so "see `worker list`" rendered the flag as
	// "--active worker list" (review:backticks-in-flag-usage-become-the-placeholder,
	// caught by TestFlagUsagePlaceholdersAreDeliberate).
	cmd.Flags().BoolVar(&active, "active", false, "only sessions that never ENDED — not presence; see worker list's LAST DRIVEN")
	cmd.Flags().StringVar(&as, "as", "", "only one worker's sessions (name within the team App, or worker id)")
	cmd.Flags().StringVar(&repo, "repo", "", "filter by repository")
	cmd.Flags().StringVar(&pr, "pr", "", "provenance query: sessions behind this PR (number, owner/repo#N, or URL)")
	cmd.Flags().StringVar(&issue, "issue", "", "provenance query: sessions behind this issue")
	cmd.Flags().StringVar(&commit, "commit", "", "provenance query: sessions behind this commit")
	cmd.Flags().StringVar(&branch, "branch", "", "provenance query: sessions behind this branch")
	cmd.Flags().StringVarP(&memory, "memory", "m", "", "explicit team-memory override for the provenance query (binding/--app are the defaults)")
	cmd.MarkFlagsMutuallyExclusive("pr", "issue", "commit", "branch")
	cmd.Flags().IntVar(&limit, "limit", 0, "max results (server default when unset)")
	cmd.Flags().IntVar(&offset, "offset", 0, "results to skip")
	return cmd
}

// runProvenanceQuery answers "which sessions produced this artifact":
// normalize the ref, equality-match (ref, kind) in the worklog, resolve each
// recorded session. The worklog is the N:M join (D13/D14) — the denormalized
// Session columns are never consulted. Worker names come from the worklog
// rows themselves (TeamWorkItem.workerName, denormalized at write — #974),
// so no roster read is needed.
func runProvenanceQuery(cmd *cobra.Command, f *cmdutil.Factory, client graphql.Client, kind, ref, memory string) error {
	ctx := cmd.Context()
	// The binding supplies defaults (the worker's App, repo for bare numbers)
	// but the query must also run from an unbound checkout with -m or --app.
	b, _, err := readBinding(ctx)
	if err != nil {
		b = nil
	}
	// #399: the worklog read addresses the APP, and the binding records the
	// bound worker's App — so a bound worktree needs no flag. Precedence is
	// specificity: an explicit -m (resolved to ITS App — deliberately not
	// resolveTeamApp, which would let an ambient App context override the
	// memory the caller named, the Codex P1 on PR #409), then the explicit
	// --app flag, then the binding (appId, or a pre-#399 binding's team
	// memory), then the ambient App context.
	appRef := ""
	switch {
	case memory != "":
		appRef, err = appForTeamMemory(ctx, client, cmdutil.CanonicalMemoryRef(memory))
	case f.AppFlag != "":
		appRef = f.AppFlag
	case b != nil && b.AppID != "":
		appRef = b.AppID
	case b != nil && b.TeamMemory != "":
		appRef, err = appForTeamMemory(ctx, client, b.TeamMemory)
	default:
		if ambient, aerr := f.App(); aerr == nil && ambient != "" {
			appRef = ambient
		}
	}
	if err != nil {
		return err
	}
	if appRef == "" {
		return exitcode.Newf(exitcode.Usage, "the provenance query reads a team App's worklog — pass --app <ref> or -m <team-memory> (or run it from a bound worktree)")
	}
	canonical, _, err := normalizeArtifactRef(kind, ref, defaultRepo(ctx, b))
	if err != nil {
		return exitcode.New(exitcode.Usage, err)
	}
	// #396: the dedicated provenance read replaces a generic findObjects loop
	// over the `worklog` collection. kind stays part of the match — PRs and
	// issues share GitHub's number space, so ref alone would mix an issue's
	// sessions into a PR's provenance — but the server now normalizes the ref
	// filter to the same canonical key it stored, so the client no longer has
	// to agree with it about spelling.
	type workerRef struct {
		id   *string
		name *string
	}
	sessionIDs := []string{}
	workerBySession := map[string]workerRef{}
	seen := map[string]bool{}
	pageSize := sessionPageSize
	for offset := 0; ; {
		off := offset
		resp, werr := gen.TeamWorkItems(
			ctx, client, appRef, nil, &canonical, &kind, nil, &pageSize, &off,
		)
		if werr != nil {
			return api.MapError(werr)
		}
		items := resp.TeamWorkItems.Items
		for _, it := range items {
			if it.SessionId != "" && !seen[it.SessionId] {
				seen[it.SessionId] = true
				sessionIDs = append(sessionIDs, it.SessionId)
				ref := workerRef{id: it.WorkerId}
				if it.WorkerName != "" {
					name := it.WorkerName
					ref.name = &name
				}
				workerBySession[it.SessionId] = ref
			}
		}
		// Page to exhaustion — --pr promises the COMPLETE provenance set, and
		// an unbounded read is one default page (the issue-#23 rule).
		offset += len(items)
		if len(items) < pageSize {
			break
		}
	}
	sessions := []sessionDTO{}
	for _, id := range sessionIDs {
		resp, err := gen.GetTeamSession(ctx, client, id)
		if err != nil {
			return api.MapError(err)
		}
		if resp.Session == nil {
			// The worklog names it but this principal can't read it — surface
			// the id rather than silently dropping the row (the nodes-list
			// visibility-gap rule applies to fan-outs like this one). The
			// worklog row still supplies the worker id + name, so the stub
			// keeps the actionable ref alongside the label.
			fmt.Fprintf(f.IOStreams.ErrOut, "note: session %s is recorded in the worklog but not visible to you\n", id)
			sessions = append(sessions, sessionDTO{ID: id, WorkerID: workerBySession[id].id, WorkerName: workerBySession[id].name})
			continue
		}
		sessions = append(sessions, sessionDTOFromFields(resp.Session.TeamSessionFields, workerBySession[id].name))
	}
	return output.Write(f.IOStreams, f.JSON, sessions, func(w io.Writer) error {
		// Same treatment as the listing table above (#486).
		t := output.NewTable(w, "WORKER", "ROLE", "USER", "TOOL", "HOST", "MODEL", "STARTED", "TRANSCRIPT", "SESSION")
		for _, s := range sessions {
			t.Row(dash(s.WorkerName), dash(s.WorkerRole), dash(s.UserID), dash(s.Tool), dash(s.Host), dash(s.LLMModel), s.StartedAt, dash(s.TranscriptPath), s.ID)
		}
		return t.Flush()
	})
}

func strOrEmpty(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

// sessionStartWorker is the worker as `session start` reports it: everything
// workerDTO carries EXCEPT the four fields the bind itself invalidates.
//
// This whole response is built from the PRE-MUTATION read, so every
// working-state field on it describes the moment BEFORE the bind:
//
//   - startSession CLAIMS the hold for a person (cor:agt:020:09 — "casting does
//     not hold; binding claims"), so heldByUserId/heldAt would tell a caller
//     `heldByUserId: null` immediately after the bind that set it (PR #504).
//   - startSession OPENS a session, so hasLiveSession would report `false`
//     immediately after the call that made it true, and lastActiveAt would
//     predate the stint being reported (PR #523 review, Codex P1). That one is
//     worse than stale: it is the NEGATION of the operation that just
//     succeeded, in the same document that reports its success.
//
// Omitted rather than re-read: `session start` reports the session it just
// created, not current staffing, and a round trip on the hot path to decorate
// fields nobody asked for is the wrong trade. `worker get` is the staffing read.
//
// Omitted rather than NULLED, and the four need different machinery to achieve
// that. The hold pair carries `omitempty` on workerDTO, so nil omits. The
// activity pair deliberately does NOT — a null there is load-bearing on
// `worker list`, where it is the signal that the read was gated — so nilling
// them here would publish `hasLiveSession: null`, i.e. "you are not permitted
// to know", which is a false account of why the value is missing. They are
// shadowed with omitempty instead: the outer fields sit at depth 0 and win over
// the embedded ones, so a nil drops the key entirely.
type sessionStartWorker struct {
	workerDTO
	// Shadowing workerDTO's fields of the same name. Always nil in practice;
	// present only so encoding/json takes THESE tags rather than the embedded
	// ones. If either is ever populated here, it must come from a post-bind
	// read, not from `w`.
	HasLiveSession *bool   `json:"hasLiveSession,omitempty"`
	LastActiveAt   *string `json:"lastActiveAt,omitempty"`
}

func sessionStartWorkerDTO(w gen.WorkerFields) sessionStartWorker {
	dto := workerDTOFromFields(w)
	dto.HeldByUserID, dto.HeldAt = nil, nil
	dto.HasLiveSession, dto.LastActiveAt = nil, nil
	return sessionStartWorker{workerDTO: dto}
}

// resolveHandoff reads the continuity record from --handoff, --handoff-file, or
// stdin (`--handoff -`), returning "" when none was asked for.
//
// It deliberately does NOT reuse chat.ResolveBody, which the flag shape is
// modelled on: a chat body is REQUIRED and an empty one is an error, while a
// handoff is optional — ending a session without one is a normal thing to do,
// and the server treats an omitted handoff as "no continuity record" rather
// than a failure.
//
// What it keeps from that precedent is refusing an EXPLICIT empty: someone who
// typed --handoff "" or pointed at an empty file meant to write something, and
// silently ending with no record is the outcome #1029 exists to prevent.
// rescueHandoff spills a handoff the server did not accept, and tells the
// caller where it went and how to retry with it.
//
// `cor:agt:020:10` refuses the END when the handoff write fails, precisely so a
// transient failure cannot destroy "prose that exists nowhere else … at the
// moment its author has stopped working and cannot be asked to retype it".
// That protects the SESSION. It does not protect the PROSE, and this client had
// a path where the client itself was the destroyer: `--handoff -` drains stdin
// into memory, so a refused end discarded the only copy and the pipe was
// already consumed. The spec's own rationale, one layer below the layer it
// governs.
//
// Spills on ANY end failure, not only HANDOFF_WRITE_FAILED. A transport error
// leaves us unable to tell whether the handoff was written, and a spare file is
// cheap while retyping a stint's closing prose is not. Retrying against it is
// safe either way: the sequence is append-only per STINT, not per write, so a
// handoff already recorded is not duplicated by a second attempt
// (`cor:agt:020:10`).
//
// BEST EFFORT, and never masks the real error. A failure to spill is reported
// and the original error still returns — swapping the server's refusal for a
// local file-write error would hide the thing the caller has to act on.
// handoffRescue is what a failed end was carrying, and what a retry needs to
// reproduce the SAME invocation.
//
// A struct rather than five positional arguments: the two booleans-and-strings
// nearest each other (server, summary) are both "carry this across to the
// retry", and getting them the wrong way round would compile.
type handoffRescue struct {
	text string
	// src is where the prose came from, and it decides what the last-resort
	// branch may PRINT. Only a consumed stdin has no other copy.
	src       handoffSource
	sessionID string
	// server OVERRIDES the invocation's own --server in the printed retry.
	// Exactly one caller needs that and the reason is sharp (PR #528 review,
	// Codex): on a binding/server MISMATCH the invocation resolved the WRONG
	// deployment — that is what was refused — so composing the retry from it
	// would print a command carrying the rejected server plus an explicit
	// --session, which bypasses the very check that refused. Pasting it would
	// send the handoff to the wrong deployment, and to an unrelated session
	// there if ids overlap.
	server string
	// summary is carried so a retry reproduces the whole invocation. Dropping
	// it would let a pasted command succeed while silently leaving a label the
	// caller explicitly asked for unset (PR #528 review, Codex).
	summary string
	// answered: the server responded AND committed nothing, so the handoff is
	// definitely not recorded. False for a transport failure (the request may
	// have been applied and only the reply lost) and for a PARTIAL SUCCESS —
	// GraphQL returns data plus errors when a nested resolver fails after the
	// mutation ran, and this operation selects a nullable nested worker, so an
	// answer alone does not mean a refusal. See rescueHandoff.
	answered bool
}

// rescueHandoff spills a handoff the server did not record, and tells the
// caller where it went and how to retry with it.
//
// `cor:agt:020:10` refuses the END when the handoff write fails, precisely so a
// transient failure cannot destroy "prose that exists nowhere else … at the
// moment its author has stopped working and cannot be asked to retype it".
// That protects the SESSION. It does not protect the PROSE, and this client had
// a path where the client itself was the destroyer: `--handoff -` drains stdin
// into memory, so a refused end discarded the only copy and the pipe was
// already consumed. The spec's own rationale, one layer below the layer it
// governs.
//
// WHAT IT CLAIMS DEPENDS ON WHAT IT KNOWS (PR #528 review, Codex). A refusal
// the server ANSWERED means nothing was committed, and saying so is a fact. A
// transport failure means the end may have succeeded with only the reply lost,
// and asserting "NOT recorded" there is a claim about server state the client
// does not have — the more so because a later retry failing would then look
// like confirmation of data loss. The REMEDY is identical either way, because
// one stint records one handoff and a duplicate cannot be appended, so only the
// wording changes.
//
// BEST EFFORT, and never masks the real error. A failure to spill is reported
// and the original error still returns — swapping the server's refusal for a
// local file-write error would hide the thing the caller has to act on.
func rescueHandoff(f *cmdutil.Factory, r handoffRescue, cause error) error {
	if strings.TrimSpace(r.text) == "" {
		return cause // nothing was at risk
	}
	outcome := "may not have been recorded"
	if r.answered {
		outcome = "was NOT recorded"
	}
	path, werr := spillHandoff(r.text)

	// JSON MODE PUTS THIS INSIDE THE ERROR, NOT BESIDE IT (PR #528 review,
	// Codex P1). Under --json the error envelope `{"error":{…}}` is written to
	// STDERR, so a plain-text notice on the same stream leaves stderr
	// unparseable — and it would do so on precisely the recovery path an agent
	// most needs to read. The human branch prints separately because there
	// stderr carries no document.
	//
	// Folded into the message rather than added as a new key: the `--json`
	// error shape is `{code, message}` for every command, and widening it from
	// one command's rescue path would be a contract change made in the wrong
	// place.
	if f.JSON {
		if werr != nil {
			return exitcode.New(exitcode.FromError(cause),
				fmt.Errorf("%w — the handoff %s and could not be saved locally either (%v). %s",
					cause, outcome, werr, strings.TrimSpace(lastResort(r))))
		}
		return exitcode.New(exitcode.FromError(cause),
			fmt.Errorf("%w — the handoff %s and was saved to %s; %s (safe to retry: one stint "+
				"records one handoff, so this cannot double-write)",
				cause, outcome, path, retryLine(f, r, path)))
	}

	if werr != nil {
		fmt.Fprintf(f.IOStreams.ErrOut,
			"! the handoff %s, and could not be saved locally either (%v).\n%s",
			outcome, werr, lastResort(r))
		return cause
	}
	fmt.Fprintf(f.IOStreams.ErrOut,
		"! the handoff %s. Saved it to %s\n"+
			"  %s\n"+
			"  (safe to retry — one stint records one handoff, so this cannot double-write.)\n",
		outcome, path, retryLine(f, r, path))
	return cause
}

// lastResort says how to recover the prose when even the spill failed.
//
// PRINTING IT IS THE LAST OPTION, not the first (PR #528 review, Codex). A
// handoff is a stint's working notes — what is blocked, what is half-done, which
// customer — and stderr is retained in CI logs and agent transcripts. Dumping it
// there when a perfectly good copy already exists is needless exposure of
// exactly the content this team is careful about elsewhere.
//
// So it is printed only when the prose genuinely has nowhere else to be: a
// CONSUMED STDIN. A --handoff-file still has its file (checked, not assumed —
// the spill may have failed because the disk filled, which could equally have
// truncated something else), and an inline --handoff was typed as an argument,
// so the caller's shell history or the agent's own context still holds it.
func lastResort(r handoffRescue) string {
	if r.src.path != "" {
		if _, err := os.Stat(r.src.path); err == nil {
			return "  Your handoff is unchanged at " + r.src.path + " — retry with --handoff-file.\n"
		}
		// The source is GONE, so the in-memory copy is now the only one. Checked
		// rather than assumed for exactly this reason: the spill may have failed
		// because the disk filled or the volume went away, and either could have
		// taken the source with it. Pointing at a file that is not there would
		// be the most confident possible way to lose the prose.
		return printableHandoff(r.text)
	}
	if !r.src.fromStdin {
		// Passed as an argument: the caller's shell history or the calling
		// process still has it, so print the remedy rather than the content.
		return "  Re-run with --handoff-file, or pass the same --handoff text again.\n"
	}
	// Consumed from a pipe and never displayed: there is no scrollback holding
	// it and it is gone when this process exits.
	return printableHandoff(r.text)
}

// printableHandoff renders the prose itself, delimited so multi-line text is
// separable from the diagnostics around it by eye and by script.
func printableHandoff(text string) string {
	return "  It is printed below because there is nowhere else it survives — copy it now.\n" +
		"----- handoff begins -----\n" + text + "\n----- handoff ends -----\n"
}

// retryLine renders the recovery command, carrying enough of the ORIGINAL
// invocation that running it targets the same thing and asks for the same
// outcome.
//
// TWO SHAPES, because "ready-to-run" is only a promise that can be kept on
// POSIX (PR #528 review, Codex). There, single-quoting makes every argument
// literal and the line is pasteable as printed. On Windows the arguments are
// listed as DATA instead: no quoting a Go process can apply survives both
// cmd.exe's %VAR% and PowerShell's $var expansion, and it cannot tell which it
// is talking to — so printing a command line there would be advertising a
// guarantee that does not hold, which is the defect this whole command exists
// to stop, applied to its own output.
//
// Without a session id there is no full command to give: the failure happened
// before one was resolved (no binding in this worktree, or an unreadable one).
// It names the flag as the thing the caller must supply instead of pretending
// to know it.
func retryLine(f *cmdutil.Factory, r handoffRescue, path string) string {
	server := r.server
	if server == "" {
		server, _ = f.Server()
	}
	// The placeholder is NOT quoted: it is a slot for the reader to fill, and
	// `'<id>'` reads like a value they should paste verbatim. Kept out of
	// shellQuote rather than special-cased inside it, so that function keeps
	// one job.
	session := "<id>"
	if r.sessionID != "" {
		session = shellQuote(r.sessionID)
	}
	if runtime.GOOS == "windows" {
		out := "retry `hadron team session end` with:"
		if server != "" {
			out += "\n            --server        " + server
		}
		// --json is carried because an agent chose it: a retry that succeeds
		// with human output breaks the parser waiting on the other end (PR #528
		// review, Codex).
		if f.JSON {
			out += "\n            --json"
		}
		if r.summary != "" {
			out += "\n            --summary       " + r.summary
		}
		out += "\n            --session       " + session +
			"\n            --handoff-file  " + path +
			"\n          (values shown raw — quote them as your shell requires)"
		if r.sessionID == "" {
			out += "\n          (this failed before a session was resolved, so supply the id)"
		}
		return out
	}
	args := "hadron team session end"
	if server != "" {
		args += " --server " + shellQuote(server)
	}
	if f.JSON {
		args += " --json"
	}
	if r.summary != "" {
		args += " --summary " + shellQuote(r.summary)
	}
	args += " --session " + session + " --handoff-file " + shellQuote(path)
	if r.sessionID == "" {
		return "retry:  " + args +
			"\n          (this failed before a session was resolved, so supply the id)"
	}
	return "retry:  " + args
}

// shellQuote makes an argument safe to paste into a POSIX shell.
//
// POSIX ONLY, deliberately, and the retry rendering below is what makes that
// honest: on Windows the command is not presented as pasteable at all.
//
// The history is worth keeping, because the second attempt looked right (PR
// #528 review, Codex, three times). Single-quoting is wrong on Windows —
// backslashes are the path separator, and cmd.exe reads single quotes as
// literal characters. Double quotes are ALSO wrong: cmd.exe still expands
// %VAR% inside them and PowerShell still expands $var, so a summary containing
// either is silently altered by the command that promised to reproduce the
// invocation. And a Go process cannot reliably tell which of the two shells it
// would be pasted into.
//
// So there is no double-quote rule that serves both, which means the fix is not
// better escaping — it is not claiming to have escaped. Inside single quotes on
// POSIX every character except `'` is literal, so that promise IS keepable
// there, and it is kept.
func shellQuote(s string) string {
	if s != "" && !strings.ContainsAny(s, " \t\n\r\v\"'\\$`&;|<>()*?[]{}#~!=") {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// spillHandoff writes the prose somewhere the caller can get it back.
//
// Cleans up after itself on failure and CHECKS Close (PR #528 review, Copilot).
// Close is part of the write, not a formality: a deferred, ignored Close can
// swallow a flush error, and this function's whole contract is that the path it
// returns holds the prose. Returning a path to a truncated file would be the
// reassurance-without-the-thing failure, in the one place a caller has nothing
// else left to fall back on.
func spillHandoff(text string) (string, error) {
	f, err := os.CreateTemp("", "hadron-handoff-*.md")
	if err != nil {
		return "", err
	}
	name := f.Name()
	if _, err := f.WriteString(text); err != nil {
		_ = f.Close()
		_ = os.Remove(name) // no half-written file left claiming to be a handoff
		return "", err
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(name)
		return "", err
	}
	return name, nil
}

// handoffSource says where the prose came from, which decides what the
// last-resort branch may print (PR #528 review, Codex).
type handoffSource struct {
	path      string // non-empty when it came from --handoff-file
	fromStdin bool   // `--handoff -`: consumed, and held nowhere else
}

func resolveHandoff(cmd *cobra.Command, handoff, handoffFile string, stdin io.Reader) (string, handoffSource, error) {
	changed := cmd.Flags().Changed
	if !changed("handoff") && !changed("handoff-file") {
		return "", handoffSource{}, nil
	}
	var text string
	var src handoffSource
	switch {
	case changed("handoff-file"):
		data, err := os.ReadFile(handoffFile) // #nosec G304 — an operator-supplied path is the point
		if err != nil {
			return "", handoffSource{}, exitcode.Newf(exitcode.Usage, "reading --handoff-file: %v", err)
		}
		text, src.path = string(data), handoffFile
	case handoff == "-":
		data, err := io.ReadAll(stdin)
		if err != nil {
			return "", handoffSource{}, exitcode.Newf(exitcode.Usage, "reading the handoff from stdin: %v", err)
		}
		text, src.fromStdin = string(data), true
	default:
		text = handoff
	}
	if strings.TrimSpace(text) == "" {
		return "", handoffSource{}, exitcode.Newf(exitcode.Usage,
			"the handoff is empty — write what the next driver needs, or omit --handoff to end without a continuity record")
	}
	return text, src, nil
}
