package team

import (
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/hadron-memory/hadron-cli/internal/api"
	"github.com/hadron-memory/hadron-cli/internal/api/gen"
	"github.com/hadron-memory/hadron-cli/internal/cmdutil"
	"github.com/hadron-memory/hadron-cli/internal/exitcode"
	"github.com/hadron-memory/hadron-cli/internal/output"
)

// hadron-cli#842 over hadron-server#1956 (cor:agt:020:03): a post or a work
// record is never silently attributed to the human, and a fresh context
// recovers its worker from the server. The resume claim is SERVER state,
// judged by the caller's token: the receipt names no claim id, so the CLI has
// nothing to hold, store, pass in argv or log. Its only local identity
// material stays the worktree binding, an optional cache (Holger, #12291).

// noBindingRefusal is the typed refusal for an authored act from a context
// with no worker binding. NotFound, the code `session log` has always given
// "no active session". It is one fixed string: it names no worker, session or
// claim, so it cannot disclose which bindings exist.
func noBindingRefusal(what string, asMe bool) error {
	alt := ""
	if asMe {
		alt = ", or post as yourself with --as-me"
	}
	return exitcode.Newf(exitcode.NotFound,
		"no worker session is bound here, so %s would be attributed to you, the person, and is refused (cor:agt:020:03). "+
			"Recover your worker with `hadron team session start` (no worker named: it redeems your resume claim, or your one live worker session), "+
			"bind one with `hadron team session start --as <worker>`%s", what, alt)
}

type resumeFlags struct {
	pick, tool, host, model, transcript string
}

// resumeCandidateDTO is the stable --json shape of one CHOOSE candidate.
type resumeCandidateDTO struct {
	SessionID   string  `json:"sessionId"`
	WorkerID    string  `json:"workerId"`
	WorkerName  string  `json:"workerName"`
	Description *string `json:"description"`
	// CLAIM or LIVE_SESSION.
	Source string `json:"source"`
	// When the claim lapses; null for a live session.
	ExpiresAt *string `json:"expiresAt"`
}

type resumeChooseDTO struct {
	ResumeOutcome string               `json:"resumeOutcome"`
	Candidates    []resumeCandidateDTO `json:"candidates"`
}

// runSessionResume is `session start` with no worker named and no grant.
func runSessionResume(cmd *cobra.Command, f *cmdutil.Factory, rf resumeFlags) error {
	ctx := cmd.Context()
	// Everything that can refuse locally refuses BEFORE resumeSession, which
	// spends a claim. A flag the server's input cannot carry is refused rather
	// than dropped: a resumed session would silently lack it.
	for _, name := range []string{"force", "repo", "branch", "reports-to", "memory"} {
		if cmd.Flags().Changed(name) {
			return exitcode.Newf(exitcode.Usage,
				"--%s does not apply to a resume (start with no worker named); name the worker with --as to use it", name)
		}
	}
	var pick *string
	if cmd.Flags().Changed("session") {
		p := strings.TrimSpace(rf.pick)
		if p == "" {
			return exitcode.Newf(exitcode.Usage, "--session must name a session a resume listed; omit it to let the server decide")
		}
		pick = &p
	}
	// Outside a git worktree this errors: there is nowhere to cache the
	// binding, and spending the claim anyway would strand it.
	existing, _, err := readBinding(ctx)
	if err != nil {
		return err
	}
	var lapsedBinding bool
	if existing != nil {
		if err := checkBindingServer(f, existing); err != nil {
			return err
		}
		// A worktree whose binding is still LIVE needs no resume: the
		// binding already carries the worker. That is the ordinary shape of
		// a shed in the same worktree, so say so plainly instead of the
		// "another agent may be working here" takeover refusal below, which
		// is right for a named bind and wrong here. Best-effort: on any doubt
		// the general guard still decides.
		if c, cerr := f.GraphQLClient(); cerr == nil {
			if r, lerr := gen.GetBoundSessionLiveness(ctx, c, existing.SessionID); lerr == nil && r != nil && r.Session != nil && r.Session.IsLive {
				return exitcode.Newf(exitcode.Conflict,
					"this worktree is still bound to worker %s (session %s, live), so there is nothing to resume: the binding already carries the worker, and `hadron team session whoami` shows it. A resume claim is for a context with no binding; an unused one lapses on its own",
					existing.WorkerName, existing.SessionID)
			}
		}
		lapsedBinding, err = checkExistingBinding(ctx, f, existing)
		if err != nil {
			return err
		}
	}
	client, err := f.GraphQLClient()
	if err != nil {
		return err
	}
	host := rf.host
	if host == "" {
		host, _ = os.Hostname()
	}
	resp, err := gen.ResumeTeamSession(ctx, client, &gen.ResumeSessionInput{
		Session:        pick,
		Tool:           optStr(rf.tool),
		Host:           optStr(host),
		LlmModel:       optStr(rf.model),
		TranscriptPath: optStr(rf.transcript),
	})
	if err != nil {
		return api.MapError(err)
	}
	r := resp.ResumeSession
	if r == nil {
		return exitcode.Newf(exitcode.Error, "server returned no resume answer")
	}
	if r.Outcome == gen.ResumeOutcomeChoose {
		return renderResumeChoice(f, r.Candidates)
	}
	if r.Session == nil {
		return exitcode.Newf(exitcode.Error, "server answered %s with no session", r.Outcome)
	}
	s := r.Session.TeamSessionFields
	// NEVER ROLL BACK. A plain or launched start ends its session when the
	// binding cannot be completed; a RESUMED session is the worker's
	// continuing one, and ending it would destroy exactly what the claim
	// recovered. The server's fast path finds it again: it is the caller's one
	// live session now.
	retry := func(cause error) error {
		return fmt.Errorf("%w — worker session %s is bound on the server and stays open; run `hadron team session start` again to pick it up", cause, s.Id)
	}
	w, err := launchedWorker(ctx, client, s)
	if err != nil {
		return retry(err)
	}
	// The cache describes the session the SERVER bound, not this command's
	// flags (Anne, #892 blind P2). A RESUMED session keeps the provenance it
	// was started with, and the server ignores the flags for it; only a
	// STARTED session takes them. Caching the flags instead left a
	// no-argument resume with an empty tool, which the next `session log`
	// sends and the server refuses. A flag fills only a field the session
	// lacks.
	tool := sessionOr(s.Tool, rf.tool)
	model := sessionOr(s.LlmModel, rf.model)
	noteIgnoredFlag(f, "tool", rf.tool, s.Tool)
	noteIgnoredFlag(f, "model", rf.model, s.LlmModel)
	server, _ := f.Server()
	path, err := writeBinding(ctx, &binding{
		AppBound:   true,
		SessionID:  s.Id,
		WorkerID:   w.Id,
		WorkerName: w.Name,
		WorkerRole: strOrEmpty(w.Role),
		AgentID:    w.AgentId,
		AppID:      w.AppId,
		Server:     server,
		StartedAt:  s.StartedAt,
		Tool:       tool,
		Model:      model,
		Repo:       strOrEmpty(s.Repo),
	})
	if err != nil {
		return retry(err)
	}
	outcome := string(r.Outcome)
	// tookOver. STARTED is a NEW session, and startSession's gate admits it
	// only with no live session of the worker: provably nobody displaced.
	// RESUMED is an EXISTING session, and the fast path picks it precisely
	// because it reads live; another context may still be driving it, so
	// whether this displaced anyone is unknown (null), never false.
	live := liveUnknown
	if r.Outcome == gen.ResumeOutcomeStarted {
		live = liveNo
	}
	return finishBind(cmd, f, client, s, w, path, lapsedBinding, existing, live, "", &outcome)
}

// sessionOr is the session's recorded value, or the flag when it has none.
func sessionOr(recorded *string, flag string) string {
	if recorded != nil && strings.TrimSpace(*recorded) != "" {
		return *recorded
	}
	return flag
}

// noteIgnoredFlag says on stderr when a flag disagrees with the session the
// server resumed: the session keeps its own value, so the flag had no effect.
func noteIgnoredFlag(f *cmdutil.Factory, name, flag string, recorded *string) {
	if flag == "" || recorded == nil || strings.TrimSpace(*recorded) == "" || *recorded == flag {
		return
	}
	fmt.Fprintf(f.IOStreams.ErrOut, "note: the resumed session records --%s %s; your --%s %s was not applied to it\n", name, *recorded, name, flag)
}

// renderResumeChoice lists a CHOOSE answer and exits 2: nothing is bound, so
// a script must not read it as success. Every candidate is the caller's own
// session (the server keys the read on the caller).
func renderResumeChoice(f *cmdutil.Factory, cands []*gen.ResumeTeamSessionResumeSessionResumeSessionResultCandidatesResumeCandidate) error {
	dto := resumeChooseDTO{ResumeOutcome: string(gen.ResumeOutcomeChoose), Candidates: []resumeCandidateDTO{}}
	for _, c := range cands {
		if c == nil {
			continue
		}
		dto.Candidates = append(dto.Candidates, resumeCandidateDTO{
			SessionID: c.SessionId, WorkerID: c.WorkerId, WorkerName: c.WorkerName,
			Description: c.Description, Source: string(c.Source), ExpiresAt: c.ExpiresAt,
		})
	}
	if err := output.Write(f.IOStreams, f.JSON, dto, func(w io.Writer) error {
		if _, err := fmt.Fprintln(w, "Several worker sessions of yours could be resumed; nothing was bound. Pick one:"); err != nil {
			return err
		}
		for _, c := range dto.Candidates {
			about := ""
			if c.Description != nil && *c.Description != "" {
				about = " — " + *c.Description
			}
			from := "your live session"
			if c.Source == string(gen.ResumeSourceClaim) {
				from = "resume claim"
				if c.ExpiresAt != nil {
					from += ", expires " + *c.ExpiresAt
				}
			}
			if _, err := fmt.Fprintf(w, "  %s%s (%s)\n    hadron team session start --session %s\n", c.WorkerName, about, from, c.SessionID); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		return err
	}
	return exitcode.Silent(exitcode.Usage)
}

// resumeClaimDTO is the stable --json shape of `session resume-claim`. It
// carries no claim id because the server issues none.
type resumeClaimDTO struct {
	SessionID string `json:"sessionId"`
	WorkerID  string `json:"workerId"`
	// From the binding; null with --session, where no binding names it.
	WorkerName *string `json:"workerName"`
	ExpiresAt  string  `json:"expiresAt"`
}

func newCmdSessionResumeClaim(f *cmdutil.Factory) *cobra.Command {
	var sessionID string
	cmd := &cobra.Command{
		Use:   "resume-claim [--session <id>]",
		Short: "Write a resume claim before a context reset that keeps the session (a shed)",
		Long: `Write a RESUME CLAIM for this worktree's worker session (hadron-cli#842,
cor:agt:020:03), before a deliberate context reset that keeps the session
open: a shed. The fresh context then recovers the worker by running
` + "`hadron team session start`" + ` with no worker named, within 60 seconds of
this write.

The claim is for a context that has NO binding: another directory, a fresh
checkout, a host whose worktree went away. A fresh context in THIS worktree
needs no claim, since the binding here still carries the worker
(` + "`team session whoami`" + `), and a resume here refuses (exit 5) because it is
still bound. An unused claim lapses on its own.

The claim is server-side and judged by your own token. The receipt names no
claim id: there is nothing to keep, pass on or protect. It is single use, and
writing a new one replaces any unused claim for the same session.

For a ROTATION, which ends the session, use
` + "`team session end --resume-claim`" + ` instead: it writes the claim with the
handoff, before the end.

--session claims an explicit session id instead of the bound one (recovery
when the binding is gone). Someone else's session answers as an absent one;
an ended session refuses SESSION_ENDED, and a session with no worker refuses
SESSION_NOT_WORKER_BOUND.`,
		Example: `  hadron team session resume-claim   # then reset; in the new context, with no binding:
  hadron team session start --tool claude-code`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			id := strings.TrimSpace(sessionID)
			if cmd.Flags().Changed("session") && id == "" {
				return exitcode.Newf(exitcode.Usage, "--session must name a session id; omit it to claim this worktree's bound session")
			}
			var workerName *string
			if id == "" {
				b, _, err := readBinding(ctx)
				if err != nil {
					return err
				}
				if b == nil || b.SessionID == "" {
					return exitcode.Newf(exitcode.NotFound,
						"no worker session is bound in this worktree — bind one first, or pass --session <id> to claim one explicitly")
				}
				if err := checkBindingServer(f, b); err != nil {
					return err
				}
				id, workerName = b.SessionID, optStr(b.WorkerName)
			}
			client, err := f.GraphQLClient()
			if err != nil {
				return err
			}
			resp, err := gen.CreateResumeClaim(ctx, client, id)
			if err != nil {
				return api.MapError(err)
			}
			rc := resp.CreateResumeClaim
			if rc == nil {
				return exitcode.Newf(exitcode.Error, "server returned no resume claim receipt")
			}
			dto := resumeClaimDTO{SessionID: rc.SessionId, WorkerID: rc.WorkerId, WorkerName: workerName, ExpiresAt: rc.ExpiresAt}
			return output.Write(f.IOStreams, f.JSON, dto, func(w io.Writer) error {
				who := dto.WorkerID
				if dto.WorkerName != nil {
					who = *dto.WorkerName
				}
				_, err := fmt.Fprintf(w, "✓ resume claim written for %s (session %s), expires %s\n"+
					"  within 60 seconds, run `hadron team session start` with no worker named in the fresh context to resume it\n",
					who, dto.SessionID, dto.ExpiresAt)
				return err
			})
		},
	}
	cmd.Flags().StringVar(&sessionID, "session", "", "claim this session id instead of the worktree's bound one (recovery)")
	return cmd
}
