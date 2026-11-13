package team

// `hadron team task find-untracked` on a task intake ADAPTER (hadron-cli#1961,
// cor:agt:120, cor:agt:120:04; plan docs/plans/1961-intake-adapter-runner.md).
// The adapter is a local executable that knows the team's tracker; Hadron makes
// no tracker call. The sequence (cor:agt:120, "The claim sequence"):
//
//	adapter list-untracked -> check the items against Hadron (batch read) ->
//	with --track, the CREATE is the claim -> the winner calls mark-tracked.
//
// The rules that must not break (the FIT's seven, issue #1961):
//  1. The create is the claim: TASK_EXTERNAL_ID_TAKEN means this caller LOST.
//     Never look up the holder, adopt it or mark-tracked it.
//  2. The tracker's mark never makes a claim: an item is dropped only because
//     Hadron holds it.
//  3. The external id is the adapter's URL as written; no normalisation.
//  4. --limit counts tasks HELD: a lost create does not count; keep paging.
//  5. No offset reaches the adapter (--skip is refused on this path).
//  6. The portal URL is the server's; null means none is passed.
//  7. The claim is an ordinary root create (cor:agt:050:06); no tracker label
//     reaches Hadron.

import (
	"context"
	"errors"
	"fmt"
	"github.com/Khan/genqlient/graphql"
	"io"
	"strings"

	"github.com/hadron-memory/hadron-cli/internal/api"
	"github.com/hadron-memory/hadron-cli/internal/api/gen"
	"github.com/hadron-memory/hadron-cli/internal/cmdutil"
	"github.com/hadron-memory/hadron-cli/internal/exitcode"
	"github.com/hadron-memory/hadron-cli/internal/intake"
	"github.com/hadron-memory/hadron-cli/internal/output"
)

const (
	adapterEnv          = "HADRON_INTAKE_ADAPTER"
	adapterDefaultList  = 20
	adapterDefaultTrack = 1
	adapterMaxLimit     = 100
	adapterTitleRunes   = 200
	externalIDTaken     = "TASK_EXTERNAL_ID_TAKEN"
)

type adapterItemDTO struct {
	ExternalID string `json:"externalId"`
	Title      string `json:"title"`
}

// adapterListDTO is the list's --json shape on the adapter path.
type adapterListDTO struct {
	Adapter  string           `json:"adapter"`
	Items    []adapterItemDTO `json:"items"`
	Complete bool             `json:"complete"`
}

type adapterClaimedDTO struct {
	ID         string  `json:"id"`
	Handle     string  `json:"handle"`
	Title      string  `json:"title"`
	Status     string  `json:"status"`
	ExternalID string  `json:"externalId"`
	PortalURL  *string `json:"portalUrl"`
	Assignee   *string `json:"assignee"`
	Marked     bool    `json:"marked"`
	MarkError  *string `json:"markError"`
	// StartError is why a claim the caller holds could not be started: the
	// task exists and is PENDING (a retry finds its external id held and does
	// not create a second).
	StartError *string `json:"startError"`
}

// adapterClaimDTO is the --track --json shape: the tasks this call holds, the
// external ids it lost to another caller, and why the walk stopped short.
type adapterClaimDTO struct {
	Adapter       string              `json:"adapter"`
	Tasks         []adapterClaimedDTO `json:"tasks"`
	Lost          []string            `json:"lost"`
	Complete      bool                `json:"complete"`
	StoppedReason *string             `json:"stoppedReason"`
}

type adapterOpts struct {
	path      string
	forWorker string
	track     bool
	limit     int // 0: the default
	assignee  *gen.AgentTaskPartyInput
	owner     *gen.AgentTaskPartyInput
}

// adapterErr maps a runner error to its exit code. Skew has its own.
func adapterErr(err error) error {
	var skew *intake.SkewError
	switch {
	case errors.As(err, &skew):
		return exitcode.Newf(exitcode.AdapterSkew, "%s", skew.Error())
	case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		return exitcode.Newf(exitcode.Cancelled, "interrupted")
	}
	return exitcode.Newf(exitcode.Error, "%s", output.SafeLine(err.Error(), 400))
}

// heldExternalIDs asks Hadron which of ids a task of the App holds (any state).
func heldExternalIDs(ctx context.Context, client graphql.Client, appRef string, ids []string) (map[string]bool, error) {
	held := map[string]bool{}
	if len(ids) == 0 {
		return held, nil
	}
	resp, err := gen.IntakeTasksByExternalIds(ctx, client, appRef, ids)
	if err != nil {
		if api.IsGraphQLValidationOnlyFor(err, "externalIds") {
			return nil, exitcode.Newf(exitcode.Usage, "this server cannot check outside items against its tasks (it predates hadron-server#4084); nothing was claimed")
		}
		return nil, mapTaskError(err)
	}
	for _, t := range resp.AgentTasks {
		if t != nil && t.ExternalId != nil {
			held[*t.ExternalId] = true
		}
	}
	return held, nil
}

// requireCalls refuses, before Hadron is touched, an adapter that does not
// advertise a call this run needs: list-untracked always, mark-tracked for
// --track (a claim the adapter cannot mark would leave a durable claim behind).
func requireCalls(desc intake.Describe, track bool) error {
	need := []string{"list-untracked"}
	if track {
		need = append(need, "mark-tracked")
	}
	for _, c := range need {
		if !desc.Has(c) {
			return exitcode.Newf(exitcode.Usage, "the intake adapter %s does not answer %s, which this command needs; nothing was claimed", output.SafeCell(desc.Name), c)
		}
	}
	return nil
}

// runFindUntrackedAdapter runs the flow with the adapter already described
// (runAdapterCommand does that first, before any App or binding resolution).
func runFindUntrackedAdapter(ctx context.Context, f *cmdutil.Factory, b *binding, appRef string, runner *intake.Runner, desc intake.Describe, o adapterOpts) error {
	client, err := f.GraphQLClient()
	if err != nil {
		return err
	}
	limit := o.limit
	if limit == 0 {
		limit = adapterDefaultList
		if o.track {
			limit = adapterDefaultTrack
		}
	}
	boundWorker := ""
	if b != nil && b.WorkerID != "" && bindingServerMatches(f, b) {
		boundWorker = b.WorkerID
	}
	var claim adapterClaim
	if o.track {
		claim = adapterClaim{f: f, ctx: ctx, client: client, runner: runner, appRef: appRef, b: b, boundWorker: boundWorker, o: o}
		if err := claim.assignee(); err != nil {
			return err
		}
	}
	list := adapterListDTO{Adapter: desc.Name, Items: []adapterItemDTO{}}
	claimed := adapterClaimDTO{Adapter: desc.Name, Tasks: []adapterClaimedDTO{}, Lost: []string{}}
	var stopErr error
	walkErr := runner.Walk(ctx, o.forWorker, pageHint(limit), func(p intake.Page) (bool, error) {
		ids := make([]string, 0, len(p.Items))
		for _, it := range p.Items {
			ids = append(ids, it.ExternalID)
		}
		held, err := heldExternalIDs(ctx, client, appRef, ids)
		if err != nil {
			stopErr = err
			return true, nil
		}
		for _, it := range p.Items {
			if held[it.ExternalID] { // Hadron is authoritative (rule 2)
				continue
			}
			if !o.track {
				list.Items = append(list.Items, adapterItemDTO{ExternalID: it.ExternalID, Title: output.SafeLine(it.Title, adapterTitleRunes)})
				if len(list.Items) >= limit {
					return true, nil
				}
				continue
			}
			res, err := claim.one(it)
			switch {
			case err != nil:
				stopErr = err
				return true, nil
			case res.lost:
				claimed.Lost = append(claimed.Lost, it.ExternalID)
			default:
				claimed.Tasks = append(claimed.Tasks, res.task)
				if res.task.MarkError != nil { // a failed mark leaves the claim standing; stop adding more
					stopErr = exitcode.Newf(exitcode.Error, "the adapter's mark failed for %s (the claim stands): %s", output.SafeCell(it.ExternalID), output.SafeCell(*res.task.MarkError))
					return true, nil
				}
				if len(claimed.Tasks) >= limit { // rule 4: tasks HELD
					return true, nil
				}
			}
		}
		return false, nil
	})
	if walkErr != nil && stopErr == nil {
		stopErr = adapterErr(walkErr)
	}
	complete := stopErr == nil
	var reason *string
	if stopErr != nil {
		s := "stopped"
		reason = &s
	}
	if !o.track {
		list.Complete = complete
		if err := writeAdapterList(f, list); err != nil {
			return err
		}
		return stopErr
	}
	claimed.Complete, claimed.StoppedReason = complete, reason
	if err := writeAdapterClaim(f, claimed); err != nil {
		return err
	}
	return stopErr
}

// pageHint is the page size asked of the adapter: a hint, bounded by the
// contract's page cap.
func pageHint(limit int) int {
	if limit < 20 {
		return 20
	}
	return min(limit, intake.MaxItemsPerPage)
}

type adapterClaim struct {
	f           *cmdutil.Factory
	ctx         context.Context
	client      graphql.Client
	runner      *intake.Runner
	appRef      string
	b           *binding
	boundWorker string
	o           adapterOpts
	party       *gen.AgentTaskPartyInput
}

// assignee settles who the claims go to: --assignee, else --for X, else the
// bound worker. A claim with none of these would be queued for the platform's
// matcher, which is not what a claim is.
func (c *adapterClaim) assignee() error {
	switch {
	case c.o.assignee != nil:
		c.party = c.o.assignee
	case c.o.forWorker != "":
		w := c.o.forWorker
		c.party = &gen.AgentTaskPartyInput{Worker: &w}
	case c.boundWorker != "":
		w := c.boundWorker
		c.party = &gen.AgentTaskPartyInput{Worker: &w}
	default:
		return exitcode.Newf(exitcode.Usage, "--track needs someone to hold the claim: pass --for <worker> or --assignee <party>, or run it in a worktree bound to a worker; nothing was sent")
	}
	return nil
}

type claimOne struct {
	lost bool
	task adapterClaimedDTO
}

// one claims a single item: the create is the claim; the winner marks.
func (c *adapterClaim) one(it intake.Item) (claimOne, error) {
	title := output.SafeLine(it.Title, adapterTitleRunes)
	if strings.TrimSpace(title) == "" {
		title = output.SafeLine(it.ExternalID, adapterTitleRunes)
	}
	ext := it.ExternalID // as written (rule 3)
	in := gen.CreateAgentTaskInput{
		AppRef:     &c.appRef,
		Title:      title,
		Source:     &ext,
		ExternalId: &ext,
		Assignee:   c.party,
		Owner:      c.o.owner,
	}
	resp, err := gen.IntakeClaimTask(c.ctx, c.client, &in)
	if err != nil {
		if api.HasErrorCode(err, externalIDTaken) {
			return claimOne{lost: true}, nil // another caller won: nothing else (rule 1)
		}
		return claimOne{}, api.WithServerCode(mapTaskError(err), err)
	}
	t := resp.CreateAgentTask
	dto := adapterClaimedDTO{ID: t.Id, Handle: t.Handle, Title: t.Title, Status: string(t.Status), ExternalID: ext, PortalURL: t.PortalUrl}
	var worker intake.Worker
	if t.Assignee != nil {
		name := ""
		if t.Assignee.Name != nil {
			name = *t.Assignee.Name
		}
		dto.Assignee = &name
		worker = intake.Worker{ID: t.Assignee.Id, Name: name}
		if c.b != nil && c.boundWorker != "" && t.Assignee.Id == c.boundWorker {
			worker.Role, worker.Model = c.b.WorkerRole, c.b.Model
			// The assignee is the caller, so the caller may start it (the
			// server lets only the assignee start); anyone else's stays PENDING.
			st, err := gen.IntakeStartTask(c.ctx, c.client, t.Id)
			switch {
			case err != nil:
				msg := output.SafeLine(mapTaskError(err).Error(), 300)
				dto.StartError = &msg
			case st.StartAgentTask != nil:
				dto.Status = string(st.StartAgentTask.Status)
			}
		}
	}
	task := intake.Task{ID: t.Id, Handle: t.Handle}
	if t.PortalUrl != nil { // rule 6: the server's, or none
		task.PortalURL = *t.PortalUrl
	}
	if err := c.runner.MarkTracked(c.ctx, ext, task, worker); err != nil {
		msg := output.SafeLine(err.Error(), 300)
		dto.MarkError = &msg
	} else {
		dto.Marked = true
	}
	return claimOne{task: dto}, nil
}

func writeAdapterList(f *cmdutil.Factory, dto adapterListDTO) error {
	return output.Write(f.IOStreams, f.JSON, dto, func(w io.Writer) error {
		if len(dto.Items) == 0 {
			_, err := fmt.Fprintf(w, "no untracked items (adapter %s)\n", output.SafeCell(dto.Adapter))
			return err
		}
		t := output.NewTable(w, "EXTERNAL ID", "TITLE")
		for _, it := range dto.Items {
			t.Row(output.SafeCell(it.ExternalID), output.SafeCell(it.Title))
		}
		return t.Flush()
	})
}

func writeAdapterClaim(f *cmdutil.Factory, dto adapterClaimDTO) error {
	err := output.Write(f.IOStreams, f.JSON, dto, func(w io.Writer) error {
		if len(dto.Tasks) == 0 {
			_, err := fmt.Fprintln(w, "claimed nothing: no untracked item was won")
			return err
		}
		t := output.NewTable(w, "TASK", "STATUS", "MARKED", "EXTERNAL ID", "TITLE")
		for _, task := range dto.Tasks {
			marked := "yes"
			if !task.Marked {
				marked = "NO"
			}
			t.Row(output.SafeCell(taskRef(task.Handle, task.ID)), output.SafeCell(task.Status), marked, output.SafeCell(task.ExternalID), output.SafeCell(task.Title))
		}
		return t.Flush()
	})
	if err != nil {
		return err
	}
	for _, id := range dto.Lost {
		_, _ = fmt.Fprintf(f.IOStreams.ErrOut, "note: another caller claimed %s first; not counted\n", output.SafeCell(id))
	}
	for _, task := range dto.Tasks {
		if task.StartError != nil {
			_, _ = fmt.Fprintf(f.IOStreams.ErrOut, "note: %s exists but could not be started (%s); it is PENDING, and a retry will not create it again\n", output.SafeCell(taskRef(task.Handle, task.ID)), output.SafeCell(*task.StartError))
		}
		if strings.EqualFold(task.Status, "pending") && task.Assignee != nil {
			_, _ = fmt.Fprintf(f.IOStreams.ErrOut, "note: %s is PENDING for %s, who starts it\n", output.SafeCell(taskRef(task.Handle, task.ID)), output.SafeCell(*task.Assignee))
		}
	}
	return nil
}
