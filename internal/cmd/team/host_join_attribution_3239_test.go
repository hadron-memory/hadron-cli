package team

import (
	"context"
	"testing"
)

// hadron-cli#3239: join and remove are person-only on the server, so the
// worktree attribution policy must never put a worker session on them. Two
// guards stand: the policy's list of authored writes leaves the host
// mutations out, and every request is sent in personCtx.
func TestHostMutationsAreNeverAttributed3239(t *testing.T) {
	for _, field := range []string{"joinAgentTeamHost", "removeAgentTeamHost", "updateAgentTeamHost"} {
		if authoredWriteField(field) {
			t.Errorf("%s is on the attribution list: a bound worktree would send a worker session the server refuses", field)
		}
	}
	if !asPerson(personCtx(context.Background())) {
		t.Errorf("personCtx does not mark the write the person's own")
	}
}
