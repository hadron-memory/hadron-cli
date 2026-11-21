package spec

import (
	"fmt"
	"io"
	"regexp"

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
}

// allocatedOutcomeDTO is the `--json` answer when the node was reserved but the
// fill did not (or may not have) landed. The number is real and stays
// reserved; the caller finishes it with `spec edit`, never by rerunning.
type allocatedOutcomeDTO struct {
	Citation  string `json:"citation"`
	MemoryID  string `json:"memoryId"`
	Allocated bool   `json:"allocated"`
	// Filled is "false" when the server refused the fill, "unknown" when its
	// answer was lost or unusable (it may have been applied).
	Filled string `json:"filled"`
	Reason string `json:"reason"`
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
		res := newResultDTO{
			MemoryID: memURN, Tags: in.tags, Abstract: userAbs, Edges: append([]plannedEdgeDTO{}, edgePlan...), DryRun: true,
			Citation: "", AllocatesUnder: in.alloc.parent.Format(), AfterFloor: in.alloc.after,
		}
		if res.Edges == nil {
			res.Edges = []plannedEdgeDTO{}
		}
		// The scaffold is shown against a placeholder citation, never a guessed
		// number: the server picks the real one at write time.
		ph := in.alloc.parent
		if in.newFeature {
			ph.Feature = "NNN"
		} else {
			ph.Rule = "NN"
		}
		if bodyGiven {
			res.Content = userBody
		} else {
			res.Content = tierBody(ph, in.title)
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

	a, aerr := api.AllocateSpecNode(cmd.Context(), client, memURN, in.alloc.parent.Format(), in.alloc.after, in.title)
	if aerr != nil {
		if api.IsDefiniteRefusal(aerr) {
			return api.MapError(aerr) // refused: zero writes, nothing reserved
		}
		return exitcode.Newf(exitcode.Unavailable,
			"allocation outcome unknown (%v): a placeholder may have been reserved under %s. Nothing was retried. Check `hadron spec ls -m %s --prefix %s` for an unfilled placeholder named %q before rerunning; rerunning allocates another number",
			api.MapError(aerr), in.alloc.parent.Format(), memURN, in.alloc.parent.Format(), in.title)
	}

	target, perr := ParseCitation(a.Loc)
	if perr != nil {
		return reservedUnfilled(f, memURN, a.Loc, "unknown", fmt.Errorf("the server returned a loc this CLI cannot parse: %v", perr))
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
		if api.IsDefiniteRefusal(uerr) {
			state = "false"
		}
		return reservedUnfilled(f, memURN, a.Loc, state, api.MapError(uerr))
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
				return fmt.Errorf("created %s but its contract %s failed: %w", target.Format(), cc.Format(), api.MapError(cErr))
			}
			res.Also = append(res.Also, cr)
		}
	}
	return output.Write(f.IOStreams, f.JSON, res, func(w io.Writer) error { return renderNewResult(w, res) })
}

// reservedUnfilled reports a node the server reserved (a real, unminted
// placeholder at loc) whose one guarded fill was refused or lost. It never
// suggests rerunning `spec new`: that would allocate another number.
func reservedUnfilled(f *cmdutil.Factory, memURN, loc, filled string, cause error) error {
	var what string
	exit := exitcode.Error
	if filled == "unknown" {
		what = fmt.Sprintf("the fill's outcome is unknown (%v), so %s may or may not hold your content: read it with `hadron spec get %s -m %s` first", cause, loc, loc, memURN)
		exit = exitcode.Unavailable
	} else {
		what = fmt.Sprintf("the fill was refused (%v), so %s is an empty placeholder", cause, loc)
	}
	msg := fmt.Sprintf("allocated %s but %s. The number is reserved and stays reserved. Finish it with `hadron spec edit %s -m %s --content-file <file>`; do NOT rerun `spec new`, which would allocate another number. An unfilled placeholder blocks the corpus's next mint (cor:dmo:120:07)", loc, what, loc, memURN)
	if f.JSON {
		_ = output.Write(f.IOStreams, true, allocatedOutcomeDTO{Citation: loc, MemoryID: memURN, Allocated: true, Filled: filled, Reason: cause.Error()}, nil)
	}
	return exitcode.Newf(exit, "%s", msg)
}
