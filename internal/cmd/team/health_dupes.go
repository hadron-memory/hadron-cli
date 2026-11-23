package team

// The #2027 fix phase of `team health sweep`: detect more than one agent
// process and more than one terminal tab per bound-worker worktree, and on
// an applying run stop the unsupervised duplicates (a pidfd-pinned SIGTERM,
// never a group signal — the duplicate's tree belongs to someone's shell) and
// close extra bare-shell tabs. Detection is fail-closed everywhere: no /proc,
// no Orca answer or an unreadable screen is no evidence, never a reason to
// act. The phase never fails the sweep: its findings join the receipt, and
// its failures join as alerts. Stopping a process never ends or rebinds a
// session — this file makes no session call but the one notification post,
// which is attributed through the sweep's own live binding (cli#892).

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"time"

	"github.com/hadron-memory/hadron-cli/internal/api/gen"
	"github.com/hadron-memory/hadron-cli/internal/cmd/orca"
	"github.com/hadron-memory/hadron-cli/internal/cmdutil"
	"github.com/hadron-memory/hadron-cli/internal/healthfix"
	"github.com/hadron-memory/hadron-cli/internal/hostpoller"
	"github.com/hadron-memory/hadron-cli/internal/orcaterm"
	ts "github.com/hadron-memory/hadron-cli/internal/terminalstatus"
)

// dupeFix, dupeAlert and dupeSkip are the receipt rows the fix phase adds.
type dupeFix struct {
	Worker  string `json:"worker"`
	Action  string `json:"action"`
	Subject string `json:"subject"`
	Applied bool   `json:"applied"`
}
type dupeAlert struct {
	Worker string `json:"worker"`
	Code   string `json:"code"`
	Count  int    `json:"count"`
}
type dupeSkip struct {
	Worker string `json:"worker"`
	Code   string `json:"code"`
}

// dupesResult is the whole phase's output. Every slice is non-nil so the
// --json contract renders [] and never null.
type dupesResult struct {
	Fixes  []dupeFix
	Alerts []dupeAlert
	// Skips keys its receipt rows "skips", matching the Go field and the
	// fixes/alerts pair beside it (#2037 review: the first draft keyed it
	// "skipped"; one spelling per concept).
	Skips   []dupeSkip
	applied int // fixes actually applied, for the one notification post
}

// stopLease is the self-chosen window between the identity re-verification
// and the SIGTERM: enough to cover the verification reads, short enough that
// a wedged sweep cannot signal a long-stale decision.
const stopLease = 5 * time.Second

// runHealthDupes is the fix phase. applying is the --dry-run=false posture.
// Errors are reported in the result, never returned.
func runHealthDupes(ctx context.Context, f *cmdutil.Factory, cfg *hostpoller.Config, server *hostServer, workers []dupeWorker, applying bool, errOut io.Writer) dupesResult {
	res := dupesResult{Fixes: []dupeFix{}, Alerts: []dupeAlert{}, Skips: []dupeSkip{}}
	inScope := worktreesOf(cfg, workers)
	if len(inScope) == 0 {
		return res // nothing bound on this host: ask Orca and the server nothing
	}
	client, terms := sweepOrcaClient(ctx, cfg)
	census := map[string][]hostpoller.WorktreeAgent{}
	tabs := map[string][]healthfix.Tab{}
	wt := map[string]dupeWorktree{}
	candidates := map[string]bool{}
	for _, tree := range inScope {
		wt[tree.path] = tree
		agents := hostpoller.ScanWorktreeAgents(tree.path)
		census[tree.path] = agents
		tb := worktreeTabs(ctx, client, terms, tree.path)
		tabs[tree.path] = tb
		if len(agents) > 1 || len(tb) > 1 {
			candidates[tree.path] = true
		}
	}
	if len(candidates) == 0 {
		return res // a clean host asks the server nothing new
	}
	paused, ok := sweepPause(ctx, server, cfg.App)
	if !ok {
		// The pause state cannot be proven: fail closed, report only.
		paused = true
		res.Skips = append(res.Skips, dupeSkip{Worker: "*", Code: "pause-unreadable"})
	}
	mode := healthfix.Mode{Applying: applying, Paused: paused}
	apply := applying && !paused
	var appliedActions []string
	for _, w := range sortedPaths(candidates) {
		observation := healthfix.Worktree{WorkerID: wt[w].workerID, WorkerName: wt[w].workerName, Path: w, Agents: toFixAgents(census[w]), Tabs: tabs[w]}
		plan := healthfix.Decide(observation, mode)
		for _, a := range plan.Alerts {
			res.Alerts = append(res.Alerts, dupeAlert{Worker: a.Worker, Code: a.Code, Count: a.Count})
		}
		for _, s := range plan.Skips {
			res.Skips = append(res.Skips, dupeSkip{Worker: s.Worker, Code: s.Code})
		}
		for _, fix := range plan.Fixes {
			row := dupeFix{Worker: fix.Worker, Action: fix.Action, Subject: fix.Subject}
			switch {
			case !apply:
				// Dry or paused: listed, never applied.
			case fix.Action == healthfix.StopAgent:
				pid := subjectPID(census[w], fix)
				if pid == 0 {
					res.Alerts = append(res.Alerts, dupeAlert{Worker: fix.Worker, Code: "stop-failed", Count: 1})
					continue
				}
				if err := stopAgent(pid, startTicksOf(census[w], pid)); err != nil {
					res.Alerts = append(res.Alerts, dupeAlert{Worker: fix.Worker, Code: "stop-failed", Count: 1})
					continue
				}
				row.Applied = true
			case fix.Action == healthfix.CloseTab:
				// The recheck re-LISTS, then re-reads: the initial List is
				// minutes stale by now, and a pane added to the tab since
				// would be closed unread (#2037 review). The close needs
				// every terminal of the tab as it is NOW to classify bare,
				// and a live handle from that same fresh listing. A declined
				// close leaves its skip row in the receipt (#2037 N3) and NO
				// close-tab fix row (the tests pin that): the skip code says
				// why it was declined, and the next sweep rechecks.
				handle, stillBare, decline := tabBareNow(ctx, client, w, fix.Subject)
				if handle == "" || !stillBare {
					res.Skips = append(res.Skips, dupeSkip{Worker: fix.Worker, Code: decline})
					continue
				}
				if err := client.CloseTab(ctx, handle); err != nil {
					res.Alerts = append(res.Alerts, dupeAlert{Worker: fix.Worker, Code: "close-failed", Count: 1})
					continue
				}
				row.Applied = true
			}
			res.Fixes = append(res.Fixes, row)
			if row.Applied {
				res.applied++
				appliedActions = append(appliedActions, fmt.Sprintf("%s: %s", wt[w].workerName, fix.Action))
			}
		}
	}
	if res.applied > 0 {
		postSweepNotice(ctx, f, cfg.App, res.applied, appliedActions, errOut)
	}
	return res
}

// dupeWorker is one registered worker of this host.
type dupeWorker struct {
	workerID   string
	workerName string
	urn        string
}

// dupeWorktree is one in-scope worktree: absolute path plus its worker.
type dupeWorktree struct {
	path       string
	workerID   string
	workerName string
}

// worktreesOf lists the bound worktrees of this host's registered workers:
// every configured worktree of a registered worker whose own session binding
// names that worker. A worktree with no matching binding is never returned —
// the sweep does not touch it whatever it hosts.
func worktreesOf(cfg *hostpoller.Config, workers []dupeWorker) []dupeWorktree {
	ctx := context.Background()
	out := []dupeWorktree{}
	seen := map[string]bool{}
	for _, w := range workers {
		ref := cfg.RefFor(w.workerID, w.urn, w.workerName)
		if ref == "" {
			continue
		}
		wc, ok := cfg.Workers[ref]
		if !ok {
			continue
		}
		for _, token := range wc.Worktrees {
			path := filepath.Join(cfg.WorktreesRoot, token)
			if seen[path] {
				continue // two workers naming one worktree: the later is a config
				// mistake; the first claim wins, the sweep never double-walks it
			}
			b, ok := orca.BoundAt(ctx, path)
			if !ok || b.WorkerID != w.workerID {
				continue // unbound, or bound to someone else: not ours to touch
			}
			seen[path] = true
			out = append(out, dupeWorktree{path: path, workerID: w.workerID, workerName: w.workerName})
		}
	}
	return out
}

// sweepOrcaClient builds the sweep's own Orca client (list, read, close —
// never send) with the scrubbed environment the status read uses, and lists
// the terminals once. An unreachable Orca answers no terminals: the tab half
// of detection degrades, the report does not.
func sweepOrcaClient(ctx context.Context, cfg *hostpoller.Config) (*orcaterm.Client, []orcaterm.Terminal) {
	bin := cfg.Delivery.OrcaBin
	if bin == "" {
		bin = "orca"
	}
	key, err := cfg.ReadHostKey()
	if err != nil {
		key = "" // ScrubbedEnv still drops the named credential variables
	}
	client := orcaterm.NewSweepClient(orcaterm.ExecEnv(bin, orcaterm.CallTimeout, hostpoller.ScrubbedEnv(os.Environ(), key)))
	terms, err := client.List(ctx)
	if err != nil {
		return client, nil
	}
	return client, terms
}

// worktreeTabs groups the listed terminals of one worktree into tabs and
// classifies each terminal's screen. A tab is bare only on positive evidence:
// every one of its terminals classifies bare_shell (a busy, agent-chrome or
// unreadable screen is not a shell, and a missing tabId cannot be counted).
func worktreeTabs(ctx context.Context, client *orcaterm.Client, terms []orcaterm.Terminal, worktree string) []healthfix.Tab {
	real, err := filepath.EvalSymlinks(worktree)
	if err != nil {
		return nil
	}
	now := time.Now()
	order := []string{}
	byTab := map[string]*healthfix.Tab{}
	for i := range terms {
		t := &terms[i]
		treal, err := filepath.EvalSymlinks(t.WorktreePath)
		if err != nil || treal != real || t.TabID == "" {
			continue
		}
		if _, seen := byTab[t.TabID]; !seen {
			byTab[t.TabID] = &healthfix.Tab{ID: t.TabID, BareShell: true}
			order = append(order, t.TabID)
		}
		obs := orcaterm.Classify(*t, client.ReadScreen(ctx, t.Handle), now)
		if obs.Reason != ts.BareShell {
			byTab[t.TabID].BareShell = false
		}
	}
	out := make([]healthfix.Tab, 0, len(order))
	for _, id := range order {
		out = append(out, *byTab[id])
	}
	return out
}

// toFixAgents maps the census onto the decision core's agents.
func toFixAgents(ws []hostpoller.WorktreeAgent) []healthfix.Agent {
	out := make([]healthfix.Agent, 0, len(ws))
	for _, w := range ws {
		out = append(out, healthfix.Agent{PID: w.PID, Base: w.Base, Display: w.Display, Supervised: w.Supervised, StartTicks: w.StartTicks})
	}
	return out
}

// sweepPause reads whether a team pause is in force: any worker's pending
// reason TEAM_PAUSED. ok is false when the answer could not be read — the
// caller then fails closed to report-only.
func sweepPause(ctx context.Context, server *hostServer, appRef string) (paused, ok bool) {
	resp, err := gen.HealthSweepPause(ctx, server.client, appRef)
	if err != nil {
		return false, false
	}
	if resp == nil || resp.Workers == nil {
		return false, false
	}
	for _, w := range resp.Workers.Items {
		if w == nil || w.CheckStatus == nil {
			continue
		}
		if w.CheckStatus.PendingReason != nil && *w.CheckStatus.PendingReason == gen.WorkerPendingReasonTeamPaused {
			return true, true
		}
	}
	return false, true
}

// stopAgent sends one SIGTERM to the duplicate agent, pinned: the pidfd holds
// the process the census saw (its start time is re-proved), so a reused pid
// is skipped, not killed. No group signal — the duplicate's tree belongs to
// the shell it was typed into — and no escalation: a SIGTERM that changes
// nothing is re-detected next sweep.
func stopAgent(pid int, startTicks int64) error {
	ctrl := hostpoller.LinuxProcessControl{}
	h, err := ctrl.Observe(pid)
	if err != nil {
		return err
	}
	defer h.Close()
	if id := h.Identity(); id.StartTime != fmt.Sprintf("%d", startTicks) {
		return errors.New("the process is not the one the census saw")
	}
	t0, err := ctrl.Now()
	if err != nil {
		return err
	}
	_, err = h.SignalWithin(syscall.SIGTERM, t0, stopLease, ctrl.Now)
	return err
}

// subjectPID finds the census agent a stop-agent fix names.
func subjectPID(census []hostpoller.WorktreeAgent, fix healthfix.Fix) int {
	for _, a := range census {
		if strings.HasPrefix(fix.Subject, fmt.Sprintf("pid %d (", a.PID)) {
			return a.PID
		}
	}
	return 0
}

// startTicksOf is the census's start-time key for a pid.
func startTicksOf(census []hostpoller.WorktreeAgent, pid int) int64 {
	for _, a := range census {
		if a.PID == pid {
			return a.StartTicks
		}
	}
	return 0
}

// tabBareNow is the read-adjacent recheck before a close: it re-LISTS the
// terminals (the sweep's first listing is stale by now — a pane split into
// the tab since would be closed unread), groups the named tab from that
// FRESH listing, re-reads every terminal of it, and requires the bare-shell
// classification to hold NOW. It answers a live handle of the tab from the
// same fresh listing ("" when the tab is gone), whether the tab is still
// provably bare, and — when it is not — the receipt's skip code saying which
// (#2037 N3: the decline used to be a silent continue): a tab that turned
// mid-turn, or grew a pane, stays this sweep's hands off, visibly.
func tabBareNow(ctx context.Context, client *orcaterm.Client, worktree, subject string) (handle string, bare bool, decline string) {
	const prefix = "tab "
	if !strings.HasPrefix(subject, prefix) {
		return "", false, "close-recheck-failed" // not a tab subject: Decide's shape, not ours to guess
	}
	id := strings.TrimPrefix(subject, prefix)
	real, err := filepath.EvalSymlinks(worktree)
	if err != nil {
		return "", false, "close-recheck-failed"
	}
	terms, err := client.List(ctx)
	if err != nil {
		return "", false, "close-recheck-failed"
	}
	now := time.Now()
	seen := 0
	for i := range terms {
		t := &terms[i]
		treal, err := filepath.EvalSymlinks(t.WorktreePath)
		if err != nil || treal != real || t.TabID != id {
			continue
		}
		seen++
		if orcaterm.Classify(*t, client.ReadScreen(ctx, t.Handle), now).Reason != ts.BareShell {
			return "", false, "close-not-bare" // turned, or the added pane is live: mid-turn now
		}
		if handle == "" {
			handle = t.Handle
		}
	}
	if seen == 0 {
		return "", false, "close-gone" // left between the listing and the recheck
	}
	return handle, true, ""
}

// postSweepNotice posts the ONE notification line an applying sweep with
// applied fixes earns (gate edge 2), attributed through the sweep process's
// own live worker-session binding — never silently as the human (cli#892),
// never into a terminal. The binding is read without the ended-session
// check: the server verifies the sessionRef itself (PR #473), and the sweep
// spends no extra call on it. Unbound (the timer) posts nothing; a failing
// post leaves one stderr note; the sweep's exit is not the post's business.
func postSweepNotice(ctx context.Context, f *cmdutil.Factory, appRef string, n int, actions []string, errOut io.Writer) {
	b, _, err := readBindingFile(ctx)
	if err != nil || b == nil || b.SessionID == "" {
		return
	}
	client, err := f.GraphQLClient()
	if err != nil {
		fmt.Fprintln(errOut, "note: could not post the health sweep notice: no usable ambient client")
		return
	}
	line := fmt.Sprintf("health sweep: %d fix%s applied — %s", n, fixPlural(n), strings.Join(actions, "; "))
	ref := b.SessionID
	if _, err := gen.CreateTeamChatMessage(ctx, client, appRef, line, nil, &ref); err != nil {
		fmt.Fprintln(errOut, "note: the health sweep notice could not be posted; the fixes stand")
	}
}

func fixPlural(n int) string {
	if n == 1 {
		return ""
	}
	return "es"
}

func sortedPaths(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
