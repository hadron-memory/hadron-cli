package secret

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/Khan/genqlient/graphql"

	"github.com/hadron-memory/hadron-cli/internal/exitcode"
)

// scripted answers each SecretChangeRequest read with the next state (the last
// one repeats), and counts the reads.
type scripted2738 struct {
	states []string
	reads  int
}

func (s *scripted2738) MakeRequest(_ context.Context, req *graphql.Request, resp *graphql.Response) error {
	i := s.reads
	if i >= len(s.states) {
		i = len(s.states) - 1
	}
	s.reads++
	raw, _ := json.Marshal(map[string]any{"secretChangeRequest": map[string]any{
		"id": "r1", "state": s.states[i], "operation": "update", "createdAt": "c", "expiresAt": "e",
	}})
	return json.Unmarshal(raw, resp.Data)
}

func fastPoll2738(t *testing.T) {
	t.Helper()
	old := changePollInterval
	changePollInterval = time.Millisecond
	t.Cleanup(func() { changePollInterval = old })
}

func codeOf2738(err error) int { return exitcode.FromError(err) }

// The wait keeps reading while the request is open and ends on completion.
func TestPollChangeWaitsThroughOpenThenCompletes2738(t *testing.T) {
	fastPoll2738(t)
	c := &scripted2738{states: []string{"open", "open", "completed"}}
	d, err := pollChange(context.Background(), c, "r1", time.Second)
	if err != nil || d == nil || d.State != "completed" || c.reads != 3 {
		t.Fatalf("dto=%v err=%v reads=%d", d, err, c.reads)
	}
}

// Still open when the wait runs out: 13, the last state is returned, and
// nothing but reads was sent (the request is never withdrawn).
func TestPollChangeTimeoutExits13AndKeepsTheRequest2738(t *testing.T) {
	fastPoll2738(t)
	c := &scripted2738{states: []string{"open"}}
	d, err := pollChange(context.Background(), c, "r1", 30*time.Millisecond)
	if codeOf2738(err) != exitcode.WaitTimeout {
		t.Fatalf("exit = %d, want 13 (%v)", codeOf2738(err), err)
	}
	if d == nil || d.State != "open" || c.reads < 2 {
		t.Errorf("last state kept, polled more than once: %v reads=%d", d, c.reads)
	}
}

// The caller's own cancel (Ctrl-C) is not the command's timeout: exit 6.
func TestPollChangeCancelExits6NotTimeout2738(t *testing.T) {
	fastPoll2738(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := pollChange(ctx, &scripted2738{states: []string{"open"}}, "r1", time.Second)
	if codeOf2738(err) != exitcode.Cancelled {
		t.Fatalf("exit = %d, want 6 (%v)", codeOf2738(err), err)
	}
}
