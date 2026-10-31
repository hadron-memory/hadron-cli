package team

// `hadron team worker compile` with no worker argument (cli#1463): compile the
// App's boot prompts in one run. The server decides staleness — the
// RECOMPILE_AVAILABLE lint on each compiled node (cor:agt:040:13) — and the CLI
// re-implements none of its include selection. Compile and approve stay
// separate (cor:agt:040:09, :18): the batch prints approve lines, never runs
// them. Each compile is sent once (cor:agt:040:12); a lost response is checked
// with one read, never retried.

import (
	"context"
	"fmt"
	"io"
	"sort"
	"strings"

	"github.com/Khan/genqlient/graphql"
	"github.com/hadron-memory/hadron-cli/internal/api"
	"github.com/hadron-memory/hadron-cli/internal/api/gen"
	"github.com/hadron-memory/hadron-cli/internal/cmd/bootcompile"
	"github.com/hadron-memory/hadron-cli/internal/cmdutil"
	"github.com/hadron-memory/hadron-cli/internal/exitcode"
	"github.com/hadron-memory/hadron-cli/internal/output"
	"github.com/spf13/cobra"
)

type batchWorkerRow = gen.WorkersBootPointersWorkersWorkersPageItemsWorker
type batchLintNode = gen.CompiledPromptLintBatchNodeBatchNodeBatchResultNodesNode

// reasonFirstCompile marks a worker with no compiled prompt. Most such workers
// have no source at all (the server refuses BOOT_PROMPT_NO_SOURCE and they are
// skipped); the CLI cannot tell which without re-deriving the server's source
// lookup, so the human output folds them into one line.
const reasonFirstCompile = "no compiled prompt yet: a first compile, skipped if the worker has no source"

// Outcomes of a lost compile response.
const (
	lostLanded    = "landed"
	lostNotLanded = "not-landed"
	lostUnknown   = "unknown"
)

type compileBatchTargetDTO struct {
	Name            string  `json:"name"`
	ID              string  `json:"id"`
	URN             string  `json:"urn"`
	CompiledNodeRef *string `json:"compiledNodeRef"`
	Reason          string  `json:"reason"`
}

type compileBatchCompiledDTO struct {
	Name            string `json:"name"`
	ID              string `json:"id"`
	URN             string `json:"urn"`
	CompiledNodeRef string `json:"compiledNodeRef"`
	Revision        int    `json:"revision"`
	ContentHash     string `json:"contentHash"`
	PointerSet      bool   `json:"pointerSet"`
	ApprovalIssued  bool   `json:"approvalIssued"`
	ApproveCommand  string `json:"approveCommand"`
}

type compileBatchRefusalDTO struct {
	Name     string `json:"name"`
	ID       string `json:"id"`
	URN      string `json:"urn"`
	Code     string `json:"code"`
	ExitCode int    `json:"exitCode"`
	Message  string `json:"message"`
}

type compileBatchUnknownDTO struct {
	Name            string  `json:"name"`
	ID              string  `json:"id"`
	URN             string  `json:"urn"`
	CompiledNodeRef *string `json:"compiledNodeRef"`
	Reason          string  `json:"reason"`
	// Outcome is set only for a lost compile response: landed, not-landed or
	// unknown. A worker the plan could not decide was never compiled: "".
	Outcome        string  `json:"outcome"`
	RevisionBefore *int    `json:"revisionBefore"`
	RevisionAfter  *int    `json:"revisionAfter"`
	ApproveCommand *string `json:"approveCommand"`
}

type compileBatchDTO struct {
	App       string                    `json:"app"`
	Applied   bool                      `json:"applied"`
	All       bool                      `json:"all"`
	ToCompile []compileBatchTargetDTO   `json:"toCompile"`
	Compiled  []compileBatchCompiledDTO `json:"compiled"`
	Unchanged []compileBatchTargetDTO   `json:"unchanged"`
	Skipped   []compileBatchRefusalDTO  `json:"skipped"`
	Failed    []compileBatchRefusalDTO  `json:"failed"`
	Unknown   []compileBatchUnknownDTO  `json:"unknown"`
}

// batchPlanItem is one selected worker with what the plan read about it.
type batchPlanItem struct {
	target   compileBatchTargetDTO
	baseline *int // the compiled node's revision before compiling; nil = none read
	deleted  bool // the pointer named a deleted node
}

func approveCommand(ref string, rev int) string {
	return fmt.Sprintf("hadron node approve %s --revision %d", ref, rev)
}

func runWorkerCompileBatch(cmd *cobra.Command, f *cmdutil.Factory, opts bootcompile.BatchOptions) error {
	ctx := cmd.Context()
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
	roster, err = filterOnly(roster, opts.Only, scope.Ref)
	if err != nil {
		return err
	}

	dto := compileBatchDTO{
		App: scope.Ref, Applied: opts.Apply, All: opts.All,
		ToCompile: []compileBatchTargetDTO{}, Compiled: []compileBatchCompiledDTO{},
		Unchanged: []compileBatchTargetDTO{}, Skipped: []compileBatchRefusalDTO{},
		Failed: []compileBatchRefusalDTO{}, Unknown: []compileBatchUnknownDTO{},
	}

	// The server's verdict for every live compiled prompt, in batched reads.
	refs := []string{}
	for _, w := range roster {
		if ref := strPtr(w.BootPromptRef); ref != "" && !isTrue(w.BootPromptTargetDeleted) {
			refs = append(refs, ref)
		}
	}
	nodes, err := readCompiledLint(ctx, client, refs)
	if err != nil {
		return err
	}

	plan := []batchPlanItem{}
	exit := exitcode.OK
	for _, w := range roster {
		t := compileBatchTargetDTO{Name: w.Name, ID: w.Id, URN: strPtr(w.Urn)}
		ref := strPtr(w.BootPromptRef)
		if ref != "" {
			t.CompiledNodeRef = &ref
		}
		item := batchPlanItem{target: t}
		switch node, ok := nodes[ref]; {
		case ref == "":
			t.Reason = reasonFirstCompile
		case isTrue(w.BootPromptTargetDeleted):
			item.deleted = true
			t.Reason = "the compiled prompt is deleted"
		case !ok:
			if !opts.All {
				dto.Unknown = append(dto.Unknown, compileBatchUnknownDTO{Name: t.Name, ID: t.ID, URN: t.URN, CompiledNodeRef: t.CompiledNodeRef,
					Reason: "the compiled prompt is unavailable to you; not compiled (--all compiles it)"})
				exit = max(exit, exitcode.Error)
				continue
			}
			t.Reason = "--all"
		default:
			rev := node.Revision
			item.baseline = &rev
			switch {
			case opts.All:
				t.Reason = "--all"
			case node.LintWarnings == nil:
				dto.Unknown = append(dto.Unknown, compileBatchUnknownDTO{Name: t.Name, ID: t.ID, URN: t.URN, CompiledNodeRef: t.CompiledNodeRef,
					Reason: "the server's staleness check is unavailable; not compiled (--all compiles it)", RevisionBefore: &rev})
				exit = max(exit, exitcode.Error)
				continue
			case recompileAvailable(node):
				t.Reason = "a source changed since the last compile (RECOMPILE_AVAILABLE)"
			default:
				t.Reason = "no source changed since the last compile"
				dto.Unchanged = append(dto.Unchanged, t)
				continue
			}
		}
		item.target = t
		dto.ToCompile = append(dto.ToCompile, t)
		plan = append(plan, item)
	}

	if opts.Apply {
		for _, item := range plan {
			exit = max(exit, compileOne(ctx, client, &dto, item, opts.SetPointer))
		}
	}

	if werr := output.Write(f.IOStreams, f.JSON, dto, func(w io.Writer) error {
		return renderCompileBatch(w, ctx, f, scope, dto)
	}); werr != nil {
		return werr
	}
	if exit != exitcode.OK {
		return exitcode.Silent(exit)
	}
	return nil
}

// compileOne sends the compile once and files the worker in its bucket,
// returning the exit code it contributes.
func compileOne(ctx context.Context, client graphql.Client, dto *compileBatchDTO, item batchPlanItem, setPointer *bool) int {
	t := item.target
	r, err := gen.CompileWorkerBootPrompt(ctx, client, t.ID, setPointer)
	if err == nil && (r.CompileWorkerBootPrompt == nil || r.CompileWorkerBootPrompt.Node == nil || r.CompileWorkerBootPrompt.Node.Urn == "") {
		dto.Unknown = append(dto.Unknown, compileBatchUnknownDTO{Name: t.Name, ID: t.ID, URN: t.URN, CompiledNodeRef: t.CompiledNodeRef,
			Reason: "the server returned no compiled prompt; check with node get before compiling again", Outcome: lostUnknown, RevisionBefore: item.baseline})
		return exitcode.Error
	}
	if err == nil {
		res := r.CompileWorkerBootPrompt
		dto.Compiled = append(dto.Compiled, compileBatchCompiledDTO{Name: t.Name, ID: t.ID, URN: t.URN,
			CompiledNodeRef: res.Node.Urn, Revision: res.RevNo, ContentHash: res.ContentHash,
			PointerSet: res.PointerSet, ApprovalIssued: res.ApprovalIssued, ApproveCommand: approveCommand(res.Node.Urn, res.RevNo)})
		return exitcode.OK
	}
	code := api.ServerCodeOf(err)
	mapped := api.MapError(err)
	ec := exitcode.FromError(mapped)
	switch {
	case code == "BOOT_PROMPT_NO_SOURCE" || code == "BOOT_PROMPT_TARGET_RETIRED":
		dto.Skipped = append(dto.Skipped, compileBatchRefusalDTO{Name: t.Name, ID: t.ID, URN: t.URN, Code: code, ExitCode: ec, Message: mapped.Error()})
		return exitcode.OK
	case code == "" && ec == exitcode.Unavailable:
		dto.Unknown = append(dto.Unknown, checkLostCompile(ctx, client, item, setPointer))
		return exitcode.Unavailable
	default:
		if ec == exitcode.OK {
			ec = exitcode.Error
		}
		dto.Failed = append(dto.Failed, compileBatchRefusalDTO{Name: t.Name, ID: t.ID, URN: t.URN, Code: code, ExitCode: ec, Message: mapped.Error()})
		return ec
	}
}

// checkLostCompile makes the ONE read that decides whether a lost compile
// landed, against the baseline read before compiling. It never compiles again.
func checkLostCompile(ctx context.Context, client graphql.Client, item batchPlanItem, setPointer *bool) compileBatchUnknownDTO {
	t := item.target
	u := compileBatchUnknownDTO{Name: t.Name, ID: t.ID, URN: t.URN, CompiledNodeRef: t.CompiledNodeRef,
		Reason: "the compile response was lost; it was not sent again", Outcome: lostUnknown, RevisionBefore: item.baseline}
	switch {
	case t.CompiledNodeRef != nil:
		r, err := gen.CompiledNodeRevision(ctx, client, *t.CompiledNodeRef)
		if err != nil || r.Node == nil {
			u.Reason += "; the revision check failed too: check with node get before compiling again"
			return u
		}
		after := r.Node.Revision
		u.RevisionAfter = &after
		switch {
		case item.baseline == nil:
			u.Reason += "; no baseline revision to compare: check with node get before compiling again"
		case after > *item.baseline:
			u.Outcome = lostLanded
			cmd := approveCommand(*t.CompiledNodeRef, after)
			u.ApproveCommand = &cmd
			u.Reason += fmt.Sprintf("; a new revision landed (%d, was %d): read it before approving", after, *item.baseline)
		default:
			u.Outcome = lostNotLanded
			u.Reason += fmt.Sprintf("; no new revision (still %d)", after)
		}
	case setPointer != nil && !*setPointer:
		u.Reason += "; with --no-set-pointer and no earlier compiled prompt there is nothing to check: use team worker get"
	default:
		r, err := gen.WorkerBootPointer(ctx, client, t.ID)
		if err != nil || r.Worker == nil {
			u.Reason += "; the pointer check failed too: check with team worker get before compiling again"
			return u
		}
		if ref := strPtr(r.Worker.BootPromptRef); ref != "" {
			u.Outcome = lostLanded
			u.CompiledNodeRef = &ref
			u.Reason += "; the first compile landed (the worker's pointer is set): read the revision with node get before approving"
		} else {
			u.Outcome = lostNotLanded
			u.Reason += "; the worker's pointer is still unset"
		}
	}
	return u
}

func recompileAvailable(n *batchLintNode) bool {
	// RECOMPILE_AVAILABLE sits at position 0 and the page is ordered by
	// position, so it is on the first page whenever it holds.
	for _, w := range n.LintWarnings.Items {
		if w != nil && w.Kind == gen.LintKindRecompileAvailable {
			return true
		}
	}
	return false
}

// scanBootPointers pages the App's live workers with their pointers.
func scanBootPointers(ctx context.Context, client graphql.Client, appRef string) ([]batchWorkerRow, error) {
	rows := []batchWorkerRow{}
	limit := workerPageSize
	for offset := 0; ; {
		off := offset
		resp, err := gen.WorkersBootPointers(ctx, client, appRef, nil, &limit, &off)
		if err != nil {
			return nil, api.MapError(err)
		}
		if resp.Workers == nil {
			break
		}
		for _, w := range resp.Workers.Items {
			if w != nil && w.RetiredAt == nil {
				rows = append(rows, *w)
			}
		}
		offset += len(resp.Workers.Items)
		if len(resp.Workers.Items) < workerPageSize || offset >= resp.Workers.Total {
			break
		}
	}
	sort.SliceStable(rows, func(i, j int) bool { return strings.ToLower(rows[i].Name) < strings.ToLower(rows[j].Name) })
	return rows, nil
}

// filterOnly keeps the --only workers, refusing before any write when one is
// not a live worker in the App.
func filterOnly(rows []batchWorkerRow, only []string, appRef string) ([]batchWorkerRow, error) {
	if len(only) == 0 {
		return rows, nil
	}
	kept, missing := []batchWorkerRow{}, []string{}
	seen := map[string]bool{}
	for _, want := range only {
		found := false
		for _, w := range rows {
			if strings.EqualFold(w.Name, want) || w.Id == want || strings.EqualFold(strPtr(w.Urn), want) {
				found = true
				if !seen[w.Id] {
					seen[w.Id] = true
					kept = append(kept, w)
				}
			}
		}
		if !found {
			missing = append(missing, want)
		}
	}
	if len(missing) > 0 {
		return nil, exitcode.Newf(exitcode.NotFound, "not a live worker in %s: %s (nothing was compiled)", appRef, strings.Join(missing, ", "))
	}
	sort.SliceStable(kept, func(i, j int) bool { return strings.ToLower(kept[i].Name) < strings.ToLower(kept[j].Name) })
	return kept, nil
}

// readCompiledLint reads the compiled prompts in nodeBatch pages, keyed by the
// ref asked for. An unavailable ref is simply absent from the map.
func readCompiledLint(ctx context.Context, client graphql.Client, refs []string) (map[string]*batchLintNode, error) {
	out := map[string]*batchLintNode{}
	queue := append([]string(nil), refs...)
	for len(queue) > 0 {
		n := min(api.NodeBatchCap, len(queue))
		chunk := queue[:n]
		queue = queue[n:]
		r, err := gen.CompiledPromptLintBatch(ctx, client, chunk)
		if err != nil {
			return nil, api.MapError(err)
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
	// The pointer is the canonical URN, and so is node.urn; an id-shaped
	// pointer would not match, so map by id too.
	for _, node := range out {
		out[node.Id] = node
	}
	return out, nil
}

func renderCompileBatch(w io.Writer, ctx context.Context, f *cmdutil.Factory, scope appScope, dto compileBatchDTO) error {
	p := func(format string, args ...any) error {
		_, err := fmt.Fprintf(w, format, args...)
		return err
	}
	if err := p("app: %s (%s)\n", describeApp(ctx, f, scope.Ref), scope.Source); err != nil {
		return err
	}
	if !dto.Applied {
		first := 0
		for _, r := range dto.ToCompile {
			if r.Reason == reasonFirstCompile {
				first++
			}
		}
		if err := p("DRY RUN: %d to compile (%d changed, %d with no compiled prompt yet), %d unchanged, %d unknown. Pass --apply to compile; each compile writes a new revision.\n",
			len(dto.ToCompile), len(dto.ToCompile)-first, first, len(dto.Unchanged), len(dto.Unknown)); err != nil {
			return err
		}
	}
	// Workers with no compiled prompt and no-source skips are usually most of
	// an App's staff: one summary line each, not a row per worker.
	firstCompile, noSource := []string{}, []string{}
	t := output.NewTable(w, "WORKER", "RESULT", "DETAIL")
	if !dto.Applied {
		for _, r := range dto.ToCompile {
			if r.Reason == reasonFirstCompile {
				firstCompile = append(firstCompile, r.Name)
				continue
			}
			t.Row(output.SafeCell(r.Name), "to compile", output.SafeCell(r.Reason))
		}
	}
	for _, r := range dto.Compiled {
		t.Row(output.SafeCell(r.Name), "compiled", output.SafeCell(fmt.Sprintf("%s revision %d", r.CompiledNodeRef, r.Revision)))
	}
	for _, r := range dto.Unchanged {
		t.Row(output.SafeCell(r.Name), "unchanged", output.SafeCell(r.Reason))
	}
	for _, r := range dto.Skipped {
		if r.Code == "BOOT_PROMPT_NO_SOURCE" {
			noSource = append(noSource, r.Name)
			continue
		}
		t.Row(output.SafeCell(r.Name), "skipped", output.SafeCell(r.Code))
	}
	for _, r := range dto.Failed {
		t.Row(output.SafeCell(r.Name), "failed", output.SafeCell(r.Code+": "+r.Message))
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
	if len(firstCompile) > 0 {
		if err := p("to compile, %d with no compiled prompt yet (each is skipped if it has no source): %s\n", len(firstCompile), output.SafeCell(strings.Join(firstCompile, ", "))); err != nil {
			return err
		}
	}
	if len(noSource) > 0 {
		if err := p("skipped, %d with no source (BOOT_PROMPT_NO_SOURCE): %s\n", len(noSource), output.SafeCell(strings.Join(noSource, ", "))); err != nil {
			return err
		}
	}
	if !dto.Applied {
		return nil
	}
	lines := []string{}
	for _, r := range dto.Compiled {
		lines = append(lines, r.ApproveCommand+"   # "+r.Name)
	}
	for _, r := range dto.Unknown {
		if r.ApproveCommand != nil {
			lines = append(lines, *r.ApproveCommand+"   # "+r.Name+" (lost response; read it first)")
		}
	}
	if err := p("\n# Review each revision (node get <ref> --body-only), then approve it; nothing was approved.\n"); err != nil {
		return err
	}
	for _, l := range lines {
		if err := p("%s\n", output.SafeCell(l)); err != nil {
			return err
		}
	}
	return nil
}

func strPtr(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

func isTrue(b *bool) bool { return b != nil && *b }
