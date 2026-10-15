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
		Tool:       rf.tool,
		Model:      rf.model,
	})
	if err != nil {
		return retry(err)
	}
	outcome := string(r.Outcome)
	// liveNo: the server's lifecycle gate admitted the resume only with no
	// other live session of the worker, so nobody was displaced.
	return finishBind(cmd, f, client, s, w, path, lapsedBinding, existing, liveNo, "", &outcome)
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
	SessionID  string `json:"sessionId"`
	WorkerID   string `json:"workerId"`
	WorkerName string `json:"workerName"`
	ExpiresAt  string `json:"expiresAt"`
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
		Example: `  hadron team session resume-claim   # then reset the context, and in the new one:
  hadron team session start --tool claude-code`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			id := strings.TrimSpace(sessionID)
			workerName := ""
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
				id, workerName = b.SessionID, b.WorkerName
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
				if dto.WorkerName != "" {
					who = dto.WorkerName
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
