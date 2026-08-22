package team

import (
	"context"
	"fmt"
	"io"
	"strings"

	"github.com/Khan/genqlient/graphql"
	"github.com/spf13/cobra"

	"github.com/hadron-memory/hadron-cli/internal/api"
	"github.com/hadron-memory/hadron-cli/internal/api/gen"
	"github.com/hadron-memory/hadron-cli/internal/cmdutil"
	"github.com/hadron-memory/hadron-cli/internal/exitcode"
	"github.com/hadron-memory/hadron-cli/internal/output"
)

func newCmdWorker(f *cmdutil.Factory) *cobra.Command {
	cmd := &cobra.Command{
		Use:     "worker <command>",
		Aliases: []string{"workers", "staff"},
		Short:   "Manage the team's staff — named castings of installed agents",
		Long: `A worker is the NAMED CASTING of an installed agent into this App
(cor:dmo:050:11): "Iris" is the backend-engineer agent cast into the
eng-team App. The name is unique per App and binds to this one casting
forever — retiring a worker keeps its name reserved (PR trailers and chat
history reference it), so there is no rename, and ` + "`worker rm`" + ` only removes
a casting that never did anything.

The worker carries the identity; the agent carries the reusable persona
dressing (role + prompt template, edited with ` + "`agent update`" + `). One agent
can be cast many times — Iris and Henry can both be the backend-engineer.

Commands take the worker's name (resolved within the App from --app, the
App context, or the worktree binding), its URN, or its id. The URN
(hrn:worker:<root>:<app-slug>:<slug>, #991) keys on a permanent DERIVED
slug — never on the name, since two names may legally slugify to one token
— and needs no App context, which makes it the portable way to name a
worker in scripts.`,
	}
	cmd.AddCommand(newCmdWorkerCast(f))
	cmd.AddCommand(newCmdWorkerList(f))
	cmd.AddCommand(newCmdWorkerGet(f))
	cmd.AddCommand(newCmdWorkerRelease(f))
	cmd.AddCommand(newCmdWorkerRetire(f))
	cmd.AddCommand(newCmdWorkerRm(f))
	return cmd
}

// castPreviewDTO is the stable --json shape of `worker cast --dry-run` —
// scalars plus the ids behind each name (the actionable refs). Reserved is
// always false: the preview holds nothing (cor:agt:020:03 — no lease by law).
//
// `teamAgentId`/`teamAgentName` are GONE (hadron-cli#496): they named the Team
// Agent whose register resolved the name, and there is no register. Casting
// reads no system memory at all now, so there is nothing to report.
type castPreviewDTO struct {
	Name               string  `json:"name"`
	Role               *string `json:"role"`
	AgentID            string  `json:"agentId"`
	AgentName          string  `json:"agentName"`
	Prompt             *string `json:"prompt"`
	HasNamePlaceholder *bool   `json:"hasNamePlaceholder"`
	Reserved           bool    `json:"reserved"`
}

func newCmdWorkerCast(f *cmdutil.Factory) *cobra.Command {
	var role, name, agentRef, promptOverride string
	var dryRun bool
	cmd := &cobra.Command{
		Use:   "cast --name <n> (--role <role> | --agent <ref>) [--prompt-override <text>] [--dry-run]",
		Short: "Cast an installed agent as a named worker",
		Long: `Cast a worker: ONE platform call (castWorker, cor:agt:020:01). The server
resolves the agent — --agent names it directly (it must be installed in
this App), or --role picks the single installed agent whose persona role
matches (zero or several candidates refuse typed, never a guess) — takes
the name, and provisions the worker's working memory.

--name IS REQUIRED (hadron-server#1050). A name is PERMANENT for this App
— retirement and uninstall never free it (cor:agt:020:02) — so it is
chosen, never derived: the server will not invent a permanent identifier
nobody picked. A cast without one refuses WORKER_NAME_REQUIRED. The claim
is one attempt, and WORKER_NAME_TAKEN is the answer rather than a retry;
` + "`worker list --include-retired`" + ` shows what is already taken —
retired workers keep their names, so the default listing under-reports it.

This used to allocate for you, walking the role's cast-list register past
taken names. That register is gone, so a bare --role no longer casts.

--role is also no longer validated against defined roles (cor:agt:020:00
§1 — cast lists are ergonomics, never a gate). An unrecognized role simply
resolves no agent (WORKER_AGENT_NOT_FOUND); with an explicit --agent it is
just the casting's label.

--prompt-override layers per-worker individuality over the agent's prompt
template; the resolved boot briefing (template with {{name}}/{{role}}
bound, then the override) is printed on success.

--dry-run (#404, castWorkerPreview) runs the cast's EXACT resolution —
same arguments, same typed refusals — up to but not
including the writes, and shows what would be created: the name, the
agent, the composed prompt. A refusal on the dry run IS the answer that
the real cast would refuse the same way. THE PREVIEW RESERVES NOTHING:
no lease exists, so a previewed name may be gone at cast time.

The team App comes from the persistent --app flag (or the configured App
context).`,
		Example: `  hadron team worker cast --app acme.com:eng-team --role backend-engineer --name Iris
  hadron team worker cast --app acme.com:eng-team --agent hrn:agent:acme.com:qa --name Uma`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			appRef, err := f.App()
			if err != nil {
				return err
			}
			if appRef == "" {
				return exitcode.Newf(exitcode.Usage, "no team App — pass --app <ref> (or set a default App context)")
			}
			if role == "" && agentRef == "" {
				return exitcode.Newf(exitcode.Usage, "pass --role <role> or --agent <ref> — the server never guesses the agent to cast")
			}
			// Refused HERE rather than relayed. The server's
			// WORKER_NAME_REQUIRED carries the reason but not the remedy, and
			// this is the flag whose absence used to be the NORMAL way to cast
			// — every doc, task node and muscle memory in the team says
			// `cast --role <r>` and nothing else (hadron-cli#496). Someone
			// hitting this is following instructions that were correct last
			// week, so the message has to say what to type now.
			// NORMALIZED, not just validated (PR #500 review). Checking
			// TrimSpace while sending the raw value let `--name " Iris "` past
			// the guard and cast a worker whose name literally carries the
			// spaces — and that name is PERMANENT for the App
			// (cor:agt:020:02), so there is no rename and `worker rm` only
			// helps while the worker has never been used. Treating whitespace
			// as non-semantic for the check and as semantic on the wire is the
			// worst of both: it also produces a WORKER_NAME_TAKEN nobody can
			// explain, since the roster shows the trimmed spelling.
			name = strings.TrimSpace(name)
			if name == "" {
				// --include-retired, deliberately (PR #500 review). A retired
				// worker keeps its name FOREVER, and `worker list` hides
				// retired staff by default — so the plain listing under-reports
				// what is taken, and a reader picking an apparently-free name
				// from it gets WORKER_NAME_TAKEN. A remedy that points at an
				// incomplete answer is the failure this whole message exists to
				// avoid.
				return exitcode.Newf(exitcode.Usage,
					"--name is required: a worker name is permanent for this App, so it is chosen rather than derived — pass --name <n> (`hadron team worker list --include-retired` shows the names already taken; retired workers keep theirs)")
			}
			client, err := f.GraphQLClient()
			if err != nil {
				return err
			}
			if dryRun {
				// A Query, not a dryRun flag on the mutation (#964): previewing
				// needs no mutation permission and mints no fake Worker row.
				// Same typed refusals as the cast — surfaced verbatim, because
				// a refusal here IS the dry-run's answer.
				preview, perr := gen.CastWorkerPreview(cmd.Context(), client, appRef,
					optStr(agentRef), optStr(role), optStr(name), optStr(promptOverride))
				if perr != nil {
					return api.MapError(perr)
				}
				if preview.CastWorkerPreview == nil {
					return exitcode.Newf(exitcode.Error, "server returned no preview")
				}
				p := preview.CastWorkerPreview
				dto := castPreviewDTO{
					Name: p.Name, Role: p.Role, AgentID: p.AgentId, AgentName: p.AgentName,
					Prompt: p.Prompt, HasNamePlaceholder: p.HasNamePlaceholder,
				}
				return output.Write(f.IOStreams, f.JSON, dto, func(w io.Writer) error {
					fmt.Fprintf(w, "would cast %s%s — agent %s (%s)\n", dto.Name, roleSuffix(dto.Role), dto.AgentName, dto.AgentID)
					if dto.HasNamePlaceholder != nil && !*dto.HasNamePlaceholder {
						fmt.Fprintf(w, "  ! the agent's prompt template never binds {{name}} — this worker would be nameless in its own briefing\n")
					}
					if dto.Prompt != nil && *dto.Prompt != "" {
						fmt.Fprintf(w, "\n%s\n", *dto.Prompt)
					}
					fmt.Fprintf(w, "\nnothing was created, and %q is NOT reserved — it may be gone at cast time\n", dto.Name)
					return nil
				})
			}
			resp, err := gen.CastWorker(cmd.Context(), client, appRef,
				optStr(agentRef), optStr(role), optStr(name), optStr(promptOverride))
			if err != nil {
				// Thin by directive: the server's typed errors carry the
				// guidance (WORKER_AGENT_AMBIGUOUS says to pass --agent) — map
				// to exit codes, surface the message verbatim. The one
				// exception is WORKER_NAME_REQUIRED, refused above, because it
				// is the refusal a reader following last week's instructions
				// will hit.
				return api.MapError(err)
			}
			if resp.CastWorker == nil {
				return exitcode.Newf(exitcode.Error, "server returned no worker")
			}
			dto := workerDTOFromFields(resp.CastWorker.WorkerFields)
			return output.Write(f.IOStreams, f.JSON, dto, func(w io.Writer) error {
				if _, err := fmt.Fprintf(w, "✓ cast %s%s — worker %s\n", dto.Name, roleSuffix(dto.Role), dto.ID); err != nil {
					return err
				}
				if dto.Prompt != nil && *dto.Prompt != "" {
					if _, err := fmt.Fprintf(w, "\n%s\n", *dto.Prompt); err != nil {
						return err
					}
				}
				return nil
			})
		},
	}
	cmd.Flags().StringVar(&role, "role", "", "cast by role: the single installed agent whose persona role matches")
	cmd.Flags().StringVar(&agentRef, "agent", "", "cast this agent (ID or URN; must be installed in the App)")
	cmd.Flags().StringVar(&name, "name", "", "the worker's name — REQUIRED, and permanent for this App")
	cmd.Flags().StringVar(&promptOverride, "prompt-override", "", "per-worker individuality appended to the agent's prompt template")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "preview the cast (name, agent, composed prompt) without creating anything; reserves nothing")
	return cmd
}

func newCmdWorkerList(f *cmdutil.Factory) *cobra.Command {
	var includeRetired bool
	cmd := &cobra.Command{
		Use:     "list [--include-retired]",
		Aliases: []string{"ls"},
		Short:   "The team's staff — this App's workers",
		Long: `List the App's workers (cor:agt:020:01: the staff are the Workers; the
AppAgent join is the install roster, ` + "`hadron app agent list`" + `). The team
App resolves from --app (or the configured App context), falling back to
the worktree binding's team memory.

That resolution is ambient, so the human output opens with the App it
landed on AND where that came from (#458) — the same bare command in two
worktrees bound to different teams lists different staff, and without the
scope line the two outputs look identical.

Retired workers are hidden unless --include-retired — their names stay
bound to them forever, so the retired list is also the reserved-name list.`,
		Example: `  hadron team worker list --app acme.com:eng-team
  hadron team worker list --include-retired --json`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			b, err := readBindingOrNilWithApp(ctx, f)
			if err != nil {
				return err
			}
			scope, err := resolveTeamAppScope(ctx, f, b)
			if err != nil {
				return err
			}
			client, err := f.GraphQLClient()
			if err != nil {
				return err
			}
			// The ROSTER projection, not the prompt-bearing one (#459): this
			// answers "who is on staff", and the resolved briefing is a detail
			// of one worker. `scanWorkers` stays as it is because resolveWorker
			// rides it — session start's boot briefing needs the prompt.
			rows, err := scanWorkerRoster(ctx, client, scope.Ref)
			if err != nil {
				return err
			}
			workers := []workerRosterDTO{}
			for _, w := range rows {
				dto := workerRosterDTOFromFields(w)
				if dto.Retired && !includeRetired {
					continue
				}
				workers = append(workers, dto)
			}
			return output.Write(f.IOStreams, f.JSON, workers, func(w io.Writer) error {
				// Whose staff, and why this App — the scope line runs BEFORE the
				// table and on an empty staff too, which is exactly the moment
				// "did I point this at the right team?" is worth answering.
				// Human branch only: the --json shape stays the bare array it has
				// always been, and already carries appId on every row.
				if _, err := fmt.Fprintf(w, "app: %s (%s)\n", describeApp(ctx, f, scope.Ref), scope.Source); err != nil {
					return err
				}
				// The worker URN replaces AGENT ID: it is the addressable handle
				// (the one #1008 signs with), and it is readable — the App slug is
				// in it. AGENT ID was the weakest column, an opaque id nobody acts
				// on; `worker get` still shows it.
				t := output.NewTable(w, "WORKER", "ROLE", "RETIRED", "URN", "ID")
				for _, wk := range workers {
					retired := "—"
					if wk.RetiredAt != nil {
						retired = *wk.RetiredAt
					}
					t.Row(wk.Name, dash(wk.Role), retired, dash(wk.URN), wk.ID)
				}
				return t.Flush()
			})
		},
	}
	cmd.Flags().BoolVar(&includeRetired, "include-retired", false, "include retired workers (their names stay reserved)")
	return cmd
}

// workerForArg resolves a worker command's positional argument: the App scope
// comes from --app / the App context / the worktree binding when available,
// and a worker id works without any of them.
func workerForArg(cmd *cobra.Command, f *cmdutil.Factory, arg string) (gen.WorkerFields, error) {
	var zero gen.WorkerFields
	ctx := cmd.Context()
	client, err := f.GraphQLClient()
	if err != nil {
		return zero, err
	}
	b, err := readBindingOrNilWithApp(ctx, f)
	if err != nil {
		b = nil
	}
	appRef, err := resolveTeamApp(ctx, f, b)
	if err != nil {
		appRef = "" // no App scope — resolveWorker still handles the id form
	}
	return resolveWorker(ctx, client, appRef, arg)
}

func newCmdWorkerGet(f *cmdutil.Factory) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "get <name-or-id>",
		Short: "Show a worker (by name within the App, or by id)",
		Example: `  hadron team worker get Iris
  hadron team worker get wkr_123 --json`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			w, err := workerForArg(cmd, f, args[0])
			if err != nil {
				return err
			}
			client, err := f.GraphQLClient()
			if err != nil {
				return err
			}
			dto := workerDTOFromFields(w)
			return output.Write(f.IOStreams, f.JSON, dto, func(out io.Writer) error {
				// The App gets its own line, named rather than spelled as the
				// UUID it was parenthesised as (#458). No source phrase here: this
				// is the WORKER's own App off the row, not an ambient scope that
				// might have been the wrong one.
				fmt.Fprintf(out, "%s%s\n  worker: %s\n  app: %s\n  agent: %s\n",
					dto.Name, roleSuffix(dto.Role), dto.ID, describeApp(cmd.Context(), f, dto.AppID), dto.AgentID)
				if dto.URN != nil && *dto.URN != "" {
					fmt.Fprintf(out, "  urn: %s\n", *dto.URN)
				}
				if dto.MemoryID != nil {
					fmt.Fprintf(out, "  memory: %s\n", *dto.MemoryID)
				}
				// The HOLD (cor:agt:020:09) — whose name this is, which is a
				// different question from whether a session is live. Rendered
				// only when known: heldByUserId masks to null on deny, so
				// printing "held by: —" would answer "nobody" to a caller who
				// merely cannot see, and this is the surface someone checks
				// before asking for a name.
				if dto.HeldByUserID != nil {
					fmt.Fprintf(out, "  held by: %s", describeHolder(cmd.Context(), client, *dto.HeldByUserID))
					if dto.HeldAt != nil {
						fmt.Fprintf(out, " (since %s)", *dto.HeldAt)
					}
					fmt.Fprintln(out)
				}
				if dto.Retired {
					fmt.Fprintf(out, "  retired: %s\n", *dto.RetiredAt)
				}
				fmt.Fprintf(out, "  created: %s\n", dto.CreatedAt)
				if dto.Prompt != nil && *dto.Prompt != "" {
					fmt.Fprintf(out, "\n%s\n", *dto.Prompt)
				}
				return nil
			})
		},
	}
	return cmd
}

// retireResultDTO is the stable --json shape of `worker retire`.
type retireResultDTO struct {
	ID        string  `json:"id"`
	Name      string  `json:"name"`
	RetiredAt *string `json:"retiredAt"`
	Status    string  `json:"status"`
}

func newCmdWorkerRetire(f *cmdutil.Factory) *cobra.Command {
	var yes bool
	cmd := &cobra.Command{
		Use:   "retire <name-or-id>",
		Short: "Retire a worker — it stops working; its name stays reserved forever",
		Long: `Retire a worker: it stops authoring and takes no new sessions, and its
name stays bound to it forever — PR trailers and chat history reference the
name, so it is never freed for a new casting (cor:agt:020:02). Retiring is
idempotent; there is no un-retire.

This is the normal end of a worker. The only removal is ` + "`worker rm`" + `, the
hard-delete escape for a casting that NEVER did anything (a typo'd cast);
anything with history refuses it and retires instead.`,
		Example: `  hadron team worker retire Iris --yes`,
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			w, err := workerForArg(cmd, f, args[0])
			if err != nil {
				return err
			}
			if err := cmdutil.Confirm(f.IOStreams, yes,
				fmt.Sprintf("Retire worker %s (%s)? It stops working; the name stays reserved for it forever.", w.Name, w.Id)); err != nil {
				return err
			}
			client, err := f.GraphQLClient()
			if err != nil {
				return err
			}
			resp, err := gen.RetireWorker(cmd.Context(), client, w.Id)
			if err != nil {
				return api.MapError(err)
			}
			if resp.RetireWorker == nil {
				return exitcode.Newf(exitcode.Error, "server returned no worker")
			}
			dto := workerDTOFromFields(resp.RetireWorker.WorkerFields)
			result := retireResultDTO{ID: dto.ID, Name: dto.Name, RetiredAt: dto.RetiredAt, Status: "retired"}
			return output.Write(f.IOStreams, f.JSON, result, func(out io.Writer) error {
				_, err := fmt.Fprintf(out, "✓ retired worker %s — the name stays bound to it and is never re-cast\n", dto.Name)
				return err
			})
		},
	}
	cmd.Flags().BoolVar(&yes, "yes", false, "skip the confirmation prompt")
	return cmd
}

// removeResultDTO is the stable --json shape of `worker rm`.
type removeResultDTO struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Status string `json:"status"`
}

func newCmdWorkerRm(f *cmdutil.Factory) *cobra.Command {
	var yes bool
	cmd := &cobra.Command{
		Use:     "rm <name-or-id>",
		Aliases: []string{"delete"},
		Short:   "Hard-delete a NEVER-USED casting (frees its name)",
		Long: `Hard-delete a worker that never did anything — the one removal escape
(cor:dmo:050:11), for a miscast: wrong role, typo'd name. The server refuses
with WORKER_IN_USE when any session was ever bound to it or its working
memory holds content; a worker with history retires instead
(` + "`worker retire`" + `), keeping its name reserved. Deleting also removes the
empty working memory, and — unlike retiring — frees the name.`,
		Example: `  hadron team worker rm Irsi --yes`,
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			w, err := workerForArg(cmd, f, args[0])
			if err != nil {
				return err
			}
			if err := cmdutil.ConfirmDeletion(f.IOStreams, yes,
				fmt.Sprintf("worker %s (%s) — only a never-used casting can be deleted; this frees its name", w.Name, w.Id)); err != nil {
				return err
			}
			client, err := f.GraphQLClient()
			if err != nil {
				return err
			}
			resp, err := gen.DeleteWorker(cmd.Context(), client, w.Id)
			if err != nil {
				return api.MapError(err)
			}
			if !resp.DeleteWorker {
				return exitcode.Newf(exitcode.Error, "worker %s was not deleted", w.Name)
			}
			result := removeResultDTO{ID: w.Id, Name: w.Name, Status: "deleted"}
			return output.Write(f.IOStreams, f.JSON, result, func(out io.Writer) error {
				_, err := fmt.Fprintf(out, "✓ deleted never-used worker %s — its name is free again\n", w.Name)
				return err
			})
		},
	}
	cmd.Flags().BoolVar(&yes, "yes", false, "skip the confirmation prompt")
	return cmd
}

// releaseResultDTO is the stable --json shape of `worker release`.
//
// `wasHeld` + `releasedFromUserId` describe the state BEFORE the call, because
// that is the only place the answer exists: the mutation returns the worker
// post-release, where the hold is null by definition. A consumer that wanted
// to know whether anything happened could not compute it from the payload
// otherwise.
type releaseResultDTO struct {
	ID   string  `json:"id"`
	Name string  `json:"name"`
	URN  *string `json:"urn"`
	// WasHeld is false for the idempotent no-op — releasing a name nobody
	// held. Distinguishing it is the point: `✓ released` on a no-op is a
	// receipt for something that did not happen.
	//
	// NULLABLE for the same reason as Forced: heldByUserId masks to null on
	// DENY, so a nil hold can also mean "held, but not visible to you". Null
	// says exactly that. `false` there would read as "this name is free to
	// bind", and a caller acting on it meets WORKER_HELD at the next
	// `session start` (PR #504 review).
	WasHeld *bool `json:"wasHeld"`
	// ReleasedFromUserID is the prior holder, null on a no-op. The ID, not a
	// name (review:entity-fields-not-display-labels) — it addresses a person
	// the caller may need to contact.
	ReleasedFromUserID *string `json:"releasedFromUserId"`
	// Forced is true when the caller was NOT the prior holder — an admin
	// force-release, which the server announces in the team chat. Surfaced so
	// a scripted caller can tell a routine hand-back from a visible override.
	//
	// NULLABLE, and only ever null when the caller's own identity could not be
	// read against a held name (PR #504 review). `false` would claim a private
	// act and `true` an announcement, and both are assertions the CLI cannot
	// make there — the same reason workerDTO has no `held` boolean.
	Forced *bool  `json:"forced"`
	Status string `json:"status"`
}

// currentUserID reads the caller's own user id. THREE states, not two
// (PR #504 review): the id; "" with known=true for a caller that definitively
// has no user; and known=false when the lookup itself FAILED.
//
// Collapsing the last two is the "unknown is not none" mistake
// (review:a-claim-must-not-outrun-its-evidence). A failed AuthContext read is
// not evidence that the caller is somebody else — treating it as such
// reclassifies a legitimate self-release as a force-release, refuses it
// non-interactively, and then claims a team-chat announcement the server never
// made.
//
// authContext rather than `me` on purpose: it is credential-agnostic, so an
// App-key caller resolves to a null user rather than an error. That IS an
// answer — per cor:agt:020:09 an App key holds nothing, so such a caller is
// never the holder.
func currentUserID(ctx context.Context, client graphql.Client) (id string, known bool) {
	resp, err := gen.AuthContext(ctx, client)
	if err != nil || resp.AuthContext == nil {
		return "", false
	}
	if resp.AuthContext.User == nil {
		return "", true // an App key holds nothing: definitively not the holder
	}
	return resp.AuthContext.User.Id, true
}

// describeHolder renders a prior holder for a human prompt: their name when it
// can be read, the raw id when it cannot.
//
// Decoration, never a gate — the same posture as describeApp. A caller
// entitled to release a name may not be entitled to read the holder's user
// record, and failing the release over a display label would be absurd.
func describeHolder(ctx context.Context, client graphql.Client, userID string) string {
	resp, err := gen.GetUser(ctx, client, userID)
	if err != nil || resp.User == nil {
		return userID
	}
	name, handle := "", ""
	if resp.User.Name != nil {
		name = *resp.User.Name
	}
	if resp.User.Handle != nil {
		handle = *resp.User.Handle
	}
	switch {
	case name != "" && handle != "":
		return fmt.Sprintf("%s (@%s)", name, handle)
	case handle != "":
		return "@" + handle
	case name != "":
		return fmt.Sprintf("%s (%s)", name, userID)
	default:
		return userID
	}
}

func newCmdWorkerRelease(f *cmdutil.Factory) *cobra.Command {
	var yes bool
	cmd := &cobra.Command{
		Use:   "release <name-or-id>",
		Short: "Release the hold on a worker name, so somebody else can take it",
		Long: `Release the HOLD on a worker name (cor:agt:020:09). A name is held by a
PERSON, and this is the ONLY thing that frees one — not ` + "`session end`" + `, not
an idle window, not an expiry, not a reap, not closing your chat session.

RELEASING IS NOT RETIRING. The worker keeps working, keeps its name, keeps
its history; the name is not freed for a different casting (it stays
permanently allotted to this one, cor:agt:020:02). All that changes is who
may bind it next. To stop a worker instead, use ` + "`worker retire`" + `.

A RETIRED worker can be released — the call succeeds — but nobody can bind
it afterwards either way (` + "`session start`" + ` refuses WORKER_RETIRED whether or
not the name is held). Releasing one clears the hold and nothing else; the
receipt and the confirmation say so for the worker in front of you.

WHAT GOES WITH IT: the worker's working memory and handoff history follow
the NAME, not the holder — so releasing hands your notes to whoever takes
it next. That is the intended transfer, and the reason nothing private
belongs in a worker memory. (For a retired worker there is no next holder,
so they simply stay with the name.)

TWO ACTS, and the server decides which you may perform. Releasing YOUR OWN
name owes nobody notice. Releasing SOMEONE ELSE'S is an admin
force-release: it exists so a departed colleague's names are not held
forever, and it ANNOUNCES ITSELF IN THE TEAM CHAT, naming you and them.
This command tells you which one you are about to do, and asks first when
it is the second.

Idempotent: releasing a name nobody holds changes nothing and says so.`,
		Example: `  hadron team worker release Iris
  hadron team worker release hrn:worker:acme.com:eng-team:iris --yes`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			w, err := workerForArg(cmd, f, args[0])
			if err != nil {
				return err
			}
			client, err := f.GraphQLClient()
			if err != nil {
				return err
			}
			// The hold BEFORE the call. The mutation returns the worker
			// post-release, where it is null by construction, so this is the
			// only moment the prior state is knowable.
			priorHolder := w.HeldByUserId
			// NO VISIBILITY PROBE. An earlier version inferred one from
			// prompt/promptOverride/memoryId being readable, since heldByUserId
			// is masked alongside them. That is UNSOUND: all three are
			// legitimately nullable — an agent with no template, no per-worker
			// override, and a best-effort memory provision that failed — so a
			// real, visible, unheld worker read as "cannot see" and got hedged
			// output plus a spurious prompt. The repo's own retiredWorkerJSON
			// fixture has exactly that shape (PR #504 review).
			//
			// There is no explicit "can you read working state" signal on
			// Worker, so a nil hold is IRREDUCIBLY ambiguous: unheld, or held
			// and masked from you. The command therefore never claims which,
			// and does not prompt — prompting on every idempotent no-op would
			// spend the case #495 asked to keep quiet in exchange for a guess.
			// hadron-server#1073 asks for the prior holder in the payload,
			// which resolves this outright: the receipt could report what
			// happened rather than predict it.
			me, meKnown := currentUserID(ctx, client)
			// A THREE-state answer: yes, no, or "cannot tell". Nil only when
			// the identity lookup failed against a HELD name — the one case
			// where we genuinely do not know whether this is a public act. It
			// is treated as force for the PROMPT (conservative) and as unknown
			// in the RECEIPT (honest); those are different jobs.
			var forced, wasHeld *bool
			if priorHolder != nil {
				wasHeld = boolPtr(true)
				if meKnown {
					forced = boolPtr(*priorHolder != me)
				}
			}
			// Both stay nil on a nil hold, and stay nil HONESTLY: unheld and
			// masked-from-you are indistinguishable here.

			// Only the force branch prompts. A self-release loses nothing and
			// notifies nobody, and a prompt on the ordinary end-of-work step
			// is the kind people learn to --yes past reflexively — which would
			// then also skip the prompt that matters.
			//
			// Confirm, not ConfirmDeletion: nothing is destroyed, and "This
			// cannot be undone" would be false — the next holder can release
			// it back.
			if priorHolder != nil && (forced == nil || *forced) && !yes {
				// Built only when a prompt can be shown: describeHolder costs
				// a read, and the prompt is an ARGUMENT to Confirm, so
				// composing it unconditionally would make --yes pay for a
				// string Confirm discards.
				// The KNOWN force branch states it flatly. Reusing the "if it
				// is not" hedge there — written for the unknown-identity branch
				// — made the one case we are CERTAIN about sound conditional,
				// which is backwards (PR #504 review).
				//
				// The transfer clause is instance-specific either way: nobody
				// takes a retired name, so promising a next holder there is the
				// same false promise the receipt avoids.
				prompt := releasePrompt(w.Name, describeHolder(ctx, client, *priorHolder),
					w.RetiredAt, forced != nil)
				if err := cmdutil.Confirm(f.IOStreams, false, prompt); err != nil {
					return err
				}
			}

			// RE-READ the hold immediately before mutating (PR #504 review, P1).
			//
			// releaseWorker takes no precondition and does not return the prior
			// holder, so the hold can change between the pre-read and the call.
			// An admin could approve a prompt naming one person and release
			// another — and worse, a pre-read showing "unheld" or "me" skips the
			// prompt, so a hold taken in between would be force-released
			// SILENTLY while the receipt reported a self-release. The act
			// performed would differ from the act described.
			//
			// This does NOT close the race. It narrows it from human thinking
			// time at a prompt to one round trip, and turns the silent-force
			// case into a refusal. Closing it needs an expectedHolder
			// precondition or the prior holder in the payload — hadron-server#1073.
			fresh, ferr := gen.GetWorker(ctx, client, w.Id)
			if ferr != nil {
				return api.MapError(ferr)
			}
			if fresh.Worker == nil {
				return exitcode.Newf(exitcode.NotFound, "no worker %q", w.Id)
			}
			if !sameHolder(priorHolder, fresh.Worker.HeldByUserId) {
				return exitcode.Newf(exitcode.Conflict,
					"the hold on %s changed while this ran — it is %s now, so releasing it would not be the act just "+
						"described; re-run to see the current state",
					w.Name, holderPhrase(ctx, client, fresh.Worker.HeldByUserId))
			}
			// RETIREMENT too, not only the holder. The confirmation's transfer
			// clause branches on it — "to whoever takes the name next" versus
			// "stays with the name" — so a retirement landing between the
			// prompt and the call leaves the caller having approved a
			// description that no longer fits (PR #504 review).
			if (w.RetiredAt == nil) != (fresh.Worker.RetiredAt == nil) {
				state := "retired"
				if fresh.Worker.RetiredAt == nil {
					state = "un-retired"
				}
				return exitcode.Newf(exitcode.Conflict,
					"%s was %s while this ran, which changes what releasing it means; re-run to see the current state",
					w.Name, state)
			}

			resp, err := gen.ReleaseWorker(ctx, client, w.Id)
			if err != nil {
				return api.MapError(err)
			}
			if resp.ReleaseWorker == nil {
				return exitcode.Newf(exitcode.Error, "server returned no worker")
			}
			dto := workerDTOFromFields(resp.ReleaseWorker.WorkerFields)
			result := releaseResultDTO{
				ID: dto.ID, Name: dto.Name, URN: dto.URN,
				WasHeld: wasHeld, ReleasedFromUserID: priorHolder,
				Forced: forced, Status: "released",
			}
			if priorHolder == nil {
				result.Status = "no-visible-hold"
			}
			return output.Write(f.IOStreams, f.JSON, result, func(out io.Writer) error {
				// The nil-hold case is reported as what it IS: no hold was
				// visible. Not "was not held" — heldByUserId masks to null on
				// deny, so nil means "unheld OR held and invisible to you", and
				// there is no sound way to tell them apart from here.
				//
				// The distinction matters because of what a reader DOES with
				// it: "was not held" reads as "this name is free to bind", and
				// a caller acting on that meets WORKER_HELD at the next
				// `session start`. One extra word buys the difference between
				// an honest report and a confident wrong one.
				if priorHolder == nil {
					_, err := fmt.Fprintf(out,
						"· no hold on %s was visible to you — nothing was released that you could see\n", dto.Name)
					return err
				}
				switch {
				case result.Forced == nil:
					// Never claim an announcement we cannot verify, and never
					// hide one that may have happened. Saying "announced" would
					// assert a public act that may not have occurred; saying
					// nothing would conceal one that did.
					if _, err := fmt.Fprintf(out,
						"✓ released %s (previously held by %s) — your own identity could not be read, so if that "+
							"was not you, the server will have posted a notice to the team chat\n",
						dto.Name, describeHolder(ctx, client, *priorHolder)); err != nil {
						return err
					}
				case *result.Forced:
					// "posts", not "announced". The notification is BEST-EFFORT
					// server-side — an unreachable chat never blocks the release
					// — and the payload carries no delivery signal, so asserting
					// the notice appeared is a third claim this command cannot
					// verify (PR #504 review). The PROMPT still says POSTS TO
					// THE TEAM CHAT in the strong form: that is a warning about
					// what the act IS, before you consent to it, and overstating
					// there errs toward caution. This is a report of what
					// happened, and errs toward accuracy.
					if _, err := fmt.Fprintf(out,
						"✓ force-released %s from %s — the server posts a notice to the team chat (best-effort)\n",
						dto.Name, describeHolder(ctx, client, *priorHolder)); err != nil {
						return err
					}
				default:
					// "anyone may bind it now" is FALSE for a retired worker —
					// startSession refuses one (WORKER_RETIRED) whether or not
					// its name is held, so releasing the hold frees nothing a
					// caller can use. Found by sweeping every sentence this
					// command prints and asking what proves each, after the
					// third unverifiable claim in one review (PR #504).
					next := "anyone may bind it now"
					if dto.Retired {
						next = "the worker is retired, so nobody can bind it — the name is simply no longer held"
					}
					if _, err := fmt.Fprintf(out, "✓ released %s — %s\n", dto.Name, next); err != nil {
						return err
					}
				}
				_, err := fmt.Fprintf(out,
					"  its working memory and handoff history go with the name, not with you\n")
				return err
			})
		},
	}
	cmd.Flags().BoolVar(&yes, "yes", false, "skip the confirmation on an admin force-release (a self-release never prompts)")
	return cmd
}

func boolPtr(b bool) *bool { return &b }

// sameHolder compares two optional holder ids — nil-safe, since "unheld" is a
// legitimate value on both sides.
func sameHolder(a, b *string) bool {
	switch {
	case a == nil && b == nil:
		return true
	case a == nil || b == nil:
		return false
	default:
		return *a == *b
	}
}

// holderPhrase renders an optional holder for the changed-hold refusal.
func holderPhrase(ctx context.Context, client graphql.Client, userID *string) string {
	if userID == nil {
		return "unheld"
	}
	return "held by " + describeHolder(ctx, client, *userID)
}

// releasePrompt is the whole confirmation shown before a release that may not
// be the caller's own. Extracted for the same reason as its transfer clause:
// cmdutil.Confirm's prompt branch is unreachable without a TTY, so nothing
// could otherwise read the string it builds — and BOTH halves of it have now
// been wrong in review.
//
// classified=false is the unknown-identity branch and keeps a conditional. The
// KNOWN force branch states it flatly: reusing "if it is not" there made the
// one case we are certain about sound uncertain, which is backwards for a
// warning that a public act is about to happen (PR #504 review).
func releasePrompt(name, holder string, retiredAt *string, classified bool) string {
	if !classified {
		return fmt.Sprintf("%s is held by %s, and this CLI could not read your own identity to tell whether "+
			"that is you. If it is not, releasing it POSTS TO THE TEAM CHAT naming you and them, %s Continue?",
			name, holder, releasePromptTransferClause(retiredAt))
	}
	return fmt.Sprintf("%s is held by %s, not you. Releasing it POSTS TO THE TEAM CHAT naming you and them, "+
		"%s Continue?", name, holder, releasePromptTransferClause(retiredAt))
}

// releasePromptTransferClause is the half of the force prompt that says where
// the worker's notes go. Extracted so it is testable: cmdutil.Confirm's prompt
// branch is unreachable without a TTY, so a command-level test can never read
// the string it builds.
//
// The clause is INSTANCE-specific and must hold for THIS worker. Nobody takes a
// retired name (startSession refuses WORKER_RETIRED), so promising a next
// holder there is the same false promise the receipt avoids — caught in review
// after I fixed the receipts and not the prompt.
func releasePromptTransferClause(retiredAt *string) string {
	if retiredAt != nil {
		return "and its working memory and handoff history stay with the name — the worker is retired, " +
			"so nobody can bind it."
	}
	return "and hands that worker's working memory and handoff history to whoever takes the name next."
}
