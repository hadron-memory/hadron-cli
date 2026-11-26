package spec

import (
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/Khan/genqlient/graphql"
	"github.com/spf13/cobra"

	"github.com/hadron-memory/hadron-cli/internal/api"
	"github.com/hadron-memory/hadron-cli/internal/api/gen"
	"github.com/hadron-memory/hadron-cli/internal/exitcode"
	"github.com/hadron-memory/hadron-cli/internal/output"
)

// The retire door (cli#1736, hadron-server#3429, cor:spc:010:02). `spec
// rescind` and both `spec supersede` modes retire through ONE retireSpec
// request. The server composes the banner and abstract, judges every refusal and
// writes in one transaction, so this file holds no text composition and no
// eligibility rule (cor:api:240:01): the command reads the revision, asks the
// door, and reports what the answer, or one current-state re-read, shows.

// The outcome of the retirement request, in --json.
const (
	retireOutcomePlanned = "planned" // --dry-run
	retireOutcomeRetired = "retired" // the door answered with the retired spec
	retireOutcomeRefused = "refused" // the server refused the request
	retireOutcomeUnknown = "unknown" // no usable answer
)

// edgeStatusRefused is the superseded-by edge of a refused retirement.
const edgeStatusRefused = "refused"

const manualReviewWording = "This spec needs manual review; the CLI makes no automatic repair."

const causeNotVisible = "refused, nothing written; the cause isn't visible to this read"

const serverComposes = "the server composes the banner and abstract when it applies this; no preview of that text exists"

// retireCurrentDTO is the spec as ONE re-read after a refused or unanswered
// request showed it. It is current state, never proof of what that request did.
type retireCurrentDTO struct {
	// Retired is null when the re-read could not see the spec.
	Retired *bool `json:"retired"`
	// LinkedTo lists the readable superseded-by targets: [] when the read shows
	// no such link, null when it cannot tell.
	LinkedTo  []string `json:"linkedTo"`
	Revision  *int     `json:"revision"`
	Statement string   `json:"statement"`
}

// retireFailureKind is how a retireSpec request that did not succeed ended.
type retireFailureKind int

const (
	retireConflict retireFailureKind = iota
	retireUnsupported
	retireTooLong
	retireRefused
	retireUnanswered
)

type retireFailure struct {
	kind retireFailureKind
	err  error // the transport or GraphQL error; nil for a malformed success
}

// sendRetire sends the one retirement request. It never resends.
func sendRetire(cmd *cobra.Command, client graphql.Client, in *gen.RetireSpecInput) (*gen.RetireSpecRetireSpecRetireSpecResult, *retireFailure) {
	resp, err := gen.RetireSpec(cmd.Context(), client, in)
	if err != nil {
		kind := retireUnanswered
		switch {
		case api.IsRetireSpecUnsupported(err):
			kind = retireUnsupported
		case !api.IsRefusedBeforeCommit(err):
		case api.HasErrorCode(err, "NODE_WRITE_CONFLICT"):
			kind = retireConflict
		case api.HasErrorCode(err, "NodeAbstractTooLongError"):
			kind = retireTooLong
		default:
			kind = retireRefused
		}
		return nil, &retireFailure{kind: kind, err: err}
	}
	if resp == nil || !usableRetireResult(resp.RetireSpec, in) {
		return nil, &retireFailure{kind: retireUnanswered}
	}
	return resp.RetireSpec, nil
}

// usableRetireResult reports an answer that shows the retirement that was asked
// for. Anything less is not rendered as success.
func usableRetireResult(r *gen.RetireSpecRetireSpecRetireSpecResult, in *gen.RetireSpecInput) bool {
	if r == nil || r.Node == nil || r.Node.Id != in.Ref || !hasTag(r.Node.Tags, supersededTag) {
		return false
	}
	if in.Form != gen.RetireSpecFormSupersede {
		return true
	}
	e := r.SuccessorEdge
	return e != nil && e.Target != nil && in.SuccessorRef != nil && e.Target.Id == *in.SuccessorRef
}

// retireReport is what a failed request is reported as: the current state, when
// it was read, and the error the command exits with.
type retireReport struct {
	outcome string
	current *retireCurrentDTO
	err     error
}

// reportRetireFailure turns a failure into the command's report. Only a conflict
// and an unanswered request re-read the spec, once. created is the successor
// create mode made before asking ("" otherwise); its id is named in every error,
// because it stays whatever the door answered.
func reportRetireFailure(cmd *cobra.Command, client graphql.Client, in *gen.RetireSpecInput, f *retireFailure, created string) retireReport {
	prefix := ""
	if created != "" {
		prefix = fmt.Sprintf("created the successor reported above (id %s), which stays; ", output.SafeCell(created))
	}
	wrap := func(code int, msg string) error {
		return api.WithAuthoringAttestationCause(exitcode.Newf(code, "%s%s", prefix, msg), f.err)
	}
	// A refusal the CLI adds nothing to keeps the server's own presentation.
	mapped := func() error {
		m := api.MapError(f.err)
		if created == "" {
			return m
		}
		return wrap(exitcode.FromError(m), fmt.Sprintf("the retirement was refused: %v", m))
	}
	switch f.kind {
	case retireConflict:
		state := readRetireState(cmd, client, in)
		cur := currentState(state)
		wrote := ""
		if created != "" {
			wrote = "this retirement request wrote nothing. "
		}
		return retireReport{retireOutcomeRefused, cur, wrap(exitcode.Conflict,
			wrote+conflictReading(state, in)+". "+cur.Statement)}
	case retireUnsupported:
		return retireReport{retireOutcomeRefused, nil, wrap(exitcode.Usage,
			"this server does not support retireSpec (hadron-server#3429), so a spec cannot be retired on it; there is no fallback to separate writes, and no spec was retired by this request")}
	case retireTooLong:
		x := api.ErrorExtensions(f.err, "NodeAbstractTooLongError")
		return retireReport{retireOutcomeRefused, nil, wrap(exitcode.Usage, fmt.Sprintf(
			"the server refused the retirement abstract it composed: %v characters, and the cap is %v; pass a shorter --summary or --reason", x["length"], x["max"]))}
	case retireRefused:
		return retireReport{retireOutcomeRefused, nil, mapped()}
	}
	cur := currentState(readRetireState(cmd, client, in))
	code := exitcode.Error
	cause := "the answer did not show the retirement"
	if f.err != nil {
		m := api.MapError(f.err)
		if exitcode.FromError(m) == exitcode.Unavailable {
			code, cause = exitcode.Unavailable, "no answer arrived"
		} else {
			cause = m.Error()
		}
	}
	return retireReport{retireOutcomeUnknown, cur, wrap(code, fmt.Sprintf(
		"the retirement request got no usable answer (%s), so its outcome is unknown; it was not resent. %s. Check `hadron spec get <citation> -m <memory>` before running it again",
		cause, cur.Statement))}
}

// readRetireState is the one re-read. A nil Spec means it could not see it.
func readRetireState(cmd *cobra.Command, client graphql.Client, in *gen.RetireSpecInput) *gen.GetSpecRetireStateResponse {
	successor, with := in.Ref, false
	if in.SuccessorRef != nil {
		successor, with = *in.SuccessorRef, true
	}
	// A partial answer still shows what it shows: the spec is used when present.
	resp, err := gen.GetSpecRetireState(cmd.Context(), client, in.Ref, successor, with)
	if resp == nil || (resp.Spec != nil && resp.Spec.Id != in.Ref) {
		return &gen.GetSpecRetireStateResponse{}
	}
	if err != nil && resp.Spec == nil {
		return &gen.GetSpecRetireStateResponse{}
	}
	return resp
}

// successorLinks reads the superseded-by* edges (the prefix, ruling 0213):
// the readable targets, and whether any target is hidden from this read.
func successorLinks(s *gen.SpecRetireState) (targets []string, hidden bool) {
	targets = []string{}
	for _, e := range s.OutgoingEdges {
		if e == nil || !strings.HasPrefix(edgeNameStr(e.Name), supersededByLabel) {
			continue
		}
		if e.Target == nil {
			hidden = true
			continue
		}
		label := output.SafeCell(e.Target.Loc)
		if e.Target.MemoryId != s.MemoryId {
			label += " (in memory " + output.SafeCell(e.Target.MemoryId) + ")"
		}
		targets = append(targets, label)
	}
	return targets, hidden
}

func currentState(state *gen.GetSpecRetireStateResponse) *retireCurrentDTO {
	if state.Spec == nil {
		return &retireCurrentDTO{Statement: "the current state is unknown (the spec could not be re-read)"}
	}
	s := &state.Spec.SpecRetireState
	retired, revision := hasTag(s.Tags, supersededTag), s.Revision
	cur := &retireCurrentDTO{Retired: &retired, Revision: &revision, Statement: "currently retired"}
	if !retired {
		cur.Statement = "currently not retired"
	}
	targets, hidden := successorLinks(s)
	switch {
	case len(targets) > 0:
		cur.LinkedTo = targets
		cur.Statement += "; currently linked to " + strings.Join(targets, ", ")
	case hidden:
		cur.Statement += "; the successor link is currently unknown to this read"
	default:
		cur.LinkedTo = targets
		cur.Statement += "; currently not linked to a successor"
	}
	return cur
}

// conflictReading words a NODE_WRITE_CONFLICT, whose one guarantee is that this
// request wrote nothing. The rest describes the spec as it is now, in the
// order the door judges it; it invents no edge the read cannot see.
func conflictReading(state *gen.GetSpecRetireStateResponse, in *gen.RetireSpecInput) string {
	if state.Spec == nil {
		return causeNotVisible
	}
	s := &state.Spec.SpecRetireState
	if hasTag(s.Tags, supersededTag) {
		return "refused: the spec is currently already retired (nothing written)"
	}
	targets, hidden := successorLinks(s)
	switch {
	case len(targets) > 0:
		return "refused, nothing written: the spec is currently linked to " + strings.Join(targets, ", ") + ", not retired. " + strings.TrimSuffix(manualReviewWording, ".")
	case hidden:
		return causeNotVisible
	case s.Revision != in.ExpectedRevision:
		return fmt.Sprintf("refused, nothing written: the spec changed since read (read at revision %d, currently revision %d); re-run to start from a fresh read", in.ExpectedRevision, s.Revision)
	}
	if in.SuccessorRef != nil && state.Successor != nil {
		n := &state.Successor.SpecRetireState
		links, hiddenLink := successorLinks(n)
		if n.Id == s.Id || n.MemoryId != s.MemoryId || hasTag(n.Tags, supersededTag) || len(links) > 0 || hiddenLink {
			return "refused, nothing written: the successor can't be used"
		}
	}
	return causeNotVisible
}

// fetchForRetire is the one pre-read: the node the citation names, read raw,
// with the revision the door is asked to match. It decides nothing about
// whether the spec may be retired; that is the door's answer.
func fetchForRetire(cmd *cobra.Command, client graphql.Client, memURN, loc string) (*gen.GetSpecNodeForEditNode, error) {
	loc, err := validateSpecLoc(loc)
	if err != nil {
		return nil, err
	}
	id, err := resolveSpecNode(cmd, client, memURN, loc)
	if err != nil {
		return nil, err
	}
	resp, err := gen.GetSpecNodeForEdit(cmd.Context(), client, id)
	if err != nil {
		if api.IsSchemaSkew(err) && strings.Contains(err.Error(), "revision") {
			return nil, exitcode.Newf(exitcode.Usage,
				"this server predates node revisions (hadron-server#1339) and the retireSpec door (hadron-server#3429), so a spec cannot be retired on it; nothing was written")
		}
		return nil, api.MapError(err)
	}
	if resp == nil || resp.Node == nil {
		return nil, exitcode.Newf(exitcode.NotFound, "spec not found (the citation given is not shown)")
	}
	if !isSpec(resp.Node.Tags, resp.Node.Role) {
		return nil, exitcode.Newf(exitcode.Usage, "the node (the citation given is not shown) is not a spec (no \"spec\" tag or spec role)")
	}
	return resp.Node, nil
}

// givenText is an optional wording flag: nil when omitted, refused when blank,
// so a script's empty variable never reads as "no reason" (and, in create mode,
// is refused before a successor exists).
func givenText(cmd *cobra.Command, flag, value string) (*string, error) {
	if !cmd.Flags().Changed(flag) {
		return nil, nil
	}
	v := strings.TrimSpace(value)
	if v == "" {
		return nil, exitcode.Newf(exitcode.Usage, "--%s is blank; give text, or omit the flag", flag)
	}
	return &v, nil
}

// dryRunNote is the line a dry run carries in both output forms.
const dryRunNote = "dry run: nothing was written; " + serverComposes + ". Applying ends the spec's approval."

// renderInputs prints a dry run: what would be sent, and nothing the server
// would compose.
func renderInputs(say func(string, ...any), form gen.RetireSpecForm, loc, id string, revision int, reason, summary *string) {
	say("  spec: %s (node %s) at revision %d\n  form: %s\n", loc, id, revision, form)
	if reason != nil {
		say("  reason: %s\n", output.SafeLine(*reason, 4000))
	}
	if summary != nil {
		say("  summary: %s\n", output.SafeLine(*summary, 4000))
	}
	say("%s\n", dryRunNote)
}

// sayer writes lines until the first failure, which it keeps.
func sayer(w io.Writer) (func(string, ...any), func() error) {
	var err error
	return func(format string, a ...any) {
		if err == nil {
			_, err = fmt.Fprintf(w, format, a...)
		}
	}, func() error { return err }
}

// registerCheck looks for a corpus register BEFORE the retirement, so nothing is
// read after a success, and returns what prints the reminder once it happened.
// The register node is what makes a corpus legacy (cli#1146). A failed lookup
// never fails the command.
func registerCheck(cmd *cobra.Command, client graphql.Client, memURN string, w io.Writer, oldLoc, newLoc string) func() {
	var text strings.Builder
	registerReminder(&text, oldLoc, newLoc)
	if text.Len() == 0 {
		return func() {}
	}
	_, err := resolveSpecNode(cmd, client, memURN, "register")
	var coded *exitcode.CodedError
	switch {
	case err == nil:
		return func() { _, _ = io.WriteString(w, text.String()) }
	case errors.As(err, &coded) && coded.Code == exitcode.NotFound:
		return func() {} // no register node: nothing to remind about
	}
	return func() {
		fmt.Fprintf(w, "note: could not check whether this corpus has a register (%s); if it does, update it. The supersede itself succeeded.\n", output.SafeCell(err.Error()))
	}
}
