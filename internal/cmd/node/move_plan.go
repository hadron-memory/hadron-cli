package node

import (
	"fmt"
	"io"

	"github.com/Khan/genqlient/graphql"
	"github.com/spf13/cobra"

	"github.com/hadron-memory/hadron-cli/internal/api"
	"github.com/hadron-memory/hadron-cli/internal/api/gen"
	"github.com/hadron-memory/hadron-cli/internal/cmdutil"
	"github.com/hadron-memory/hadron-cli/internal/exitcode"
	"github.com/hadron-memory/hadron-cli/internal/output"
)

// `node move --dry-run` renders the server's plan (planNodeMove,
// cor:api:190:03; hadron-cli#741). The server runs the real move's
// resolution, gates and checks without writing, so the rebased URNs,
// truncation, link warnings and refusals are all its own: the CLI predicts
// nothing (cor:api:240:01). A refusal is the move's refusal and exits as the
// move would.

type movePlanNode struct {
	ID         string `json:"id"`
	CurrentURN string `json:"currentUrn"`
	NewURN     string `json:"newUrn"`
}

type movePlanLinkWarning struct {
	NodeID  string `json:"nodeId"`
	NodeLoc string `json:"nodeLoc"`
	Raw     string `json:"raw"`
	Status  string `json:"status"`
	Target  string `json:"target"`
}

// movePlanDTO is the stable --json shape of `node move --dry-run`.
// LinkWarnings carries the server's answer as sent, in three states: a list
// of warnings; an empty list when the server found none; or null, which the
// server sends when it withholds them (LinkWarningsWithheld, the caller can't
// read the destination memory). A null without the withheld flag is passed
// through as null and the text says the server reported none.
type movePlanDTO struct {
	Status               string                `json:"status"` // COMPLETE or TRUNCATED
	Complete             bool                  `json:"complete"`
	TotalCount           int                   `json:"totalCount"`
	Truncated            bool                  `json:"truncated"` // the CLI's verdict, always !Complete (cli#2320)
	PointInTime          bool                  `json:"pointInTime"`
	Nodes                []movePlanNode        `json:"nodes"`
	LinkWarnings         []movePlanLinkWarning `json:"linkWarnings"`
	LinkWarningsWithheld bool                  `json:"linkWarningsWithheld"`
}

func planMove(cmd *cobra.Command, f *cmdutil.Factory, client graphql.Client, memory, source string, targetURN, targetMemory *string, limit *int) error {
	sourceRef, err := cmdutil.ResolveNodeRef(cmd, client, memory, source)
	if err != nil {
		return err
	}
	resp, err := gen.PlanNodeMove(cmd.Context(), client, sourceRef, targetURN, targetMemory, limit)
	if err != nil {
		return api.MapError(err)
	}
	if resp == nil || resp.PlanNodeMove == nil {
		return exitcode.Newf(exitcode.Error, "the server returned no move plan")
	}
	dto := movePlanFrom(resp.PlanNodeMove)
	if err := output.Write(f.IOStreams, f.JSON, dto, func(w io.Writer) error { return renderMovePlan(w, dto) }); err != nil {
		return err
	}
	if !dto.Complete {
		return exitcode.New(exitcode.Error, truncatedPlanError(len(dto.Nodes), dto.TotalCount, limit))
	}
	return nil
}

// truncatedPlanError says how to see more without promising what the server
// may not give: the server bounds --limit (cor:api:190:03), and the CLI does
// not copy that bound. When the server listed fewer nodes than --limit asked
// for, that shortfall IS its bound, so the message says a plan cannot list
// this subtree in full rather than suggesting a rerun that cannot help.
func truncatedPlanError(listed, total int, limit *int) error {
	if limit != nil && listed < *limit {
		return fmt.Errorf("TRUNCATED: the plan lists %d of %d nodes, fewer than --limit %d: that is the server's bound, so a plan cannot list this subtree in full", listed, total, *limit)
	}
	return fmt.Errorf("TRUNCATED: the plan lists %d of %d nodes; rerun with a larger --limit to list more (the server bounds it, so a subtree beyond that bound stays TRUNCATED)", listed, total)
}

// movePlanFrom copies the plan into the command's DTO. It is complete only
// when the server says it is not truncated AND it lists every node it counts,
// so a short list is never presented as the whole subtree.
func movePlanFrom(p *gen.PlanNodeMovePlanNodeMoveNodeMovePlan) movePlanDTO {
	dto := movePlanDTO{
		TotalCount:           p.TotalCount,
		PointInTime:          p.PointInTime,
		Nodes:                []movePlanNode{},
		LinkWarningsWithheld: p.LinkWarningsWithheld,
	}
	for _, n := range p.Nodes {
		if n != nil {
			dto.Nodes = append(dto.Nodes, movePlanNode{ID: n.Id, CurrentURN: n.CurrentUrn, NewURN: n.NewUrn})
		}
	}
	if p.LinkWarnings != nil {
		dto.LinkWarnings = []movePlanLinkWarning{}
		for _, w := range p.LinkWarnings {
			if w != nil {
				dto.LinkWarnings = append(dto.LinkWarnings, movePlanLinkWarning{NodeID: w.NodeId, NodeLoc: w.NodeLoc, Raw: w.Raw, Status: string(w.Status), Target: w.Target})
			}
		}
	}
	dto.Complete = !p.Truncated && len(dto.Nodes) == p.TotalCount && len(dto.Nodes) == len(p.Nodes)
	// truncated is the CLI's verdict, not a copy of the server's flag: the
	// server flagged the plan OR it lists fewer nodes than it counts. So the
	// JSON never reads complete beside status TRUNCATED (cli#2320,
	// cor:api:190:03).
	dto.Truncated = !dto.Complete
	dto.Status = "TRUNCATED"
	if dto.Complete {
		dto.Status = "COMPLETE"
	}
	return dto
}

func renderMovePlan(w io.Writer, dto movePlanDTO) error {
	lines := []string{"Move plan from the server. Nothing was written."}
	if dto.PointInTime {
		lines = append(lines, "It is true now and reserves nothing: a change before the move can still end the move with a conflict.")
	}
	if dto.Complete {
		lines = append(lines, fmt.Sprintf("COMPLETE: %d %s would move", len(dto.Nodes), nodesWord(len(dto.Nodes))))
	} else {
		lines = append(lines, fmt.Sprintf("TRUNCATED: the plan lists %d of %d nodes", len(dto.Nodes), dto.TotalCount))
	}
	for _, l := range lines {
		if _, err := fmt.Fprintln(w, l); err != nil {
			return err
		}
	}
	t := output.NewTable(w)
	for _, n := range dto.Nodes {
		t.Row(output.SafeCell(n.CurrentURN), output.SafeCell("→"), output.SafeCell(n.NewURN))
	}
	if err := t.Flush(); err != nil {
		return err
	}
	switch {
	case dto.LinkWarningsWithheld:
		_, err := fmt.Fprintln(w, "Link warnings: withheld (you can't read the destination memory)")
		return err
	case dto.LinkWarnings == nil:
		_, err := fmt.Fprintln(w, "Link warnings: not reported by the server")
		return err
	case len(dto.LinkWarnings) == 0:
		_, err := fmt.Fprintln(w, "Link warnings: none")
		return err
	}
	if _, err := fmt.Fprintf(w, "Link warnings: %d\n", len(dto.LinkWarnings)); err != nil {
		return err
	}
	t = output.NewTable(w)
	for _, lw := range dto.LinkWarnings {
		t.Row(output.SafeCell(lw.NodeLoc), output.SafeCell(lw.Status), output.SafeCell(lw.Raw), output.SafeCell(lw.Target))
	}
	return t.Flush()
}

func nodesWord(n int) string {
	if n == 1 {
		return "node"
	}
	return "nodes"
}
