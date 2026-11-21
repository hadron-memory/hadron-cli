package question

import (
	"fmt"
	"io"
	"strings"

	"github.com/hadron-memory/hadron-cli/internal/cmdutil"
	"github.com/hadron-memory/hadron-cli/internal/output"
)

// Human renderings. Every server-supplied string goes through SafeCell /
// SafeLine so a title or answer cannot inject terminal control sequences.

func actorName(a actorRefDTO) string {
	switch {
	case a.Handle != nil && *a.Handle != "":
		return "@" + *a.Handle
	case a.Name != nil && *a.Name != "":
		return *a.Name
	case a.URN != nil && *a.URN != "":
		return *a.URN
	}
	return a.ID
}

func actorNames(in []actorRefDTO) string {
	if len(in) == 0 {
		return "—"
	}
	names := make([]string, 0, len(in))
	for _, a := range in {
		names = append(names, output.SafeCell(actorName(a)))
	}
	return strings.Join(names, ", ")
}

// writeQuestion renders one question; verb (e.g. "✓ asked") prefixes the heading
// on a write and is empty on a read. head, when set, replaces the verb line with
// a command-specific first line printed before the question block.
func writeQuestion(f *cmdutil.Factory, q questionDTO, verb string) error {
	return output.Write(f.IOStreams, f.JSON, q, func(w io.Writer) error {
		return writeQuestionBlock(w, q, verb)
	})
}

func writeQuestionBlock(w io.Writer, q questionDTO, verb string) error {
	head := fmt.Sprintf("%s  %s", output.SafeCell(q.Code), output.SafeCell(q.Title))
	if verb != "" {
		head = verb + " " + head
	}
	var b strings.Builder
	fmt.Fprintln(&b, head)
	fmt.Fprintf(&b, "  state: %s  tier: %s  priority: %s  revision: %d", q.State, q.Tier, q.Priority, q.Revision)
	if q.Authorizes {
		b.WriteString("  authorizes")
	}
	b.WriteString("\n")
	fmt.Fprintf(&b, "  urn: %s\n", output.SafeCell(q.URN))
	if q.PortalURL != nil {
		fmt.Fprintf(&b, "  URL: %s\n", output.SafeCell(*q.PortalURL))
	}
	fmt.Fprintf(&b, "  deciders: %s\n", actorNames(q.Deciders))
	switch {
	case q.EffectiveAssignee != nil:
		fmt.Fprintf(&b, "  assignee: %s\n", output.SafeCell(actorName(*q.EffectiveAssignee)))
	case q.EffectiveAssigneeID != nil:
		fmt.Fprintf(&b, "  assignee: %s\n", output.SafeCell(*q.EffectiveAssigneeID))
	}
	if q.Background != nil && *q.Background != "" {
		fmt.Fprintf(&b, "  background: %s\n", output.SafeLine(*q.Background, 400))
	}
	if q.WhyNow != nil && *q.WhyNow != "" {
		fmt.Fprintf(&b, "  why now: %s\n", output.SafeLine(*q.WhyNow, 400))
	}
	if q.NeededBy != nil {
		fmt.Fprintf(&b, "  needed by: %s\n", output.SafeCell(*q.NeededBy))
		if q.DefaultAction != nil && *q.DefaultAction != "" {
			fmt.Fprintf(&b, "  if unanswered: %s\n", output.SafeLine(*q.DefaultAction, 400))
		}
	}
	if q.Withdrawn != nil {
		fmt.Fprintf(&b, "  withdrawn %s: %s\n", output.SafeCell(q.Withdrawn.At), output.SafeLine(q.Withdrawn.Reason, 400))
	}
	if q.Approval != nil {
		fmt.Fprintf(&b, "  approval: %s at revision %d (by a decider: %t)\n",
			output.SafeCell(q.Approval.Integrity), q.Approval.Revision, q.Approval.ByDecider)
	}
	for _, o := range q.Options {
		mark := ""
		if q.Recommendation != nil && q.Recommendation.Key == o.Key {
			mark = "  (recommended)"
		}
		fmt.Fprintf(&b, "  - %s: %s%s\n", output.SafeCell(o.Key), output.SafeCell(o.Label), mark)
		for _, t := range []struct {
			name string
			v    *string
		}{{"why choose", o.WhyChoose}, {"why not", o.WhyNot}, {"risks", o.Risks}} {
			if t.v != nil && *t.v != "" {
				fmt.Fprintf(&b, "      %s: %s\n", t.name, output.SafeLine(*t.v, 300))
			}
		}
	}
	if q.Recommendation != nil {
		if len(q.Options) == 0 {
			fmt.Fprintf(&b, "  recommended: %s\n", output.SafeCell(q.Recommendation.Key))
		}
		if q.Recommendation.Reason != nil && *q.Recommendation.Reason != "" {
			fmt.Fprintf(&b, "  recommendation reason: %s\n", output.SafeLine(*q.Recommendation.Reason, 400))
		}
	}
	if q.NoRecommendationReason != nil {
		fmt.Fprintf(&b, "  no recommendation: %s\n", output.SafeLine(*q.NoRecommendationReason, 400))
	}
	if a := q.Answer; a != nil {
		fmt.Fprintf(&b, "  answer: %s (%s, %s)\n", answerText(a), a.Via, output.SafeCell(a.At))
		if a.Reason != nil && *a.Reason != "" {
			fmt.Fprintf(&b, "    reason: %s\n", output.SafeLine(*a.Reason, 400))
		}
	} else {
		b.WriteString("  answer: —\n")
	}
	if earlier := len(q.Answers); earlier > 0 {
		n := earlier
		if q.Answer != nil {
			n--
		}
		if n > 0 {
			fmt.Fprintf(&b, "  earlier entries: %d\n", n)
		}
	}
	_, err := io.WriteString(w, b.String())
	return err
}

// answerText is how an answer reads on one line: its option key, free text, or both.
func answerText(a *answerDTO) string {
	text := "(text)"
	if a.Key != nil {
		text = output.SafeCell(*a.Key)
	}
	if a.Text != nil && *a.Text != "" {
		if a.Key != nil {
			text += ": " + output.SafeLine(*a.Text, 400)
		} else {
			text = output.SafeLine(*a.Text, 400)
		}
	}
	return text
}

// writeLog renders a question log's settings; verb as in writeQuestion.
func writeLog(f *cmdutil.Factory, l questionLogDTO, verb string) error {
	return output.Write(f.IOStreams, f.JSON, l, func(w io.Writer) error {
		head := fmt.Sprintf("%s  %s", output.SafeCell(l.Name), output.SafeCell(l.Loc))
		if verb != "" {
			head = verb + " " + head
		}
		var b strings.Builder
		fmt.Fprintln(&b, head)
		fmt.Fprintf(&b, "  prefix: %s  codes issued: %d  revision: %d\n", output.SafeCell(l.Prefix), l.LastCode, l.Revision)
		fmt.Fprintf(&b, "  urn: %s\n", output.SafeCell(l.URN))
		if l.PortalURL != nil {
			fmt.Fprintf(&b, "  URL: %s\n", output.SafeCell(*l.PortalURL))
		}
		fmt.Fprintf(&b, "  default deciders: %s\n", actorNames(l.DefaultDeciders))
		fmt.Fprintf(&b, "  eligible deciders: %s\n", actorNames(l.EligibleDeciders))
		for _, p := range l.Purges {
			fmt.Fprintf(&b, "  purged %s at %s: %s\n", output.SafeCell(p.Code), output.SafeCell(p.At), output.SafeLine(p.Reason, 400))
		}
		_, err := io.WriteString(w, b.String())
		return err
	})
}
