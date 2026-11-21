package spec

import (
	"fmt"
	"io"
	"regexp"
	"strings"

	"github.com/Khan/genqlient/graphql"
	"github.com/spf13/cobra"

	"github.com/hadron-memory/hadron-cli/internal/api"
	"github.com/hadron-memory/hadron-cli/internal/api/gen"
	"github.com/hadron-memory/hadron-cli/internal/cmdutil"
	"github.com/hadron-memory/hadron-cli/internal/exitcode"
	"github.com/hadron-memory/hadron-cli/internal/output"
)

// allocatedInput is a `spec new` whose citation the SERVER picks: the two
// number-allocating modes (--new-feature; --feature without --rule/--flow,
// optionally --rule-after). Explicit-loc modes (--rule, --flow, positional
// <loc>, --contract, --new-module/--new-product/--new-path) never come here.
type allocatedInput struct {
	alloc                  deferredAlloc
	parentLoc, inheritLoc  string
	title                  string
	content, contentFile   string
	abstract, abstractFile string
	tags                   []string
	newFeature             bool
	noEdges, noContract    bool
	dryRun                 bool
	// contentSet is true when --content/--content-file was given at all, even
	// as an empty value: an explicit blank body is refused, not scaffolded.
	contentSet bool
	// memID is the id of the memory being written, read from its own nodes.
	memID string
}

// allocatedOutcomeDTO is the `--json` answer when the node was reserved but the
// fill did not (or may not have) landed. The number is real and stays
// reserved; the caller finishes it with the Recovery commands, never by
// rerunning `spec new`.
type allocatedOutcomeDTO struct {
	Citation  string `json:"citation"`
	MemoryID  string `json:"memoryId"`
	Allocated bool   `json:"allocated"`
	// FillStatus is "refused" when the server refused the fill, "unknown" when
	// its answer was lost or unusable (it may have been applied).
	FillStatus string `json:"fillStatus"`
	Reason     string `json:"reason"`
	// Recovery resumes authoring at the reserved citation: everything the
	// failed fill carried (title, abstract, body, tags, role, edges) and, for
	// a feature, its general-provisions contract.
	Recovery []string `json:"recovery"`
}

// contractOutcomeDTO is the `--json` answer when a new feature was reserved AND
// filled but its general-provisions contract (an explicit loc) was refused or
// lost. The feature is complete; only the contract needs inspecting.
type contractOutcomeDTO struct {
	Citation         string   `json:"citation"`
	MemoryID         string   `json:"memoryId"`
	Allocated        bool     `json:"allocated"`
	FillStatus       string   `json:"fillStatus"`
	ContractCitation string   `json:"contractCitation"`
	ContractStatus   string   `json:"contractStatus"` // "refused" | "unknown"
	Reason           string   `json:"reason"`
	Recovery         []string `json:"recovery"`
}

// pendingCopyPrefix matches a title that already starts with a "<parent>:NN — "
// prefix. The allocated number is not known before the call, so the copied-
// prefix refusal (#1148) is checked against the parent's shape and runs BEFORE
// any write: refusing after the call would strand a reserved placeholder.
func pendingCopyPrefix(parentLoc string) *regexp.Regexp {
	return regexp.MustCompile(`^\s*(` + regexp.QuoteMeta(parentLoc) + `:[0-9]+)` + regexp.QuoteMeta(specNameSeparator))
}

// runNewAllocated is `spec new` with the number picked by allocateSpecNode
// (cli#398, cor:spc:020:13):
//
//  1. every check that can fail on input runs first (title prefix, content and
//     abstract reads, edge targets), so a refusal leaves nothing reserved;
//  2. ONE allocateSpecNode call, never retried: it picks the number and creates
//     the unminted placeholder in one transaction;
//  3. ONE revision-guarded updateSpecNode fills that same node on the returned
//     id and revision (content, abstract, tags, role, data, seq, edges inline).
//     There is no second create, no createEdge and no client-side number.
//
// A refused or lost fill leaves a real reserved placeholder; it is reported,
// never repaired by allocating again.
func runNewAllocated(cmd *cobra.Command, f *cmdutil.Factory, client graphql.Client, memURN string, in allocatedInput) error {
	if m := pendingCopyPrefix(in.alloc.parent.Format()).FindStringSubmatch(in.title); m != nil {
		return refuseNewTitlePrefix(in.title, m[1])
	}
	// Read the caller's text once, before the write. Defaults that name the
	// citation are composed after the server answers.
	bodyGiven := in.content != "" || in.contentFile != ""
	userBody, err := resolveBodyOr(in.content, in.contentFile, f.IOStreams.In, func() string { return "" })
	if err != nil {
		return err
	}
	if (bodyGiven || in.contentSet) && strings.TrimSpace(userBody) == "" {
		// A blank body is accepted by the server but leaves the placeholder
		// unfilled (cor:dmo:120:07): refuse BEFORE reserving a number.
		return exitcode.Newf(exitcode.Usage, "the supplied body is empty or blank, which would leave the reserved spec an unfilled placeholder: give real content, or omit --content/--content-file to get the scaffold")
	}
	userAbs, err := cmdutil.ResolveTextInput("abstract", in.abstract, in.abstractFile, f.IOStreams.In)
	if err != nil {
		return err
	}

	var edgePlan []plannedEdgeDTO
	if !in.noEdges {
		if in.parentLoc != "" {
			edgePlan = append(edgePlan, plannedEdgeDTO{Label: in.title, Target: in.parentLoc})
		}
		if in.inheritLoc != "" {
			edgePlan = append(edgePlan, plannedEdgeDTO{Label: inheritEdgeLabel, Target: in.inheritLoc})
		}
	}

	if in.dryRun {
		if err := cmdutil.RequireWriteAccessForDryRun(cmd, client, memURN); err != nil {
			return err
		}
		// The preview is built against a symbolic citation, never a guessed
		// number: the server picks the real one at write time.
		ph := in.alloc.parent
		if in.newFeature {
			ph.Feature = "NNN"
		} else {
			ph.Rule = "NN"
		}
		previewAbs := userAbs
		if previewAbs == "" {
			previewAbs = tierAbstract(ph, in.title)
		}
		res := newResultDTO{
			MemoryID: memURN, Name: specName(ph, in.title), Tags: in.tags, Abstract: previewAbs,
			Edges: append([]plannedEdgeDTO{}, edgePlan...), DryRun: true,
			Citation: "", AllocatesUnder: in.alloc.parent.Format(), AfterFloor: in.alloc.after,
		}
		if bodyGiven {
			res.Content = userBody
		} else {
			res.Content = tierBody(ph, in.title)
		}
		if in.newFeature && !in.noContract {
			if cc, ok := ph.ChildContract(); ok {
				ctitle := in.title + " general provisions"
				cr := newResultDTO{
					Citation: cc.Format(), MemoryID: memURN, Name: specName(cc, ctitle), Tags: in.tags,
					Abstract: tierAbstract(cc, ctitle), Content: tierBody(cc, ctitle), Edges: []plannedEdgeDTO{}, DryRun: true,
				}
				if !in.noEdges {
					cr.Edges = append(cr.Edges, plannedEdgeDTO{Label: ctitle, Target: ph.Format()})
				}
				res.Also = append(res.Also, cr)
			}
		}
		return output.Write(f.IOStreams, f.JSON, res, func(w io.Writer) error { return renderNewResult(w, res) })
	}

	// Resolve every edge target BEFORE reserving anything, so an unresolvable
	// parent or inheritance target leaves no placeholder behind.
	var edges []*gen.NodeEdgeInput
	if !in.noEdges {
		edges, err = resolveSpecEdges(cmd, client, memURN, "the next "+in.alloc.parent.Format()+" number", edgePlan, nil)
		if err != nil {
			return err
		}
	}

	// The allocation must land in THIS memory. The scan that planned the
	// number already read the memory's nodes, so their memory id is known before
	// anything is reserved; without it the answer could not be vouched for.
	memID := in.memID
	if memID == "" {
		return exitcode.Newf(exitcode.Error, "cannot tell which memory %s is: no node of it was read, so the allocation could not be verified; nothing was reserved", memURN)
	}

	a, aerr := api.AllocateSpecNode(cmd.Context(), client, memURN, in.alloc.parent.Format(), in.alloc.after, in.title)
	if aerr != nil {
		if api.IsDefiniteRefusalAt(aerr, "allocateSpecNode") {
			return api.MapError(aerr) // refused: zero writes, nothing reserved
		}
		return allocationUnknown(memURN, in, api.MapError(aerr))
	}
	target, verr := validateAllocation(a, memID, in.alloc.parent, in.newFeature, in.alloc.after)
	if verr != nil {
		// An unusable answer to a call that may have committed: unknown, and no
		// fill is sent on a node this CLI cannot vouch for.
		return allocationUnknown(memURN, in, verr)
	}
	name := specName(target, in.title)
	abs := userAbs
	if abs == "" {
		abs = tierAbstract(target, in.title)
	}
	body := userBody
	if !bodyGiven {
		body = tierBody(target, in.title)
	}
	nodeType := "info"
	rev := a.Revision
	id := a.Id
	fill := gen.UpdateNodeInput{
		AuthoredWith:     cmdutil.AuthoredWith(cmd.Context()),
		Id:               &id,
		ExpectedRevision: &rev,
		Name:             &name,
		Tags:             in.tags,
		NodeType:         &nodeType,
		Abstract:         &abs,
		Content:          &body,
		Data:             specDataRaw(),
		Seq:              specSeq(target),
		Role:             specRole(),
		Edges:            edges,
	}
	if _, uerr := api.UpdateSpecNode(cmd.Context(), client, &fill); uerr != nil {
		state := "unknown"
		if api.IsDefiniteRefusalAt(uerr, "updateSpecNode") {
			state = "refused"
		}
		rc := recoveryContext{memURN: memURN, loc: a.Loc, title: in.title, tags: in.tags, edges: edgePlan, target: target, noEdges: in.noEdges}
		if in.newFeature && !in.noContract {
			if cc, ok := target.ChildContract(); ok {
				rc.contract = cc.Format()
			}
		}
		return reservedUnfilled(f, rc, state, api.MapError(uerr))
	}

	res := newResultDTO{
		Citation: target.Format(), MemoryID: memURN, Name: name, Tags: in.tags, Abstract: abs,
		Edges: []plannedEdgeDTO{}, DryRun: false,
	}
	res.Edges = append(res.Edges, edgePlan...)

	// A new feature's general-provisions contract is an EXPLICIT loc
	// (<feature>:00), which the allocator never hands out, so it stays on
	// createSpecNode exactly as before.
	if in.newFeature && !in.noContract {
		if cc, ok := target.ChildContract(); ok {
			ctitle := in.title + " general provisions"
			cbody := tierBody(cc, ctitle)
			cabs := tierAbstract(cc, ctitle)
			cname := specName(cc, ctitle)
			cIn := gen.CreateNodeInput{
				AuthoredWith: cmdutil.AuthoredWith(cmd.Context()),
				MemoryId:     memURN,
				Loc:          cc.Format(),
				Name:         cname,
				Tags:         in.tags,
				NodeType:     &nodeType,
				Abstract:     &cabs,
				Content:      &cbody,
				Data:         specDataRaw(),
				Seq:          specSeq(cc),
				Role:         specRole(),
			}
			cr := newResultDTO{Citation: cc.Format(), MemoryID: memURN, Name: cname, Tags: in.tags, Abstract: cabs, Edges: []plannedEdgeDTO{}}
			if !in.noEdges {
				cIn.Edges = []*gen.NodeEdgeInput{inlineSpecEdge(a.Id, ctitle)}
				cr.Edges = append(cr.Edges, plannedEdgeDTO{Label: ctitle, Target: target.Format()})
			}
			if _, cErr := api.CreateSpecNode(cmd.Context(), client, &cIn); cErr != nil {
				return contractFailed(f, memURN, target, cc, ctitle, in.tags, in.noEdges, cErr)
			}
			res.Also = append(res.Also, cr)
		}
	}
	return output.Write(f.IOStreams, f.JSON, res, func(w io.Writer) error { return renderNewResult(w, res) })
}

// recoveryContext is what the failed fill carried, so recovery can restore it.
type recoveryContext struct {
	memURN, loc, title string
	tags               []string
	edges              []plannedEdgeDTO
	target             Citation
	contract           string // the feature's contract loc still to create, or ""
	noEdges            bool   // --no-edges: the contract is recreated without its edge
}

// recoveryCommands resumes authoring at the reserved citation with the existing
// commands: every field the failed fill carried, then the contract if one was
// still to be created. None of them allocates a number.
func recoveryCommands(rc recoveryContext) []string {
	q := cmdutil.ShellQuote
	cmds := []string{fmt.Sprintf("hadron spec get %s -m %s", q(rc.loc), q(rc.memURN)),
		fmt.Sprintf("hadron spec edit %s -m %s --title %s --abstract-file <file> --content-file <file>", q(rc.loc), q(rc.memURN), q(rc.title))}
	node := fmt.Sprintf("hadron node update %s -m %s --role %s", q(rc.loc), q(rc.memURN), q(api.SpecNodeRole))
	for _, t := range rc.tags {
		node += " --tag " + q(t)
	}
	if n := specSeq(rc.target); n != nil {
		node += fmt.Sprintf(" --seq %d", *n)
	}
	node += " --data " + q(string(*specDataRaw()))
	cmds = append(cmds, node)
	for _, e := range rc.edges {
		cmds = append(cmds, fmt.Sprintf("hadron edge add -m %s --from %s --to %s --name %s", q(rc.memURN), q(rc.loc), q(e.Target), q(e.Label)))
	}
	if rc.contract != "" {
		cmds = append(cmds, contractCommand(rc.memURN, rc.contract, rc.title+" general provisions", rc.tags, rc.noEdges))
	}
	return cmds
}

// contractCommand is the one explicit-loc command that (re)creates a feature's
// general-provisions contract with the options the original run chose.
func contractCommand(memURN, loc, title string, tags []string, noEdges bool) string {
	q := cmdutil.ShellQuote
	c := fmt.Sprintf("hadron spec new -m %s %s --title %s", q(memURN), q(loc), q(title))
	for _, t := range tags {
		c += " --tag " + q(t)
	}
	if noEdges {
		c += " --no-edges"
	}
	return c
}

// reservedUnfilled reports a node the server reserved (a real, unminted
// placeholder at loc) whose one guarded fill was refused or lost. It never
// suggests rerunning `spec new`: that would allocate another number.
func reservedUnfilled(f *cmdutil.Factory, rc recoveryContext, status string, cause error) error {
	var what string
	exit := exitcode.Error
	if status == "unknown" {
		what = fmt.Sprintf("the fill's outcome is unknown (%v), so %s may or may not hold your content", cause, rc.loc)
		exit = exitcode.Unavailable
	} else {
		what = fmt.Sprintf("this fill was refused (%v), so it did not write your content; %s may still be empty, or another writer may have filled it since", cause, rc.loc)
	}
	recovery := recoveryCommands(rc)
	contractNote := ""
	if rc.contract != "" {
		contractNote = fmt.Sprintf(" The feature's contract %s was never attempted: create only that (the last command below) once the feature itself holds your content.", rc.contract)
	}
	msg := fmt.Sprintf("allocated %s but %s. The number is reserved and stays reserved. Read it first (`%s`): if it already holds your content, the fill committed and only what is missing remains; if it is still a placeholder, resume authoring at that citation, restoring what the failed fill carried (title, abstract, body, tags, role, edges):%s\n  %s\nThen, do NOT rerun `spec new`, which would allocate another number. An unfilled placeholder blocks the corpus's next mint (cor:dmo:120:07)",
		rc.loc, what, recovery[0], contractNote, strings.Join(recovery[1:], "\n  "))
	if f.JSON {
		if werr := output.Write(f.IOStreams, true, allocatedOutcomeDTO{Citation: rc.loc, MemoryID: rc.memURN, Allocated: true, FillStatus: status, Reason: cause.Error(), Recovery: recovery}, nil); werr != nil {
			msg += fmt.Sprintf(" (the --json record could not be written: %v)", werr)
		}
	}
	return exitcode.Newf(exit, "%s", msg)
}

// contractFailed reports a feature that is allocated and filled whose explicit
// general-provisions contract was refused or lost. Rerunning the original
// command would allocate ANOTHER feature, so recovery names the contract loc.
func contractFailed(f *cmdutil.Factory, memURN string, feature, contract Citation, ctitle string, tags []string, noEdges bool, cause error) error {
	status, exit := "unknown", exitcode.Unavailable
	if api.IsDefiniteRefusalAt(cause, "createSpecNode") {
		status, exit = "refused", exitcode.Error
	}
	recovery := []string{
		fmt.Sprintf("hadron spec get %s -m %s", cmdutil.ShellQuote(contract.Format()), cmdutil.ShellQuote(memURN)),
		contractCommand(memURN, contract.Format(), ctitle, tags, noEdges),
	}
	var werrNote string
	if f.JSON {
		werr := output.Write(f.IOStreams, true, contractOutcomeDTO{Citation: feature.Format(), MemoryID: memURN, Allocated: true, FillStatus: "filled",
			ContractCitation: contract.Format(), ContractStatus: status, Reason: api.MapError(cause).Error(), Recovery: recovery}, nil)
		if werr != nil {
			werrNote = fmt.Sprintf(" (the --json record could not be written: %v)", werr)
		}
	}
	return exitcode.Newf(exit, "created %s, but its contract %s was %s (%v). The feature is complete and stays: do NOT rerun the original `spec new`, which would allocate another feature. Check whether %s exists (`%s`); if it does not, create just the contract (`%s`)%s",
		feature.Format(), contract.Format(), status, api.MapError(cause), contract.Format(), recovery[0], recovery[1], werrNote)
}

// allocationUnknown reports an allocation whose outcome the CLI cannot vouch
// for (lost or unusable answer, or an error after the write): a placeholder may
// exist, nothing was retried and no fill was sent.
func allocationUnknown(memURN string, in allocatedInput, cause error) error {
	return exitcode.Newf(exitcode.Unavailable,
		"allocation outcome unknown (%v): a placeholder may have been reserved under %s. Nothing was retried. Check `hadron spec ls -m %s --prefix %s` for an unfilled placeholder named %q before rerunning; rerunning allocates another number",
		cause, in.alloc.parent.Format(), memURN, in.alloc.parent.Format(), in.title)
}

// validateAllocation checks the server's answer before anything is written on
// it: an id, a positive revision, a placeholder, and a numbered child of the
// requested parent at the expected tier. It returns the parsed citation.
func validateAllocation(a *api.AllocatedSpec, memID string, parent Citation, newFeature bool, after *int) (Citation, error) {
	if a.Id == "" {
		return Citation{}, fmt.Errorf("the server returned no node id")
	}
	if a.MemoryId != memID {
		return Citation{}, fmt.Errorf("the server returned %s in memory %q, not the requested memory", a.Loc, a.MemoryId)
	}
	if a.Revision <= 0 {
		return Citation{}, fmt.Errorf("the server returned revision %d for %s", a.Revision, a.Loc)
	}
	if !a.IsPlaceholder {
		return Citation{}, fmt.Errorf("the server returned %s as not a placeholder", a.Loc)
	}
	target, perr := ParseCitation(a.Loc)
	if perr != nil {
		return Citation{}, fmt.Errorf("the server returned a loc this CLI cannot parse: %v", perr)
	}
	if p, ok := target.Parent(); !ok || p != parent {
		return Citation{}, fmt.Errorf("the server returned %s, which is not a child of %s", a.Loc, parent.Format())
	}
	if newFeature && (target.Feature == "" || target.Rule != "" || target.Flow != "") {
		return Citation{}, fmt.Errorf("the server returned %s, which is not a feature", a.Loc)
	}
	if !newFeature && (target.Rule == "" || target.Flow != "") {
		return Citation{}, fmt.Errorf("the server returned %s, which is not a rule", a.Loc)
	}
	seq, ok := target.Seq()
	if !ok {
		return Citation{}, fmt.Errorf("the server returned %s, which has no sequence number", a.Loc)
	}
	if after != nil && seq <= *after {
		return Citation{}, fmt.Errorf("the server returned %s, at or below the requested floor %d", a.Loc, *after)
	}
	return target, nil
}
