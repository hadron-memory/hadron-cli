package node

import (
	"context"
	"testing"
	"time"

	"github.com/Khan/genqlient/graphql"
	"github.com/spf13/cobra"
)

type lintBudgetClient struct {
	t         *testing.T
	deadlines []time.Time
}

func (c *lintBudgetClient) MakeRequest(ctx context.Context, _ *graphql.Request, _ *graphql.Response) error {
	deadline, ok := ctx.Deadline()
	if !ok || deadline.After(time.Now().Add(5*time.Second)) {
		c.t.Error("advisory read has no short independent deadline")
	}
	c.deadlines = append(c.deadlines, deadline)
	return context.DeadlineExceeded
}
func TestLintWarningBudgetIsBoundedAndSharedAcrossBatchNodes(t *testing.T) {
	cmd := &cobra.Command{}
	cmd.SetContext(context.Background())
	cmd.Flags().Bool("lint-warnings", true, "")
	client := &lintBudgetClient{t: t}
	for _, id := range []string{"first", "second"} {
		slot := loadLintWarnings(cmd, client, id)
		if slot == nil || *slot != nil {
			t.Fatalf("timed-out advisory must be requested/unavailable: %#v", slot)
		}
	}
	if len(client.deadlines) != 2 || !client.deadlines[0].Equal(client.deadlines[1]) {
		t.Fatalf("batch nodes reset the budget: %v", client.deadlines)
	}
}
func TestLintWarningCanceledBudgetSkipsRequest(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	cmd := &cobra.Command{}
	cmd.SetContext(ctx)
	cmd.Flags().Bool("lint-warnings", true, "")
	client := &lintBudgetClient{t: t}
	slot := loadLintWarnings(cmd, client, "node1")
	if slot == nil || *slot != nil || len(client.deadlines) != 0 {
		t.Fatalf("canceled read reached provider: %#v %v", slot, client.deadlines)
	}
}
