package spec

import (
	"fmt"
	"io"
	"os"
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
	// expectedMemoryID is the id of the memory being written, resolved directly
	// from the memory ref. It only vouches for the allocator's answer; requests
	// carry the memory URN, never this id.
	expectedMemoryID string
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
		edges, err = resolveSpecEdges(cmd, client, memURN, edgePlan, nil)
		if err != nil {
			return err
		}
	}

	// The allocation must land in THIS memory: the id resolved from the memory
	// ref vouches for the answer. Without it nothing is reserved.
	expectedMemoryID := in.expectedMemoryID
	if expectedMemoryID == "" {
		return exitcode.Newf(exitcode.Error, "cannot tell which memory was given (the ref is not shown), so the allocation could not be verified; nothing was reserved")
	}

	a, aerr := api.AllocateSpecNode(cmd.Context(), client, memURN, in.alloc.parent.Format(), in.alloc.after, in.title)
	if aerr != nil {
		if api.IsDefiniteRefusalAt(aerr, "allocateSpecNode") {
			return api.MapError(aerr) // refused: zero writes, nothing reserved
		}
		return allocationUnknown(api.MapError(aerr))
	}
	target, verr := validateAllocation(a, expectedMemoryID, in.alloc.parent, in.newFeature, in.alloc.after)
	if verr != nil {
		// An unusable answer to a call that may have committed: unknown, and no
		// fill is sent on a node this CLI cannot vouch for.
		return allocationUnknown(verr)
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
		absFile, bodyFile := preserveFillText(a.Loc, abs, body)
		rc := recoveryContext{abstractFile: absFile, contentFile: bodyFile, memURN: memURN, loc: a.Loc, title: in.title, tags: in.tags, edges: edgePlan, target: target, noEdges: in.noEdges}
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
				return contractFailed(f, memURN, a.Loc, cc, ctitle, in.tags, in.noEdges, cErr)
			}
			res.Also = append(res.Also, cr)
		}
	}
	return output.Write(f.IOStreams, f.JSON, res, func(w io.Writer) error { return renderNewResult(w, res) })
}

// recoveryContext is what the failed fill carried, so recovery can restore it.
type recoveryContext struct {
	memURN, loc, title        string
	tags                      []string
	edges                     []plannedEdgeDTO
	target                    Citation
	abstractFile, contentFile string // preserved copies of what the fill carried, or "" when none could be written
	contract                  string // the feature's contract loc still to create, or ""
	noEdges                   bool   // --no-edges: the contract is recreated without its edge
}

// recoveryCommands resumes authoring at the reserved citation with the existing
// commands: every field the failed fill carried, then the contract if one was
// still to be created. None of them allocates a number.
func recoveryCommands(rc recoveryContext) []string {
	q := cmdutil.ShellQuote
	cmds := []string{fmt.Sprintf("hadron spec get %s -m %s", q(rc.loc), q(rc.memURN)),
		specEditCommand(rc)}
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

// recoverySteps is recoveryCommands for the refusal text: the same commands with placeholders for
// what the operator typed (#2898 policy). loc is the server's allocated citation; the --json record
// keeps the commands in full.
func recoverySteps(loc, abstractFile, contentFile string, seq *int, tags, edges int, contract bool) []string {
	q := cmdutil.ShellQuote
	edit := fmt.Sprintf("hadron spec edit %s -m <memory> --title <title>", q(loc))
	if abstractFile != "" {
		edit += " --abstract-file " + q(abstractFile)
	}
	if contentFile != "" {
		edit += " --content-file " + q(contentFile)
	}
	node := fmt.Sprintf("hadron node update %s -m <memory> --role %s", q(loc), q(api.SpecNodeRole)) + strings.Repeat(" --tag <tag>", tags)
	if seq != nil {
		node += fmt.Sprintf(" --seq %d", *seq)
	}
	node += " --data " + q(string(*specDataRaw()))
	steps := []string{fmt.Sprintf("hadron spec get %s -m <memory>", q(loc)), edit, node}
	for i := 0; i < edges; i++ {
		steps = append(steps, fmt.Sprintf("hadron edge add -m <memory> --from %s --to <target> --name <label>", q(loc)))
	}
	if contract {
		steps = append(steps, fmt.Sprintf("hadron spec new -m <memory> %s --title <title>", q(loc+":00")))
	}
	return steps
}

// specEditCommand restores the fill's title and, when preserved copies exist,
// its abstract and body from those files (a real, pasteable command).
func specEditCommand(rc recoveryContext) string {
	q := cmdutil.ShellQuote
	c := fmt.Sprintf("hadron spec edit %s -m %s --title %s", q(rc.loc), q(rc.memURN), q(rc.title))
	if rc.abstractFile != "" {
		c += " --abstract-file " + q(rc.abstractFile)
	}
	if rc.contentFile != "" {
		c += " --content-file " + q(rc.contentFile)
	}
	return c
}

// preserveFillText saves the abstract and body the failed fill carried to
// private (0600) temp files, so recovery can name real paths and a caller whose
// text came from stdin does not lose it. The text itself is never printed. A
// file that cannot be written is "" and the recovery says so.
func preserveFillText(loc, abstract, body string) (absPath, bodyPath string) {
	write := func(kind, text string) string {
		fh, err := os.CreateTemp("", "hadron-spec-"+strings.ReplaceAll(loc, ":", "-")+"-"+kind+"-*.md")
		if err != nil {
			return ""
		}
		defer func() { _ = fh.Close() }()
		if _, err := fh.WriteString(text); err != nil {
			return ""
		}
		return fh.Name()
	}
	return write("abstract", abstract), write("content", body)
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
	if rc.abstractFile == "" || rc.contentFile == "" {
		what += "; your abstract/body could not be saved to a temp file, so re-supply them to the edit command (--abstract-file/--content-file)"
	} else {
		what += fmt.Sprintf("; your abstract and body are saved in %s and %s (private copies; delete them when done)", rc.abstractFile, rc.contentFile)
	}
	contractNote := ""
	if rc.contract != "" {
		contractNote = fmt.Sprintf(" The feature's contract %s was never attempted: create only that (the last command below) once the feature itself holds your content.", rc.loc+":00")
	}
	steps := recoverySteps(rc.loc, rc.abstractFile, rc.contentFile, specSeq(rc.target), len(rc.tags), len(rc.edges), rc.contract != "")
	msg := fmt.Sprintf("allocated %s but %s. The number is reserved and stays reserved. Read it first (`%s`): if it already holds your content, the fill committed and only what is missing remains; if it is still a placeholder, resume authoring at that citation, restoring what the failed fill carried (title, abstract, body, tags, role, edges; what you typed is not shown):%s\n  %s\nThen, do NOT rerun `spec new`, which would allocate another number. An unfilled placeholder blocks the corpus's next mint (cor:dmo:120:07)",
		rc.loc, what, steps[0], contractNote, strings.Join(steps[1:], "\n  "))
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
func contractFailed(f *cmdutil.Factory, memURN, featureLoc string, contract Citation, ctitle string, tags []string, noEdges bool, cause error) error {
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
		werr := output.Write(f.IOStreams, true, contractOutcomeDTO{Citation: featureLoc, MemoryID: memURN, Allocated: true, FillStatus: "filled",
			ContractCitation: contract.Format(), ContractStatus: status, Reason: api.MapError(cause).Error(), Recovery: recovery}, nil)
		if werr != nil {
			werrNote = fmt.Sprintf(" (the --json record could not be written: %v)", werr)
		}
	}
	return exitcode.Newf(exit, "created %s, but its contract was %s (%v). The feature is complete and stays: do NOT rerun the original `spec new`, which would allocate another feature. Check whether the contract exists (`hadron spec get %s -m <memory>`); if it does not, create just the contract (`hadron spec new -m <memory> %s --title <title>`, with the --tag and --no-edges of this run; what you typed is not shown)%s",
		featureLoc, status, api.MapError(cause), cmdutil.ShellQuote(featureLoc+":00"), cmdutil.ShellQuote(featureLoc+":00"), werrNote)
}

// allocationUnknown reports an allocation whose outcome the CLI cannot vouch
// for (lost or unusable answer, or an error after the write): a placeholder may
// exist, nothing was retried and no fill was sent.
func allocationUnknown(cause error) error {
	return exitcode.Newf(exitcode.Unavailable,
		"allocation outcome unknown (%v): a placeholder may have been reserved under the parent (the citation and title given are not shown). Nothing was retried. Check `hadron spec ls -m <memory> --prefix <parent>` for an unfilled placeholder with your title before rerunning; rerunning allocates another number",
		cause)
}

// validateAllocation checks the server's answer before anything is written on
// it: an id, a positive revision, a placeholder, and a numbered child of the
// requested parent at the expected tier. It returns the parsed citation.
func validateAllocation(a *api.AllocatedSpec, expectedMemoryID string, parent Citation, newFeature bool, after *int) (Citation, error) {
	if a.Id == "" {
		return Citation{}, fmt.Errorf("the server returned no node id")
	}
	if a.MemoryId != expectedMemoryID {
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
		return Citation{}, fmt.Errorf("the server returned %s, which is not a child of the parent asked for", a.Loc)
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
	// The provider's numbering contract: a rule is 01..99 (00 is the feature's
	// general-provisions contract, never allocated), a feature a multiple of ten
	// in 010..990.
	if newFeature && (seq < 10 || seq > 990 || seq%10 != 0) {
		return Citation{}, fmt.Errorf("the server returned %s, whose number %d is not a feature number (a multiple of 10 in 010..990)", a.Loc, seq)
	}
	if !newFeature && (seq < 1 || seq > 99) {
		return Citation{}, fmt.Errorf("the server returned %s, whose number %d is not a rule number (01..99)", a.Loc, seq)
	}
	if after != nil && seq <= *after {
		return Citation{}, fmt.Errorf("the server returned %s, at or below the requested floor %d", a.Loc, *after)
	}
	return target, nil
}
