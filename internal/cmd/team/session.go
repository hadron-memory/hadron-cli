package team

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
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

Closing your CHAT SESSION does not release the worker. The worker session
outlives it and keeps the worker taken until you run ` + "`session end`" + ` or the
server reaps it, so the next driver meets a takeover prompt rather than a
free worker. End the worker session deliberately when you stop.`,
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
	AgentID        *string `json:"agentId"`
	WorkerID       *string `json:"workerId"`
	WorkerName     *string `json:"workerName"`
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
	// signal since the server-side reaper (hadron-server#930, PR #933)
	// auto-expires stale sessions on hard expiry and inactivity.
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
	if s.Worker != nil {
		name = &s.Worker.Name
	}
	return sessionDTO{
		ID: s.Id, AgentID: s.AgentId, WorkerID: s.WorkerId, WorkerName: name,
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

--as takes the worker's name — resolved within the team App, from -m, the
persistent --app flag, or the configured App context — or the worker's id,
which needs no App scope at all.

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

A worker with a still-active session is taken: the takeover requires
--force, and the last driver and start time are shown (informed override,
cor:agt:020:03 — never silent). Stale sessions are reaped server-side
(hard expiry + inactivity — logging milestones counts as activity), so an
active session usually means someone is genuinely driving. --force starts
your session alongside the taken-over one; it does not end another
driver's session. When this worktree already has a binding, --force
replaces it — first ending the session that binding names (best-effort),
so the old binding never leaves a session open. That makes --force the
remedy for an ABANDONED binding only: it never separates two live agents,
it just relabels which worker the shared tree is blamed on.`,
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
			// leave a session open (the #930 reaper would expire it
			// eventually, but an explicit end is immediate). Best-effort
			// because that session may already be ended — or live on another
			// server, which `session end`'s server guard would catch but a
			// takeover deliberately steamrolls.
			if existing != nil {
				if _, endErr := gen.EndTeamSession(ctx, client, existing.SessionID, nil); endErr != nil {
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
				return exitcode.Newf(exitcode.Conflict,
					"worker %s is being driven by %s — its worker session is still open, which a closed chat session does not end; --force takes over (a stale worker session also auto-expires server-side)",
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
				// worktree cannot end it and the worker would stay taken
				// until the reaper expires it — so compensate by ending it
				// now.
				if _, endErr := gen.EndTeamSession(ctx, client, s.Id, nil); endErr != nil {
					return fmt.Errorf("%w; additionally, rolling back session %s failed (%v) — end it with `hadron team session end --session %s`", err, s.Id, endErr, s.Id)
				}
				return fmt.Errorf("%w (session %s was rolled back — worker %s is not held)", err, s.Id, w.Name)
			}
			result := struct {
				Session     sessionDTO `json:"session"`
				Worker      workerDTO  `json:"worker"`
				BindingPath string     `json:"bindingPath"`
				TookOver    bool       `json:"tookOver"`
			}{sessionDTOFromFields(s, &w.Name), workerDTOFromFields(w), path, active != nil}
			return output.Write(f.IOStreams, f.JSON, result, func(out io.Writer) error {
				if _, err := fmt.Fprintf(out, "✓ started session %s as %s%s\n  binding: %s\n", s.Id, w.Name, roleSuffix(w.Role), path); err != nil {
					return err
				}
				// The resolved boot briefing (template bound + override) is
				// what the driver adopts — print it where they will see it.
				if w.Prompt != nil && *w.Prompt != "" {
					if _, err := fmt.Fprintf(out, "\n%s\n", *w.Prompt); err != nil {
						return err
					}
				}
				return nil
			})
		},
	}
	cmd.Flags().StringVar(&as, "as", "", "worker to drive (name within the team App, or worker id)")
	cmd.Flags().BoolVar(&force, "force", false, "take over a worker with an active session; also replaces this worktree's binding, ending its session first (best-effort)")
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
worker, and ` + "`session end`" + ` is what releases it.`,
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
	if b.ChatSeenSeq == 0 {
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
	one := 1
	resp, err := gen.TeamChatMessages(ctx, client, b.AppID, &b.ChatSeenSeq, nil, &one, nil)
	if err != nil || resp.TeamChatMessages == nil || resp.TeamChatMessages.Total == 0 {
		return // caught up, or unreadable — either way, nothing useful to say.
	}
	total := resp.TeamChatMessages.Total

	// The mentions count is a SECOND query rather than a client-side filter,
	// because the server owns mention resolution (hadron-server#979: a token
	// may match several workers, and matching is not ours to reimplement).
	// Cheap: total is exact under limit 1, verified against the live server.
	mentions := 0
	if b.WorkerID != "" {
		if m, merr := gen.TeamChatMessages(ctx, client, b.AppID, &b.ChatSeenSeq, &b.WorkerID, &one, nil); merr == nil && m.TeamChatMessages != nil {
			mentions = m.TeamChatMessages.Total
		}
	}
	fmt.Fprintf(f.IOStreams.ErrOut,
		"note: %s in the team chat since you last read (%s) — `hadron team chat read --since %d`\n",
		pluralMessages(total), pluralMentions(mentions), b.ChatSeenSeq)
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
commit included — counts as session liveness for the inactivity reaper,
so logging keeps the worker taken while work is in flight.

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
			// touch (any authorized update bumps updatedAt), so a driver
			// logging only commits is never reaped for inactivity.
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
				known := false
				for _, n := range b.PRNumbers {
					if n == number {
						known = true
						break
					}
				}
				if !known {
					b.PRNumbers = append(b.PRNumbers, number)
					// The server writes already succeeded, so a failed local
					// append only degrades whoami's history — report, don't fail.
					if _, err := writeBinding(ctx, b); err != nil {
						fmt.Fprintf(f.IOStreams.ErrOut, "note: recorded server-side, but updating the local binding failed: %v\n", err)
					}
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
	if os.Getenv("HADRON_TEAM_GIT_DIR") != "" {
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
	server, _ := f.Server()
	if b.Server != "" && server != "" && b.Server != server {
		return exitcode.Newf(exitcode.Usage,
			"this worktree's session was started against %s, but the current server is %s — rerun with `--server %s`",
			b.Server, server, b.Server)
	}
	return nil
}

// endResultDTO is the stable --json shape of `session end`.
type endResultDTO struct {
	SessionID  string `json:"sessionId"`
	WorkerName string `json:"workerName"`
	EndedAt    string `json:"endedAt"`
}

func newCmdSessionEnd(f *cmdutil.Factory) *cobra.Command {
	var summary, sessionID string
	cmd := &cobra.Command{
		Use:   "end [--summary <text>] [--session <id>]",
		Short: "End this worktree's worker session — this is what releases the worker",
		Long: `End the WORKER SESSION this worktree is bound to and clear the binding.
This is the only thing that frees the worker — unless another active worker
session still holds it (e.g. after a --force takeover; check
` + "`session list --active`" + `).

Closing your CHAT SESSION does not do this. Archive the Desktop window or
quit the Claude Code session and the worker session stays open, holding the
worker until you end it here or the server reaps it (hadron-server#1034).
So end it deliberately when you stop working, not when you close the window.

--session ends an explicit worker session id instead — the recovery path
when the binding is gone or unusable (a lost worktree, a failed binding
write, a binding written by a pre-Worker CLI) but the server-side worker
session is still open.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			b, _, err := readBinding(ctx)
			if err != nil && sessionID == "" {
				return err
			}
			id := sessionID
			workerName := ""
			if id == "" {
				if b == nil {
					return exitcode.Newf(exitcode.NotFound, "no active session in this worktree — pass --session <id> to end one without a binding")
				}
				id = b.SessionID
				workerName = b.WorkerName
				if err := checkBindingServer(f, b); err != nil {
					return err
				}
			}
			client, err := f.GraphQLClient()
			if err != nil {
				return err
			}
			resp, err := gen.EndTeamSession(ctx, client, id, optStr(summary))
			if err != nil {
				return api.MapError(err)
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
	cmd.Flags().StringVar(&summary, "summary", "", "short summary of what the session did")
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
		Short:   "List worker sessions — team presence and provenance",
		Long: `List sessions, newest first, with each session's worker joined in.
--active narrows to sessions that never ended — team presence, honest
since stale sessions are auto-expired server-side; --as narrows to one
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
				t := output.NewTable(w, "SESSION", "WORKER", "USER", "REPO", "PR", "STARTED", "ENDED", "TOOL")
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
					t.Row(s.ID, dash(worker), dash(s.UserID), dash(s.Repo), pr, s.StartedAt, dash(s.EndedAt), dash(s.Tool))
				}
				return t.Flush()
			})
		},
	}
	cmd.Flags().BoolVar(&active, "active", false, "only sessions that never ended (presence)")
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
		t := output.NewTable(w, "SESSION", "WORKER", "USER", "TOOL", "HOST", "MODEL", "STARTED", "TRANSCRIPT")
		for _, s := range sessions {
			t.Row(s.ID, dash(s.WorkerName), dash(s.UserID), dash(s.Tool), dash(s.Host), dash(s.LLMModel), s.StartedAt, dash(s.TranscriptPath))
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
