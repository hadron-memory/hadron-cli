package spec

import (
	"errors"
	"fmt"
	"io"
	"regexp"
	"strings"
	"time"

	"github.com/Khan/genqlient/graphql"
	"github.com/hadron-memory/hadron-cli/internal/api"
	"github.com/hadron-memory/hadron-cli/internal/api/gen"
	"github.com/hadron-memory/hadron-cli/internal/cmdutil"
	"github.com/hadron-memory/hadron-cli/internal/exitcode"
	"github.com/hadron-memory/hadron-cli/internal/output"
	"github.com/spf13/cobra"
)

var supersededWord = regexp.MustCompile(`(?i)\bsuperseded\b`)

var supersedeLead = regexp.MustCompile(`^SUPERSEDED \([0-9]{4}-[0-9]{2}-[0-9]{2}\) by ([^ ]+) \(`)

// A RESCINDED or WITHDRAWN spec has no successor (cor:spc:010:01), so
// supersede never rewrites it. The match is anchored at the start of the lead
// (a SUPERSEDED lead whose history cites RESCINDED is not a tombstone) or is
// the rescinded/withdrawn tag. Not isRescindedAbstract: spec rescind uses that.
var tombstoneLead = regexp.MustCompile(`(?i)^(RESCINDED|WITHDRAWN)\b`)

func isTombstoneSource(n *gen.GetNodeNode) bool {
	return tombstoneLead.MatchString(strings.TrimSpace(derefStr(n.Abstract))) || hasTag(n.Tags, "rescinded") || hasTag(n.Tags, "withdrawn")
}

// sourceTombstoneError is the early refusal: no write has been made.
func sourceTombstoneError(n *gen.GetNodeNode) error {
	if !isTombstoneSource(n) {
		return nil
	}
	return exitcode.Newf(exitcode.Conflict, "%s is a RESCINDED or WITHDRAWN spec, which has no successor (cor:spc:010:01); supersede does not rewrite it; nothing was written", n.Loc)
}

// tombstoneRaceError: the spec became a tombstone after the replacement or
// edge landed, so retirement is refused but the earlier writes stand.
type tombstoneRaceError struct{ loc string }

func (e *tombstoneRaceError) Error() string {
	return e.loc + " became a RESCINDED or WITHDRAWN tombstone"
}

func isTombstoneRace(err error) bool {
	var e *tombstoneRaceError
	return errors.As(err, &e)
}

// Only an anchored, machine-written lead is rerun evidence. A citation in the
// historical summary does not make a conflicting retirement a no-op.
func supersedeLeadState(old *gen.GetNodeNode, target string) (bool, error) {
	a := derefStr(old.Abstract)
	if !strings.HasPrefix(a, "SUPERSEDED (") {
		return false, nil
	}
	m := supersedeLead.FindStringSubmatch(a)
	if len(m) != 2 || m[1] != target {
		return false, exitcode.Newf(exitcode.Conflict, "%s already has a different or malformed supersession lead; retirement update not attempted", old.Loc)
	}
	return true, nil
}
func supersededAbstract(old *gen.GetNodeNode, target, name string, summary *string) (string, error) {
	if same, err := supersedeLeadState(old, target); err != nil {
		return "", err
	} else if same {
		return derefStr(old.Abstract), nil
	}
	history := derefStr(old.Abstract)
	if summary != nil {
		history = *summary
	}
	a := fmt.Sprintf("SUPERSEDED (%s) by %s (%s); do not apply. Historical: %s", time.Now().UTC().Format("2006-01-02"), target, name, history)
	if abstractLength(&a) > abstractHardMax {
		return "", exitcode.Newf(exitcode.Usage, "retirement lead plus history exceeds the %d UTF-16-unit abstract limit; pass a shorter --summary", abstractHardMax)
	}
	return a, nil
}

func readSupersedeTarget(cmd *cobra.Command, client graphql.Client, id string) (*gen.GetSpecSupersedeTargetNode, error) {
	r, err := gen.GetSpecSupersedeTarget(cmd.Context(), client, id)
	if err != nil {
		return nil, api.MapError(err)
	}
	if r == nil || r.Node == nil {
		return nil, exitcode.Newf(exitcode.NotFound, "successor %q is not readable", id)
	}
	if r.Node.Id != id {
		return nil, exitcode.Newf(exitcode.Conflict, "successor identity changed while reading it; nothing was written")
	}
	return r.Node, nil
}
func eligibleSupersedeTarget(old *gen.GetNodeNode, n *gen.GetSpecSupersedeTargetNode) error {
	switch {
	case n.Id == old.Id:
		return exitcode.Newf(exitcode.Usage, "a spec cannot supersede itself")
	case n.MemoryId != old.MemoryId:
		return exitcode.Newf(exitcode.Usage, "successor must be in the same memory as %s", old.Loc)
	case n.DeletedAt != nil:
		return exitcode.Newf(exitcode.NotFound, "successor is deleted")
	case !isSpec(n.Tags, n.Role):
		return exitcode.Newf(exitcode.Usage, "successor %s is not a spec", n.Loc)
	case n.IsPlaceholder:
		return exitcode.Newf(exitcode.Usage, "successor %s is a placeholder, not a written spec", n.Loc)
	case hasTag(n.Tags, supersededTag) || hasTag(n.Tags, "rescinded"):
		return exitcode.Newf(exitcode.Conflict, "successor %s is itself retired", n.Loc)
	}
	return nil
}

// Walk only the successor graph, not the corpus. Unreadable edges cannot be
// assumed acyclic. Every reachable superseded-by edge is checked, not just the
// first one, and a graph cycle already present is refused too.
func checkSupersedeCycle(cmd *cobra.Command, client graphql.Client, oldID string, target *gen.GetSpecSupersedeTargetNode) error {
	active, done := map[string]bool{}, map[string]bool{}
	var visit func(*gen.GetSpecSupersedeTargetNode) error
	visit = func(n *gen.GetSpecSupersedeTargetNode) error {
		if n.Id == oldID || active[n.Id] {
			return exitcode.Newf(exitcode.Conflict, "supersession would enter a cycle; nothing was written")
		}
		if done[n.Id] {
			return nil
		}
		active[n.Id] = true
		for _, e := range n.OutgoingEdges {
			if e == nil || edgeNameStr(e.Name) != supersededByLabel {
				continue
			}
			if e.Target == nil {
				return exitcode.Newf(exitcode.Conflict, "a successor link is unreadable, so a cycle cannot be excluded; nothing was written")
			}
			if e.Target.Id == oldID {
				return exitcode.Newf(exitcode.Conflict, "supersession would create a cycle; nothing was written")
			}
			next, err := readSupersedeTarget(cmd, client, e.Target.Id)
			if err != nil {
				return err
			}
			if err = visit(next); err != nil {
				return err
			}
		}
		delete(active, n.Id)
		done[n.Id] = true
		return nil
	}
	return visit(target)
}
func existingTargetState(old *gen.GetNodeNode, target *gen.GetSpecSupersedeTargetNode) (bool, error) {
	for _, s := range supersededBySuccessors(old) {
		if s.id != target.Id || s.otherMemory {
			return false, exitcode.Newf(exitcode.Conflict, "%s already names another or unreadable successor; nothing was written", old.Loc)
		}
	}
	same, err := supersedeLeadState(old, target.Loc)
	if err != nil || !same {
		return same, err
	}
	// A matching lead makes only the abstract a no-op. Missing edge/tag
	// facts still need the normal guarded completion path.
	if !hasTag(old.Tags, supersededTag) || len(supersededBySuccessors(old)) == 0 {
		return false, nil
	}
	return true, nil
}
func supersedeByExisting(cmd *cobra.Command, f *cmdutil.Factory, client graphql.Client, mem string, old *gen.GetNodeNode, by, reason string, summary *string, yes, dry bool) error {
	id, err := resolveSpecNode(cmd, client, mem, by)
	if err != nil {
		return err
	}
	target, err := readSupersedeTarget(cmd, client, id)
	if err != nil {
		return err
	}
	validate := func() (bool, error) {
		if err := sourceTombstoneError(old); err != nil {
			return false, err
		}
		if target.Loc != by {
			return false, exitcode.Newf(exitcode.Conflict, "successor citation changed while reading; nothing was written")
		}
		if err := eligibleSupersedeTarget(old, target); err != nil {
			return false, err
		}
		if err := checkSupersedeCycle(cmd, client, old.Id, target); err != nil {
			return false, err
		}
		return existingTargetState(old, target)
	}
	same, err := validate()
	if err != nil {
		return err
	}
	result := supersedeResultDTO{Old: old.Loc, New: target.Loc, MemoryID: mem, Name: target.Name, Tags: tagsOrEmpty(target.Tags), Edges: []supersedeEdgeDTO{{Label: supersededByLabel, Target: old.Loc + " → " + target.Loc, Status: edgeStatusPlanned}}, DryRun: dry, ExistingSuccessor: true, Retired: boolRef(false)}
	render := func(w io.Writer) error { return renderSupersede(w, result) }
	if same {
		result.Unchanged = true
		result.Retired = boolRef(hasTag(old.Tags, supersededTag))
		result.Edges[0].Status = edgeStatusCreated
		return output.Write(f.IOStreams, f.JSON, result, render)
	}
	if _, err = supersededAbstract(old, target.Loc, target.Name, summary); err != nil {
		return err
	}
	if dry {
		return output.Write(f.IOStreams, f.JSON, result, render)
	}
	if err = cmdutil.Confirm(f.IOStreams, yes, fmt.Sprintf("Retire %s as superseded by existing %s? No node is created.", old.Loc, target.Loc)); err != nil {
		return err
	}
	// Confirmation can wait arbitrarily long: re-read BOTH principals and graph
	// facts before the first write. This is not a server-side atomic fence.
	fresh, err := gen.GetNode(cmd.Context(), client, old.Id)
	if err != nil {
		return api.MapError(err)
	}
	if fresh == nil || fresh.Node == nil {
		return exitcode.Newf(exitcode.NotFound, "retired spec is no longer readable")
	}
	if fresh.Node.Id != old.Id || fresh.Node.MemoryId != old.MemoryId || fresh.Node.Loc != old.Loc || !isSpec(fresh.Node.Tags, fresh.Node.Role) {
		return exitcode.Newf(exitcode.Conflict, "retired spec identity or eligibility changed; nothing was written")
	}
	old = fresh.Node
	if err = withSupersedeRawBody(cmd, client, old); err != nil {
		return err
	}
	target, err = readSupersedeTarget(cmd, client, id)
	if err != nil {
		return err
	}
	same, err = validate()
	if err != nil {
		return err
	}
	result.Name = target.Name
	result.Tags = tagsOrEmpty(target.Tags)
	if same {
		result.Unchanged = true
		result.Retired = boolRef(true)
		result.Edges[0].Status = edgeStatusCreated
		return output.Write(f.IOStreams, f.JSON, result, render)
	}
	if _, err = supersededAbstract(old, target.Loc, target.Name, summary); err != nil {
		return err
	}
	landed := false
	for _, s := range supersededBySuccessors(old) {
		if s.id == id {
			landed = true
		}
	}
	if !landed {
		response, writeErr := gen.CreateEdge(cmd.Context(), client, old.Id, id, supersededByLabel, nil, nil, nil, nil, nil, nil)
		wrote := writeErr == nil && response != nil && response.CreateEdge != nil
		found, other, fresh, readErr := supersededByState(cmd, client, old.Id, id)
		if wrote || found {
			result.Edges[0].Status = edgeStatusCreated
		} else {
			result.Edges[0].Status = edgeStatusUnknown
		}
		if readErr != nil || !found || other != "" {
			_ = output.Write(f.IOStreams, f.JSON, result, render)
			code := exitcode.Error
			if other != "" {
				code = exitcode.Conflict
			}
			return exitcode.Newf(code, "supersession link could not be confirmed as the sole successor (write: %v; read: %v; other: %s); retirement was not attempted; inspect `hadron spec get %s -m %s` before retrying", api.MapError(writeErr), api.MapError(readErr), other, old.Loc, mem)
		}
		old = fresh
	}
	result.Edges[0].Status = edgeStatusCreated
	retired, err := retire(cmd, client, old, target.Loc, target.Name, reason, summary, false)
	result.Retired = retired
	if err != nil {
		_ = output.Write(f.IOStreams, f.JSON, result, render)
		return retireError(retired, err, old.Loc, target.Loc, mem)
	}
	maybeRegisterReminder(cmd, client, mem, f.IOStreams.ErrOut, old.Loc, target.Loc)
	return output.Write(f.IOStreams, f.JSON, result, render)
}

// Retiring writes the stored body, abstract and tags together. Check the raw
// snapshot identity and spec eligibility too, not merely the earlier compiled
// read, before adopting those fields for the write.
func withSupersedeRawBody(cmd *cobra.Command, client graphql.Client, old *gen.GetNodeNode) error {
	r, err := gen.GetSpecNodeRaw(cmd.Context(), client, old.Id)
	if err != nil {
		return api.MapError(err)
	}
	if r == nil || r.Node == nil {
		return exitcode.Newf(exitcode.NotFound, "spec is no longer readable before retirement")
	}
	n := r.Node
	if n.Id != old.Id || n.MemoryId != old.MemoryId || n.Loc != old.Loc || !isSpec(n.Tags, n.Role) {
		return exitcode.Newf(exitcode.Conflict, "spec identity or eligibility changed before retirement; update not attempted")
	}
	old.Content = n.Content
	old.Abstract = n.Abstract
	old.AbstractOriginHash = n.AbstractOriginHash
	old.Tags = tagsOrEmpty(n.Tags)
	old.Role = n.Role
	return nil
}

// New existing-successor mode uses the already-supported spec-edit CAS door.
// A partial edge may remain on conflict, but an intervening tag/body edit is
// never overwritten by the whole-tag/abstract update. There is no unguarded
// fallback on an older provider.
func withSupersedeGuardedBody(cmd *cobra.Command, client graphql.Client, old *gen.GetNodeNode) (int, error) {
	r, err := gen.GetSpecNodeForEdit(cmd.Context(), client, old.Id)
	if err != nil {
		return 0, api.MapError(err)
	}
	if r == nil || r.Node == nil {
		return 0, exitcode.Newf(exitcode.NotFound, "spec is no longer readable before retirement")
	}
	n := r.Node
	if n.Id != old.Id || n.MemoryId != old.MemoryId || n.Loc != old.Loc || !isSpec(n.Tags, n.Role) {
		return 0, exitcode.Newf(exitcode.Conflict, "spec identity or eligibility changed before retirement; update not attempted")
	}
	old.Content = n.Content
	old.Abstract = n.Abstract
	old.AbstractOriginHash = n.AbstractOriginHash
	old.Tags = tagsOrEmpty(n.Tags)
	old.Role = n.Role
	return n.Revision, nil
}
