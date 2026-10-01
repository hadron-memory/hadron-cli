package node

import (
	"fmt"
	"io"

	"github.com/hadron-memory/hadron-cli/internal/api/gen"
)

// Server-authored stamps (hadron-server#1326, cli#752): who authored a content
// state, and the advisory validation of exactly that revision. Read and shown
// as the server reports them — never inferred, never upgraded to a claim of
// compliance.
//
// The --json convention, on `node get` and the revision reads alike:
//   - contentValidation is NEVER null on a server that stamps (it is
//     NodeContentValidationStatus! there). So contentValidation: null means
//     the server predates the stamps and could not say — and then authorship:
//     null carries no information either.
//   - On a stamping server, authorship: null is the server's own answer:
//     legacy content, or attribution not recorded.

// authorshipDTO is one content state's attribution.
type authorshipDTO struct {
	// Kind is HUMAN or TASK.
	Kind       string `json:"kind"`
	AuthoredAt string `json:"authoredAt"`
	// TaskRef is null for a HUMAN, and for a TASK whose memory you cannot
	// read independently — the server redacts it; Kind still says TASK.
	TaskRef      *string            `json:"taskRef"`
	TaskRevision *int               `json:"taskRevision"`
	Human        *revisionEditorDTO `json:"human"`
}

// contentValidationDTO is the advisory validation for one revision.
type contentValidationDTO struct {
	// State is PASS | FAIL | ERROR for a report made FOR THIS revision,
	// STALE when the latest report was for an older one, UNVALIDATED when
	// there is none.
	State        string               `json:"state"`
	LatestReport *validationReportDTO `json:"latestReport"`
}

type validationReportDTO struct {
	Outcome string `json:"outcome"`
	// NodeRevision is the revision the report was made for — on a STALE
	// state, an older one.
	NodeRevision           int     `json:"nodeRevision"`
	ReportedAt             string  `json:"reportedAt"`
	ValidationTaskRef      *string `json:"validationTaskRef"`
	ValidationTaskRevision *int    `json:"validationTaskRevision"`
}

func authorshipFrom(a *gen.NodeAuthorshipFields) *authorshipDTO {
	if a == nil {
		return nil
	}
	d := &authorshipDTO{Kind: string(a.Kind), AuthoredAt: a.AuthoredAt, TaskRef: a.TaskRef, TaskRevision: a.TaskRevision}
	if a.Human != nil {
		d.Human = &revisionEditorDTO{Handle: a.Human.Handle, URN: a.Human.Urn}
	}
	return d
}

func contentValidationFrom(v *gen.NodeContentValidationFields) *contentValidationDTO {
	if v == nil {
		return nil
	}
	d := &contentValidationDTO{State: string(v.State)}
	if r := v.LatestReport; r != nil {
		d.LatestReport = &validationReportDTO{
			Outcome: string(r.Outcome), NodeRevision: r.NodeRevision, ReportedAt: r.ReportedAt,
			ValidationTaskRef: r.ValidationTaskRef, ValidationTaskRevision: r.ValidationTaskRevision,
		}
	}
	return d
}

// renderStamps prints the two stamps as indented lines. `subject` names what
// they describe ("this revision" for a live node, "this snapshot" for history).
func renderStamps(w io.Writer, indent string, a *authorshipDTO, v *contentValidationDTO, subject string) {
	if v == nil {
		fmt.Fprintf(w, "%sstamps: unknown (the server predates authorship and validation stamps)\n", indent)
		return
	}
	switch {
	case a == nil:
		fmt.Fprintf(w, "%sauthorship: not recorded (legacy content, or unknown)\n", indent)
	case a.Kind == string(gen.NodeAuthorshipKindTask) && a.TaskRef == nil:
		fmt.Fprintf(w, "%sauthorship: a task you can't read (redacted by the server), %s\n", indent, a.AuthoredAt)
	case a.Kind == string(gen.NodeAuthorshipKindTask):
		fmt.Fprintf(w, "%sauthorship: task %s%s, %s\n", indent, *a.TaskRef, atRevision(a.TaskRevision), a.AuthoredAt)
	case a.Human != nil && a.Human.Handle != nil && *a.Human.Handle != "":
		fmt.Fprintf(w, "%sauthorship: human @%s, %s\n", indent, *a.Human.Handle, a.AuthoredAt)
	case a.Human != nil && a.Human.URN != nil && *a.Human.URN != "":
		fmt.Fprintf(w, "%sauthorship: human %s, %s\n", indent, *a.Human.URN, a.AuthoredAt)
	default:
		fmt.Fprintf(w, "%sauthorship: a human (no public identity), %s\n", indent, a.AuthoredAt)
	}
	r := v.LatestReport
	switch v.State {
	case string(gen.NodeContentValidationStateUnvalidated):
		fmt.Fprintf(w, "%svalidation: UNVALIDATED — no report for %s (advisory)\n", indent, subject)
	case string(gen.NodeContentValidationStateStale):
		if r != nil {
			fmt.Fprintf(w, "%svalidation: STALE — the latest report (%s) was for revision %d, not %s (advisory)\n", indent, r.Outcome, r.NodeRevision, subject)
		} else {
			fmt.Fprintf(w, "%svalidation: STALE — the latest report was for an earlier revision (advisory)\n", indent)
		}
	default:
		by := ""
		if r != nil {
			switch {
			case r.ValidationTaskRef != nil:
				by = fmt.Sprintf(", by task %s%s at %s", *r.ValidationTaskRef, atRevision(r.ValidationTaskRevision), r.ReportedAt)
			default:
				by = fmt.Sprintf(", reported %s", r.ReportedAt)
			}
		}
		fmt.Fprintf(w, "%svalidation: %s for %s%s (advisory)\n", indent, v.State, subject, by)
	}
}

func atRevision(rev *int) string {
	if rev == nil {
		return ""
	}
	return fmt.Sprintf(" rev %d", *rev)
}
