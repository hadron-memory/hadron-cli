package team

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/Khan/genqlient/graphql"
	"github.com/spf13/cobra"

	"github.com/hadron-memory/hadron-cli/internal/api"
	"github.com/hadron-memory/hadron-cli/internal/api/gen"
	"github.com/hadron-memory/hadron-cli/internal/cmdutil"
	"github.com/hadron-memory/hadron-cli/internal/exitcode"
	"github.com/hadron-memory/hadron-cli/internal/output"
)

// hadron-cli#952: `team session shed` writes the checkpoint node, the recovery
// prompt and the resume claim for a deliberate context reset that keeps the
// session. It is built to be a host hook's command, so it never blocks the
// reset: every failure after the flags parse is one sanitized stderr line and
// exit 0.

const (
	shedCooldownFile   = "hadron-team-shed.json"
	shedDefaultCool    = 10 * time.Minute
	shedWorkItemsCap   = 20
	shedTranscriptTail = 8 << 10

	shedWritten = "written"
	shedSkipped = "skipped"
	shedFailed  = "failed"
	shedPrinted = "printed"

	shedClaimWritten = "written"
	shedClaimFailed  = "failed"
	shedClaimNone    = "none"
)

// shedDTO is the stable --json shape of `session shed`. Claim is a state word
// only: the receipt carries no claim value and none is ever echoed.
type shedDTO struct {
	Status string  `json:"status"`
	Node   *string `json:"node"`
	Claim  string  `json:"claim"`
	Reason *string `json:"reason"`
	// Prompt is set only by --print-prompt.
	Prompt *string `json:"prompt,omitempty"`
}

// shedStamp is the cooldown record, beside the binding and never inside it.
// Claim is recorded so a re-run inside the cooldown retries ONLY a claim that
// failed, and does nothing once one was written.
type shedStamp struct {
	SessionID  string `json:"sessionId"`
	LastShedAt string `json:"lastShedAt"`
	Node       string `json:"node"`
	Claim      string `json:"claim"`
}

// shedStep is one line of the recovery prompt. The prompt is generated from
// this table and a test pins every Command against the registered cobra tree,
// so the prompt cannot cite a command or flag that does not exist.
type shedStep struct {
	Command []string // cobra path plus flags, without the leading `hadron`
	Text    string
}

func shedRecoverySteps() []shedStep {
	return []shedStep{
		{[]string{"team", "session", "whoami", "--check"},
			"the binding usually SURVIVES the relaunch. Bound and active: keep it, and skip the claim step."},
		{[]string{"team", "session", "start"},
			fmt.Sprintf("with NO --as, to redeem the resume claim (single use, 60s window). Exit %d means CHOOSE: ask the human which candidate, retry with --session <chosen-id>, never select automatically. Never bind a new session with --as.", exitcode.Usage)},
		{[]string{"team", "worker", "get", "<worker name>"},
			"re-read your boot briefing."},
		{[]string{"team", "chat", "read", "--since", "<watermark>", "--all"},
			"catch up on team chat unfiltered from the watermark, then mark what you read with `hadron team chat mark-read --through <head>`."},
	}
}

func renderShedPrompt(workerName, memoryRef, loc string, watermark *int) string {
	var b strings.Builder
	fmt.Fprintf(&b, "You are %s, recovering from a shed. Your transcript was cleared on purpose and your worker binding is intact.\n", workerName)
	n := 1
	for _, s := range shedRecoverySteps() {
		cmd := "hadron " + strings.Join(s.Command, " ")
		if i := strings.Index(cmd, "<worker name>"); i >= 0 {
			cmd = strings.Replace(cmd, "<worker name>", workerName, 1)
		}
		if watermark != nil {
			cmd = strings.Replace(cmd, "<watermark>", fmt.Sprint(*watermark), 1)
		} else {
			// No validated read cursor: never invent one (head would skip the
			// unread interval); read from the saved server-side cursor.
			cmd = strings.Replace(cmd, " --since <watermark>", "", 1)
		}
		fmt.Fprintf(&b, "%d. `%s` — %s\n", n, cmd, s.Text)
		n++
	}
	fmt.Fprintf(&b, "%d. Read the handoff checkpoint, the source of truth for where you are: `hadron node get -m %s %s`. Then continue its next action unless the human redirects you.\n", n, memoryRef, loc)
	return b.String()
}

// shedField is one independently read checkpoint field. A failed read carries
// "unavailable: <class>" in place of a value; nothing is ever fabricated.
type shedField struct {
	Text string
	OK   bool
}

func unavailable(class string) shedField { return shedField{Text: "unavailable: " + class} }

type shedSnapshot struct {
	Session    *binding
	Worktree   string
	FetchedAt  string
	Head       shedField
	Cursor     shedField
	HeadSeq    *int
	CursorSeq  *int
	Work       []string
	WorkMore   int
	WorkNote   string
	Continuity shedField
	Previous   shedField
}

func renderShedBody(s shedSnapshot, prompt, transcriptTail string) string {
	var b strings.Builder
	b.WriteString("## Identity\n")
	fmt.Fprintf(&b, "- Worker: %s (%s)\n- Session: %s\n- App: %s\n- Worktree: %s\n\n",
		s.Session.WorkerName, s.Session.WorkerRole, s.Session.SessionID, s.Session.AppID, s.Worktree)
	b.WriteString("## Watermark\n")
	fmt.Fprintf(&b, "- Chat head: %s (fetched %s)\n- Read cursor: %s (fetched %s)\n\n", s.Head.Text, s.FetchedAt, s.Cursor.Text, s.FetchedAt)
	b.WriteString("## Open work\n")
	if s.WorkNote != "" {
		fmt.Fprintf(&b, "- %s\n", s.WorkNote)
	}
	for _, w := range s.Work {
		fmt.Fprintf(&b, "- %s\n", w)
	}
	if s.WorkMore > 0 {
		fmt.Fprintf(&b, "- … and %d more\n", s.WorkMore)
	}
	b.WriteString("\n## Briefing pointers\n")
	fmt.Fprintf(&b, "- Continuity handoff: %s\n- Previous session: %s\n\n", s.Continuity.Text, s.Previous.Text)
	b.WriteString("## Recovery prompt\n")
	b.WriteString(prompt)
	if transcriptTail != "" {
		b.WriteString("\n## Transcript tail\n```\n" + transcriptTail + "\n```\n")
	}
	return b.String()
}

// shedLocFor is the checkpoint loc: auto `sheds:auto-<YYYYMMDD-HHMMSS>`,
// manual `sheds:<YYYY-MM-DD-HHMM>`, both UTC, in the worker's own memory.
func shedLocFor(now time.Time, auto bool) string {
	now = now.UTC()
	if auto {
		return "sheds:auto-" + now.Format("20060102-150405")
	}
	return "sheds:" + now.Format("2006-01-02-1504")
}

func shedName(now time.Time, auto bool, b *binding) string {
	stamp := strings.TrimPrefix(shedLocFor(now, auto), "sheds:")
	if auto {
		return fmt.Sprintf("AUTO shed checkpoint %s (%s (%s))", strings.TrimPrefix(stamp, "auto-"), b.WorkerName, b.WorkerRole)
	}
	return fmt.Sprintf("Shed %s — %s", stamp, b.WorkerName)
}

// shedTailOf returns the last whole lines of the host's transcript file, at
// most shedTranscriptTail bytes. The file is the host's and is NOT scrubbed.
//
// Only a regular file is read (stat BEFORE open: opening a FIFO named by
// mistake would block the shed ahead of the checkpoint and claim), and only
// its last shedTranscriptTail bytes.
func shedTailOf(path string) (string, error) {
	pre, err := os.Stat(path)
	if err != nil {
		return "", err
	}
	if !pre.Mode().IsRegular() {
		return "", fmt.Errorf("not a regular file")
	}
	fh, err := os.Open(path) // #nosec G304 — an explicit, opt-in path the operator named
	if err != nil {
		return "", err
	}
	defer func() { _ = fh.Close() }()
	info, err := fh.Stat()
	if err != nil {
		return "", err
	}
	if !info.Mode().IsRegular() {
		return "", fmt.Errorf("not a regular file")
	}
	size := info.Size()
	off := int64(0)
	if size > shedTranscriptTail {
		off = size - shedTranscriptTail
	}
	data := make([]byte, size-off)
	n, err := fh.ReadAt(data, off)
	if err != nil && err != io.EOF {
		return "", err
	}
	data = data[:n]
	if off > 0 { // the window starts mid-line: drop the partial first line
		if i := bytes.IndexByte(data, '\n'); i >= 0 {
			data = data[i+1:]
		} else {
			data = nil
		}
	}
	return strings.TrimRight(string(data), "\n"), nil
}

func shedStampPath(ctx context.Context) (string, error) {
	dir, err := gitDir(ctx)
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, shedCooldownFile), nil
}

func readShedStamp(path string) *shedStamp {
	data, err := os.ReadFile(path) // #nosec G304 — derived from the worktree's own git dir
	if err != nil {
		return nil
	}
	var s shedStamp
	if json.Unmarshal(data, &s) != nil {
		return nil
	}
	return &s
}

func writeShedStamp(path string, s shedStamp) error {
	data, err := json.Marshal(s)
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// shedOutcome is what runShed decided; emit renders it. Reason is a fixed
// phrase, never a server message, path or claim.
type shedOutcome struct {
	dto shedDTO
}

func fail(reason string) shedOutcome {
	return shedOutcome{shedDTO{Status: shedFailed, Claim: shedClaimNone, Reason: &reason}}
}

type shedOptions struct {
	auto, printPrompt, offline bool
	cooldown                   time.Duration
	transcript                 string
	transcriptEmpty            bool
	now                        func() time.Time
}

func gather(ctx context.Context, client graphql.Client, b *binding, now time.Time) shedSnapshot {
	ctx = api.WithSession(ctx, b.SessionID)
	s := shedSnapshot{Session: b, FetchedAt: now.UTC().Format(time.RFC3339),
		Head: unavailable("not read"), Cursor: unavailable("not read"),
		Continuity: unavailable("not read"), Previous: unavailable("not read")}
	if wd, err := os.Getwd(); err == nil {
		s.Worktree = wd
	}
	if h, err := gen.TeamChatReadHead(ctx, client, b.AppID); err != nil || h.TeamChatMessages == nil {
		s.Head = unavailable("chat head read failed")
	} else if items := h.TeamChatMessages.Items; len(items) > 0 && items[0] != nil {
		seq := items[0].Seq
		s.HeadSeq, s.Head = &seq, shedField{Text: fmt.Sprintf("#%d", seq), OK: true}
	} else {
		s.Head = shedField{Text: "none (empty chat)", OK: true}
	}
	if ch, err := gen.TeamDefaultChannel(ctx, client, b.AppID); err != nil || ch.App == nil || ch.App.DefaultChannel == nil {
		s.Cursor = unavailable("channel read failed")
	} else if c, err := gen.CheckChatCursor(ctx, client, ch.App.DefaultChannel.Id, b.WorkerID, b.AppID); err != nil || c.ChannelReadState == nil {
		s.Cursor = unavailable("cursor read failed")
	} else {
		seq := c.ChannelReadState.LastSeenSeq
		s.CursorSeq, s.Cursor = &seq, shedField{Text: fmt.Sprintf("#%d", seq), OK: true}
	}
	limit := shedWorkItemsCap
	if w, err := gen.TeamWorkItems(ctx, client, b.AppID, &b.SessionID, nil, nil, nil, &limit, nil); err != nil || w.TeamWorkItems == nil {
		s.WorkNote = "unavailable: work items read failed"
	} else {
		for _, it := range w.TeamWorkItems.Items {
			if it == nil {
				continue
			}
			s.Work = append(s.Work, fmt.Sprintf("%s %s (%s)", it.Kind, it.Ref, it.Action))
		}
		if extra := w.TeamWorkItems.Total - len(s.Work); extra > 0 {
			s.WorkMore = extra
		}
		if len(s.Work) == 0 {
			s.WorkNote = "none recorded this session"
		}
	}
	if c, err := gen.GetWorkerContinuity(ctx, client, b.WorkerID); err != nil || c.Worker == nil || c.Worker.Continuity == nil {
		s.Continuity, s.Previous = unavailable("continuity read failed"), unavailable("continuity read failed")
	} else {
		k := c.Worker.Continuity
		s.Continuity = shedField{Text: "none (" + string(k.Status) + ")", OK: true}
		if k.Handoff != nil {
			s.Continuity.Text = k.Handoff.Urn + " (" + string(k.Status) + ")"
		}
		s.Previous = shedField{Text: "none", OK: true}
		if k.PreviousSession != nil {
			s.Previous.Text = k.PreviousSession.Id
		}
	}
	return s
}

func runShed(ctx context.Context, f *cmdutil.Factory, o shedOptions) shedOutcome {
	now := o.now()
	b, _, err := readBindingFile(ctx)
	if err != nil || b == nil || b.SessionID == "" {
		return fail("no worker session is bound in this worktree")
	}
	if checkBindingServer(f, b) != nil {
		return fail("the bound session belongs to a different server")
	}
	loc := shedLocFor(now, o.auto)
	if o.printPrompt && o.offline {
		p := renderShedPrompt(b.WorkerName, "<worker memory>", loc, nil)
		return shedOutcome{shedDTO{Status: shedPrinted, Claim: shedClaimNone, Prompt: &p}}
	}
	client, err := f.GraphQLClient()
	if err != nil {
		return fail("no usable credentials or server")
	}
	rctx := api.WithSession(ctx, b.SessionID)
	// Step 4 first when it can answer without the network: a cooldown hit
	// writes nothing, so it must not even need the reads.
	var stampPath string
	var prev *shedStamp
	if o.auto && !o.printPrompt {
		if p, perr := shedStampPath(ctx); perr == nil {
			stampPath = p
			if st := readShedStamp(p); st != nil && st.SessionID == b.SessionID {
				if at, e := time.Parse(time.RFC3339, st.LastShedAt); e == nil && now.Sub(at) < o.cooldown {
					prev = st
				}
			}
		}
	}
	if prev != nil && prev.Claim == shedClaimWritten {
		reason, node := "cooldown; the earlier claim lapses 60s after it was written", prev.Node
		return shedOutcome{shedDTO{Status: shedSkipped, Node: &node, Claim: shedClaimWritten, Reason: &reason}}
	}
	if prev != nil { // cooldown hit with a failed claim: retry the claim ONLY
		return finishClaim(rctx, client, b, stampPath, *prev, shedSkipped, "cooldown; only the claim was retried")
	}
	w, err := gen.GetWorker(rctx, client, b.WorkerID)
	if err != nil || w.Worker == nil || w.Worker.MemoryId == nil || *w.Worker.MemoryId == "" {
		return fail("the worker memory could not be read")
	}
	memoryID := *w.Worker.MemoryId
	snap := gather(ctx, client, b, now)
	prompt := renderShedPrompt(b.WorkerName, memoryID, loc, snap.CursorSeq)
	if o.printPrompt {
		return shedOutcome{shedDTO{Status: shedPrinted, Claim: shedClaimNone, Prompt: &prompt}}
	}
	tail := ""
	if o.transcriptEmpty {
		tail = "unavailable: --transcript was empty"
	} else if o.transcript != "" {
		t, terr := shedTailOf(o.transcript)
		if terr != nil {
			tail = "unavailable: transcript could not be read"
		} else {
			tail = t
		}
	}
	body := renderShedBody(snap, prompt, tail)
	nt := "info"
	created, err := gen.CreateNode(rctx, client, &gen.CreateNodeInput{
		MemoryId: memoryID, Loc: loc, Name: shedName(now, o.auto, b), Content: &body, NodeType: &nt,
	})
	if err != nil || created.CreateNode == nil {
		return fail("the checkpoint node could not be written")
	}
	back, err := gen.GetNode(rctx, client, created.CreateNode.Id)
	if err != nil || back.Node == nil || back.Node.Loc != loc || back.Node.Content == nil || *back.Node.Content != body {
		return fail("the checkpoint node could not be read back")
	}
	st := shedStamp{SessionID: b.SessionID, LastShedAt: now.UTC().Format(time.RFC3339), Node: back.Node.Urn, Claim: shedClaimNone}
	if stampPath == "" {
		stampPath, _ = shedStampPath(ctx)
	}
	if stampPath != "" {
		_ = writeShedStamp(stampPath, st)
	}
	return finishClaim(rctx, client, b, stampPath, st, shedWritten, "")
}

// finishClaim is step 8, ALWAYS last: nothing slow runs after it, because the
// claim lasts 60 seconds. A failure leaves the verified node and no claim.
func finishClaim(ctx context.Context, client graphql.Client, b *binding, stampPath string, st shedStamp, status, reason string) shedOutcome {
	claim := shedClaimWritten
	if r, err := gen.CreateResumeClaim(ctx, client, b.SessionID); err != nil || r.CreateResumeClaim == nil {
		claim = shedClaimFailed
		reason = "the resume claim could not be written; the node is the recovery path"
	}
	st.Claim = claim
	if stampPath != "" {
		_ = writeShedStamp(stampPath, st)
	}
	node := st.Node
	d := shedDTO{Status: status, Node: &node, Claim: claim}
	if reason != "" {
		d.Reason = &reason
	}
	return shedOutcome{d}
}

func newCmdSessionShed(f *cmdutil.Factory) *cobra.Command {
	o := &shedOptions{}
	cmd := &cobra.Command{
		Use:   "shed [--auto] [--print-prompt] [--offline] [--cooldown <d>] [--transcript <path>]",
		Short: "Write the shed checkpoint node, recovery prompt and resume claim in one step",
		Long: `Checkpoint this worktree's worker session before a deliberate context reset
that keeps it open (hadron-cli#952): read the session, chat head and read
cursor, open work and briefing pointers, write one ` + "`sheds:`" + ` node into the
worker's own memory, read it back, and only then write the resume claim.

The node is ` + "`sheds:<YYYY-MM-DD-HHMM>`" + ` (UTC), or ` + "`sheds:auto-<YYYYMMDD-HHMMSS>`" + `
with --auto. Its headings are Identity, Watermark, Open work, Briefing pointers
and Recovery prompt. Each read is independent: a failed one prints
"unavailable: <reason>" for that field and the shed continues; nothing is
fabricated. The claim comes LAST, so a failed node write can never leave one;
a failed claim leaves a verified node, reported as claim: failed. The claim
lasts 60 seconds, so reset immediately afterwards.

--auto    hook mode: never blocks the reset. Exits 0 on every failure, flag
          errors included, with one fixed line on stderr and nothing on stdout
          unless --json. A second --auto for the same session inside
          --cooldown (default 10m) writes no node: it exits 0 as skipped, and
          retries only a claim that failed. Without --auto, usage mistakes
          exit with the normal usage code; other failures still exit 0.
--print-prompt  print the recovery prompt and nothing else: no node, no claim,
          no cooldown stamp. Only the read-only reads run; with --offline none.
--transcript <path>  append the last whole lines (8 KiB at most) of that file as
          a ` + "`## Transcript tail`" + `. OPT-IN: the host's file is NOT scrubbed and
          the lines land in a node the team can read.

--json prints {"status":"written|skipped|failed|printed","node","claim":
"written|failed|none","reason"}. No claim value is ever printed.`,
		Example: `  hadron team session shed --print-prompt
  hadron team session shed --auto --transcript "$TRANSCRIPT" && exit-the-agent`,
		// Flags are parsed by hand: cobra rejects a bad flag before RunE, and
		// under --auto that rejection would exit non-zero and block the hook.
		DisableFlagParsing: true,
		Args:               cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runShedCommand(cmd, f, o, args)
		},
	}
	fl := cmd.Flags()
	fl.BoolVar(&o.auto, "auto", false, "hook mode: exit 0 on every failure, nothing on stdout without --json, cooldown applies")
	fl.BoolVar(&o.printPrompt, "print-prompt", false, "print the recovery prompt only: no node, no claim, no cooldown stamp")
	fl.BoolVar(&o.offline, "offline", false, "with --print-prompt: make no network call (placeholders for what a read would fill)")
	fl.DurationVar(&o.cooldown, "cooldown", shedDefaultCool, "with --auto: skip a second shed for the same session inside this window")
	fl.StringVar(&o.transcript, "transcript", "", "append the tail of this host transcript file to the node (opt-in; NOT scrubbed)")
	return cmd
}

func hasAutoArg(args []string) bool {
	for _, a := range args {
		if a == "--" {
			return false
		}
		if a == "--auto" || a == "--auto=true" || a == "--auto=1" {
			return true
		}
	}
	return false
}

func runShedCommand(cmd *cobra.Command, f *cmdutil.Factory, o *shedOptions, args []string) error {
	for _, a := range args {
		if a == "-h" || a == "--help" {
			return cmd.Help()
		}
	}
	auto := hasAutoArg(args)
	cmd.SilenceUsage = auto
	cmd.Flags().SetOutput(io.Discard)
	// cobra's ParseFlags is a no-op while DisableFlagParsing is set, so lift it
	// for this one call. It also merges the persistent --json/--server/--app flags,
	// which DisableFlagParsing would otherwise leave unread.
	cmd.DisableFlagParsing = false
	if err := cmd.ParseFlags(args); err != nil {
		if auto {
			return shedEmit(f, auto, fail("the command line could not be parsed"))
		}
		return exitcode.Newf(exitcode.Usage, "%v", err)
	}
	if rest := cmd.Flags().Args(); len(rest) > 0 && !auto {
		return exitcode.Newf(exitcode.Usage, "shed takes no arguments, got %q", rest[0])
	}
	if o.offline && !o.printPrompt && !auto {
		return exitcode.Newf(exitcode.Usage, "--offline only applies with --print-prompt")
	}
	// An EMPTY --transcript is not an absent one: --transcript="$UNSET" in a
	// hook must not silently drop the tail. Refused before any side effect in
	// manual mode; under --auto (which must not block) the node says so.
	o.transcriptEmpty = cmd.Flags().Changed("transcript") && strings.TrimSpace(o.transcript) == ""
	if o.transcriptEmpty && !auto {
		return exitcode.Newf(exitcode.Usage, "--transcript names a file but was empty (an unset variable?); omit it for no transcript tail")
	}
	if o.cooldown < 0 {
		if !auto {
			return exitcode.Newf(exitcode.Usage, "--cooldown must not be negative")
		}
		o.cooldown = shedDefaultCool
	}
	o.now = time.Now
	return shedEmit(f, auto, runShed(cmd.Context(), f, *o))
}

func shedEmit(f *cmdutil.Factory, auto bool, out shedOutcome) error {
	d := out.dto
	if d.Reason != nil && (d.Status == shedFailed || d.Claim == shedClaimFailed) {
		_, _ = fmt.Fprintf(f.IOStreams.ErrOut, "hadron: team session shed: %s\n", *d.Reason)
	}
	if auto && !f.JSON {
		return nil
	}
	err := output.Write(f.IOStreams, f.JSON, d, func(w io.Writer) error {
		if d.Prompt != nil {
			_, e := fmt.Fprint(w, *d.Prompt)
			return e
		}
		switch d.Status {
		case shedFailed:
			return nil
		default:
			node := ""
			if d.Node != nil {
				node = *d.Node
			}
			_, e := fmt.Fprintf(w, "✓ shed %s: %s (claim: %s)\n", d.Status, node, d.Claim)
			return e
		}
	})
	_ = err // a closed pipe must not turn a completed shed into a non-zero exit
	return nil
}
