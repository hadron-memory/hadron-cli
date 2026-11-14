package team

import (
	"context"

	"github.com/hadron-memory/hadron-cli/internal/delivery"
	"github.com/hadron-memory/hadron-cli/internal/hostpoller"
)

// healthBoundWorker names the worker a worktree's session binding holds. It is
// a variable only so a test can drive the title pass without real worktrees.
var healthBoundWorker = delivery.BoundWorker

// healthRename is one tab the title pass renamed, or would rename.
type healthRename struct {
	Worker   string  `json:"worker"`
	Terminal string  `json:"terminal"`
	From     string  `json:"from"`
	To       string  `json:"to"`
	Applied  bool    `json:"applied"`
	Error    *string `json:"error"`
}

// healthTitles is the sweep's title pass (cli#2017): stub until implemented.
func healthTitles(ctx context.Context, cfg *hostpoller.Config, workers map[string]string, apply bool) ([]healthRename, error) {
	return []healthRename{}, nil
}
