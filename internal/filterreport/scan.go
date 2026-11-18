package filterreport

import (
	"context"
	"sort"
	"strings"

	"github.com/hadron-memory/hadron-cli/internal/orcaterm"
	"github.com/hadron-memory/hadron-cli/internal/output"
)

// Row is the entire public report. No screen, draft, argv or process path is
// included beyond the terminal's already-known worktree.
type Row struct {
	Worker         string
	Worktree       string
	Terminal       string
	Classification string
	AgentTree      string
	Note           string
}

type Target struct{ Name, ID string }

// Scan joins local bindings to an authoritative team roster. Missing/unreadable
// bindings are never guessed from terminal titles or agentIdentity. One worker
// with several terminals is explicitly ambiguous rather than picked by luck.
func Scan(ctx context.Context, c *orcaterm.Client, terms []orcaterm.Terminal, targets []Target, server, procRoot string, uid int) ([]Row, error) {
	processes, err := readProcesses(procRoot, uid)
	if err != nil {
		return nil, err
	}
	candidates := map[string][]orcaterm.Terminal{}
	for _, t := range terms {
		if t.WorktreePath == "" {
			continue
		}
		b, e := ReadBinding(t.WorktreePath)
		if e != nil || (b.Server == "" || server == "" || strings.TrimRight(b.Server, "/") != strings.TrimRight(server, "/")) {
			continue
		}
		candidates[b.WorkerID] = append(candidates[b.WorkerID], t)
	}
	rows := []Row{}
	for _, worker := range targets {
		local := candidates[worker.ID]
		if len(local) == 0 {
			continue
		}
		sort.Slice(local, func(i, j int) bool { return local[i].Handle < local[j].Handle })
		row := Row{Worker: output.SafeLine(worker.Name, 256), Worktree: output.SafeLine(local[0].WorktreePath, 4096), Terminal: output.SafeLine(local[0].Handle, 256), AgentTree: treeState(processes, local[0].WorktreePath)}
		if len(local) > 1 {
			row.Classification = "AMBIGUOUS"
			row.Note = "multiple_terminals"
			row.Terminal = ""
		} else {
			row.Classification, row.Note = orcaterm.FilterProof(c.ReadScreen(ctx, local[0].Handle))
		}
		rows = append(rows, row)
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].Worker < rows[j].Worker })
	return rows, nil
}
