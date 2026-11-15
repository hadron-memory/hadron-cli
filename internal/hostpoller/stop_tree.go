package hostpoller

import "fmt"

// stopProcessTree is the Windows launch stop decision, separated from taskkill
// so its authorization/exit interleavings can run on every test host. Unix
// launches use their group ownership protocol instead.
func (a *execAgent) stopProcessTree(authorize Authorize, terminate func() (bool, string)) (bool, string) {
	select {
	case <-a.done:
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
		return true, fmt.Sprintf("the launched process (pid %d) already exited; its process tree was not touched", a.pid)
	}
	select {
	case <-a.done:
		return true, fmt.Sprintf("the launched process (pid %d) already exited; its process tree was not touched", a.pid)
	default:
	}
	return terminate()
}
