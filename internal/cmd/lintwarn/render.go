package lintwarn

import (
	"fmt"
	"io"

	"github.com/hadron-memory/hadron-cli/internal/output"
)

// UnavailableText is the one text for a null page: no conclusion is drawn, and
// it is never "no warnings" or "up to date".
const UnavailableText = "unavailable (no conclusion about warnings)"

// Line renders one warning as "  <KIND> <locator>: <server text>" (cli#2007 §5).
// The server's sentence is printed as it stands, with nothing added. compiled
// is true for lint of a compiled revision, where position is an immutable
// occurrence ordinal, not an offset. WORKER_LINK_MISSING is told apart by its
// workerField (both fields carry position 0). targetUrn, the pinned and newer
// revisions and an overridden loc print only when the server returned them.
func Line(w Warning, compiled bool) string {
	locator := fmt.Sprintf("position %d", w.Position)
	switch {
	case w.WorkerField != nil:
		locator = output.SafeCell(*w.WorkerField)
	case compiled:
		locator = fmt.Sprintf("occurrence #%d", w.Position)
	}
	s := fmt.Sprintf("  %s %s: %s", output.SafeCell(w.Kind), locator, output.SafeCell(w.Text))
	if w.PinnedRevision != nil && w.NewerRevision != nil {
		s += fmt.Sprintf(" (r%d → r%d)", *w.PinnedRevision, *w.NewerRevision)
	}
	if w.TargetURN != nil {
		s += " → " + output.SafeCell(*w.TargetURN)
	}
	if w.OverriddenNodeLoc != nil {
		s += " (overrides " + output.SafeCell(*w.OverriddenNodeLoc) + ")"
	}
	return s
}

// WriteSection prints "<label>: ..." for a drained page, or the unavailable
// line for nil. subject says what the warnings are about.
func WriteSection(out io.Writer, label, subject string, page *Page, compiled bool) error {
	if page == nil {
		_, err := fmt.Fprintf(out, "%s: %s\n", label, UnavailableText)
		return err
	}
	if page.Total == 0 {
		_, err := fmt.Fprintf(out, "%s: none hold for %s\n", label, subject)
		return err
	}
	if _, err := fmt.Fprintf(out, "%s: %d (%s)\n", label, page.Total, subject); err != nil {
		return err
	}
	for _, w := range page.Items {
		if _, err := fmt.Fprintln(out, Line(w, compiled)); err != nil {
			return err
		}
	}
	return nil
}

// PromptSubject names the compiled revision the warnings belong to: the one
// recorded for the served prompt, never the Agent's newest compile.
func PromptSubject(what string, provenanceRevSeq int) string {
	return fmt.Sprintf("%s, provenance r%d", what, provenanceRevSeq)
}
