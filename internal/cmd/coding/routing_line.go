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

// routingLine renders the bullet a new route contributes to the router's body.
func routingLine(symptom, loc, description string) string {
	d := strings.TrimSpace(description)
	if d != "" && !strings.ContainsRune(".!?", rune(d[len(d)-1])) {
		d += "."
	}
	return `- **"` + strings.TrimSpace(symptom) + `"** → [[` + loc + `]] — ` + d
}

// bodySection is one markdown section of the router's body: everything from a
// heading (or the start of the body) up to the next heading.
type bodySection struct {
	Heading  string // the heading text; "" for the preamble before the first heading
	InsertAt int    // line index a new bullet should be spliced in at
	HasList  bool   // whether the section already contains a top-level bullet list
}

// splitBodySections walks the body once, recording each section's heading and
// the line index a new bullet belongs at — after the last top-level bullet
// (and its indented continuation lines) when the section has a list, otherwise
// at the section's end with its trailing blank lines trimmed.
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
		if isTopLevelBullet(lines[i]) {
			last = i
		}
	}
	if last >= 0 {
		sec.HasList = true
		// A bullet may wrap onto indented continuation lines; the insertion
		// point is after the whole block, not after its first line.
		i := last + 1
		for i < end && isContinuation(lines[i]) {
			i++
		}
		sec.InsertAt = i
		return sec
	}
	i := end
	for i > start && strings.TrimSpace(lines[i-1]) == "" {
		i--
	}
	sec.InsertAt = i
	return sec
}

func isHeading(line string) bool {
	t := strings.TrimLeft(line, "#")
	return len(t) < len(line) && strings.HasPrefix(t, " ")
}

// isTopLevelBullet matches an unindented list item — the shape a routing line
// takes. An indented bullet is a continuation of the one above it.
func isTopLevelBullet(line string) bool {
	return strings.HasPrefix(line, "- ") || strings.HasPrefix(line, "* ")
}

// isContinuation reports whether a line belongs to the bullet above it: an
// indented line, blank or not. A blank line followed by an unindented line
// ends the block, so blanks are not consumed on their own.
func isContinuation(line string) bool {
	return line != "" && (line[0] == ' ' || line[0] == '\t') && strings.TrimSpace(line) != ""
}

// routingPlan is where a routing line lands and what the router's body becomes.
type routingPlan struct {
	Body    string // the spliced body; the input unchanged when Skipped
	Section string // the resolved heading; "" for the unheaded preamble
	Skipped bool   // the body already links the loc, so nothing was added
}

// planRoutingLine splices line into content and returns the new body.
//
// A body that already links the loc is returned unchanged and Skipped: the line
// was written by hand before the node existed, and a second identical bullet is
// noise, not a fix.
func planRoutingLine(content, section, line, loc string) (routingPlan, error) {
	if strings.Contains(content, "[["+loc+"]]") {
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
	if !target.HasList && at > 0 && strings.TrimSpace(lines[at-1]) != "" {
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
		if s.HasList {
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
