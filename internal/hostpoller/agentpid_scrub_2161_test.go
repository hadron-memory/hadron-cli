package hostpoller

import (
	"strings"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/delivery"
)

// Rook's §10 P2 on #2167: a poller that itself runs inside an Orca agent's
// tree carries that agent's HADRON_AGENT_PID. An exec-launched agent must not
// inherit it: that pid is a real ancestor of the worker's commands, so
// `session reset-context` there would end the poller's host agent.
func TestAgentEnvNeverPassesThePollersOwnAgentPID2161(t *testing.T) {
	env := agentEnv([]string{"PATH=/bin", delivery.AgentPIDEnv + "=4242"}, "", "HADRON_WORKER_ID=w1")
	for _, kv := range env {
		if strings.HasPrefix(kv, delivery.AgentPIDEnv+"=") {
			t.Fatalf("an exec agent inherited the poller's %s: %v", delivery.AgentPIDEnv, env)
		}
	}
	if len(env) != 2 {
		t.Fatalf("env %v", env)
	}
}
