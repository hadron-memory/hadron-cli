package filterreport

import (
	"context"
	"net/url"
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
		if e != nil || !serverOriginEqual(b.Server, server) {
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
		row := Row{Worker: output.SafeLine(worker.Name, 256), Worktree: output.SafeLine(local[0].WorktreePath, 4096), Terminal: output.SafeLine(local[0].Handle, 256)}
		sharedWorktree := true
		for _, t := range local[1:] {
			if t.WorktreePath != local[0].WorktreePath {
				sharedWorktree = false
				break
			}
		}
		if len(local) > 1 {
			row.Classification = "AMBIGUOUS"
			row.Note = "multiple_terminals"
			row.Terminal = ""
			// Terminals in more than one worktree: which worktree — and so
			// which process tree — is as ambiguous as the terminal, and both
			// stay empty instead of showing the handle-sorted first
			// (cli#2483 F10). Terminals sharing one worktree keep it: the
			// worktree, and the tree state derived from it, are shared facts.
			if sharedWorktree {
				row.AgentTree = treeState(processes, local[0].WorktreePath)
			} else {
				row.Worktree = ""
			}
		} else {
			row.AgentTree = treeState(processes, local[0].WorktreePath)
			row.Classification, row.Note = orcaterm.FilterProof(c.ReadScreen(ctx, local[0].Handle))
		}
		rows = append(rows, row)
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].Worker < rows[j].Worker })
	return rows, nil
}

// serverOriginEqual is the conservative binding↔server match: scheme and
// host compare case-insensitively, the scheme's default port is elided, and
// trailing slashes are trimmed on both sides. Anything else — a different
// host or port, a path difference, credentials, an empty or unparsable
// origin — never matches: deployment isolation fails closed, and a mismatch
// with no safe canonical form stays a mismatch (cli#2483 F15).
func serverOriginEqual(binding, server string) bool {
	b, s := canonicalOrigin(binding), canonicalOrigin(server)
	return b != "" && s != "" && b == s
}

// canonicalOrigin reduces one server URL to its comparable form, or "" when
// the URL must not be compared loosely (unparsable, scheme- or hostless, or
// carrying credentials).
func canonicalOrigin(raw string) string {
	raw = strings.TrimRight(raw, "/")
	u, err := url.Parse(raw)
	if err != nil || u.Scheme == "" || u.Host == "" || u.User != nil {
		return ""
	}
	scheme := strings.ToLower(u.Scheme)
	host := strings.ToLower(u.Hostname())
	if port := u.Port(); port != "" && (scheme != "https" || port != "443") && (scheme != "http" || port != "80") {
		host += ":" + port
	}
	return scheme + "://" + host + strings.TrimRight(u.EscapedPath(), "/")
}
