package hostpoller

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
	return terminate()
}
