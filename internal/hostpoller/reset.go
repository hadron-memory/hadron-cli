package hostpoller

// OpWorkerResetContext is the capability-gated operation (#2162,
// cor:agt:050:27): reset a worker's context after its task, keeping the
// binding. This CLI no longer carries out the operation — the reset-context
// host handler was removed (hadron-cli#3017) — so a collected one is
// reported FAILED and the capability is refused at config load; the name
// stays so the refusal and the report spell it once.
const OpWorkerResetContext = "worker.reset-context"

// CapabilityResetContext is the registration capability for that operation.
// No host this CLI runs declares it (hadron-cli#3017); the name stays so the
// config refusal can name what it refuses.
const CapabilityResetContext = OpWorkerResetContext

// ProcessIdentity names one agent process: its pid and the kernel's start
// time for it, an opaque token, so a reused pid is a different process.
type ProcessIdentity struct {
	PID       int    `json:"pid"`
	StartTime string `json:"startTime"`
}
