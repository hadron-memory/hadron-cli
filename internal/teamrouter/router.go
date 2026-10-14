// Package teamrouter is the Go port of the Orca attention router
// (hadron-server scripts/orca-attention-router, TypeScript) — cli#829 (T7).
//
// It nudges the Orca-hosted workers on an explicit roster when they are
// mentioned, by typing one short line into that worker's Orca terminal. It
// never reads, relays or posts a chat message, never marks anything read,
// and never hands its token to a worker.
//
// The acceptance spec is the TS suite, ported 1:1 into this package's tests
// (at server main f0ab31a0 — #1845's per-roster terminal pin). The TS router
// shelled out to `hadron api`; this port speaks GraphQL natively through the
// Server seam and keeps exec-ing only the `orca` CLI.
//
// Why the gates exist (finding
// hrn:node:hadronmemory.com:hadron-server:findings:orca-terminal-send-busy-agent-claude-queues-codex-steers):
//
//   - A prompt typed into a BUSY Codex agent steers its turn and drops the
//     rest of its task. So a nudge is sent only on proof the terminal is idle;
//     an UNTRACKED terminal can never satisfy tui-idle, so its timeout is
//     unknown, and the rendered screen decides (#7398).
//   - tui-idle reports idle while a HUMAN is typing, so the screen must also
//     prove the prompt empty (#5473), with a stale Orca draft refuted by a
//     Claude screen (#6736).
//   - Tab titles are not identity: each roster row names its worktree, and a
//     CLI worker binding in that worktree must agree.
//   - The token is adopted only when every roster worker with new attention
//     was nudged; a ledger keyed on it stops the retry re-nudging the others.
//   - Counts are recomputed tokenless immediately before the send (#1616), and
//     both gates are re-proved after that round trip, the idle one fail-closed.
package teamrouter

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"
)

// recheckIdleTimeoutMS: the idle RECHECK after the recompute is fail-closed —
// prove idle NOW or hold. A full busy-wait there could outlast a turn in
// which the worker read its own chat (Atilla's delta review of 46a12ac1).
const recheckIdleTimeoutMS = 500

// maxPages bounds the pages drained per poll: a runaway continuation fails.
const maxPages = 1000

// agentFamilies are the agentIdentity values that name an agent terminal.
var agentFamilies = map[string]bool{"claude": true, "codex": true}

// logPrefix keeps router.ts's log prefix, so the operators' greps over
// loop.log ("resolved by screen", "held N min") keep working across the port.
const logPrefix = "orca-attention-router: "

// AttentionItem is one Worker/Channel pair from the paged attention scan
// (TeamAttentionPageItem). LastRelevantSeq 0 stands for the server's null:
// seqs start at 1, so no real mention frontier is 0.
type AttentionItem struct {
	Worker         string `json:"worker"`
	WorkerName     string `json:"workerName"`
	Channel        string `json:"channel"`
	Unread         int    `json:"unread"`
	UnreadMentions int    `json:"unreadMentions"`
	FirstUnreadSeq *int   `json:"firstUnreadSeq"`
	// LastRelevantSeq is the highest seq that counts for this worker: for a
	// mention-only row, its latest mention. 0 = none.
	LastRelevantSeq int `json:"lastRelevantSeq"`
	LastSeq         int `json:"lastSeq"`
}

// AttentionPage is one page of the attention scan.
type AttentionPage struct {
	ScanComplete   bool            `json:"scanComplete"`
	NextPage       string          `json:"nextPage"`
	AdoptableSince string          `json:"adoptableSince"`
	Items          []AttentionItem `json:"items"`
}

// AttentionRequest addresses the paged surface: Since passes the adopted
// token, Page walks pages. Both empty is a tokenless scan — the first poll,
// and the fresh re-read before each send (#1616).
type AttentionRequest struct {
	Since string
	Page  string
}

// RegisterRow is the WINNING attendeeRegister row for the team Channel, as
// the attention API resolves it (App over organization; Worker over Agent
// over everyone).
type RegisterRow struct {
	NamedBy     string `json:"namedBy"`
	ChannelID   string `json:"channel"`
	MentionOnly bool   `json:"mentionOnly"`
	OwnerType   string `json:"ownerType,omitempty"`
}

// CursorResult is a worker's channelReadState (#1826) — the liveness source
// attention cannot see. FieldErrs carries GraphQL errors that arrived WITH
// data: an integer alongside errors is not a verdict (#1829 P1-1).
type CursorResult struct {
	LastSeen  *int
	FieldErrs []string
}

// Server is the typed GraphQL seam (native, never `hadron api`). Every
// method is a read: the router changes no server state.
type Server interface {
	AttentionPage(ctx context.Context, req AttentionRequest) (AttentionPage, error)
	RegisterRow(ctx context.Context, workerID string) (RegisterRow, error)
	ReadCursor(ctx context.Context, workerID string) (CursorResult, error)
	ChannelHead(ctx context.Context, channelRef string) (int, error)
}

// Outcome is one poll's result. Held maps a worker's NAME to why it was not
// nudged; any hold keeps the token unadopted.
type Outcome struct {
	Nudged []string `json:"nudged"`
	// Dropped: workers that read their chat themselves while the nudge waited (#1616).
	Dropped []string          `json:"dropped"`
	Covered []string          `json:"covered"`
	Held    map[string]string `json:"held"`
	Adopted bool              `json:"adopted"`
	// Liveness: flagged workers the liveness rule woke this poll (#1826).
	Liveness []string `json:"liveness"`
	// LivenessDropped: flagged workers that caught up while a liveness nudge
	// waited out its gates.
	LivenessDropped []string `json:"livenessDropped"`
}

func newOutcome() *Outcome {
	return &Outcome{Nudged: []string{}, Dropped: []string{}, Covered: []string{}, Held: map[string]string{},
		Liveness: []string{}, LivenessDropped: []string{}}
}

// RouterError is a failure that leaves the adopted token untouched: the poll
// changes nothing it has not already persisted, and the next poll retries.
type RouterError struct {
	Reason string
}

func (e *RouterError) Error() string { return e.Reason }

// DescribeOutcome is the one-line summary of a poll; never a token or a
// message body.
func DescribeOutcome(o *Outcome) string {
	parts := []string{"nudged: " + orNone(o.Nudged)}
	if len(o.Dropped) > 0 {
		parts = append(parts, "dropped (read while waiting): "+strings.Join(o.Dropped, ", "))
	}
	if len(o.Covered) > 0 {
		parts = append(parts, "already nudged: "+strings.Join(o.Covered, ", "))
	}
	if len(o.Liveness) > 0 {
		parts = append(parts, "liveness nudged: "+strings.Join(o.Liveness, ", "))
	}
	if len(o.LivenessDropped) > 0 {
		parts = append(parts, "liveness dropped (read while waiting): "+strings.Join(o.LivenessDropped, ", "))
	}
	if len(o.Held) > 0 {
		var held []string
		for _, n := range sortedKeys(o.Held) {
			held = append(held, n+" ("+o.Held[n]+")")
		}
		parts = append(parts, "held: "+strings.Join(held, "; "))
	}
	if o.Adopted {
		parts = append(parts, "token adopted")
	} else {
		parts = append(parts, "token held")
	}
	return strings.Join(parts, " | ")
}

func orNone(xs []string) string {
	if len(xs) == 0 {
		return "none"
	}
	return strings.Join(xs, ", ")
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	return keys
}

// router carries one poll's collaborators.
type router struct {
	cfg  *Config
	srv  Server
	orca OrcaRunner
	log  func(string)
	now  func() int64
}

// ── Attention ───────────────────────────────────────────────────────────

// pollAttention drains the paged scan (#1384). Read-only: the final page's
// adoptableSince is adopted later, if at all. Paged v2 because only it
// carries lastRelevantSeq, the mention frontier the ledger needs.
func (r *router) pollAttention(ctx context.Context, token string) (string, []AttentionItem, error) {
	var items []AttentionItem
	page := ""
	for range maxPages {
		req := AttentionRequest{Page: page}
		if page == "" {
			req.Since = token
		}
		res, err := r.srv.AttentionPage(ctx, req)
		if err != nil {
			return "", nil, routerErrorf("attention poll failed: %v", err)
		}
		items = append(items, res.Items...)
		if res.NextPage != "" {
			page = res.NextPage
			continue
		}
		if !res.ScanComplete || res.AdoptableSince == "" {
			return "", nil, routerErrorf("attention poll failed: the final page carried no adoptable token")
		}
		return res.AdoptableSince, items, nil
	}
	return "", nil, routerErrorf("attention poll failed: more than %d pages", maxPages)
}

func (r *router) itemFor(items []AttentionItem, worker string) *AttentionItem {
	for i := range items {
		if items[i].Worker == worker && items[i].Channel == r.cfg.Channel {
			return &items[i]
		}
	}
	return nil
}

// freshAttention is the worker's CURRENT attention, recomputed immediately
// before a send (#1616). Tokenless, so it is relative to the worker's own
// read cursor. The token this scan returns is DISCARDED: adoption belongs to
// the poll's own scan.
func (r *router) freshAttention(ctx context.Context, worker string) (*AttentionItem, error) {
	_, items, err := r.pollAttention(ctx, "")
	if err != nil {
		return nil, err
	}
	return r.itemFor(items, worker), nil
}

// hasMentionOnlyRow: the worker's EFFECTIVE registration on the Channel is
// its own, mention-only row. Without one, the server reports the worker for
// ANY new message with its full unread-mention backlog, so an old mention
// would make every chat message wake it. A failed query fails the poll:
// absence of evidence is not a verdict.
func (r *router) hasMentionOnlyRow(ctx context.Context, worker string) (bool, error) {
	row, err := r.srv.RegisterRow(ctx, worker)
	if err != nil {
		return false, routerErrorf("register query failed: %v", err)
	}
	return row.ChannelID == r.cfg.Channel && row.NamedBy == "WORKER" && row.MentionOnly, nil
}

// readCursor is the worker's read cursor on the team Channel, or nil on ANY
// unknown or error — fail-closed (#1826, #1829 P1-1).
func (r *router) readCursor(ctx context.Context, worker string) *int {
	res, err := r.srv.ReadCursor(ctx, worker)
	if err != nil || len(res.FieldErrs) > 0 || res.LastSeen == nil || *res.LastSeen < 0 || *res.LastSeen > seqMax {
		return nil
	}
	v := *res.LastSeen
	return &v
}

// channelHead is the team Channel's watermark, or nil on any unknown or error.
func (r *router) channelHead(ctx context.Context) *int {
	v, err := r.srv.ChannelHead(ctx, r.cfg.Channel)
	if err != nil || v < 0 || v > seqMax {
		return nil
	}
	return &v
}

// ── Gates ───────────────────────────────────────────────────────────────

// provesIdle: whether the terminal may be typed into, with THREE outcomes
// where tui-idle alone gave two (#7398). A tui-idle timeout is "busy" only
// while Orca TRACKS the terminal's output; untracked, the timeout is
// unknown, and the screen must prove an empty prompt AND positive quiet above
// it — an empty composer proves nobody typed, not that the agent is not
// working (both agents draw one mid-turn; @codex P2, #1700). A non-empty
// reason on success marks the untracked screen proof.
func (r *router) provesIdle(ctx context.Context, handle, family string, lastOutputAt *int64, timeoutMS int) (bool, string) {
	if r.isIdle(ctx, handle, timeoutMS) {
		return true, ""
	}
	if lastOutputAt == nil {
		sc := r.readScreen(ctx, handle)
		if empty, why := promptIsEmptyFromScreen(sc, family); !empty {
			return false, "unknown idle state — no output tracking, screen shows " + why
		}
		verdict, detail := "unknown", "screen unreadable"
		if sc != nil {
			verdict, detail = idleProofAbovePrompt(sc.Tail, family)
		}
		switch verdict {
		case "busy":
			return false, "unknown idle state — no output tracking, a running turn is drawn above the prompt"
		case "unknown":
			return false, "unknown idle state — no output tracking, " + detail
		}
		return true, "untracked terminal, screen proves idle (empty prompt, positive idle chrome)"
	}
	return false, "busy for " + itoa((timeoutMS+500)/1000) + "s"
}

func (r *router) promptIsEmpty(ctx context.Context, handle, family string) (bool, string) {
	return promptIsEmptyFromScreen(r.readScreen(ctx, handle), family)
}

// ── Resolution ──────────────────────────────────────────────────────────

// bindingFileName is the CLI's worktree binding (`hadron team session
// start`, internal/cmd/team/state.go), kept in the worktree's git dir.
const bindingFileName = "hadron-team-session.json"

// boundWorker is the worker id a CLI binding in this worktree names, if any.
// An MCP-bound worker leaves none; absence is not a mismatch.
func boundWorker(worktree string) string {
	gitDir := filepath.Join(worktree, ".git")
	info, err := os.Stat(gitDir)
	if err != nil {
		return ""
	}
	if !info.IsDir() {
		b, err := os.ReadFile(gitDir)
		if err != nil {
			return ""
		}
		pointer := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(string(b)), "gitdir:"))
		if filepath.IsAbs(pointer) {
			gitDir = pointer
		} else {
			gitDir = filepath.Join(worktree, pointer)
		}
	}
	b, err := os.ReadFile(filepath.Join(gitDir, bindingFileName))
	if err != nil {
		return ""
	}
	var binding struct {
		WorkerID string `json:"workerId"`
	}
	if json.Unmarshal(b, &binding) != nil {
		return ""
	}
	return binding.WorkerID
}

// unresolved is why a roster row maps to no terminal; it becomes a hold.
type unresolved struct{ reason string }

func (u *unresolved) Error() string { return u.reason }

// ResolveTerminal maps a roster row to the ONE agent terminal in its
// worktree: (handle, family, how), how being "pin" (#1845), "identity", or
// "screen" (the #1823 identity-flap fallback). An unresolvable row returns
// how "none" and an error whose text is the hold reason.
//
// A pin resolves first but is trusted only within the row's worktree,
// connected and writable. Identity is the terminal whose agentIdentity is
// claude/codex. Only when identity resolves NOTHING does the fallback read
// screens: ONE connected terminal whose screen positively shows a family.
// Ambiguity, an unclassifiable screen or a plain shell holds — never a guess.
func ResolveTerminal(entry RosterEntry, terminals []Terminal, c *Config, orca OrcaRunner) (handle, family, how string, err error) {
	r := &router{cfg: c, orca: orca}
	return r.resolveTerminal(context.Background(), entry, terminals)
}

func (r *router) resolveTerminal(ctx context.Context, entry RosterEntry, terminals []Terminal) (string, string, string, error) {
	worktree := realPath(entry.Worktree)
	if bound := boundWorker(worktree); bound != "" && bound != entry.Worker {
		return "", "", "none", &unresolved{"worktree is bound to another worker (" + bound + ")"}
	}
	var inWorktree []Terminal
	for _, t := range terminals {
		if t.WorktreePath != "" && realPath(t.WorktreePath) == worktree && t.Connected && t.Writable {
			inWorktree = append(inWorktree, t)
		}
	}
	if entry.Terminal != "" {
		for _, t := range inWorktree {
			if t.Handle != entry.Terminal {
				continue
			}
			// The family feeds the untracked idle proof: Orca's identity
			// when it still reports one, else ONE screen read of this KNOWN
			// terminal — family detection, not selection. An unknown family
			// holds fail-closed downstream; a pin never widens a gate.
			family := t.identity()
			if !agentFamilies[family] {
				family = ScreenAgentFamily(r.readScreen(ctx, t.Handle))
			}
			return t.Handle, family, "pin", nil
		}
	}
	var agents []Terminal
	for _, t := range inWorktree {
		if agentFamilies[t.identity()] {
			agents = append(agents, t)
		}
	}
	switch {
	case len(agents) > 1 && entry.Terminal != "":
		return "", "", "none", &unresolved{itoa(len(agents)) + " agent terminals in its worktree and the pinned handle is stale"}
	case len(agents) > 1:
		return "", "", "none", &unresolved{itoa(len(agents)) + " agent terminals in its worktree (ambiguous)"}
	case len(agents) == 1:
		return agents[0].Handle, agents[0].identity(), "identity", nil
	}
	var verified []Terminal
	var families []string
	for _, t := range inWorktree {
		if f := ScreenAgentFamily(r.readScreen(ctx, t.Handle)); f != "" {
			verified = append(verified, t)
			families = append(families, f)
		}
	}
	if len(verified) == 1 {
		return verified[0].Handle, families[0], "screen", nil
	}
	if entry.Terminal != "" {
		return "", "", "none", &unresolved{"no live agent terminal in its worktree and the pinned handle is stale"}
	}
	return "", "", "none", &unresolved{"no live agent terminal in its worktree"}
}

// alreadyNudged: a nudge sent under this token still covers the worker's
// attention while nothing relevant arrived since — its latest mention is no
// later than the one the nudge was about (@codex P1, #1490).
func alreadyNudged(rec LedgerNudge, ok bool, item AttentionItem) bool {
	return ok && item.LastRelevantSeq != 0 && item.LastRelevantSeq <= rec.RelevantSeq
}

func lastOutputOf(terminals []Terminal, handle string) *int64 {
	for _, t := range terminals {
		if t.Handle == handle {
			return t.LastOutputAt
		}
	}
	return nil
}

// ── One poll ────────────────────────────────────────────────────────────

// PollOnce polls attention, nudges each idle roster worker with new mentions,
// adopts the token only if all were — then runs the #1826 liveness pass over
// the flagged roster. now is the clock (epoch ms), injectable for the strict
// >N boundary; nil means the wall clock. A *RouterError leaves the adopted
// token unchanged.
func PollOnce(ctx context.Context, c *Config, s Server, orca OrcaRunner, log func(string), now func() int64) (*Outcome, error) {
	if now == nil {
		now = func() int64 { return time.Now().UnixMilli() }
	}
	if log == nil {
		log = func(string) {}
	}
	r := &router{cfg: c, srv: s, orca: orca, log: log, now: now}
	st := NewState(c.StateDir)
	if err := st.ensure(); err != nil {
		return nil, routerErrorf("state directory: %v", err)
	}
	return withLock(c.StateDir, func(stillOwner func() bool) (*Outcome, error) {
		return r.poll(ctx, st, stillOwner)
	})
}

var errLostLock = routerErrorf("lost the writer lock mid-poll; stopping without further sends or adoption")

func (r *router) poll(ctx context.Context, st *State, stillOwner func() bool) (*Outcome, error) {
	cfg := r.cfg
	mustOwn := func() error {
		if !stillOwner() {
			return errLostLock
		}
		return nil
	}
	token, err := st.token()
	if err != nil {
		return nil, routerErrorf("adopted token unreadable: %v", err)
	}
	scanToken, items, err := r.pollAttention(ctx, token)
	if err != nil {
		return nil, err
	}
	ledger := st.Ledger()
	saveLedger := func() error {
		if err := st.SaveLedger(ledger); err != nil {
			return routerErrorf("ledger write failed: %v", err)
		}
		return nil
	}
	type target struct {
		entry RosterEntry
		item  AttentionItem
	}
	var targets []target
	var flagged []RosterEntry
	for _, e := range cfg.Roster {
		// Something relevant (for a mention-only row: a mention) since the token.
		if it := r.itemFor(items, e.Worker); it != nil && it.LastRelevantSeq != 0 && it.UnreadMentions > 0 {
			targets = append(targets, target{e, *it})
		}
		if e.flagged() {
			flagged = append(flagged, e)
		}
	}

	out := newOutcome()
	var terminals []Terminal
	if len(targets) > 0 || len(flagged) > 0 {
		if terminals, err = r.listTerminals(ctx); err != nil {
			return nil, err
		}
	}
	// The handle each worker's mention nudge was DELIVERED to (#1829 local
	// P2-5): a first-ever liveness record is born with its terminal.
	mentionHandles := map[string]string{}
	clearHold := func(worker string) {
		delete(ledger.HeldSince, worker)
		delete(ledger.HeldLoggedAt, worker)
	}
	for _, t := range targets {
		entry, seen := t.entry, t.item
		ok, err := r.hasMentionOnlyRow(ctx, entry.Worker)
		if err != nil {
			return nil, err
		}
		if !ok {
			// A configuration error, not a delivery: never nudge on every message.
			out.Held[entry.Name] = "no worker-specific mention-only register row on the team Channel"
			continue
		}
		if rec, has := ledger.Nudged[entry.Worker]; alreadyNudged(rec, has, seen) {
			out.Covered = append(out.Covered, entry.Name)
			continue
		}
		handle, family, via, rerr := r.resolveTerminal(ctx, entry, terminals)
		if via == "screen" {
			// A resolution note, not a held reason (those dedupe per token):
			// these lines are the flap-incidence record for the upstream
			// Orca report — grep 'resolved by screen'.
			r.log(logPrefix + entry.Name + ": terminal resolved by screen (Orca agentIdentity tracking lost)")
		}
		if rerr != nil {
			out.Held[entry.Name] = rerr.Error()
			continue
		}
		if gate, why := r.provesIdle(ctx, handle, family, lastOutputOf(terminals, handle), cfg.BusyWaitMS); !gate {
			out.Held[entry.Name] = why
			continue
		}
		// AFTER idle, as close to the send as possible: idle says the agent
		// is not working, this says nobody is typing into it (#5473).
		empty, why := r.promptIsEmpty(ctx, handle, family)
		if !empty {
			out.Held[entry.Name] = "prompt not empty (" + why + ")"
			continue
		}
		if why != "" {
			// Passed by DISCARDING a screen-refuted draft: visible once per
			// token per worker; the draft's text is never logged.
			const note = "stale Orca draft discarded (screen proves the composer empty)"
			if ledger.DraftNotes[entry.Worker] != note {
				ledger.DraftNotes[entry.Worker] = note
				if err := saveLedger(); err != nil {
					return nil, err
				}
				r.log(logPrefix + entry.Name + ": " + note)
			}
		}
		if err := mustOwn(); err != nil {
			return nil, err
		}
		// Recomputed after every gate (#1616): the poll's numbers are as old
		// as the busy-wait was long.
		fresh, err := r.freshAttention(ctx, entry.Worker)
		if err != nil {
			return nil, err
		}
		if fresh == nil || fresh.UnreadMentions <= 0 || fresh.LastRelevantSeq == 0 {
			// Read while waiting. Record the frontier as covered so a held
			// token doesn't re-run the gates every poll; a NEW mention has a
			// higher seq and is never mistaken for covered.
			ledger.Nudged[entry.Worker] = LedgerNudge{RelevantSeq: seen.LastRelevantSeq, At: r.now(), Stages: "dropped: read while waiting"}
			clearHold(entry.Worker)
			if err := saveLedger(); err != nil {
				return nil, err
			}
			out.Dropped = append(out.Dropped, entry.Name)
			continue
		}
		// The recompute reopened BOTH windows the gates had closed: re-prove
		// idle (fail-closed, short) and empty. An untracked terminal gets the
		// screen fallback again — but on RELOADED tracking (@codex P2,
		// #1700): a terminal that produced output since gets the ordinary
		// failed-wait hold back. A vanished terminal falls to the fallback.
		relisted, err := r.listTerminals(ctx)
		if err != nil {
			return nil, err
		}
		if idle, why := r.provesIdle(ctx, handle, family, lastOutputOf(relisted, handle), recheckIdleTimeoutMS); !idle {
			out.Held[entry.Name] = "busy again after the recompute (" + why + ")"
			continue
		}
		if stillEmpty, whyNow := r.promptIsEmpty(ctx, handle, family); !stillEmpty {
			out.Held[entry.Name] = "prompt not empty on recheck (" + whyNow + ")"
			continue
		}
		if err := mustOwn(); err != nil {
			return nil, err
		}
		sent, how := r.sendNudge(ctx, handle, NudgeText(*fresh))
		if !sent {
			out.Held[entry.Name] = how
			continue
		}
		ledger.Nudged[entry.Worker] = LedgerNudge{RelevantSeq: fresh.LastRelevantSeq, At: r.now(), Stages: how}
		clearHold(entry.Worker)
		// Persisted immediately after the send, before any other step: a
		// crash or later failure must not send the same nudge again.
		if err := saveLedger(); err != nil {
			return nil, err
		}
		mentionHandles[entry.Worker] = handle
		out.Nudged = append(out.Nudged, entry.Name)
	}

	if err := r.livenessPass(ctx, st, out, flagged, terminals, mentionHandles, mustOwn); err != nil {
		return nil, err
	}

	if len(out.Held) > 0 {
		// Each held worker's reason is logged once per token, not every poll.
		var fresh, longHolds []string
		for _, e := range cfg.Roster {
			reason, held := out.Held[e.Name]
			if !held {
				continue
			}
			if ledger.Reported[e.Name] != reason {
				fresh = append(fresh, e.Name+": "+reason)
			}
			ledger.Reported[e.Name] = reason
			// Long-hold visibility (#6736): a worker pinned 30+ minutes says
			// so, at most once every 30 minutes.
			since, ok := ledger.HeldSince[e.Worker]
			if !ok {
				since = r.now()
			}
			ledger.HeldSince[e.Worker] = since
			minutes := (r.now() - since) / 60_000
			if minutes >= 30 && r.now()-ledger.HeldLoggedAt[e.Worker] >= 30*60_000 {
				ledger.HeldLoggedAt[e.Worker] = r.now()
				longHolds = append(longHolds, e.Name+" held "+itoa(int(minutes))+" min with mentions pending ("+reason+")")
			}
		}
		if err := saveLedger(); err != nil {
			return nil, err
		}
		if len(fresh) > 0 {
			r.log(logPrefix + strings.Join(fresh, "; "))
		}
		if len(longHolds) > 0 {
			r.log(logPrefix + strings.Join(longHolds, "; "))
		}
		return out, nil
	}
	if err := mustOwn(); err != nil {
		return nil, err
	}
	// Adoption clears the ledger, but a nudge may carry a frontier BEYOND the
	// adopted token's horizon (the fresh scan reads later than the main one),
	// and the next poll would list that mention again (Atilla P2 / Diego
	// P3-1). Carry the records the token cannot cover.
	carried := map[string]LedgerNudge{}
	for w, rec := range ledger.Nudged {
		horizon := 0
		if it := r.itemFor(items, w); it != nil {
			horizon = it.LastRelevantSeq
		}
		if horizon == 0 || rec.RelevantSeq > horizon {
			carried[w] = rec
		}
	}
	if err := st.adopt(scanToken); err != nil {
		return nil, routerErrorf("token adoption failed: %v", err)
	}
	if len(carried) > 0 {
		l := emptyLedger(st.base())
		l.Nudged = carried
		if err := st.SaveLedger(l); err != nil {
			return nil, routerErrorf("ledger write failed: %v", err)
		}
	}
	out.Adopted = true
	return out, nil
}

func itoa(n int) string { return strconv.Itoa(n) }
