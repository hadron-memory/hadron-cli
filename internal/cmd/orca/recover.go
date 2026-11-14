package orca

import (
	"context"
	"fmt"
	"io"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/hadron-memory/hadron-cli/internal/cmdutil"
	"github.com/hadron-memory/hadron-cli/internal/exitcode"
	"github.com/hadron-memory/hadron-cli/internal/hostpoller"
	"github.com/hadron-memory/hadron-cli/internal/orcaterm"
	"github.com/hadron-memory/hadron-cli/internal/output"
	ts "github.com/hadron-memory/hadron-cli/internal/terminalstatus"
	"github.com/spf13/cobra"
)

// recoverContract names the --json shape of `terminal recover|recreate`.
const recoverContract = "terminal-recover@1"

// Proc is one process as the never-two-agents scan sees it.
type Proc struct {
	PID  int
	Cwd  string
	Argv []string
}

// Transcript is one resumable Claude conversation file in the worktree's
// project directory: its id (the file stem) and when it was last written.
type Transcript struct {
	ID      string
	ModTime time.Time
}

// RecoverEnv is everything recover touches outside Orca and the server; tests
// inject fakes. A zero field means the real one.
type RecoverEnv struct {
	// Procs lists every process with its cwd and argv (/proc on Linux).
	Procs func() ([]Proc, error)
	// Transcripts lists the worktree's resumable transcripts. launch is the
	// worker's host.yaml launch line, which names the account's config dir.
	Transcripts func(worktree string, launch []string) ([]Transcript, error)
	// PromptStaged reports a non-empty staged recovery prompt for the worktree.
	PromptStaged func(worktree string) bool
	// Ancestors are this process and its parents' pids.
	Ancestors func() []int
	Sleep     func(time.Duration)
	// GoneWait and VerifyWait bound the two waits; they are counted in
	// pollEvery steps so a fake Sleep keeps the loops deterministic.
	GoneWait, VerifyWait time.Duration
}

const pollEvery = 500 * time.Millisecond

func (r RecoverEnv) withDefaults() RecoverEnv {
	if r.Procs == nil {
		r.Procs = scanProc
	}
	if r.Transcripts == nil {
		r.Transcripts = claudeTranscripts
	}
	if r.PromptStaged == nil {
		r.PromptStaged = stagedPrompt
	}
	if r.Ancestors == nil {
		r.Ancestors = selfAncestors
	}
	if r.Sleep == nil {
		r.Sleep = time.Sleep
	}
	if r.GoneWait == 0 {
		r.GoneWait = 20 * time.Second
	}
	if r.VerifyWait == 0 {
		r.VerifyWait = 30 * time.Second
	}
	return r
}

// --- output contract ---

type foundDTO struct {
	Status   string  `json:"status"`
	Reason   *string `json:"reason"`
	Terminal *string `json:"terminal"`
	// Session is the worker-session binding found in the worktree: "bound" or
	// "none". It is only read; recover never starts, takes over or ends one.
	Session string `json:"session"`
}

type transcriptDTO struct {
	ID         string `json:"id"`
	ModifiedAt string `json:"modifiedAt"`
	Why        string `json:"why"`
}

type createDTO struct {
	Worktree string `json:"worktree"`
	Title    string `json:"title"`
	Command  string `json:"command"`
}

type planDTO struct {
	Steps  []string  `json:"steps"`
	Close  []string  `json:"close"`
	Create createDTO `json:"create"`
	Fresh  bool      `json:"fresh"`
}

type interruptedDTO struct {
	Terminal string  `json:"terminal"`
	Status   string  `json:"status"`
	Prompt   string  `json:"prompt"`
	Reason   *string `json:"reason"`
}

type verificationDTO struct {
	Verified bool    `json:"verified"`
	Agents   int     `json:"agents"`
	Terminal *string `json:"terminal"`
	OnScreen *string `json:"onScreen"`
}

type refusalDTO struct {
	Code    int    `json:"code"`
	Reason  string `json:"reason"`
	Message string `json:"message"`
}

type recoverDTO struct {
	Contract     string           `json:"contract"`
	Action       string           `json:"action"`
	Worker       string           `json:"worker"`
	Worktree     string           `json:"worktree"`
	DryRun       bool             `json:"dryRun"`
	Result       string           `json:"result"`
	Found        *foundDTO        `json:"found"`
	Plan         *planDTO         `json:"plan"`
	Transcript   *transcriptDTO   `json:"transcript"`
	Interrupted  *interruptedDTO  `json:"interrupted"`
	Closed       []string         `json:"closed"`
	Created      *string          `json:"created"`
	Verification *verificationDTO `json:"verification"`
	Refusal      *refusalDTO      `json:"refusal"`

	// pending is the interrupted agent's visible question: screen text, so it
	// is printed in text mode only and is never part of the JSON.
	pending []string
}

// --- commands ---

type recoverOpts struct {
	config, worktree, diedAt, transcripts string
	dryRun, force, fresh                  bool
}

func newCmdRecover(f *cmdutil.Factory, env Env) *cobra.Command {
	return newRecoverLike(f, env, false)
}

func newCmdRecreate(f *cmdutil.Factory, env Env) *cobra.Command {
	return newRecoverLike(f, env, true)
}

func newRecoverLike(f *cmdutil.Factory, env Env, recreate bool) *cobra.Command {
	var o recoverOpts
	cmd := &cobra.Command{Args: cobra.ExactArgs(1), SilenceUsage: true}
	if recreate {
		cmd.Use = "recreate <worker> --config <host.yaml> [--dry-run]"
		cmd.Short = "Replace a worker's terminals with one fresh, supervised, resumed one"
		cmd.Long = recreateLong
		cmd.Example = "  hadron orca terminal recreate Bill --config ~/.config/hadron-host/host.yaml --dry-run\n  hadron orca terminal recreate Bill --config ~/.config/hadron-host/host.yaml --json"
	} else {
		cmd.Use = "recover <worker> --config <host.yaml> [--dry-run] [--force] [--fresh]"
		cmd.Short = "Bring a dark worker back: resumed, supervised, verified, never two agents"
		cmd.Long = recoverLong
		cmd.Example = "  hadron orca terminal recover Bill --config ~/.config/hadron-host/host.yaml --dry-run\n  hadron orca terminal recover Bill --config ~/.config/hadron-host/host.yaml\n  hadron orca terminal recover Bill --config ~/.config/hadron-host/host.yaml --force --json"
	}
	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		dto, err := runRecover(cmd.Context(), f, env, cmd, args[0], o, recreate)
		return renderRecover(f, dto, err)
	}
	fl := cmd.Flags()
	fl.StringVar(&o.config, "config", "", "host configuration `file` (the one team host run --config reads): the worker's launch line, worktrees and Orca binary")
	fl.BoolVar(&o.dryRun, "dry-run", false, "print the exact plan and change nothing")
	fl.StringVar(&o.worktree, "worktree", "", "the worker's worktree `path`, when host.yaml and Orca name more than one")
	fl.StringVar(&o.diedAt, "died-at", "", "when the agent died, as an RFC 3339 `time`; the transcript chosen is the newest written at or before it (default: now)")
	fl.StringVar(&o.transcripts, "transcripts-dir", "", "the Claude project `dir` holding the worktree's transcripts (default: derived from the launch line's CLAUDE_CONFIG_DIR)")
	fl.BoolVar(&o.fresh, "fresh", false, "no transcript to resume: launch without --resume on the staged recovery prompt")
	if !recreate {
		fl.BoolVar(&o.force, "force", false, "end a live (non-dark) agent first; refuse still if it cannot be shown gone")
	}
	return cmd
}

const recoverLong = `Brings a worker whose agent is gone back: its conversation resumed, supervised
and verified. It is the one command for the procedure that used to take six
steps over raw ` + "`orca`" + ` commands. Works only on the host that runs Orca and needs
the Orca CLI and the host's host.yaml (--config, the file team host run reads).

What it does, in order:
  1. classifies the worker's terminal as ` + "`status`" + ` does;
  2. dark: finds the worktree and the launch line in host.yaml (never guessed),
     picks the transcript (the newest last written at or before the death time,
     shown with why), creates one titled terminal that runs the launch line plus
     --resume <id>, and verifies it on screen;
  3. not dark (busy, idle, locked): refuses, naming the state, unless --force.
     --force closes the live agent's terminal first, says what was interrupted
     (for a locked agent, the question it was waiting on), and waits until its
     process is gone.

Acts by default; --dry-run prints the exact plan and changes nothing.

THE ONE GUARANTEE: never two agents. Before creating, it proves no agent
process is left in the worktree (the terminal list plus a scan of every
process's working directory, the agent binary first in argv, never a
contains-match on the wrapper); after creating, it proves exactly one is. If the
old agent cannot be shown gone it refuses and says why; it never signals a
process itself, and it refuses to run from inside the agent it would end.

It does not start, take over, rebind or end a worker session: the open session
stays, and the relaunched agent's first act is the ordinary bind. This is not the
platform's start-a-worker or end-session operation; it only starts and stops a
local terminal. --json reports whether a session binding was found.

Claude workers only (--resume <id>). A Codex or other worker is refused with its
own exit code. With no transcript at or before the death time it stops;
--fresh launches without --resume on the staged recovery prompt instead.

Exit codes: 0 done (or planned); 2 usage; 4 no such worker; 10 Orca not
reachable; 16 refused (a live agent, or one not shown gone); 17 no launch entry
or worktree in host.yaml; 18 not a Claude worker; 19 no safe transcript; 20 the
terminal was created but exactly one agent could not be shown.`

const recreateLong = `Replaces the worker's terminals outright: closes every terminal Orca lists for
the worker's worktree (the agent tab and stale bare shells), proves the old agent
is gone, then creates one fresh supervised, resumed terminal titled with the
worker's name and verifies it. Use it when a tab is wedged, hidden from the Orca
app, or duplicated. It implies recover's --force. Same guarantee, same flags, same
exit codes as ` + "`recover`" + ` (see ` + "`hadron orca terminal recover --help`" + `): never two
agents, no session start, takeover or end, Claude workers only.

--dry-run prints the plan and closes nothing.`

// recoverFail is a refusal or failure after the plan was made: it carries the
// partial DTO so --json still shows the plan, what was closed and the verdict.
type recoverFail struct {
	code   int
	reason string
	msg    string
}

func (e *recoverFail) Error() string { return e.msg }

func refuse(code int, reason, format string, a ...any) *recoverFail {
	return &recoverFail{code: code, reason: reason, msg: fmt.Sprintf(format, a...)}
}

func renderRecover(f *cmdutil.Factory, dto *recoverDTO, err error) error {
	var rf *recoverFail
	if e, ok := err.(*recoverFail); ok {
		rf = e
	}
	if dto == nil || (err != nil && rf == nil) {
		return err
	}
	if rf != nil {
		dto.Result = "refused"
		if rf.code == exitcode.RecoverUnverified {
			dto.Result = "unverified"
		}
		dto.Refusal = &refusalDTO{Code: rf.code, Reason: rf.reason, Message: rf.msg}
	}
	werr := output.Write(f.IOStreams, f.JSON, dto, func(w io.Writer) error { return writeRecoverText(w, dto) })
	if werr != nil {
		return werr
	}
	if rf != nil {
		if f.JSON {
			return exitcode.Silent(rf.code)
		}
		return exitcode.New(rf.code, rf)
	}
	return nil
}

func writeRecoverText(w io.Writer, d *recoverDTO) error {
	p := func(format string, a ...any) { _, _ = fmt.Fprintf(w, format+"\n", a...) }
	name := output.SafeCell(d.Worker)
	if d.Found != nil {
		line := fmt.Sprintf("%s: %s", name, d.Found.Status)
		if d.Found.Reason != nil {
			line += " (" + *d.Found.Reason + ")"
		}
		p("%s; worktree %s; session binding %s", line, output.SafeCell(d.Worktree), d.Found.Session)
	}
	if d.Interrupted != nil {
		p("interrupted: %s (%s) in %s", d.Interrupted.Status, d.Interrupted.Prompt, d.Interrupted.Terminal)
		for _, l := range d.pending {
			p("  | %s", output.SafeCell(l))
		}
	}
	if d.Plan != nil {
		if d.DryRun {
			p("dry run — nothing was changed. Plan:")
		} else {
			p("plan:")
		}
		for i, s := range d.Plan.Steps {
			p("  %d. %s", i+1, output.SafeCell(s))
		}
	}
	if d.Transcript != nil {
		p("transcript: %s (written %s): %s", d.Transcript.ID, d.Transcript.ModifiedAt, d.Transcript.Why)
	}
	if d.Plan != nil && d.DryRun {
		p("would run: %s", output.SafeCell(d.Plan.Create.Command))
	}
	if len(d.Closed) > 0 {
		p("closed: %s", strings.Join(d.Closed, ", "))
	}
	if d.Created != nil {
		p("created: %s", *d.Created)
	}
	if v := d.Verification; v != nil {
		if v.Verified {
			p("verified: exactly one agent in the worktree, terminal %s shows an agent", deref(v.Terminal))
		} else {
			p("NOT verified: %d agent process(es) in the worktree after creating", v.Agents)
		}
	}
	if d.Refusal == nil { // a refusal is printed once, by the error that carries its exit code
		p("%s", d.Result)
	}
	return nil
}

// --- the procedure ---

func runRecover(ctx context.Context, f *cmdutil.Factory, env Env, cmd *cobra.Command, arg string, o recoverOpts, recreate bool) (*recoverDTO, error) {
	if strings.TrimSpace(o.config) == "" {
		return nil, exitcode.Newf(exitcode.Usage, "--config needs the host configuration file (the one team host run --config reads)")
	}
	// An explicitly empty flag (--died-at="$UNSET") is not an absent one: it
	// would silently pick the default and, for --died-at, the wrong transcript.
	for _, name := range []string{"worktree", "died-at", "transcripts-dir"} {
		if cmd.Flags().Changed(name) {
			if v, _ := cmd.Flags().GetString(name); strings.TrimSpace(v) == "" {
				return nil, exitcode.Newf(exitcode.Usage, "--%s was given empty: pass a value or leave the flag out", name)
			}
		}
	}
	var died time.Time
	if o.diedAt != "" {
		t, err := time.Parse(time.RFC3339, o.diedAt)
		if err != nil {
			return nil, exitcode.Newf(exitcode.Usage, "--died-at must be an RFC 3339 time: %v", err)
		}
		died = t
	}
	host, err := loadHostMode(f, env, o.config, cmd.Flags().Changed("app"))
	if err != nil {
		return nil, err
	}
	henv := host.env
	rec := env.Recover.withDefaults()
	if died.IsZero() {
		died = henv.Now()
	}
	action := "recover"
	if recreate {
		action = "recreate"
	}
	force := o.force || recreate

	client := orcaterm.NewClient(henv.Orca)
	terms, err := client.List(ctx)
	if err != nil {
		return nil, exitcode.Newf(exitcode.OrcaUnreachable, "Orca is not reachable on this host: `hadron orca` needs the Orca CLI and app here")
	}
	reads, err := henv.Reads(ctx, host.cfg.App)
	if err != nil {
		return nil, err
	}
	workers, err := reads.Workers(ctx)
	if err != nil {
		return nil, err
	}
	var worker *Worker
	for i := range workers {
		w := workers[i]
		if strings.EqualFold(w.Name, strings.TrimSpace(arg)) || w.ID == arg || (w.URN != "" && w.URN == arg) {
			worker = &workers[i]
		}
	}
	if worker == nil {
		return nil, exitcode.Newf(exitcode.NotFound, "no active worker %q (give its name, URN or id)", arg)
	}
	dto := &recoverDTO{Contract: recoverContract, Action: action, Worker: worker.Name, DryRun: o.dryRun,
		Result: "planned", Closed: []string{}}
	ref := host.cfg.RefFor(worker.ID, worker.URN, worker.Name)
	if ref == "" {
		return dto, refuse(exitcode.RecoverNoLaunch, "no_launch_entry", "%s has no entry in %s: add the worker to host.yaml (provisioning step 10b) so its launch line is known", worker.Name, o.config)
	}
	wc := host.cfg.Workers[ref]
	if hostpoller.CanonicalFamily(wc.Family) != "claude" {
		return dto, refuse(exitcode.RecoverUnsupported, "unsupported_family", "%s runs as %q here: v1 resumes Claude workers only (--resume <id>); Codex and GLM are not supported yet", worker.Name, wc.Family)
	}
	if len(wc.Launch) == 0 {
		return dto, refuse(exitcode.RecoverNoLaunch, "no_launch_entry", "%s has no launch line in host.yaml", worker.Name)
	}
	wt, err := resolveWorktree(henv, ctx, host.cfg, ref, worker, terms, o.worktree)
	if err != nil {
		return dto, err
	}
	dto.Worktree = wt
	_, hasBinding := henv.BoundAt(ctx, wt)
	session := "none"
	if hasBinding {
		session = "bound"
	}

	// Classify with status's own logic, then look at each terminal here.
	bound := map[string]Bound{}
	for _, t := range terms {
		if b, ok := henv.BoundAt(ctx, t.WorktreePath); ok {
			bound[t.Handle] = b
		}
	}
	tg := target{worker: *worker}
	res := []ts.Result{observe(ctx, client, henv, terms, bound, tg)}
	if res[0].Status == ts.Idle {
		applyDarkRule(ctx, henv, func() (Reads, error) { return reads, nil }, host, []target{tg}, res, []int{0})
	}
	st := res[0]
	dto.Found = &foundDTO{Status: string(st.Status), Session: session}
	if st.Reason != nil {
		r := string(*st.Reason)
		dto.Found.Reason = &r
	}
	if st.Terminal != "" {
		h := st.Terminal
		dto.Found.Terminal = &h
	}

	inWt := terminalsIn(terms, wt)
	type seenTerm struct {
		t   orcaterm.Terminal
		obs orcaterm.Observation
		scr *orcaterm.Screen
	}
	var seen []seenTerm
	for _, t := range inWt {
		scr := client.ReadScreen(ctx, t.Handle)
		seen = append(seen, seenTerm{t, orcaterm.Classify(t, scr, henv.Now()), scr})
	}
	var closeSet []string
	var agentTerms []seenTerm
	for _, s := range seen {
		if recreate {
			closeSet = append(closeSet, s.t.Handle)
		}
		if s.obs.Reason != ts.BareShell {
			agentTerms = append(agentTerms, s)
			if !recreate {
				closeSet = append(closeSet, s.t.Handle)
			}
		}
	}
	live := st.Status != ts.Dark || len(agentTerms) > 0

	// Refuse before anything is touched.
	if live && !force {
		why := fmt.Sprintf("%s's terminal is %s", worker.Name, st.Status)
		if st.Reason != nil {
			why += " (" + string(*st.Reason) + ")"
		}
		return dto, refuse(exitcode.RecoverRefused, "live_agent", "%s and an agent is running in %s: nothing was changed. Re-run with --force to end it first (recover never leaves two agents)", why, wt)
	}
	procs, err := rec.Procs()
	if err != nil {
		return dto, refuse(exitcode.RecoverRefused, "cannot_scan", "cannot prove no agent is running in %s: the process scan failed (%v)", wt, err)
	}
	if hit := ancestorInWorktree(procs, rec.Ancestors(), wt); hit != 0 && len(closeSet) > 0 {
		return dto, refuse(exitcode.RecoverRefused, "inside_target", "this command is running inside the worktree it would close (pid %d is its ancestor): run it from outside %s", hit, wt)
	}
	if orphan := agentsIn(procs, wt, wc.Launch); !live && len(orphan) > 0 {
		return dto, refuse(exitcode.RecoverRefused, "agent_without_terminal", "an agent process (pid %d) is running in %s but no Orca terminal shows it; end it yourself first — recover never signals a process", orphan[0].PID, wt)
	}

	// The transcript, before any close: a failure here must cost nothing.
	var tx *Transcript
	if !o.fresh {
		var all []Transcript
		var err error
		if o.transcripts != "" {
			all, err = readTranscriptDir(o.transcripts)
		} else {
			all, err = rec.Transcripts(wt, wc.Launch)
		}
		if err != nil {
			return dto, refuse(exitcode.RecoverNoTranscript, "no_transcript", "cannot list transcripts for %s: %v", wt, err)
		}
		tx, dto.Transcript = chooseTranscript(all, died)
		if tx == nil {
			return dto, refuse(exitcode.RecoverNoTranscript, "no_transcript", "no transcript at or before %s for %s: nothing to resume. Use --fresh to launch without --resume on the staged recovery prompt", died.UTC().Format(time.RFC3339), worker.Name)
		}
	} else if !rec.PromptStaged(wt) {
		return dto, refuse(exitcode.RecoverNoTranscript, "no_staged_prompt", "--fresh needs a staged recovery prompt for %s (~/.local/state/hadron-worker-shed/%s.prompt) and none is there", worker.Name, filepath.Base(wt))
	}

	argv := append([]string(nil), wc.Launch...)
	if tx != nil {
		argv = append(argv, "--resume", tx.ID)
	}
	command := shellJoin(argv)
	sort.Strings(closeSet)
	dto.Plan = &planDTO{Close: orEmpty(closeSet), Fresh: o.fresh,
		Create: createDTO{Worktree: wt, Title: worker.Name, Command: command}}
	if len(closeSet) > 0 {
		dto.Plan.Steps = append(dto.Plan.Steps, "end the live agent: close terminal(s) "+strings.Join(closeSet, ", ")+" and wait until no agent process is left in the worktree")
	}
	dto.Plan.Steps = append(dto.Plan.Steps,
		"prove no agent process runs in "+wt,
		"create one terminal titled "+worker.Name+" running: "+command,
		"verify exactly one agent runs in the worktree and the new terminal shows it")
	if live {
		dto.Interrupted = &interruptedDTO{Terminal: st.Terminal, Status: string(st.Status), Prompt: string(st.Prompt)}
		if len(agentTerms) > 0 {
			a := agentTerms[0]
			dto.Interrupted = &interruptedDTO{Terminal: a.t.Handle, Status: string(a.obs.Status), Prompt: string(a.obs.Prompt)}
			if a.obs.Reason != "" {
				r := string(a.obs.Reason)
				dto.Interrupted.Reason = &r
			}
		} else if st.Reason != nil {
			r := string(*st.Reason)
			dto.Interrupted.Reason = &r
		}
		for _, s := range agentTerms {
			if s.obs.Status == ts.Locked && s.scr != nil {
				dto.pending = pendingLines(s.scr)
				break
			}
		}
	}
	if o.dryRun {
		return dto, nil
	}

	// Act. Close, then prove gone.
	for _, h := range closeSet {
		if err := orcaterm.CloseTab(ctx, henv.Orca, h); err != nil {
			already := "none closed yet"
			if len(dto.Closed) > 0 {
				already = "already closed: " + strings.Join(dto.Closed, ", ")
			}
			return dto, refuse(exitcode.RecoverRefused, "close_failed", "could not close terminal %s: Orca refused; nothing was created (%s)", h, already)
		}
		dto.Closed = append(dto.Closed, h)
	}
	if err := waitNoAgents(rec, wt, wc.Launch); err != nil {
		return dto, refuse(exitcode.RecoverRefused, "agent_not_gone", "%s: the old agent could not be shown gone after closing its terminal, so no terminal was created (that would be a second agent)", err)
	}
	handle, err := orcaterm.CreateTerminal(ctx, henv.Orca, wt, worker.Name, command)
	if err != nil {
		return dto, exitcode.Newf(exitcode.Error, "Orca refused to create the terminal; the worker has no agent now (closed: %s)", strings.Join(dto.Closed, ", "))
	}
	dto.Created = &handle
	dto.Verification = verify(ctx, client, henv, rec, wt, handle)
	if !dto.Verification.Verified {
		return dto, refuse(exitcode.RecoverUnverified, "unverified", "the terminal %s was created but exactly one agent was not shown running in %s (saw %d): check it with `hadron orca terminal status %s`", handle, wt, dto.Verification.Agents, worker.Name)
	}
	dto.Result = "recovered"
	return dto, nil
}

func orEmpty(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

// pendingLines is the last few non-empty screen lines of a locked agent: the
// question the human would otherwise lose. Text mode only.
func pendingLines(sc *orcaterm.Screen) []string {
	var out []string
	for i := len(sc.Tail) - 1; i >= 0 && len(out) < 6; i-- {
		if l := strings.TrimSpace(sc.Tail[i]); l != "" {
			out = append([]string{l}, out...)
		}
	}
	return out
}

func terminalsIn(terms []orcaterm.Terminal, wt string) []orcaterm.Terminal {
	var out []orcaterm.Terminal
	for _, t := range terms {
		if filepath.Clean(t.WorktreePath) == wt {
			out = append(out, t)
		}
	}
	return out
}

// resolveWorktree is the worker's one worktree: host.yaml's, plus any Orca
// terminal bound to the worker. More than one is the caller's to name.
func resolveWorktree(env Env, ctx context.Context, cfg *hostpoller.Config, ref string, w *Worker, terms []orcaterm.Terminal, want string) (string, error) {
	set := map[string]bool{}
	for _, tok := range cfg.Workers[ref].Worktrees {
		set[filepath.Clean(cfg.WorktreePath(tok))] = true
	}
	for _, t := range terms {
		if b, ok := env.BoundAt(ctx, t.WorktreePath); ok && b.WorkerID == w.ID {
			set[filepath.Clean(t.WorktreePath)] = true
		}
	}
	if want != "" {
		c := filepath.Clean(want)
		if !set[c] {
			return "", exitcode.Newf(exitcode.Usage, "--worktree %s is not one of %s's worktrees", want, w.Name)
		}
		return c, nil
	}
	var all []string
	for p := range set {
		all = append(all, p)
	}
	sort.Strings(all)
	switch len(all) {
	case 0:
		return "", refuse(exitcode.RecoverNoLaunch, "no_worktree", "%s has no worktree in host.yaml (worktrees:) and no Orca terminal is bound to one", w.Name)
	case 1:
		return all[0], nil
	}
	return "", exitcode.Newf(exitcode.Usage, "%s has more than one worktree (%s): pass --worktree", w.Name, strings.Join(all, ", "))
}

// chooseTranscript is the newest transcript last written at or before died.
func chooseTranscript(all []Transcript, died time.Time) (*Transcript, *transcriptDTO) {
	var best *Transcript
	newer, older := 0, 0
	for i := range all {
		t := all[i]
		if t.ModTime.After(died) {
			newer++
			continue
		}
		if best == nil || t.ModTime.After(best.ModTime) {
			if best != nil {
				older++
			}
			best = &all[i]
		} else {
			older++
		}
	}
	if best == nil {
		return nil, nil
	}
	why := fmt.Sprintf("the newest of %d, last written %s before the death time %s", len(all), died.Sub(best.ModTime).Round(time.Second), died.UTC().Format(time.RFC3339))
	if newer > 0 {
		why += fmt.Sprintf("; %d written after it were skipped", newer)
	}
	return best, &transcriptDTO{ID: best.ID, ModifiedAt: best.ModTime.UTC().Format(time.RFC3339), Why: why}
}

// --- the never-two-agents scan ---

var agentBins = map[string]bool{"claude": true, "codex": true}
var shells = map[string]bool{"bash": true, "sh": true, "dash": true, "zsh": true}

func under(cwd, wt string) bool {
	cwd = filepath.Clean(cwd)
	return cwd == wt || strings.HasPrefix(cwd, wt+string(filepath.Separator))
}

// isAgent: the agent binary is argv[0]. A process that merely mentions it
// (a wrapper, an editor, a grep) is not one.
func isAgent(p Proc) bool {
	return len(p.Argv) > 0 && agentBins[filepath.Base(p.Argv[0])]
}

// isSupervisor: the host's relaunch wrapper, a shell running the launch
// line's own first word. Left alive it would start a second agent.
func isSupervisor(p Proc, launch []string) bool {
	return len(p.Argv) > 1 && len(launch) > 0 && shells[filepath.Base(p.Argv[0])] && filepath.Base(p.Argv[1]) == filepath.Base(launch[0])
}

func agentsIn(procs []Proc, wt string, launch []string) []Proc {
	var out []Proc
	for _, p := range procs {
		if under(p.Cwd, wt) && (isAgent(p) || isSupervisor(p, launch)) {
			out = append(out, p)
		}
	}
	return out
}

func ancestorInWorktree(procs []Proc, anc []int, wt string) int {
	for _, p := range procs {
		for _, a := range anc {
			if p.PID == a && under(p.Cwd, wt) {
				return p.PID
			}
		}
	}
	return 0
}

func waitNoAgents(rec RecoverEnv, wt string, launch []string) error {
	steps := int(rec.GoneWait / pollEvery)
	if steps < 1 {
		steps = 1
	}
	var left []Proc
	for i := 0; i <= steps; i++ {
		procs, err := rec.Procs()
		if err != nil {
			return fmt.Errorf("the process scan failed (%v)", err)
		}
		if left = agentsIn(procs, wt, launch); len(left) == 0 {
			return nil
		}
		if i < steps {
			rec.Sleep(pollEvery)
		}
	}
	return fmt.Errorf("pid %d (%s) still runs in the worktree after %s", left[0].PID, filepath.Base(left[0].Argv[0]), rec.GoneWait)
}

// verify is the after half of the guarantee: exactly one agent process in the
// worktree and the new terminal showing an agent, not a shell.
func verify(ctx context.Context, client *orcaterm.Client, env Env, rec RecoverEnv, wt, handle string) *verificationDTO {
	v := &verificationDTO{Terminal: &handle}
	steps := int(rec.VerifyWait / pollEvery)
	if steps < 1 {
		steps = 1
	}
	for i := 0; i <= steps; i++ {
		procs, err := rec.Procs()
		if err == nil {
			v.Agents = 0
			for _, p := range procs {
				if under(p.Cwd, wt) && isAgent(p) {
					v.Agents++
				}
			}
		}
		if v.Agents > 1 {
			break
		}
		if terms, err := client.List(ctx); err == nil {
			for _, t := range terms {
				if t.Handle == handle {
					obs := orcaterm.Classify(t, client.ReadScreen(ctx, handle), env.Now())
					s := string(obs.Status)
					v.OnScreen = &s
					v.Verified = err == nil && v.Agents == 1 && obs.Reason != ts.BareShell && obs.Reason != ts.NoAgent
				}
			}
		}
		if v.Verified {
			break
		}
		if i < steps {
			rec.Sleep(pollEvery)
		}
	}
	return v
}

// --- shell quoting for `orca terminal create --command` ---

var plainWord = regexp.MustCompile(`^[A-Za-z0-9_@%+=:,./-]+$`)

func shellJoin(argv []string) string {
	parts := make([]string, len(argv))
	for i, a := range argv {
		if plainWord.MatchString(a) {
			parts[i] = a
		} else {
			parts[i] = "'" + strings.ReplaceAll(a, "'", `'\''`) + "'"
		}
	}
	return strings.Join(parts, " ")
}
