package hostrelaunch

// LaunchError carries only an observed closed failure classification. Unknown
// setup/verification errors stay in local JSON/logs, never a guessed wire code.
type LaunchError struct {
	Code string
	Err  error
}

func (e *LaunchError) Error() string {
	if e.Err != nil {
		return e.Err.Error()
	}
	return e.Code
}
func (e *LaunchError) Unwrap() error                 { return e.Err }
func (e *LaunchError) RelaunchFailureReason() string { return e.Code }

const (
	FailureTimeout               = "TIMEOUT"
	FailureTerminalCreateRefused = "TERMINAL_CREATE_REFUSED"
	FailureLaunchExitNonzero     = "LAUNCH_EXIT_NONZERO"
)
