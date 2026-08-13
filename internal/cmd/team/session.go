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
	// Active means "not ended" (endedAt IS NULL) — an honest liveness
	// signal since the server-side reaper (hadron-server#930, PR #933)
	// auto-expires stale sessions on hard expiry and inactivity.
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
	var as, org, repo, branch, transcript, host, tool, model, teamMemory string
	var force bool
	cmd := &cobra.Command{
		Use:   "start --as <persona> [--transcript <path>] [--tool <t>] [--force]",
		Short: "Start a session: bind this worktree to a persona",
		Long: `Start a coding session as a persona. The session is recorded server-side
(with the provenance fields: repo, branch, host, tool, transcript path,
model) and the binding is written under this worktree's git dir so
` + "`whoami`" + ` can recover it.

A persona with a still-active session is taken: the takeover requires
--force, and the last driver and start time are shown. Stale sessions are
reaped server-side (hard expiry + inactivity — logging milestones counts
as activity), so an active session usually means someone is genuinely
driving. --force starts your session alongside the taken-over one; it does
not end another driver's session. When this worktree already has a
binding, --force replaces it — first ending the session that binding names
(best-effort), so the old binding never leaves a session open.`,
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
			// leave a session open (the #930 reaper would expire it
			// eventually, but an explicit end is immediate). Best-effort
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
					"persona %s is being driven by %s — --force takes over (a stale session also auto-expires server-side)",
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
			// #396/#391: bind the session to the team App up front. The worklog
			// write (`recordTeamWork`) refuses a session that is not a session
			// OF the App — deliberately, since work is recorded THROUGH a
			// session — and `updateSession` cannot set appId afterwards, so a
			// session that starts unbound can never record work. The team
			// memory IS the App's shared memory, so -m determines the App.
			var appRefArg *string
			if teamMemory != "" {
				resolved, aerr := appForTeamMemory(ctx, client, cmdutil.CanonicalMemoryRef(teamMemory))
				if aerr != nil {
					return aerr
				}
				appRefArg = &resolved
			} else if ambient, aerr := f.App(); aerr == nil && ambient != "" {
				// No -m, but an App context is configured — bind to it anyway.
				// Binding is only possible at start (updateSession cannot set
				// appId), so an unbound session can NEVER record work: take
				// every chance not to create one.
				appRefArg = &ambient
			}
			input := &gen.SessionInput{
				Id:             newSessionID(),
				AgentRef:       &p.Id,
				AppRef:         appRefArg,
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
			teamMem := ""
			if teamMemory != "" {
				teamMem = cmdutil.CanonicalMemoryRef(teamMemory)
			}
			path, err := writeBinding(ctx, &binding{
				SessionID:   s.Id,
				AgentID:     p.Id,
				AgentURN:    p.Urn,
				PersonaName: personaName,
				PersonaRole: strOrEmpty(p.PersonaRole),
				Server:      server,
				StartedAt:   s.StartedAt,
				TeamMemory:  teamMem,
				Tool:        tool,
				Repo:        repo,
				Model:       model,
			})
			if err != nil {
				// The server session already exists; without a binding this
				// worktree cannot end it and the persona would stay taken
				// until the reaper expires it — so compensate by ending it
				// now.
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
	cmd.Flags().StringVarP(&teamMemory, "memory", "m", "", "team App memory (worklog home) — recorded in the binding for `session log`/`list --pr`")
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

func newCmdSessionLog(f *cmdutil.Factory) *cobra.Command {
	var pr, issue, commit, branch, action, detail, memory string
	cmd := &cobra.Command{
		Use:   "log (--pr | --issue | --commit | --branch) <ref> [--action <a>]",
		Short: "Record an artifact milestone for the current session",
		Long: `Record an external-artifact milestone for this worktree's session in the
team worklog — the collection behind the provenance query
(` + "`session list --pr <ref>`" + `: which sessions produced this PR?). The worklog
home is the team memory recorded at ` + "`session start -m`" + ` (or --memory here);
declare its schema once with ` + "`team init`" + `.

Refs are normalized to one canonical string per artifact (owner/repo#371,
owner/repo@sha, owner/repo:branch) — a URL, the short form, or a bare
number/sha/branch are all accepted; a bare value is qualified by the
session's recorded --repo (or the git remote). --pr and --branch
additionally denormalize onto Session.prNumber / Session.branch (latest
wins; display convenience only). Every logged milestone — issue and
commit included — counts as session liveness for the inactivity reaper,
so logging keeps the persona taken while work is in flight. Without a
team memory, --pr/--branch degrade to the denormalization alone.`,
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
			teamMem := b.TeamMemory
			if memory != "" {
				teamMem = cmdutil.CanonicalMemoryRef(memory)
			}
			if teamMem == "" && kind != "pr" && kind != "branch" {
				return exitcode.Newf(exitcode.Usage,
					"an %s milestone lives only in the team worklog — pass -m <team-memory> (or record it with `session start -m`), after a one-time `hadron team init`", kind)
			}
			client, err := f.GraphQLClient()
			if err != nil {
				return err
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
			if teamMem != "" {
				// #396: the dedicated operation owns the record — its field set, the
				// `at` stamp, and the persona derivation (the server resolves the
				// session's persona, or the attributed user's handle, rather than
				// trusting the binding's copy). The CLI used to compose all of that
				// and write it through the generic object surface, which put the
				// record shape in two places with nothing keeping them in step.
				appRef, aerr := appForTeamMemory(ctx, client, teamMem)
				if aerr != nil {
					return aerr
				}
				var detailArg *json.RawMessage
				if detailRaw != nil {
					detailArg = &detailRaw
				}
				logged, rerr := gen.RecordTeamWork(
					ctx, client, appRef, b.SessionID, b.Tool, kind, canonical, action, detailArg,
				)
				if rerr != nil {
					// A session that started without an App cannot record work, and
					// no mutation can bind it after the fact — so say what to do
					// instead of surfacing the bare typed code.
					if api.HasErrorCode(rerr, "SESSION_NOT_IN_APP") {
						return exitcode.Newf(exitcode.Usage,
							"session %s is not bound to this team App, so it cannot record work — a session started before App binding (or without -m) cannot be bound afterwards; run `hadron team session start --as %s -m %s` to start a bound one",
							b.SessionID, b.PersonaName, teamMem)
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
				fmt.Fprintf(f.IOStreams.ErrOut, "note: no team memory recorded — only the Session field was set; `hadron team init` + `session start -m <memory>` enable the worklog\n")
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
	cmd.Flags().StringVarP(&memory, "memory", "m", "", "team App memory override (defaults to the binding's)")
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
// holding its persona.
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
	var as, org, repo, pr, issue, commit, branch, memory string
	var active bool
	var limit, offset int
	cmd := &cobra.Command{
		Use:     "list [--active] [--as <persona>] [--repo <r>] | (--pr | --issue | --commit | --branch) <ref> [-m <team-memory>]",
		Aliases: []string{"ls"},
		Short:   "List sessions — team presence and session provenance",
		Long: `List sessions, newest first, with each session's persona joined in.
--active narrows to sessions that never ended — team presence, honest
since stale sessions are auto-expired server-side; --as narrows to
one persona's sessions. Both narrow client-side over the full list; plain
listings page server-side.

--pr <ref> is THE provenance query: which sessions produced this PR? It
looks the normalized ref up in the team worklog (` + "`session log`" + ` writes it)
and resolves each recorded session — several rows per PR are expected and
desirable (a PR spanning three sessions yields three transcripts).
--issue/--commit/--branch ask the same question of the other artifact
kinds. The worklog home comes from -m or the worktree binding. A recorded
session that is no longer visible to you still lists (id only), rather
than being silently dropped.`,
		Example: `  hadron team session list --active
  hadron team session list --pr 371 -m acme.com::eng-team
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
				return runProvenanceQuery(cmd, f, client, kind, ref, memory, org)
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
	cmd.Flags().StringVar(&pr, "pr", "", "provenance query: sessions behind this PR (number, owner/repo#N, or URL)")
	cmd.Flags().StringVar(&issue, "issue", "", "provenance query: sessions behind this issue")
	cmd.Flags().StringVar(&commit, "commit", "", "provenance query: sessions behind this commit")
	cmd.Flags().StringVar(&branch, "branch", "", "provenance query: sessions behind this branch")
	cmd.Flags().StringVarP(&memory, "memory", "m", "", "team App memory holding the worklog (defaults to the binding's)")
	cmd.MarkFlagsMutuallyExclusive("pr", "issue", "commit", "branch")
	cmd.Flags().IntVar(&limit, "limit", 0, "max results (server default when unset)")
	cmd.Flags().IntVar(&offset, "offset", 0, "results to skip")
	return cmd
}

// runProvenanceQuery answers "which sessions produced this artifact":
// normalize the ref, equality-match (ref, kind) in the worklog, resolve each
// recorded session. The worklog is the N:M join (D13/D14) — the denormalized
// Session columns are never consulted.
func runProvenanceQuery(cmd *cobra.Command, f *cmdutil.Factory, client graphql.Client, kind, ref, memory, org string) error {
	ctx := cmd.Context()
	// The binding supplies defaults (worklog home, repo for bare numbers)
	// but the query must also run from an unbound checkout with -m.
	b, _, err := readBinding(ctx)
	if err != nil {
		b = nil
	}
	teamMem := ""
	if b != nil {
		teamMem = b.TeamMemory
	}
	if memory != "" {
		teamMem = cmdutil.CanonicalMemoryRef(memory)
	}
	if teamMem == "" {
		return exitcode.Newf(exitcode.Usage, "the provenance query reads the team worklog — pass -m <team-memory> (or record it with `session start -m`)")
	}
	canonical, _, err := normalizeArtifactRef(kind, ref, defaultRepo(ctx, b))
	if err != nil {
		return exitcode.New(exitcode.Usage, err)
	}
	// The worklog read addresses the APP (the team memory IS its shared
	// app-class memory), so resolve the memory the caller named — from -m or
	// the binding — to its App. Deliberately NOT resolveTeamApp: that prefers
	// --app / the configured App context, which would let the provenance query
	// answer for a different App than the team memory it is scoped to.
	appRef, err := appForTeamMemory(ctx, client, teamMem)
	if err != nil {
		return err
	}
	// #396: the dedicated provenance read replaces a generic findObjects loop
	// over the `worklog` collection. kind stays part of the match — PRs and
	// issues share GitHub's number space, so ref alone would mix an issue's
	// sessions into a PR's provenance — but the server now normalizes the ref
	// filter to the same canonical key it stored, so the client no longer has
	// to agree with it about spelling.
	sessionIDs := []string{}
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
			}
		}
		// Page to exhaustion — --pr promises the COMPLETE provenance set, and
		// an unbounded read is one default page (the issue-#23 rule).
		offset += len(items)
		if len(items) < pageSize {
			break
		}
	}
	roster, err := scanPersonaAgents(ctx, client, optStr(org))
	if err != nil {
		return err
	}
	nameByAgent := map[string]*string{}
	for i := range roster {
		nameByAgent[roster[i].Id] = roster[i].PersonaName
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
			// visibility-gap rule applies to fan-outs like this one).
			fmt.Fprintf(f.IOStreams.ErrOut, "note: session %s is recorded in the worklog but not visible to you\n", id)
			sessions = append(sessions, sessionDTO{ID: id})
			continue
		}
		sessions = append(sessions, sessionDTOFromFields(resp.Session.TeamSessionFields, personaNameFor(nameByAgent, resp.Session.AgentId)))
	}
	return output.Write(f.IOStreams, f.JSON, sessions, func(w io.Writer) error {
		t := output.NewTable(w, "SESSION", "PERSONA", "USER", "TOOL", "HOST", "MODEL", "STARTED", "TRANSCRIPT")
		for _, s := range sessions {
			t.Row(s.ID, dash(s.PersonaName), dash(s.UserID), dash(s.Tool), dash(s.Host), dash(s.LLMModel), s.StartedAt, dash(s.TranscriptPath))
		}
		return t.Flush()
	})
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
