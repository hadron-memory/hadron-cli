package team

// `hadron team worker approve` (cli#1466): approve the App's compiled worker
// boot prompts in one run. Approval only (hadron-server#2973 ruling (a)):
// no signing request, no step-up, and no register grant is read. The server
// decides authority per item (cor:dmo:120:02, cor:agt:040:18); the CLI checks
// none. Each approveNode is pinned to the revision the preview read, sent
// once, and is its own server transaction. Tamper is the server's verdict
// (verifyNode TAMPERED, cor:dmo:120:05), never inferred here.

import (
	"context"
	"fmt"
	"io"
	"strings"

	"github.com/Khan/genqlient/graphql"
	"github.com/hadron-memory/hadron-cli/internal/api"
	"github.com/hadron-memory/hadron-cli/internal/api/gen"
	"github.com/hadron-memory/hadron-cli/internal/cmdutil"
	"github.com/hadron-memory/hadron-cli/internal/exitcode"
	"github.com/hadron-memory/hadron-cli/internal/output"
	"github.com/spf13/cobra"
)

type approvalBatchNode = gen.BootPromptApprovalBatchNodeBatchNodeBatchResultNodesNode

// approveAuthority and approveLimits are carried in --json so an agent reading
// the result sees the same caveats a human reads in help.
const approveAuthority = "the server decides authority for each worker (whoever manages the App, acting as themselves); a register grant is not checked by the CLI or the server"

// approveAuthorityAsWorker replaces the constant under --as-worker (cli#1872),
// where the default sentence ("acting as themselves", "a register grant is not
// checked") would be false. No JSON key is added.
const approveAuthorityAsWorker = "approvals go out as the bound worker; the server admits each only under a live bootprompt.compile.approve grant naming that compiled node, and decides who may approve; the CLI checks no grant"

var approveLimits = []string{
	"each approval is its own server transaction: there is no rollback, and an earlier approval stands when a later one fails",
	"a tamper that lands between the preview's verifyNode and the approval is not caught (a tamper changes no revision)",
	"serve is a snapshot of what a serve would give when it was read, not a guarantee for the next bind",
}

type approveTargetDTO struct {
	Name            string  `json:"name"`
	ID              string  `json:"id"`
	URN             string  `json:"urn"`
	CompiledNodeRef *string `json:"compiledNodeRef"`
	Revision        *int    `json:"revision"`
	State           *string `json:"state"`
	Reason          string  `json:"reason"`
}

type approveServeDTO struct {
	ServedRevSeq *int    `json:"servedRevSeq"`
	Refusal      *string `json:"refusal"`
	Unread       bool    `json:"unread"`
}

type approveDoneDTO struct {
	Name            string          `json:"name"`
	ID              string          `json:"id"`
	URN             string          `json:"urn"`
	CompiledNodeRef string          `json:"compiledNodeRef"`
	Revision        int             `json:"revision"`
	Serve           approveServeDTO `json:"serve"`
}

type approveRefusalDTO struct {
	Name            string `json:"name"`
	ID              string `json:"id"`
	URN             string `json:"urn"`
	CompiledNodeRef string `json:"compiledNodeRef"`
	Revision        int    `json:"revision"`
	Code            string `json:"code"`
	ExitCode        int    `json:"exitCode"`
	Message         string `json:"message"`
}

type approveUnknownDTO struct {
	Name            string  `json:"name"`
	ID              string  `json:"id"`
	URN             string  `json:"urn"`
	CompiledNodeRef *string `json:"compiledNodeRef"`
	Revision        *int    `json:"revision"`
	// Outcome is set only for a lost approval response: landed, not-landed or
	// unknown. A worker the preview could not decide was never sent: "".
	Outcome string `json:"outcome"`
	Reason  string `json:"reason"`
}

type approveBatchDTO struct {
	App             string              `json:"app"`
	Applied         bool                `json:"applied"`
	Authority       string              `json:"authority"`
	Limits          []string            `json:"limits"`
	ToApprove       []approveTargetDTO  `json:"toApprove"`
	Approved        []approveDoneDTO    `json:"approved"`
	AlreadyApproved []approveTargetDTO  `json:"alreadyApproved"`
	Tampered        []approveTargetDTO  `json:"tampered"`
	Skipped         []approveTargetDTO  `json:"skipped"`
	Failed          []approveRefusalDTO `json:"failed"`
	Unknown         []approveUnknownDTO `json:"unknown"`
}

func newCmdWorkerApprove(f *cmdutil.Factory) *cobra.Command {
	var only []string
	var apply, asWorker bool
	cmd := &cobra.Command{
		Use:   "approve",
		Short: "Approve the App's compiled worker boot prompts in one run",
		Long: `Approve the compiled boot prompt of every live worker in the App whose current
revision is not approved: a worker with its own compiled prompt (team worker
compile), its target not deleted, and approval state NOT_APPROVED or
SUPERSEDED. Install- and Agent-level compiled prompts are not included.

Without --apply this is a read-only preview: each worker is listed as to
approve, already approved, tampered, skipped (no compiled prompt, deleted
target, unavailable to you, encrypted memory) or unknown, with its compiled node and revision. With
--apply, each to-approve worker is sent one approval, in roster order, pinned
to the revision the preview read. A worker whose prompt changed since then is
refused REVISION_MISMATCH (exit 5): run the preview again.

By default the server decides who may approve, for each worker: whoever manages
the App, acting as themselves. The CLI checks no authority itself, and a
register grant is not checked by the CLI or the server. With --as-worker the
approvals go out through this worktree's bound worker session instead: the
server admits each only for a compiled prompt that a signed
bootprompt.compile.approve grant names (to any worker in the App), records the
approver as the worker under the batch grant, and refuses the rest (exit 8,
with its code). The CLI still checks no grant. --as-worker never falls back to
you: without a usable binding for this server and App it refuses (exit 2) and
sends nothing, even for a preview. The grant does not cover signing: where
an organisation has switched on signing of compiled boot prompts (its own
setting, not the passkey switch), a person still signs each compiled prompt. A refusal for one worker does
not stop the others; every worker is reported. Each approval is its own server
transaction, with no rollback: an earlier approval stands when a later one
fails. Nothing is signed and no signing request is opened.

A SUPERSEDED row is checked with the server's verifyNode. When the server
reports TAMPERED, the row is an integrity finding to investigate, not a
pending review: it is never approved in a batch, and the run exits 5. After
investigating, approve it on its own with node approve <ref> --revision N.
A tamper that lands between the preview and --apply is not caught, because a
tamper changes no revision.

After each approval the command reads, once, what a serve would give that
worker at that moment: "a serve now would give rev N", "would still give rev
M", "would be refused: CODE" (for example BOOT_PROMPT_NOT_SIGNED where the
organisation requires signing), or "serving state unread". It is a snapshot,
not a guarantee for the next bind. The read does not change the exit code.

A lost approval response is checked with one read of the approval state and is
never sent again: the worker is reported as unknown with outcome landed,
not-landed or unknown (exit 7).

Exit codes: the maximum over the workers. 0 when every selected worker is
approved or nothing is pending; 5 for the server refusals REVISION_MISMATCH
and NODE_IS_PLACEHOLDER, for a prompt in an encrypted memory (approval does
not apply, so it is never sent) and for a tampered row; 8 when the server refuses the approval
(BOOT_PROMPT_APPROVAL_FORBIDDEN, BOOT_PROMPT_WORKER_SESSION_REFUSED); 7 for
a lost response; 4 for an --only worker that is not live in the App (nothing
is sent); 1 for a row whose state the command could not decide.

--json: app, applied, authority, limits, and the buckets toApprove, approved
(with serve{servedRevSeq, refusal, unread}), alreadyApproved, tampered,
skipped, failed{code, exitCode, message} and unknown{outcome, reason}.`,
		Example: `  hadron team worker approve
  hadron team worker approve --apply
  hadron team worker approve --only Iris,Theo --apply --json`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			opts := []string{}
			for _, name := range only {
				if name = strings.TrimSpace(name); name != "" {
					opts = append(opts, name)
				}
			}
			// Changed, not len: `--only=` parses to an empty slice, and a
			// dropped filter would approve the whole App.
			if cmd.Flags().Changed("only") && len(opts) == 0 {
				return exitcode.Newf(exitcode.Usage, "--only needs at least one worker")
			}
			return runWorkerApproveBatch(cmd, f, opts, apply, asWorker)
		},
	}
	cmd.Flags().StringSliceVar(&only, "only", nil, "approve only these workers (comma-separated names, ids or URNs)")
	cmd.Flags().BoolVar(&apply, "apply", false, "send the approvals (without it the command only previews)")
	cmd.Flags().BoolVar(&asWorker, "as-worker", false, cmdutil.AsWorkerFlagUsage)
	return cmd
}

// approvePlanItem is one worker the preview found eligible, pinned.
type approvePlanItem struct {
	target   approveTargetDTO
	ref      string
	revision int
}

func runWorkerApproveBatch(cmd *cobra.Command, f *cmdutil.Factory, only []string, apply, asWorker bool) error {
	ctx := cmd.Context()
	// --as-worker resolves its session before ANY request (cli#1872).
	var send func(context.Context) (context.Context, error)
	authority := approveAuthority
	if asWorker {
		m, err := f.ResolveWorkerWrite(ctx)
		if err != nil {
			return err
		}
		send = m
		authority = approveAuthorityAsWorker
	}
	scope, err := compileNameScope(cmd, f)
	if err != nil {
		return err
	}
	client, err := f.GraphQLClient()
	if err != nil {
		return err
	}
	roster, err := scanBootPointers(ctx, client, scope.Ref)
	if err != nil {
		return err
	}
	roster, err = filterOnlyFor(roster, only, scope.Ref, "nothing was approved")
	if err != nil {
		return err
	}

	dto := approveBatchDTO{
		App: scope.Ref, Applied: apply, Authority: authority, Limits: approveLimits,
		ToApprove: []approveTargetDTO{}, Approved: []approveDoneDTO{}, AlreadyApproved: []approveTargetDTO{},
		Tampered: []approveTargetDTO{}, Skipped: []approveTargetDTO{}, Failed: []approveRefusalDTO{},
		Unknown: []approveUnknownDTO{},
	}

	refs := []string{}
	for _, w := range roster {
		if ref := strPtr(w.BootPromptRef); ref != "" && !isTrue(w.BootPromptTargetDeleted) {
			refs = append(refs, ref)
		}
	}
	// A failed read aborts before anything is sent: the preview must be whole.
	nodes, err := readApprovalStates(ctx, client, refs)
	if err != nil {
		return err
	}

	exit := exitcode.OK
	plan := []approvePlanItem{}
	for _, w := range roster {
		t := approveTargetDTO{Name: w.Name, ID: w.Id, URN: strPtr(w.Urn)}
		ref := strPtr(w.BootPromptRef)
		if ref != "" {
			t.CompiledNodeRef = &ref
		}
		node, ok := nodes[ref]
		switch {
		case ref == "":
			t.Reason = "no compiled prompt of its own"
			dto.Skipped = append(dto.Skipped, t)
			continue
		case isTrue(w.BootPromptTargetDeleted):
			t.Reason = "the compiled prompt is deleted"
			dto.Skipped = append(dto.Skipped, t)
			continue
		case !ok:
			t.Reason = "the compiled prompt is unavailable to you"
			dto.Skipped = append(dto.Skipped, t)
			continue
		}
		rev := node.Revision
		t.Revision = &rev
		if node.ApprovalStatus == nil {
			dto.Unknown = append(dto.Unknown, approveUnknownDTO{Name: t.Name, ID: t.ID, URN: t.URN, CompiledNodeRef: t.CompiledNodeRef, Revision: &rev,
				Reason: "the server reported no approval state; not approved"})
			exit = max(exit, exitcode.Error)
			continue
		}
		state := string(node.ApprovalStatus.State)
		t.State = &state
		switch node.ApprovalStatus.State {
		case gen.NodeApprovalStateApproved:
			t.Reason = "the current revision is approved"
			dto.AlreadyApproved = append(dto.AlreadyApproved, t)
			continue
		case gen.NodeApprovalStateNotApplicable:
			// Never sent, but not a success: an unapprovable selected prompt
			// must not let a script read the batch as done (exit 5).
			t.Reason = "approval does not apply (encrypted memory); not sent"
			dto.Skipped = append(dto.Skipped, t)
			exit = max(exit, exitcode.Conflict)
			continue
		case gen.NodeApprovalStateNotApproved:
			t.Reason = "not approved"
		case gen.NodeApprovalStateSuperseded:
			verdict, reason := verifySuperseded(ctx, client, ref)
			switch verdict {
			case gen.NodeIntegrityStateSuperseded:
				t.Reason = "changed by a legitimate edit since its approval (verifyNode SUPERSEDED)"
			case gen.NodeIntegrityStateTampered:
				t.Reason = fmt.Sprintf("integrity finding (verifyNode TAMPERED): investigate, then approve it on its own with node approve %s --revision %d", ref, rev)
				dto.Tampered = append(dto.Tampered, t)
				exit = max(exit, exitcode.Conflict)
				continue
			default:
				dto.Unknown = append(dto.Unknown, approveUnknownDTO{Name: t.Name, ID: t.ID, URN: t.URN, CompiledNodeRef: t.CompiledNodeRef, Revision: &rev,
					Reason: reason + "; not approved"})
				exit = max(exit, exitcode.Error)
				continue
			}
		default:
			dto.Unknown = append(dto.Unknown, approveUnknownDTO{Name: t.Name, ID: t.ID, URN: t.URN, CompiledNodeRef: t.CompiledNodeRef, Revision: &rev,
				Reason: "unrecognised approval state " + state + "; not approved"})
			exit = max(exit, exitcode.Error)
			continue
		}
		dto.ToApprove = append(dto.ToApprove, t)
		plan = append(plan, approvePlanItem{target: t, ref: ref, revision: rev})
	}

	if apply {
		for _, item := range plan {
			exit = max(exit, approveOne(ctx, client, &dto, item, send))
		}
	}

	if werr := output.Write(f.IOStreams, f.JSON, dto, func(w io.Writer) error {
		return renderApproveBatch(w, ctx, f, scope, dto)
	}); werr != nil {
		return werr
	}
	if exit != exitcode.OK {
		return exitcode.Silent(exit)
	}
	return nil
}

// verifySuperseded asks the server whether a SUPERSEDED row is a legitimate
// edit or a tamper. Only SUPERSEDED or TAMPERED decide the row; any other
// answer (a refusal, an empty result, INTACT, NOT_APPROVED) leaves it
// undecided, with a reason, and it is never promoted to an eligible row.
func verifySuperseded(ctx context.Context, client graphql.Client, ref string) (gen.NodeIntegrityState, string) {
	r, err := gen.VerifyNode(ctx, client, ref)
	switch {
	case err != nil:
		return "", "the server's integrity check failed (" + api.MapError(err).Error() + ")"
	case r.VerifyNode == nil:
		return "", "the server returned no integrity check"
	}
	switch s := r.VerifyNode.State; s {
	case gen.NodeIntegrityStateSuperseded, gen.NodeIntegrityStateTampered:
		return s, ""
	default:
		return "", fmt.Sprintf("the integrity check reported %s for a SUPERSEDED approval state", s)
	}
}

// approveOne sends the pinned approval once and files the worker in its
// bucket, returning the exit code it contributes.
func approveOne(ctx context.Context, client graphql.Client, dto *approveBatchDTO, item approvePlanItem, send func(context.Context) (context.Context, error)) int {
	t := item.target
	rev := item.revision
	sendCtx := ctx
	if send != nil {
		c, serr := send(ctx)
		if serr != nil {
			dto.Failed = append(dto.Failed, approveRefusalDTO{Name: t.Name, ID: t.ID, URN: t.URN, CompiledNodeRef: item.ref, Revision: item.revision, ExitCode: exitcode.Usage, Message: serr.Error()})
			return exitcode.Usage
		}
		sendCtx = c
	}
	r, err := gen.ApproveNode(sendCtx, client, item.ref, &rev)
	if err == nil {
		if r.ApproveNode == nil || r.ApproveNode.Approval == nil {
			// An empty success may have landed: the same one status read as a
			// lost response decides, and nothing is sent again.
			u := checkLostApproval(ctx, client, item)
			u.Reason = strings.Replace(u.Reason, "the approval response was lost", "the server returned no approval", 1)
			dto.Unknown = append(dto.Unknown, u)
			return exitcode.Error
		}
		approved := r.ApproveNode.Approval.Revision
		dto.Approved = append(dto.Approved, approveDoneDTO{Name: t.Name, ID: t.ID, URN: t.URN, CompiledNodeRef: item.ref,
			Revision: approved, Serve: readServe(ctx, client, t.ID)})
		return exitcode.OK
	}
	code := api.ServerCodeOf(err)
	mapped := api.MapError(err)
	ec := exitcode.FromError(mapped)
	if code == "" && ec == exitcode.Unavailable {
		dto.Unknown = append(dto.Unknown, checkLostApproval(ctx, client, item))
		return exitcode.Unavailable
	}
	if ec == exitcode.OK {
		ec = exitcode.Error
	}
	dto.Failed = append(dto.Failed, approveRefusalDTO{Name: t.Name, ID: t.ID, URN: t.URN, CompiledNodeRef: item.ref, Revision: rev,
		Code: code, ExitCode: ec, Message: mapped.Error()})
	return ec
}

// checkLostApproval makes the ONE read that decides whether a lost approval
// landed at the pinned revision. It never approves again.
func checkLostApproval(ctx context.Context, client graphql.Client, item approvePlanItem) approveUnknownDTO {
	t := item.target
	rev := item.revision
	u := approveUnknownDTO{Name: t.Name, ID: t.ID, URN: t.URN, CompiledNodeRef: t.CompiledNodeRef, Revision: &rev,
		Outcome: lostUnknown, Reason: "the approval response was lost; it was not sent again"}
	r, err := gen.BootPromptApprovalBatch(ctx, client, []string{item.ref})
	if err != nil || r.NodeBatch == nil {
		u.Reason += "; the status check failed too: check with node get before approving again"
		return u
	}
	var node *approvalBatchNode
	for _, n := range r.NodeBatch.Nodes {
		if n != nil && (n.Urn == item.ref || n.Id == item.ref) {
			node = n
		}
	}
	switch {
	case node == nil || node.ApprovalStatus == nil:
		u.Reason += "; the status check returned no approval state: check with node get before approving again"
	case node.ApprovalStatus.State == gen.NodeApprovalStateApproved && node.ApprovalStatus.Approval != nil && node.ApprovalStatus.Approval.Revision == rev:
		u.Outcome = lostLanded
		u.Reason += fmt.Sprintf("; revision %d is approved", rev)
	default:
		u.Outcome = lostNotLanded
		u.Reason += fmt.Sprintf("; no approval on record at revision %d (approval state %s): run the preview again", rev, node.ApprovalStatus.State)
	}
	return u
}

// readServe is the ONE serving read after a successful approval: what a serve
// would give the worker now. A failed or empty read leaves the approval
// standing and says so.
func readServe(ctx context.Context, client graphql.Client, workerID string) approveServeDTO {
	r, err := gen.GetWorkerBootPromptServe(ctx, client, workerID)
	if err != nil || r.Worker == nil || r.Worker.BootPromptServe == nil {
		return approveServeDTO{Unread: true}
	}
	s := r.Worker.BootPromptServe
	switch {
	case s.Served != nil:
		n := s.Served.ServedRevSeq
		return approveServeDTO{ServedRevSeq: &n}
	case s.Refusal != nil:
		code := string(*s.Refusal)
		return approveServeDTO{Refusal: &code}
	default:
		return approveServeDTO{Unread: true}
	}
}

func serveCopy(rev int, s approveServeDTO) string {
	switch {
	case s.ServedRevSeq != nil && *s.ServedRevSeq == rev:
		return fmt.Sprintf("approved rev %d; a serve now would give rev %d", rev, rev)
	case s.ServedRevSeq != nil:
		return fmt.Sprintf("approved rev %d; a serve now would still give rev %d", rev, *s.ServedRevSeq)
	case s.Refusal != nil:
		return fmt.Sprintf("approved rev %d; a serve now would be refused: %s", rev, *s.Refusal)
	default:
		return fmt.Sprintf("approved rev %d; serving state unread", rev)
	}
}

// readApprovalStates reads the compiled prompts' revision and approval state
// in nodeBatch pages, keyed by the ref asked for (and by id). An unavailable
// ref is simply absent.
func readApprovalStates(ctx context.Context, client graphql.Client, refs []string) (map[string]*approvalBatchNode, error) {
	out := map[string]*approvalBatchNode{}
	queue := append([]string(nil), refs...)
	for len(queue) > 0 {
		n := min(api.NodeBatchCap, len(queue))
		chunk := queue[:n]
		queue = queue[n:]
		r, err := gen.BootPromptApprovalBatch(ctx, client, chunk)
		if err != nil {
			return nil, api.MapError(err)
		}
		if r.NodeBatch == nil {
			return nil, exitcode.Newf(exitcode.Error, "the server returned no node batch")
		}
		res := r.NodeBatch
		for _, node := range res.Nodes {
			if node != nil {
				out[node.Urn] = node
			}
		}
		if res.Truncated {
			if len(res.Nodes) == 0 {
				return nil, exitcode.Newf(exitcode.Error, "nodeBatch truncated without returning any node (%d omitted)", len(res.Omitted))
			}
			queue = append(append([]string(nil), res.Omitted...), queue...)
		}
	}
	for _, node := range out {
		out[node.Id] = node
	}
	return out, nil
}

func renderApproveBatch(w io.Writer, ctx context.Context, f *cmdutil.Factory, scope appScope, dto approveBatchDTO) error {
	p := func(format string, args ...any) error {
		_, err := fmt.Fprintf(w, format, args...)
		return err
	}
	if err := p("app: %s (%s)\n", describeApp(ctx, f, scope.Ref), scope.Source); err != nil {
		return err
	}
	if !dto.Applied {
		if err := p("PREVIEW: %d to approve, %d already approved, %d tampered, %d skipped, %d unknown. Pass --apply to approve; nothing was approved.\n",
			len(dto.ToApprove), len(dto.AlreadyApproved), len(dto.Tampered), len(dto.Skipped), len(dto.Unknown)); err != nil {
			return err
		}
	}
	revOf := func(r *int) string {
		if r == nil {
			return ""
		}
		return fmt.Sprintf(" rev %d", *r)
	}
	refOf := func(r *string) string {
		if r == nil {
			return "-"
		}
		return *r
	}
	t := output.NewTable(w, "WORKER", "RESULT", "DETAIL")
	if !dto.Applied {
		for _, r := range dto.ToApprove {
			t.Row(output.SafeCell(r.Name), "to approve", output.SafeCell(refOf(r.CompiledNodeRef)+revOf(r.Revision)+": "+r.Reason))
		}
	}
	for _, r := range dto.Approved {
		t.Row(output.SafeCell(r.Name), "approved", output.SafeCell(serveCopy(r.Revision, r.Serve)))
	}
	for _, r := range dto.AlreadyApproved {
		t.Row(output.SafeCell(r.Name), "already approved", output.SafeCell(refOf(r.CompiledNodeRef)+revOf(r.Revision)))
	}
	for _, r := range dto.Tampered {
		t.Row(output.SafeCell(r.Name), "tampered", output.SafeCell(r.Reason))
	}
	for _, r := range dto.Skipped {
		t.Row(output.SafeCell(r.Name), "skipped", output.SafeCell(r.Reason))
	}
	for _, r := range dto.Failed {
		t.Row(output.SafeCell(r.Name), "failed", output.SafeCell(fmt.Sprintf("rev %d: %s: %s", r.Revision, r.Code, r.Message)))
	}
	for _, r := range dto.Unknown {
		label := "unknown"
		if r.Outcome != "" {
			label = "unknown (" + r.Outcome + ")"
		}
		t.Row(output.SafeCell(r.Name), label, output.SafeCell(r.Reason))
	}
	if err := t.Flush(); err != nil {
		return err
	}
	return p("note: %s.\n", dto.Authority)
}
