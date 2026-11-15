package hostpoller

import "fmt"

// stopTreeAuthorized is the Windows launch stop decision, separated from taskkill
// so its authorization/exit interleavings can run on every test host. Unix
// launches use their group ownership protocol instead.
func (a *execAgent) stopTreeAuthorized(authorize Authorize, terminate func() (bool, string)) (bool, string) {
	select {
	case <-a.done:
		// Preserve the original already-Done contract: silent success, without
		// acquiring authority or consulting exit detail (the waiter may own mu).
		return true, ""
	default:
	}
	if authorize != nil {
		// One forceful action here, so one authorization, held through it.
		release, ok := authorize()
		if !ok {
			return false, declinedDetail(a.pid)
		}
		defer release()
	}
	// Authorization may have waited while cmd.Wait reaped the launch and
	// released its process handle. Re-prove that this launch has not exited
	// before taskkill can act on a numeric PID (#2099). exited is stored before
	// Done closes: the waiter can be delayed on mu between those two steps.
	if a.exited.Load() {
		return a.treeAlreadyExited()
	}
	select {
	case <-a.done:
		return a.treeAlreadyExited()
	default:
	}
	return terminate()
}

// treeAlreadyExited deliberately does not read a.detail under mu: the waiter
// publishes exited before it can acquire that mutex, and the stop needs to
// finish even while the waiter is blocked there. This reports the stop action,
// not the launch's eventual exit status.
func (a *execAgent) treeAlreadyExited() (bool, string) {
	return true, fmt.Sprintf("the launched process (pid %d) already exited; its process tree was not touched", a.pid)
}
