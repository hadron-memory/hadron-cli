package decision

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

// writeDecision renders one decision; verb (e.g. "✓ asked") prefixes the
// heading on a write and is empty on a read.
func writeDecision(f *cmdutil.Factory, d decisionDTO, verb string) error {
	return output.Write(f.IOStreams, f.JSON, d, func(w io.Writer) error {
		head := fmt.Sprintf("%s  %s", output.SafeCell(d.Code), output.SafeCell(d.Title))
		if verb != "" {
			head = verb + " " + head
		}
		var b strings.Builder
		fmt.Fprintln(&b, head)
		fmt.Fprintf(&b, "  state: %s  tier: %s  urgency: %s  revision: %d", d.State, d.Tier, d.Urgency, d.Revision)
		if d.Authorizes {
			b.WriteString("  authorizes")
		}
		if d.Archival {
			b.WriteString("  archival")
		}
		b.WriteString("\n")
		fmt.Fprintf(&b, "  urn: %s\n", output.SafeCell(d.URN))
		if d.PortalURL != nil {
			fmt.Fprintf(&b, "  URL: %s\n", output.SafeCell(*d.PortalURL))
		}
		fmt.Fprintf(&b, "  deciders: %s\n", actorNames(d.Deciders))
		if d.Context != nil && *d.Context != "" {
			fmt.Fprintf(&b, "  context: %s\n", output.SafeLine(*d.Context, 400))
		}
		if d.Withdrawn != nil {
			fmt.Fprintf(&b, "  withdrawn %s: %s\n", output.SafeCell(d.Withdrawn.At), output.SafeLine(d.Withdrawn.Reason, 400))
		}
		if d.Approval != nil {
			fmt.Fprintf(&b, "  approval: %s at revision %d (by a decider: %t)\n",
				output.SafeCell(d.Approval.Integrity), d.Approval.Revision, d.Approval.ByDecider)
		}
		for _, p := range d.Parts {
			fmt.Fprintf(&b, "  %s  %s\n", output.SafeCell(p.Citation), output.SafeLine(p.Question, 400))
			for _, o := range p.Options {
				mark := ""
				if p.Recommendation != nil && p.Recommendation.Key == o.Key {
					mark = "  (recommended)"
				}
				fmt.Fprintf(&b, "    - %s: %s%s\n", output.SafeCell(o.Key), output.SafeCell(o.Label), mark)
			}
			if p.Recommendation != nil && len(p.Options) == 0 {
				fmt.Fprintf(&b, "    recommended: %s\n", output.SafeCell(p.Recommendation.Key))
			}
			if p.NoRecommendationReason != nil {
				fmt.Fprintf(&b, "    no recommendation: %s\n", output.SafeLine(*p.NoRecommendationReason, 400))
			}
			if a := p.Answer; a != nil {
				ans := "free text"
				if a.Key != nil {
					ans = output.SafeCell(*a.Key)
				}
				if a.Text != nil && *a.Text != "" {
					ans += ": " + output.SafeLine(*a.Text, 400)
				}
				fmt.Fprintf(&b, "    answer: %s (%s, %s)\n", ans, a.Via, output.SafeCell(a.At))
			} else {
				b.WriteString("    answer: —\n")
			}
		}
		_, err := io.WriteString(w, b.String())
		return err
	})
}

// writeLog renders a decision log's settings; verb as in writeDecision.
func writeLog(f *cmdutil.Factory, l decisionLogDTO, verb string) error {
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
