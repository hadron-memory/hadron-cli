package team

import (
	"context"
	"encoding/json"
	"fmt"
	"io"

	"github.com/Khan/genqlient/graphql"
	"github.com/hadron-memory/hadron-cli/internal/api"
	"github.com/hadron-memory/hadron-cli/internal/api/gen"
	"github.com/hadron-memory/hadron-cli/internal/cmdutil"
	"github.com/hadron-memory/hadron-cli/internal/exitcode"
	"github.com/hadron-memory/hadron-cli/internal/output"
	"github.com/spf13/cobra"
)

type immutableFindingDTO struct {
	ID       string `json:"id"`
	Severity string `json:"severity"`
	Text     string `json:"text"`
}
type immutableFindingPageDTO struct {
	Items   []immutableFindingDTO `json:"items"`
	Total   int                   `json:"total"`
	HasMore bool                  `json:"hasMore"`
}
type immutableFindingsReadDTO struct {
	TaskID   string                   `json:"taskId"`
	Findings *immutableFindingPageDTO `json:"findings"`
}
type resultWithFindingsDTO struct {
	taskResultDTO
	Findings *immutableFindingPageDTO `json:"findings"`
}
type taskWithFindingsDTO struct {
	taskDTO
	Result   *resultWithFindingsDTO `json:"result"`
	Warnings []taskWarningDTO       `json:"warnings"`
}

func immutablePage(v gen.ImmutableTaskFindingPage) (*immutableFindingPageDTO, error) {
	if v.Items == nil || v.Total == nil || v.HasMore == nil || *v.Total < 0 || len(v.Items) > *v.Total {
		return nil, exitcode.Newf(exitcode.Error, "invalid findings page")
	}
	p := &immutableFindingPageDTO{Items: make([]immutableFindingDTO, 0, len(v.Items)), Total: *v.Total, HasMore: *v.HasMore}
	for _, item := range v.Items {
		if item == nil || item.Id == nil || item.Severity == nil || item.Text == nil || *item.Id == "" {
			return nil, exitcode.Newf(exitcode.Error, "invalid null finding")
		}
		p.Items = append(p.Items, immutableFindingDTO{*item.Id, string(*item.Severity), *item.Text})
	}
	return p, nil
}

// renderImmutableFindings does not infer completion from the number served.
// Severity is a literal label only, never sorting, colour or exit policy.
func renderImmutableFindings(w io.Writer, task string, p *immutableFindingPageDTO, limit, offset int) error {
	if p == nil {
		_, err := fmt.Fprintln(w, "no result yet")
		return err
	}
	for _, item := range p.Items {
		if _, err := fmt.Fprintf(w, "  %s [%s]\n    %s\n", output.SafeCell(item.ID), output.SafeCell(item.Severity), output.SafeBlock(item.Text, 2000, "    ")); err != nil {
			return err
		}
	}
	if _, err := fmt.Fprintf(w, "findings: shown %d / total %d; hasMore %t\n", len(p.Items), p.Total, p.HasMore); err != nil {
		return err
	}
	if !p.HasMore {
		return nil
	}
	if limit == 0 {
		_, err := fmt.Fprintln(w, "count-only page; no items consumed (request a positive --limit to read findings)")
		return err
	}
	if len(p.Items) == 0 {
		_, err := fmt.Fprintln(w, "continuation made no progress; inspect the findings page before continuing")
		return err
	}
	if offset > 2147483647-len(p.Items) {
		_, err := fmt.Fprintln(w, "continuation exceeds the GraphQL Int offset range; inspect the findings page before continuing")
		return err
	}
	_, err := fmt.Fprintf(w, "continue: hadron team task findings %s --offset %d\n", output.SafeCell(task), offset+len(p.Items))
	return err
}

func finishTaskWithFindings(ctx context.Context, client graphql.Client, f *cmdutil.Factory, taskID, outcome, summary, evidence string, findings []*gen.AgentTaskFindingInput) error {
	mctx, sink := api.WithWarningSink(ctx)
	wire, err := json.Marshal(findings)
	if err != nil {
		return err
	}
	resp, err := gen.AgentTaskFinishWithFindings(mctx, client, taskID, outcome, optStr(summary), optStr(evidence), wire)
	if err != nil {
		return api.WithServerCode(uncertainFindingsFinish(mapTaskError(err)), err)
	}
	invalid := func() error {
		return uncertainFindingsFinish(exitcode.Newf(exitcode.Error, "invalid finish findings response"))
	}
	if resp == nil || resp.FinishAgentTask == nil {
		return invalid()
	}
	returned := resp.FinishAgentTask
	if returned.Id == "" || returned.Result == nil || returned.FindingsResult == nil || returned.FindingsResult.Findings == nil {
		return invalid()
	}
	page, err := immutablePage(returned.FindingsResult.Findings.ImmutableTaskFindingPage)
	if err != nil {
		return uncertainFindingsFinish(err)
	}
	if len(page.Items) > 20 {
		return invalid()
	}
	task := taskDTOFrom(returned.AgentTaskFields)
	warnings := taskWarningsFor(sink, "finishAgentTask")
	if f.JSON {
		return output.Write(f.IOStreams, true, taskWithFindingsDTO{task, &resultWithFindingsDTO{*task.Result, page}, warnings}, nil)
	}
	return writeTaskMutation(f, task, warnings, func(w io.Writer) error {
		if _, err := fmt.Fprintf(w, "✓ finished task %s (%s)\n", taskRef(task.Handle, task.ID), task.Result.Outcome); err != nil {
			return err
		}
		if task.Result.Summary != nil {
			if _, err := fmt.Fprintf(w, "  %s\n", *task.Result.Summary); err != nil {
				return err
			}
		}
		return renderImmutableFindings(w, task.ID, page, 20, 0)
	})
}

// A refusal with an authoritative mapped code keeps its established refusal.
// Network/unknown-answer failures and malformed receipts must not invite replay.
func uncertainFindingsFinish(err error) error {
	code := exitcode.FromError(err)
	if code != exitcode.Error && code != exitcode.Unavailable {
		return err
	}
	return exitcode.Newf(code, "finish state may have changed; inspect `hadron team task findings <task>` before deciding what to do; do not resend automatically (%s)", output.SafeBlock(err.Error(), 2000, "  "))
}

func newCmdTaskResultFindings(f *cmdutil.Factory) *cobra.Command {
	var limit, offset int64
	c := &cobra.Command{Use: "findings <task>", Short: "Read one page of immutable task result findings", Long: `Read immutable findings recorded at finish (server#3248/#2662, cor:agt:050:30).
Requires a provider containing server#3248, only for this command and opt-in finish.
This is not the separate latest finding-dispositions surface. Uses the task's existing readers.
Default --limit 20; explicit 0 is count-only; larger positive GraphQL Int values pass
through for provider capping (200). --offset is non-negative. Follow hasMore and the
actual served count; no automatic page drain, read cursor, retry or writes.
A task with no result prints no result yet (JSON findings:null); an empty finished
result has a page with items:[],total:0,hasMore:false. Missing/unreadable task exits4.
Human text neutralizes terminal controls; JSON retains complete original valid text.
Severity is only a label and never changes exit status or task outcome.
Sign in first. A task id or URN works directly; a handle uses the current App.`, Example: `  hadron team task findings AT0.7
  hadron team task findings AT0.7 --limit 0 --json
  hadron team task findings AT0.7 --limit 20 --offset 20`, Args: cobra.ExactArgs(1), RunE: func(c *cobra.Command, a []string) error {
		if limit < 0 || limit > 2147483647 || offset < 0 || offset > 2147483647 {
			return exitcode.Newf(exitcode.Usage, "--limit and --offset must be non-negative GraphQL Int values")
		}
		ref, err := taskRefArg(f, a)
		if err != nil {
			return err
		}
		client, err := f.GraphQLClient()
		if err != nil {
			return err
		}
		resp, err := gen.AgentTaskResultFindings(c.Context(), client, ref, int(limit), int(offset))
		if err != nil {
			return api.WithServerCode(mapTaskError(err), err)
		}
		if resp == nil || resp.AgentTask == nil {
			return exitcode.Newf(exitcode.NotFound, "task is missing or unreadable")
		}
		if resp.AgentTask.Id == "" {
			return exitcode.Newf(exitcode.Error, "invalid task findings response")
		}
		dto := immutableFindingsReadDTO{TaskID: resp.AgentTask.Id}
		if resp.AgentTask.Result != nil {
			if resp.AgentTask.Result.Findings == nil {
				return exitcode.Newf(exitcode.Error, "invalid null findings page")
			}
			dto.Findings, err = immutablePage(resp.AgentTask.Result.Findings.ImmutableTaskFindingPage)
			if err != nil {
				return err
			}
			if int64(len(dto.Findings.Items)) > limit {
				return exitcode.Newf(exitcode.Error, "findings page exceeds requested limit")
			}
		}
		return output.Write(f.IOStreams, f.JSON, dto, func(w io.Writer) error {
			return renderImmutableFindings(w, dto.TaskID, dto.Findings, int(limit), int(offset))
		})
	}}
	c.Flags().Int64Var(&limit, "limit", 20, "page size (0 count-only; provider caps large requests)")
	c.Flags().Int64Var(&offset, "offset", 0, "non-negative page offset")
	return c
}
