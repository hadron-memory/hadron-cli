package teamrouter

import (
	"os"
	"path/filepath"
	"slices"
	"time"
)

// LivenessStatus is one flagged worker's watch state.
type LivenessStatus struct {
	IdleMinutes     *int64 `json:"idleMinutes"`
	CoveredFrontier *int   `json:"coveredFrontier"`
}

// StatusReport is `team router status`: local state only — token presence,
// ledger contents, the lock, the liveness clocks. No network, and never the
// token itself.
type StatusReport struct {
	StateDir                string                     `json:"stateDir"`
	TokenAdopted            bool                       `json:"tokenAdopted"`
	NudgedUnderCurrentToken []string                   `json:"nudgedUnderCurrentToken"`
	Held                    map[string]string          `json:"held"`
	Locked                  bool                       `json:"locked"`
	Roster                  []string                   `json:"roster"`
	Liveness                map[string]*LivenessStatus `json:"liveness"`
	// PendingAcks (v2): workers whose last delivered wake has not been
	// acknowledged yet; the next poll acknowledges it without resending.
	PendingAcks []string `json:"pendingAcks"`
}

// Status reads the router's local state for c.
func Status(c *Config) (*StatusReport, error) {
	st := NewState(c.StateDir)
	rep := &StatusReport{StateDir: c.StateDir, NudgedUnderCurrentToken: []string{}, Held: map[string]string{},
		Roster: []string{}, Liveness: map[string]*LivenessStatus{}, PendingAcks: []string{}}
	_, err := os.Stat(c.StateDir)
	present := err == nil
	liveness := map[string]LivenessRecord{}
	if present {
		tok, err := st.token()
		if err != nil {
			return nil, routerErrorf("adopted token unreadable: %v", err)
		}
		rep.TokenAdopted = tok != ""
		ledger := st.Ledger()
		rep.NudgedUnderCurrentToken = sortedKeys(ledger.Nudged)
		rep.Held = ledger.Reported
		_, err = os.Stat(filepath.Join(c.StateDir, ".writer-lock"))
		rep.Locked = err == nil
		liveness = st.Liveness()
		pending := st.sentRecords(time.Now().UnixMilli())
		for _, e := range c.Roster {
			if _, ok := pending[e.Worker]; ok {
				rep.PendingAcks = append(rep.PendingAcks, e.Name)
			}
		}
	}
	for _, e := range c.Roster {
		rep.Roster = append(rep.Roster, e.Name)
		if !e.flagged() {
			continue
		}
		r, ok := liveness[e.Worker]
		if !ok {
			rep.Liveness[e.Name] = nil
			continue
		}
		ls := &LivenessStatus{CoveredFrontier: r.CoveredFrontier}
		if r.IdleSince != nil {
			m := (time.Now().UnixMilli() - *r.IdleSince + 30_000) / 60_000
			ls.IdleMinutes = &m
		}
		rep.Liveness[e.Name] = ls
	}
	slices.Sort(rep.NudgedUnderCurrentToken)
	return rep, nil
}
