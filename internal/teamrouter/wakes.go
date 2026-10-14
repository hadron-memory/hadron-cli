package teamrouter

// The v2 delivery source (cli#829 PR 2; plan hadron-server:design:named-heartbeats
// §7): the server decides WHO, WHY and HOW OFTEN through dueWakes; the router
// decides HOW, with the same resolution and send gates as v1. It runs on an
// App key with attention:deliver and no session (cor:agt:080:22), and
// acknowledges each delivered wake with its proof (cor:agt:080:16/:17).
//
// What v2 changes from v1 (plan §7.2): no token adoption, no ledger.json,
// no mention-only prerequisite — the server's delivery record owns "no double
// nudge". What it keeps: every resolution and send gate, and a fresh
// per-worker re-read right before the send. The re-read is not v1's count
// recompute for its own sake: an outstanding wake is re-handed IDENTICALLY
// with a fresh proof (the server's reservation), so re-reading costs no budget,
// starts the proof's 15-minute window at the send rather than at the scan
// (gates can busy-wait a minute per worker), and drops a worker that is no
// longer due. Each delivered wake is acknowledged at once; a sent-record
// persisted BEFORE the ack lets the next poll acknowledge a re-handed wake
// without typing it again.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"time"
)

// WakeChannel is one channel range a wake covers.
type WakeChannel struct {
	ID               string
	Name             string
	FromSeq          int
	ThroughSeq       int
	LatestMentionSeq *int
}

// WakeTask is a TASK reason's task (#1708).
type WakeTask struct {
	Ref   string // stable label, e.g. TASK:<id>:assigned
	Title string
	Event string
}

// WakeReason is why a wake is due: a MENTION, a ticker TICK, or a TASK event.
type WakeReason struct {
	Kind   string
	Ticker string // the ticker's name, for TICK
	Task   *WakeTask
}

// Wake is one due wake for one worker.
type Wake struct {
	Worker         string
	WorkerName     string
	Unread         int
	UnreadMentions int
	Channels       []WakeChannel
	Reasons        []WakeReason
	// Proof is handed back to acknowledgeWakes after delivery: server-signed,
	// bound to this requester, good for 15 minutes. Never logged, never typed.
	Proof string
}

// DormantWorker is a dormant worker whose mentions were withheld.
type DormantWorker struct {
	Worker   string
	Name     string
	Mentions int
}

// WakePage is one page of dueWakes.
type WakePage struct {
	Status    string // OK | WAKE_BUDGET_EXHAUSTED
	Withheld  int
	Dormant   []DormantWorker
	Wakes     []Wake
	EndCursor string
	HasNext   bool
}

// AckResult is acknowledgeWakes' answer.
type AckResult struct {
	Acknowledged int
	Unchanged    int
}

// WakeServer is the v2 seam: the due-wake read and the acknowledgement.
type WakeServer interface {
	DueWakes(ctx context.Context, workerRefs []string, after string) (WakePage, error)
	AcknowledgeWakes(ctx context.Context, proofs []string) (AckResult, error)
}

// WakeOutcome is one v2 poll's result.
type WakeOutcome struct {
	Nudged []string `json:"nudged"`
	// Dropped: no longer due at the fresh re-read just before the send.
	Dropped []string          `json:"dropped"`
	Held    map[string]string `json:"held"`
	// AckFailed: delivered, but the acknowledgement was refused or failed.
	// The sent-record keeps the next poll from typing it again.
	AckFailed map[string]string `json:"ackFailed"`
	// Recovered: an earlier delivery whose acknowledgement this poll completed
	// without sending again.
	Recovered []string `json:"recovered"`
	// Would: shadow mode only — who would be woken, and why.
	Would    map[string]string `json:"would"`
	Status   string            `json:"status"`
	Withheld int               `json:"withheld"`
	Dormant  []string          `json:"dormant"`
}

// maxWakePages bounds the dueWakes pages drained per poll.
const maxWakePages = 1000

// sentRecordTTL: a sent-record older than this is dropped. A proof lives 15
// minutes and the server voids a wake it can no longer acknowledge, so an
// identical wake a day later is a new delivery, not a re-hand.
const sentRecordTTL = 24 * time.Hour

func newWakeOutcome() *WakeOutcome {
	return &WakeOutcome{Nudged: []string{}, Dropped: []string{}, Held: map[string]string{}, AckFailed: map[string]string{},
		Recovered: []string{}, Would: map[string]string{}, Status: "OK", Dormant: []string{}}
}

// wakeIdentity is what makes two wakes the same delivery: the worker, each
// channel's range, and each reason. The server re-hands an unacknowledged
// wake with exactly these, and any later wake differs in at least one.
func wakeIdentity(w Wake) string {
	var parts []string
	for _, c := range w.Channels {
		parts = append(parts, fmt.Sprintf("c:%s:%d-%d", c.ID, c.FromSeq, c.ThroughSeq))
	}
	for _, r := range w.Reasons {
		key := "r:" + r.Kind
		if r.Ticker != "" {
			key += ":" + r.Ticker
		}
		if r.Task != nil {
			key += ":" + r.Task.Ref
		}
		parts = append(parts, key)
	}
	slices.Sort(parts)
	return w.Worker + "|" + strings.Join(parts, "|")
}

// sentRecord marks a delivered wake whose acknowledgement has not landed.
type sentRecord struct {
	Identity string `json:"identity"`
	At       int64  `json:"at"`
}

func (s *State) sentPath() string      { return filepath.Join(s.dir, "wakes-sent.json") }
func (s *State) wakeNotesPath() string { return filepath.Join(s.dir, "wake-notes.json") }

// sentRecords reads the sent-records, sanitized: anything malformed is
// dropped. Losing a record can cost one repeated nudge, never a silenced one.
func (s *State) sentRecords(now int64) map[string]sentRecord {
	out := map[string]sentRecord{}
	b, err := os.ReadFile(s.sentPath())
	if err != nil {
		return out
	}
	var raw map[string]sentRecord
	if json.Unmarshal(b, &raw) != nil {
		return out
	}
	for w, r := range raw {
		if r.Identity != "" && now-r.At < sentRecordTTL.Milliseconds() {
			out[w] = r
		}
	}
	return out
}

// wakeNotes: what was last logged, so a steady condition logs once.
type wakeNotes struct {
	Budget   string            `json:"budget"`
	Reported map[string]string `json:"reported"`
	Drafts   map[string]string `json:"drafts"`
}

func (s *State) wakeNotes() wakeNotes {
	n := wakeNotes{Reported: map[string]string{}, Drafts: map[string]string{}}
	if b, err := os.ReadFile(s.wakeNotesPath()); err == nil {
		_ = json.Unmarshal(b, &n)
	}
	if n.Reported == nil {
		n.Reported = map[string]string{}
	}
	if n.Drafts == nil {
		n.Drafts = map[string]string{}
	}
	return n
}

// wakeRouter is one v2 poll's collaborators.
type wakeRouter struct {
	*router
	srv WakeServer
}

// drain reads every page of dueWakes for the roster.
func (r *wakeRouter) drain(ctx context.Context, refs []string) (WakePage, error) {
	var all WakePage
	after := ""
	for range maxWakePages {
		page, err := r.srv.DueWakes(ctx, refs, after)
		if err != nil {
			return WakePage{}, routerErrorf("dueWakes failed: %v", err)
		}
		all.Wakes = append(all.Wakes, page.Wakes...)
		all.Status, all.Withheld, all.Dormant = page.Status, page.Withheld, page.Dormant
		if !page.HasNext || page.EndCursor == "" {
			return all, nil
		}
		after = page.EndCursor
	}
	return WakePage{}, routerErrorf("dueWakes failed: more than %d pages", maxWakePages)
}

// fresh re-reads one worker's wake: nil when it is no longer due.
func (r *wakeRouter) fresh(ctx context.Context, worker string) (*Wake, error) {
	page, err := r.srv.DueWakes(ctx, []string{worker}, "")
	if err != nil {
		return nil, routerErrorf("dueWakes re-read failed: %v", err)
	}
	for i := range page.Wakes {
		if page.Wakes[i].Worker == worker {
			return &page.Wakes[i], nil
		}
	}
	return nil, nil
}

// ack acknowledges one proof; the reason on failure never carries the proof.
func (r *wakeRouter) ack(ctx context.Context, proof string) (bool, string) {
	if _, err := r.srv.AcknowledgeWakes(ctx, []string{proof}); err != nil {
		return false, "acknowledgement failed: " + strings.ReplaceAll(err.Error(), proof, "<proof>")
	}
	return true, ""
}

// describeWake is shadow mode's summary: reasons and ranges, no proof.
func describeWake(w Wake) string {
	var reasons []string
	for _, rs := range w.Reasons {
		switch {
		case rs.Task != nil:
			reasons = append(reasons, rs.Kind+" "+rs.Task.Ref)
		case rs.Ticker != "":
			reasons = append(reasons, rs.Kind+" "+rs.Ticker)
		default:
			reasons = append(reasons, rs.Kind)
		}
	}
	var ranges []string
	for _, c := range w.Channels {
		ranges = append(ranges, fmt.Sprintf("%s #%d–#%d", channelLabel(c), c.FromSeq, c.ThroughSeq))
	}
	return strings.Join(reasons, ", ") + "; " + strings.Join(ranges, ", ")
}

func channelLabel(c WakeChannel) string {
	if c.Name != "" {
		return c.Name
	}
	return c.ID
}

// PollWakes runs one v2 poll. With shadow set it only reads dueWakes: no
// terminal is touched, nothing is sent or acknowledged, no state is written
// (the server still reserves what it hands out — see the plan doc).
func PollWakes(ctx context.Context, c *Config, s WakeServer, orca OrcaRunner, log func(string), now func() int64, shadow bool) (*WakeOutcome, error) {
	if now == nil {
		now = func() int64 { return time.Now().UnixMilli() }
	}
	if log == nil {
		log = func(string) {}
	}
	r := &wakeRouter{router: &router{cfg: c, orca: orca, log: log, now: now}, srv: s}
	out := newWakeOutcome()
	if len(c.Roster) == 0 {
		// An empty workerRefs reads every worker of the App: never ask for that.
		return out, nil
	}
	refs := make([]string, 0, len(c.Roster))
	for _, e := range c.Roster {
		refs = append(refs, e.Worker)
	}
	if shadow {
		page, err := r.drain(ctx, refs)
		if err != nil {
			return nil, err
		}
		r.summarize(out, page)
		for _, e := range c.Roster {
			if w := wakeFor(page.Wakes, e.Worker); w != nil {
				out.Would[e.Name] = describeWake(*w)
			}
		}
		return out, nil
	}
	st := NewState(c.StateDir)
	if err := st.ensure(); err != nil {
		return nil, routerErrorf("state directory: %v", err)
	}
	return withLock(c.StateDir, func(stillOwner func() bool) (*WakeOutcome, error) {
		page, err := r.drain(ctx, refs)
		if err != nil {
			return nil, err
		}
		r.summarize(out, page)
		if err := r.deliver(ctx, st, out, page, stillOwner); err != nil {
			return nil, err
		}
		return out, r.report(st, out)
	})
}

func wakeFor(wakes []Wake, worker string) *Wake {
	for i := range wakes {
		if wakes[i].Worker == worker {
			return &wakes[i]
		}
	}
	return nil
}

func (r *wakeRouter) summarize(out *WakeOutcome, page WakePage) {
	if page.Status != "" {
		out.Status = page.Status
	}
	out.Withheld = page.Withheld
	for _, d := range page.Dormant {
		name := d.Name
		if name == "" {
			name = d.Worker
		}
		out.Dormant = append(out.Dormant, name)
	}
}

func (r *wakeRouter) deliver(ctx context.Context, st *State, out *WakeOutcome, page WakePage, stillOwner func() bool) error {
	sent := st.sentRecords(r.now())
	notes := st.wakeNotes()
	saveSent := func() error {
		if err := writeJSON(st.sentPath(), sent); err != nil {
			return routerErrorf("sent-record write failed: %v", err)
		}
		return nil
	}
	mustOwn := func() error {
		if !stillOwner() {
			return errLostLock
		}
		return nil
	}
	var terminals []Terminal
	listed := false
	for _, entry := range r.cfg.Roster {
		wake := wakeFor(page.Wakes, entry.Worker)
		if wake == nil {
			continue
		}
		// A re-handed wake already delivered: acknowledge it, type nothing.
		if rec, ok := sent[entry.Worker]; ok && rec.Identity == wakeIdentity(*wake) {
			if acked, why := r.ack(ctx, wake.Proof); acked {
				delete(sent, entry.Worker)
				if err := saveSent(); err != nil {
					return err
				}
				out.Recovered = append(out.Recovered, entry.Name)
			} else {
				out.AckFailed[entry.Name] = why
			}
			continue
		}
		if !listed {
			var err error
			if terminals, err = r.listTerminals(ctx); err != nil {
				return err
			}
			listed = true
		}
		handle, family, via, rerr := r.resolveTerminal(ctx, entry, terminals)
		if via == "screen" {
			r.log(logPrefix + entry.Name + ": terminal resolved by screen (Orca agentIdentity tracking lost)")
		}
		if rerr != nil {
			out.Held[entry.Name] = rerr.Error()
			continue
		}
		if gate, why := r.provesIdle(ctx, handle, family, lastOutputOf(terminals, handle), r.cfg.BusyWaitMS); !gate {
			out.Held[entry.Name] = why
			continue
		}
		empty, why := r.promptIsEmpty(ctx, handle, family)
		if !empty {
			out.Held[entry.Name] = "prompt not empty (" + why + ")"
			continue
		}
		if why != "" && notes.Drafts[entry.Worker] != wakeIdentity(*wake) {
			notes.Drafts[entry.Worker] = wakeIdentity(*wake)
			r.log(logPrefix + entry.Name + ": stale Orca draft discarded (screen proves the composer empty)")
		}
		if err := mustOwn(); err != nil {
			return err
		}
		fresh, err := r.fresh(ctx, entry.Worker)
		if err != nil {
			return err
		}
		if fresh == nil {
			out.Dropped = append(out.Dropped, entry.Name)
			continue
		}
		relisted, err := r.listTerminals(ctx)
		if err != nil {
			return err
		}
		if idle, why := r.provesIdle(ctx, handle, family, lastOutputOf(relisted, handle), recheckIdleTimeoutMS); !idle {
			out.Held[entry.Name] = "busy again after the re-read (" + why + ")"
			continue
		}
		if stillEmpty, whyNow := r.promptIsEmpty(ctx, handle, family); !stillEmpty {
			out.Held[entry.Name] = "prompt not empty on recheck (" + whyNow + ")"
			continue
		}
		if err := mustOwn(); err != nil {
			return err
		}
		ok, how := r.sendNudge(ctx, handle, WakeNudgeText(*fresh, r.cfg.Channel))
		if !ok {
			out.Held[entry.Name] = how
			continue
		}
		// Durable BEFORE the acknowledgement: a crash or a refused ack must
		// not make the next poll type this wake again.
		sent[entry.Worker] = sentRecord{Identity: wakeIdentity(*fresh), At: r.now()}
		if err := saveSent(); err != nil {
			return err
		}
		out.Nudged = append(out.Nudged, entry.Name)
		if acked, why := r.ack(ctx, fresh.Proof); acked {
			delete(sent, entry.Worker)
			if err := saveSent(); err != nil {
				return err
			}
		} else {
			out.AckFailed[entry.Name] = why
		}
	}
	return writeJSON(st.wakeNotesPath(), notes)
}

// report logs what changed: holds and ack failures once per reason, the
// budget and dormancy once per state.
func (r *wakeRouter) report(st *State, out *WakeOutcome) error {
	notes := st.wakeNotes()
	var lines []string
	reported := map[string]string{}
	for _, e := range r.cfg.Roster {
		reason, held := out.Held[e.Name]
		if !held {
			reason, held = out.AckFailed[e.Name]
		}
		if !held {
			continue
		}
		reported[e.Name] = reason
		if notes.Reported[e.Name] != reason {
			lines = append(lines, e.Name+": "+reason)
		}
	}
	notes.Reported = reported
	budget := out.Status
	if len(out.Dormant) > 0 {
		budget += "; dormant with mentions withheld: " + strings.Join(out.Dormant, ", ")
	}
	if budget != notes.Budget {
		if budget != "OK" {
			lines = append(lines, "dueWakes "+budget)
		}
		notes.Budget = budget
	}
	if len(lines) > 0 {
		r.log(logPrefix + strings.Join(lines, "; "))
	}
	if err := writeJSON(st.wakeNotesPath(), notes); err != nil {
		return routerErrorf("wake notes write failed: %v", err)
	}
	return nil
}

// WakeNudgeText renders a v2 wake as the one line typed into the terminal:
// why (the reasons), what (counts and each channel's range), and where to
// start — the team chat with v1's read anchor, any other channel by id, any
// task by id. Never the proof.
func WakeNudgeText(w Wake, teamChannel string) string {
	var why []string
	var tasks []WakeTask
	mention := false
	for _, r := range w.Reasons {
		switch {
		case r.Kind == "MENTION":
			if !mention {
				mention = true
				why = append(why, plural(w.UnreadMentions, "mention"))
			}
		case r.Kind == "TICK":
			name := r.Ticker
			if name == "" {
				name = "ticker"
			}
			why = append(why, name+" tick")
		case r.Kind == "TASK" && r.Task != nil:
			event := strings.ToLower(strings.ReplaceAll(r.Task.Event, "_", " "))
			why = append(why, fmt.Sprintf("task %s: %s (%s)", event, r.Task.Title, r.Task.Ref))
			tasks = append(tasks, *r.Task)
		default:
			why = append(why, strings.ToLower(r.Kind))
		}
	}
	var b strings.Builder
	fmt.Fprintf(&b, "Hadron team attention (%s)", strings.Join(why, "; "))
	var team *WakeChannel
	var others []WakeChannel
	var ranges []string
	for i, c := range w.Channels {
		rng := fmt.Sprintf("%s #%d–#%d", channelLabel(c), c.FromSeq, c.ThroughSeq)
		if c.LatestMentionSeq != nil {
			rng += fmt.Sprintf(", latest mention #%d", *c.LatestMentionSeq)
		}
		ranges = append(ranges, rng)
		if c.ID == teamChannel {
			team = &w.Channels[i]
		} else {
			others = append(others, c)
		}
	}
	if len(w.Channels) > 0 {
		fmt.Fprintf(&b, ": %d relevant unread (%s); %s.", w.Unread, plural(w.UnreadMentions, "mention"), strings.Join(ranges, "; "))
	} else {
		b.WriteString(".")
	}
	for _, c := range others {
		since := max(0, c.FromSeq-1)
		fmt.Fprintf(&b, " %s: CLI `hadron channel read %s --since %d --limit 200`, then mark read through your own session (`hadron channel mark-read %s --attendee <you> --seq <highest read>`).",
			channelLabel(c), c.ID, since, c.ID)
	}
	for _, t := range tasks {
		if id := taskID(t.Ref); id != "" {
			fmt.Fprintf(&b, " Task: `hadron team task get %s`.", id)
		}
	}
	const trailer = " (Orca attention router; wake as of send.)"
	if team != nil {
		latest := 0
		if team.LatestMentionSeq != nil {
			latest = *team.LatestMentionSeq
		}
		from := team.FromSeq
		b.WriteString(readAnchor(AttentionItem{Unread: w.Unread, UnreadMentions: w.UnreadMentions,
			FirstUnreadSeq: &from, LastRelevantSeq: latest, LastSeq: team.ThroughSeq}, trailer))
		return b.String()
	}
	b.WriteString(" If nothing needs action, finish silently — no chat post." + trailer)
	return b.String()
}

func plural(n int, noun string) string {
	if n == 1 {
		return fmt.Sprintf("1 %s", noun)
	}
	return fmt.Sprintf("%d %ss", n, noun)
}

// taskID is <id> from a reason ref TASK:<id>:<event>.
func taskID(ref string) string {
	parts := strings.Split(ref, ":")
	if len(parts) >= 3 && parts[0] == "TASK" {
		return parts[1]
	}
	return ""
}

// appKeyPrefix is an App key's token prefix (cor:aut:010:01).
const appKeyPrefix = "hdr_app_"

// ReadAppKeyFile reads the router's App key from a private file: owner-only
// permissions (on unix), exactly one hdr_app_ key. The key is never put in
// argv or the environment, and no refusal quotes the file's content.
func ReadAppKeyFile(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return "", routerErrorf("App key file %s does not exist", path)
		}
		return "", routerErrorf("App key file %s: %v", path, errors.Unwrap(err))
	}
	defer f.Close() //nolint:errcheck // read-only
	info, err := f.Stat()
	if err != nil {
		return "", routerErrorf("App key file %s: %v", path, errors.Unwrap(err))
	}
	if runtime.GOOS != "windows" && info.Mode().Perm()&0o077 != 0 {
		return "", routerErrorf("App key file %s is readable by others (mode %o): chmod 600 it", path, info.Mode().Perm())
	}
	buf := make([]byte, 4096)
	n, _ := f.Read(buf)
	key := strings.TrimSpace(string(buf[:n]))
	if key == "" || strings.ContainsAny(key, " \t\n\r") || !strings.HasPrefix(key, appKeyPrefix) {
		return "", routerErrorf("App key file %s does not hold exactly one App key (%s…)", path, appKeyPrefix)
	}
	return key, nil
}

// DescribeWakeOutcome is the one-line summary of a v2 poll; never a proof.
func DescribeWakeOutcome(o *WakeOutcome) string {
	parts := []string{"nudged: " + orNone(o.Nudged)}
	if len(o.Dropped) > 0 {
		parts = append(parts, "dropped (no longer due): "+strings.Join(o.Dropped, ", "))
	}
	if len(o.Recovered) > 0 {
		parts = append(parts, "acknowledged an earlier delivery: "+strings.Join(o.Recovered, ", "))
	}
	if len(o.AckFailed) > 0 {
		var failed []string
		for _, n := range sortedKeys(o.AckFailed) {
			failed = append(failed, n+" ("+o.AckFailed[n]+")")
		}
		parts = append(parts, "delivered, acknowledgement pending: "+strings.Join(failed, "; "))
	}
	if len(o.Held) > 0 {
		var held []string
		for _, n := range sortedKeys(o.Held) {
			held = append(held, n+" ("+o.Held[n]+")")
		}
		parts = append(parts, "held: "+strings.Join(held, "; "))
	}
	if len(o.Would) > 0 {
		var would []string
		for _, n := range sortedKeys(o.Would) {
			would = append(would, n+" ("+o.Would[n]+")")
		}
		parts = append(parts, "SHADOW would wake: "+strings.Join(would, "; "))
	}
	if o.Status != "" && o.Status != "OK" {
		parts = append(parts, fmt.Sprintf("%s (%d withheld)", o.Status, o.Withheld))
	}
	if len(o.Dormant) > 0 {
		parts = append(parts, "dormant, mentions withheld: "+strings.Join(o.Dormant, ", "))
	}
	return strings.Join(parts, " | ")
}
