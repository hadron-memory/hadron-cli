package hostpoller

import "errors"

// reserveWorktreeLaunch is shared admission for normal C3 starts and C5's
// periodic relaunch. It uses p.mu, with no cross-process/global launch lock.
// The reservation lasts through launch/tracking; network and Orca calls do
// not hold p.mu, so existing tracking and stop paths can still inspect state.
func (p *Poller) reserveWorktreeLaunch(token string) (func(), error) {
	if token == "" {
		return nil, errors.New("worktree launch has no token")
	}
	p.mu.Lock()
	if p.launchingWorktrees == nil {
		p.launchingWorktrees = map[string]bool{}
	}
	if p.launchingWorktrees[token] {
		p.mu.Unlock()
		return nil, errors.New("a launch is already in flight for this worktree")
	}
	p.launchingWorktrees[token] = true
	p.mu.Unlock()
	return func() { p.mu.Lock(); delete(p.launchingWorktrees, token); p.mu.Unlock() }, nil
}
