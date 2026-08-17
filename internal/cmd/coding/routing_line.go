package coding

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/hadron-memory/hadron-cli/internal/exitcode"
)

// The router's BODY, not just its edges.
//
// `preflight lint` reads the outgoing edges, but a human or an LLM reading the
// router reads its prose: every live preflight body carries one bullet per
// route — `- **"<symptom>"** → [[<loc>]] — <what it explains>`. A route wired
// as an edge with no such line is invisible to the way the node is actually
// consumed, which is why `preflight create` writes both.
//
// Where the line goes is NOT guessable in general, and the three live routers
// prove it:
//
//	hadronmemory.com::hadron-cli  one flat bullet list, no headings  → unambiguous
//	hadronmemory.com::dev         ~10 headed sections, most with     → needs --section
//	                              routing bullets
//	micromentor.org::mm-app       NO routing bullets at all — the    → needs --no-body-line
//	                              edge list IS the router
//
// So the planner resolves an insertion point only when there is exactly one
// candidate, and otherwise refuses with the list of headings to choose from.
// It runs against the body BEFORE anything is written, so an ambiguous router
// is a usage error rather than a half-finished write.

// routeArrow separates a routing bullet's trigger from its destination in every
// live router.
const routeArrow = "→"

// routingLine renders the bullet a new route contributes to the router's body,
// pointing at a target in the router's OWN memory.
func routingLine(symptom, loc, description string) string {
	return routingLineTo(symptom, wikiLink(loc), description)
}

// routingLineTo is the same bullet with the target reference already rendered,
// for a route whose target lives elsewhere.
func routingLineTo(symptom, target, description string) string {
	d := strings.TrimSpace(description)
	if !endsSentence(d) {
		d += "."
	}
	return routeBulletStem + strings.TrimSpace(symptom) + `"** ` + routeArrow + ` ` + target + ` — ` + d
}

// wikiLink is the same-memory reference form: a bare [[loc]], which resolves
// within the containing node's own memory (cor:urn:020:01).
func wikiLink(loc string) string { return "[[" + loc + "]]" }

// crossMemoryLink is the reference form for a target in ANOTHER memory. A bare
// wikilink would resolve against the ROUTER's memory and find nothing, so the
// live routers spell these as `<loc>` in `<memory>` — followed by a human,
// unambiguous to a reader, and not a link that silently goes nowhere.
func crossMemoryLink(loc, memory string) string {
	return "`" + loc + "` in `" + memory + "`"
}

// endsSentence reports whether d already carries terminal punctuation. Compared
// by suffix rather than by last BYTE: a description ending in a multi-byte
// terminator ("…") would otherwise look unpunctuated and gain a stray period.
func endsSentence(d string) bool {
	if d == "" {
		return true // nothing to punctuate
	}
	for _, end := range []string{".", "!", "?", "…", ":"} {
		if strings.HasSuffix(d, end) {
			return true
		}
	}
	return false
}

// bodySection is one markdown section of the router's body: everything from a
// heading (or the start of the body) up to the next heading.
type bodySection struct {
	Heading   string // the heading text; "" for the preamble before the first heading
	InsertAt  int    // line index a new bullet should be spliced in at
	HasRoutes bool   // whether the section already carries routing bullets
}

// splitBodySections walks the body once, recording each section's heading and
// the line index a new bullet belongs at — after the last ROUTING bullet (and
// its continuation lines) when the section has one, otherwise at the section's
// end with its trailing blank lines trimmed.
func splitBodySections(lines []string) []bodySection {
	var out []bodySection
	start := 0
	flush := func(end int) {
		out = append(out, sectionAt(lines, start, end, headingText(lines, start)))
	}
	for i, l := range lines {
		if i > start && isHeading(l) {
			flush(i)
			start = i
		}
	}
	flush(len(lines))
	return out
}

func headingText(lines []string, start int) string {
	if start < len(lines) && isHeading(lines[start]) {
		return strings.TrimSpace(strings.TrimLeft(lines[start], "#"))
	}
	return ""
}

func sectionAt(lines []string, start, end int, heading string) bodySection {
	sec := bodySection{Heading: heading}
	last := -1
	for i := start; i < end; i++ {
		if isRoutingBullet(lines[i]) {
			last = i
		}
	}
	if last >= 0 {
		sec.HasRoutes = true
		sec.InsertAt = endOfBullet(lines, last, end)
		return sec
	}
	// No routing list here. A new line goes at the END of the section, starting
	// its own list — never appended to whatever ordinary bullets the section
	// happens to contain (usage instructions, a not-yet-documented backlog),
	// which would file the route among unrelated items.
	i := end
	for i > start && strings.TrimSpace(lines[i-1]) == "" {
		i--
	}
	sec.InsertAt = i
	return sec
}

// endOfBullet returns the line index just past the bullet starting at `at`,
// including its continuation lines. A list item may run to several paragraphs,
// so a blank line ends the item only when what FOLLOWS it is unindented —
// otherwise the insertion point lands in the middle of the last entry.
func endOfBullet(lines []string, at, end int) int {
	i := at + 1
	for i < end {
		if isContinuation(lines[i]) {
			i++
			continue
		}
		if strings.TrimSpace(lines[i]) == "" {
			j := i
			for j < end && strings.TrimSpace(lines[j]) == "" {
				j++
			}
			if j < end && isContinuation(lines[j]) {
				i = j // the item continues past the blank
				continue
			}
		}
		break
	}
	return i
}

func isHeading(line string) bool {
	t := strings.TrimLeft(line, "#")
	return len(t) < len(line) && strings.HasPrefix(t, " ")
}

// routeBulletStem is how every routing bullet opens across the live routers:
// an unindented item whose trigger is a bold, quoted phrase.
const routeBulletStem = `- **"`

// isRoutingBullet matches a line shaped like the routing lines this command
// writes — `- **"<trigger>"** → …`.
//
// Membership is deliberately narrow. An earlier version accepted ANY unindented
// bullet, so a router with one ordinary Markdown list and no routing list at all
// (mm-app's "How to use this index", a not-yet-documented backlog) read as
// unambiguous and had a route silently filed among unrelated items. Requiring
// the routing shape means such a router yields no candidate and is refused with
// `--section` / `--no-body-line`, which is the posture the planner is for: a
// list of things shaped like what we are adding, or nothing.
func isRoutingBullet(line string) bool {
	return strings.HasPrefix(line, routeBulletStem) && strings.Contains(line, routeArrow)
}

// isContinuation reports whether a line belongs to the bullet above it: an
// indented, non-blank line. Blank lines are handled by endOfBullet, which looks
// past them to decide whether the item continues.
func isContinuation(line string) bool {
	return strings.TrimSpace(line) != "" && (line[0] == ' ' || line[0] == '\t')
}

// routingPlan is where a routing line lands and what the router's body becomes.
type routingPlan struct {
	Body    string // the spliced body; the input unchanged when Skipped
	Section string // the resolved heading; "" for the unheaded preamble
	Skipped bool   // the body already links the loc, so nothing was added
}

// planRoutingLine splices line into content and returns the new body. linkKey is
// the rendered target reference (wikiLink or crossMemoryLink) whose presence
// means the body already points at this node.
//
// A body that already carries the link is returned unchanged and Skipped: the
// line was written by hand, and a second identical bullet is noise, not a fix.
func planRoutingLine(content, section, line, linkKey string) (routingPlan, error) {
	if strings.Contains(content, linkKey) {
		return routingPlan{Body: content, Skipped: true}, nil
	}
	lines := strings.Split(content, "\n")
	secs := splitBodySections(lines)

	target, err := pickSection(secs, section)
	if err != nil {
		return routingPlan{}, err
	}

	at := target.InsertAt
	out := make([]string, 0, len(lines)+2)
	out = append(out, lines[:at]...)
	// A section with no list yet needs a blank line between the prose above and
	// the bullet; appending to an existing list must not introduce one, or the
	// list renders as two.
	if !target.HasRoutes && at > 0 && strings.TrimSpace(lines[at-1]) != "" {
		out = append(out, "")
	}
	out = append(out, line)
	out = append(out, lines[at:]...)
	return routingPlan{Body: strings.Join(out, "\n"), Section: target.Heading}, nil
}

// pickSection resolves --section, or infers the one unambiguous candidate.
func pickSection(secs []bodySection, section string) (bodySection, error) {
	if strings.TrimSpace(section) != "" {
		return matchSection(secs, strings.TrimSpace(section))
	}
	var candidates []bodySection
	for _, s := range secs {
		if s.HasRoutes {
			candidates = append(candidates, s)
		}
	}
	switch len(candidates) {
	case 1:
		return candidates[0], nil
	case 0:
		return bodySection{}, exitcode.Newf(exitcode.Usage,
			"the router's body has no routing list to extend — pass --section <heading> to start one there, or --no-body-line to wire the edges only%s",
			headingHint(secs))
	default:
		return bodySection{}, exitcode.Newf(exitcode.Usage,
			"the router's body has %d sections with routing lines — pass --section <heading> to say which one%s",
			len(candidates), headingHint(candidates))
	}
}

func matchSection(secs []bodySection, want string) (bodySection, error) {
	for _, s := range secs {
		if strings.EqualFold(s.Heading, want) {
			return s, nil
		}
	}
	var partial []bodySection
	for _, s := range secs {
		if s.Heading != "" && strings.Contains(strings.ToLower(s.Heading), strings.ToLower(want)) {
			partial = append(partial, s)
		}
	}
	switch len(partial) {
	case 1:
		return partial[0], nil
	case 0:
		return bodySection{}, exitcode.Newf(exitcode.Usage,
			"--section %q matches no heading in the router's body%s", want, headingHint(secs))
	default:
		return bodySection{}, exitcode.Newf(exitcode.Usage,
			"--section %q is ambiguous%s", want, headingHint(partial))
	}
}

// headingHint lists the headings a caller can choose between. The unnamed
// preamble is omitted — there is no string that would select it.
func headingHint(secs []bodySection) string {
	var names []string
	for _, s := range secs {
		if s.Heading != "" {
			names = append(names, strconv.Quote(s.Heading))
		}
	}
	if len(names) == 0 {
		return ""
	}
	return fmt.Sprintf(" (headings: %s)", strings.Join(names, ", "))
}
