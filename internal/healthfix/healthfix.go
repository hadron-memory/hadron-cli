// Package healthfix decides the health sweep's duplicate fixes (hadron-cli
// #2027). It is pure over what the sweep observed — agent processes, terminal
// tabs, the reset and pause guards — so every rule of the issue's acceptance
// and the gate's edge limits is a table row here, not a wiring path.
//
// The guards this package owns, from the issue and Xabat's CHECKED gate:
//   - the only agent is never stopped (nothing to decide below two agents);
//   - exactly one supervised agent: the others are stopped; none supervised:
//     nothing is stopped and one alert is raised (the issue's "keep the
//     oldest and report it" names the keeper for the report; the acceptance
//     line "two unsupervised agents: nothing stopped, one alert" is the
//     oracle); two or more supervised is an anomaly that fails closed to an
//     alert;
//   - a bare-shell tab closes only when another, non-bare-shell tab of the
//     same worktree remains — a worktree whose every tab is a bare shell is
//     a no_agent report (relaunch is #1947's), and a tab running an agent
//     never positively classifies bare shell in the first place;
//   - a resetting worker is skipped entirely and rechecked next sweep;
//   - under a team pause the fixes are decided but the worker is marked so
//     the caller reports without applying.
package healthfix

import "fmt"

type (
	// Agent is one agent process the census found in a worktree.
	Agent struct {
		PID        int
		Base       string // claude | codex
		Display    string // the redacted, bounded #1474 line
		Supervised bool
		StartTicks int64 // /proc start-time ticks; bigger means started later
	}
	// Tab is one terminal tab of a worktree. BareShell is true only on
	// positive evidence: every terminal in the tab classified bare shell.
	Tab struct {
		ID        string
		BareShell bool
	}
	// Worktree is one bound-worker worktree's observation.
	Worktree struct {
		WorkerID   string
		WorkerName string
		Path       string
		Agents     []Agent
		Tabs       []Tab
		Resetting  bool
	}
	// Mode is the sweep's posture.
	Mode struct {
		Applying bool
		Paused   bool
	}
	// Fix is one would-be fix. The caller marks whether it was applied.
	Fix struct {
		Worker  string
		Action  string // stop-agent | close-tab
		Subject string
	}
	// Alert is a condition the sweep reports but does not act on.
	Alert struct {
		Worker string
		Code   string // unsupervised-agents | multiple-supervised
		Count  int
	}
	// Skip is a worker the sweep deliberately left alone, and why.
	Skip struct {
		Worker string
		Code   string // resetting | team-paused
	}
	// Plan is what one worktree's observation adds up to.
	Plan struct {
		Fixes  []Fix
		Alerts []Alert
		Skips  []Skip
	}
)

// Actions.
const (
	StopAgent = "stop-agent"
	CloseTab  = "close-tab"
)

// Decide turns one worktree's observation into its plan. It never stops the
// only agent, never proposes a close that would empty the worktree of agent
// tabs, and never proposes anything for a resetting worker.
func Decide(w Worktree, m Mode) Plan {
	p := Plan{Fixes: []Fix{}, Alerts: []Alert{}, Skips: []Skip{}}
	if w.Resetting {
		p.Skips = append(p.Skips, Skip{Worker: w.WorkerID, Code: "resetting"})
		return p
	}
	supervised := 0
	for i := range w.Agents {
		if w.Agents[i].Supervised {
			supervised++
		}
	}
	switch {
	case len(w.Agents) > 1 && supervised == 1:
		for i := range w.Agents {
			a := &w.Agents[i]
			if !a.Supervised {
				p.Fixes = append(p.Fixes, Fix{Worker: w.WorkerID, Action: StopAgent,
					Subject: fmt.Sprintf("pid %d (%s): %s", a.PID, a.Base, a.Display)})
			}
		}
	case len(w.Agents) > 1 && supervised == 0:
		p.Alerts = append(p.Alerts, Alert{Worker: w.WorkerID, Code: "unsupervised-agents", Count: len(w.Agents)})
	case supervised > 1:
		p.Alerts = append(p.Alerts, Alert{Worker: w.WorkerID, Code: "multiple-supervised", Count: supervised})
	}
	bare, solid := 0, 0
	for _, tb := range w.Tabs {
		if tb.BareShell {
			bare++
		} else {
			solid++
		}
	}
	if len(w.Tabs) > 1 && solid > 0 {
		for _, tb := range w.Tabs {
			if tb.BareShell {
				p.Fixes = append(p.Fixes, Fix{Worker: w.WorkerID, Action: CloseTab, Subject: "tab " + tb.ID})
			}
		}
	}
	if m.Paused && (len(p.Fixes) > 0 || len(p.Alerts) > 0) {
		p.Skips = append(p.Skips, Skip{Worker: w.WorkerID, Code: "team-paused"})
	}
	return p
}
