package team

import (
	"context"
	"fmt"
	"io"
	"os"
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
		Short:   "Drive a persona from this worktree",
		Long: `A session binds the current git worktree to a persona and records who is
driving it, from where, with which tool. The binding lives under the
worktree's git dir, so it survives a context compaction — ` + "`whoami`" + ` reads
it back.`,
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
	ID             string  `json:"id"`
	AgentID        *string `json:"agentId"`
	PersonaName    *string `json:"personaName"`
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
	// Active means "never ended". There is no server-side stale-session
	// reaper yet (hadron-server#930), so a crashed session stays active
	// until ended or taken over.
	Active bool `json:"active"`
}

func sessionDTOFromFields(s gen.TeamSessionFields, personaName *string) sessionDTO {
	return sessionDTO{
		ID: s.Id, AgentID: s.AgentId, PersonaName: personaName, UserID: s.UserId,
		Type: s.Type, Repo: s.Repo, Branch: s.Branch, PRNumber: s.PrNumber,
		StartedAt: s.StartedAt, EndedAt: s.EndedAt, Host: s.Host, Tool: s.Tool,
		TranscriptPath: s.TranscriptPath, LLMModel: s.LlmModel, Active: s.EndedAt == nil,
	}
}

const sessionPageSize = 200

// scanSessions pages the sessions list (ordered startedAt desc; the server
// has no agent/active filter, issue-#23 style: an unbounded call is one
// default page) and calls visit per session until visit returns false or the
// list is exhausted.
func scanSessions(ctx context.Context, client graphql.Client, repo *string, visit func(gen.TeamSessionFields) bool) error {
	limit := sessionPageSize
	for offset := 0; ; {
		off := offset
		resp, err := gen.TeamSessions(ctx, client, repo, &limit, &off)
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

// personaActivity scans for the persona's most recent session and its most
// recent still-active one. The scan runs to exhaustion unless an active
// session shows up: active sessions are not necessarily the newest rows, so
// absence can only be proven by reading the whole list.
func personaActivity(ctx context.Context, client graphql.Client, agentID string) (last, active *gen.TeamSessionFields, err error) {
	err = scanSessions(ctx, client, nil, func(s gen.TeamSessionFields) bool {
		if s.AgentId == nil || *s.AgentId != agentID {
			return true
		}
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

func newCmdSessionStart(f *cmdutil.Factory) *cobra.Command {
	var as, org, repo, branch, transcript, host, tool, model string
	var force bool
	cmd := &cobra.Command{
		Use:   "start --as <persona> [--transcript <path>] [--tool <t>] [--force]",
		Short: "Start a session: bind this worktree to a persona",
		Long: `Start a coding session as a persona. The session is recorded server-side
(with the provenance fields: repo, branch, host, tool, transcript path,
model) and the binding is written under this worktree's git dir so
` + "`whoami`" + ` can recover it.

A persona with a still-active session is taken: the takeover requires
--force, and until the server-side stale-session reaper lands
(hadron-server#930) a crashed session also counts as active — the last
driver and start time are shown so you can judge staleness yourself.
--force starts your session alongside the taken-over one; it does not end
another driver's session. When this worktree already has a binding, --force
replaces it — first ending the session that binding names (best-effort),
so the old binding never orphans an active session.`,
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
				return exitcode.Newf(exitcode.Conflict,
					"this worktree is already bound to persona %s (session %s) — `hadron team session end` first, or --force to replace the binding",
					existing.PersonaName, existing.SessionID)
			}
			client, err := f.GraphQLClient()
			if err != nil {
				return err
			}
			// --force over an existing binding: best-effort end the session it
			// names before replacing it, so the overwritten binding does not
			// orphan an active session (there is no reaper, #930). Best-effort
			// because that session may already be ended — or live on another
			// server, which `session end`'s server guard would catch but a
			// takeover deliberately steamrolls.
			if existing != nil {
				if _, endErr := gen.EndTeamSession(ctx, client, existing.SessionID, nil); endErr != nil {
					fmt.Fprintf(f.IOStreams.ErrOut, "note: could not end previously bound session %s (%v) — if it is still active, end it manually\n", existing.SessionID, endErr)
				} else {
					fmt.Fprintf(f.IOStreams.ErrOut, "ended previously bound session %s (persona %s)\n", existing.SessionID, existing.PersonaName)
				}
			}
			p, err := resolvePersona(ctx, client, optStr(org), as)
			if err != nil {
				return err
			}
			personaName := ""
			if p.PersonaName != nil {
				personaName = *p.PersonaName
			}
			last, active, err := personaActivity(ctx, client, p.Id)
			if err != nil {
				return err
			}
			if active != nil && !force {
				return exitcode.Newf(exitcode.Conflict,
					"persona %s is being driven by %s — no stale-session reaper exists yet (hadron-server#930), so a crashed session also holds the persona; --force takes over",
					personaName, describeSession(active))
			}
			if active != nil {
				fmt.Fprintf(f.IOStreams.ErrOut, "taking over persona %s from %s\n", personaName, describeSession(active))
			} else if last != nil {
				fmt.Fprintf(f.IOStreams.ErrOut, "persona %s was last driven by %s\n", personaName, describeSession(last))
			}
			if host == "" {
				host, _ = os.Hostname()
			}
			input := &gen.SessionInput{
				Id:             newSessionID(),
				AgentRef:       &p.Id,
				Repo:           optStr(repo),
				Branch:         optStr(branch),
				Host:           optStr(host),
				Tool:           optStr(tool),
				TranscriptPath: optStr(transcript),
				LlmModel:       optStr(model),
			}
			resp, err := gen.StartTeamSession(ctx, client, input)
			if err != nil {
				return api.MapError(err)
			}
			if resp.StartSession == nil {
				return exitcode.Newf(exitcode.Error, "server returned no session")
			}
			s := resp.StartSession.TeamSessionFields
			server, _ := f.Server()
			path, err := writeBinding(ctx, &binding{
				SessionID:   s.Id,
				AgentID:     p.Id,
				AgentURN:    p.Urn,
				PersonaName: personaName,
				PersonaRole: strOrEmpty(p.PersonaRole),
				Server:      server,
				StartedAt:   s.StartedAt,
			})
			if err != nil {
				// The server session already exists; without a binding this
				// worktree cannot end it and the persona would stay taken
				// (no reaper, #930) — so compensate by ending it now.
				if _, endErr := gen.EndTeamSession(ctx, client, s.Id, nil); endErr != nil {
					return fmt.Errorf("%w; additionally, rolling back session %s failed (%v) — end it with `hadron team session end --session %s`", err, s.Id, endErr, s.Id)
				}
				return fmt.Errorf("%w (session %s was rolled back — persona %s is not held)", err, s.Id, personaName)
			}
			result := struct {
				Session     sessionDTO `json:"session"`
				BindingPath string     `json:"bindingPath"`
				TookOver    bool       `json:"tookOver"`
			}{sessionDTOFromFields(s, &personaName), path, active != nil}
			return output.Write(f.IOStreams, f.JSON, result, func(w io.Writer) error {
				_, err := fmt.Fprintf(w, "✓ started session %s as %s%s\n  binding: %s\n", s.Id, personaName, roleSuffix(p.PersonaRole), path)
				return err
			})
		},
	}
	cmd.Flags().StringVar(&as, "as", "", "persona to drive (name, agent ID, or agent URN)")
	cmd.Flags().StringVar(&org, "org", "", "disambiguate a persona name that exists in more than one org")
	cmd.Flags().BoolVar(&force, "force", false, "take over a persona with an active session; also replaces this worktree's binding, ending its session first (best-effort)")
	cmd.Flags().StringVar(&repo, "repo", "", "repository the session works on, e.g. owner/repo")
	cmd.Flags().StringVar(&branch, "branch", "", "branch the session works on")
	cmd.Flags().StringVar(&transcript, "transcript", "", "path of the driving tool's transcript on this host")
	cmd.Flags().StringVar(&host, "host", "", "machine identifier (defaults to this hostname)")
	cmd.Flags().StringVar(&tool, "tool", "", "driving tool, e.g. claude-code or codex")
	cmd.Flags().StringVar(&model, "model", "", "LLM model driving the session")
	_ = cmd.MarkFlagRequired("as")
	return cmd
}

func newCmdSessionWhoami(f *cmdutil.Factory) *cobra.Command {
	return &cobra.Command{
		Use:   "whoami",
		Short: "Show which persona this worktree is bound to",
		Long: `Read the worktree's session binding back — the compaction-recovery read.
Local only: it reports what ` + "`session start`" + ` recorded, without asking the
server whether the session is still open.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			b, path, err := readBinding(cmd.Context())
			if err != nil {
				return err
			}
			if b == nil {
				return exitcode.Newf(exitcode.NotFound, "no persona is bound to this worktree — `hadron team session start --as <name>`")
			}
			result := struct {
				*binding
				BindingPath string `json:"bindingPath"`
			}{b, path}
			return output.Write(f.IOStreams, f.JSON, result, func(w io.Writer) error {
				fmt.Fprintf(w, "%s%s\n  session: %s (started %s)\n  agent: %s\n", b.PersonaName, roleSuffix(optStr(b.PersonaRole)), b.SessionID, b.StartedAt, b.AgentURN)
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

// logResultDTO is the stable --json shape of `session log`. Recorded is
// "local" while the worklog is not built (#369 slice 3); a server-recorded
// milestone will use a different value, so agents can branch on it.
type logResultDTO struct {
	SessionID string `json:"sessionId"`
	PRNumber  int    `json:"prNumber"`
	Recorded  string `json:"recorded"`
}

func newCmdSessionLog(f *cmdutil.Factory) *cobra.Command {
	var pr int
	cmd := &cobra.Command{
		Use:   "log --pr <number>",
		Short: "Record a milestone for the current session (slice-1: local only)",
		Long: `Record a work milestone for this worktree's session. Slice 1 takes a bare
PR number and records it in the local binding (shown by whoami).

TODO(#369 slice 3): the shared worklog collection and the denormalized
Session.prNumber replace this local record — the server currently has no
mutation that updates an existing session, so nothing is written
server-side yet. Session.prNumber is a display convenience either way;
provenance queries go through the worklog.`,
		Example: `  hadron team session log --pr 371`,
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			if pr <= 0 {
				return exitcode.Newf(exitcode.Usage, "--pr must be a positive PR number")
			}
			b, _, err := readBinding(ctx)
			if err != nil {
				return err
			}
			if b == nil {
				return exitcode.Newf(exitcode.NotFound, "no active session in this worktree — `hadron team session start --as <name>` first")
			}
			known := false
			for _, n := range b.PRNumbers {
				if n == pr {
					known = true
					break
				}
			}
			if !known {
				b.PRNumbers = append(b.PRNumbers, pr)
				if _, err := writeBinding(ctx, b); err != nil {
					return err
				}
			}
			fmt.Fprintf(f.IOStreams.ErrOut, "note: recorded locally only — the shared worklog and Session.prNumber land with #369 slice 3\n")
			result := logResultDTO{SessionID: b.SessionID, PRNumber: pr, Recorded: "local"}
			return output.Write(f.IOStreams, f.JSON, result, func(w io.Writer) error {
				_, err := fmt.Fprintf(w, "✓ logged PR #%d for session %s\n", pr, b.SessionID)
				return err
			})
		},
	}
	cmd.Flags().IntVar(&pr, "pr", 0, "pull-request number this session worked on")
	_ = cmd.MarkFlagRequired("pr")
	return cmd
}

// endResultDTO is the stable --json shape of `session end`.
type endResultDTO struct {
	SessionID   string `json:"sessionId"`
	PersonaName string `json:"personaName"`
	EndedAt     string `json:"endedAt"`
}

func newCmdSessionEnd(f *cmdutil.Factory) *cobra.Command {
	var summary, sessionID string
	cmd := &cobra.Command{
		Use:   "end [--summary <text>] [--session <id>]",
		Short: "End this worktree's session",
		Long: `End the session this worktree is bound to and clear the binding. Ending a
session frees its persona — unless another active session still holds it
(e.g. after a --force takeover; check ` + "`session list --active`" + `).

--session ends an explicit session id instead — the recovery path when the
binding is gone (a lost worktree, a failed binding write) but the server
session is still open.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			b, _, err := readBinding(ctx)
			if err != nil && sessionID == "" {
				return err
			}
			id := sessionID
			personaName := ""
			if id == "" {
				if b == nil {
					return exitcode.Newf(exitcode.NotFound, "no active session in this worktree — pass --session <id> to end one without a binding")
				}
				id = b.SessionID
				personaName = b.PersonaName
				// The binding records which server the session lives on. Ending
				// it against a different backend would "succeed" in confusion:
				// the mutation fails to find the session (or worse, finds an
				// unrelated one) while the real session keeps its persona.
				server, _ := f.Server()
				if b.Server != "" && server != "" && b.Server != server {
					return exitcode.Newf(exitcode.Usage,
						"this worktree's session was started against %s, but the current server is %s — rerun with `--server %s`",
						b.Server, server, b.Server)
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
			result := endResultDTO{SessionID: id, PersonaName: personaName, EndedAt: endedAt}
			return output.Write(f.IOStreams, f.JSON, result, func(w io.Writer) error {
				who := ""
				if personaName != "" {
					who = " (persona " + personaName + ")"
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
	var as, org, repo string
	var active bool
	var limit, offset int
	cmd := &cobra.Command{
		Use:     "list [--active] [--as <persona>] [--repo <r>]",
		Aliases: []string{"ls"},
		Short:   "List sessions — team presence and session provenance",
		Long: `List sessions, newest first, with each session's persona joined in.
--active narrows to sessions that never ended (team presence — but note
there is no stale-session reaper yet, hadron-server#930); --as narrows to
one persona's sessions. Both narrow client-side over the full list; plain
listings page server-side.

TODO(#369 slice 3): --pr — the worklog-backed provenance query (PR →
sessions → transcripts).`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			if limit < 0 || offset < 0 {
				return exitcode.Newf(exitcode.Usage, "--limit and --offset must be non-negative")
			}
			client, err := f.GraphQLClient()
			if err != nil {
				return err
			}
			// One roster read joins personaName onto every row (sessions only
			// carry agentId) and resolves --as.
			roster, err := scanPersonaAgents(ctx, client, optStr(org))
			if err != nil {
				return err
			}
			nameByAgent := map[string]*string{}
			for i := range roster {
				nameByAgent[roster[i].Id] = roster[i].PersonaName
			}
			var asAgentID string
			if as != "" {
				p, err := resolvePersona(ctx, client, optStr(org), as)
				if err != nil {
					return err
				}
				asAgentID = p.Id
			}

			sessions := []sessionDTO{}
			filtered := active || as != ""
			if filtered {
				// Client-side narrowing needs the whole list; --limit/--offset
				// then slice the filtered result.
				err = scanSessions(ctx, client, optStr(repo), func(s gen.TeamSessionFields) bool {
					if active && s.EndedAt != nil {
						return true
					}
					if asAgentID != "" && (s.AgentId == nil || *s.AgentId != asAgentID) {
						return true
					}
					sessions = append(sessions, sessionDTOFromFields(s, personaNameFor(nameByAgent, s.AgentId)))
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
				resp, err := gen.TeamSessions(ctx, client, optStr(repo), lim, off)
				if err != nil {
					return api.MapError(err)
				}
				for _, s := range resp.Sessions {
					if s == nil {
						continue
					}
					sessions = append(sessions, sessionDTOFromFields(s.TeamSessionFields, personaNameFor(nameByAgent, s.AgentId)))
				}
			}
			return output.Write(f.IOStreams, f.JSON, sessions, func(w io.Writer) error {
				t := output.NewTable(w, "SESSION", "PERSONA", "USER", "REPO", "PR", "STARTED", "ENDED", "TOOL")
				for _, s := range sessions {
					pr := "—"
					if s.PRNumber != nil {
						pr = fmt.Sprintf("#%d", *s.PRNumber)
					}
					// A session by a non-persona (or unreadable) agent shows
					// the raw agent id rather than nothing.
					persona := s.PersonaName
					if persona == nil {
						persona = s.AgentID
					}
					t.Row(s.ID, dash(persona), dash(s.UserID), dash(s.Repo), pr, s.StartedAt, dash(s.EndedAt), dash(s.Tool))
				}
				return t.Flush()
			})
		},
	}
	cmd.Flags().BoolVar(&active, "active", false, "only sessions that never ended (presence)")
	cmd.Flags().StringVar(&as, "as", "", "only one persona's sessions (name, agent ID, or agent URN)")
	cmd.Flags().StringVar(&org, "org", "", "roster scope for the persona join and --as resolution")
	cmd.Flags().StringVar(&repo, "repo", "", "filter by repository")
	cmd.Flags().IntVar(&limit, "limit", 0, "max results (server default when unset)")
	cmd.Flags().IntVar(&offset, "offset", 0, "results to skip")
	return cmd
}

// personaNameFor joins a session's agentId onto the roster; nil when the
// agent is unknown or not a persona (the caller renders it as the raw id).
func personaNameFor(nameByAgent map[string]*string, agentID *string) *string {
	if agentID == nil {
		return nil
	}
	return nameByAgent[*agentID]
}

func strOrEmpty(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}
