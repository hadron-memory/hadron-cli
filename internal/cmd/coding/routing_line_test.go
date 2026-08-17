package coding

import (
	"strings"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/exitcode"
)

// The three live router shapes, verbatim in structure. Each one resolves
// differently, which is the whole reason the planner refuses to guess.

// flatRouter is hadronmemory.com::hadron-cli: one unheaded bullet list with
// prose on both sides. Unambiguous.
const flatRouter = `# Preflight

Tech memory for the ` + "`hadron`" + ` CLI. Read this node, then follow the link that matches your task.

- **"How is the code organized?"** → [[architecture]] — layering and the command anatomy.
- **"What may I change without breaking users?"** → [[conventions:output-contract]] — the published contract.

House rule: when you uncover a new gotcha, add a node for it AND a routing line above.
`

// sectionedRouter is hadronmemory.com::dev: many headed sections, most of them
// carrying routing bullets. Needs --section.
const sectionedRouter = `# Preflight — start here

Read this at the start of every change.

## Database / Prisma

- **"Flaky P2002 from upsert"** → [[findings:prisma-upsert-not-race-safe]] — upsert is not race-safe.

## MCP and OAuth

- **"Every deploy kills active MCP connections"** → [[findings:mcp-stale-session-400-vs-404]] — unknown session must 404.

## When in doubt about the database

- **"Is this already documented?"** → run ` + "`hadron_find_nodes`" + ` with symptom keywords.
`

// edgeOnlyRouter is micromentor.org::mm-app: the edge list IS the router, and
// the body's bullets are instructions, not routes. Nothing to extend.
const edgeOnlyRouter = `# Preflight (mm-app)

This node is a **router**: each outgoing edge is labeled with a planned action.

## How to use this index

Match your intent to an edge, then follow it.
`

func TestPlanRoutingLineFlatRouter(t *testing.T) {
	line := routingLine("To fix a flaky test", "findings:flaky", "The countdown starts before the await")
	plan, err := planRoutingLine(flatRouter, "", line, wikiLink("findings:flaky"))
	if err != nil {
		t.Fatalf("an unambiguous router needs no --section: %v", err)
	}
	if plan.Skipped {
		t.Fatal("the line should have been written")
	}
	got := plan.Body
	lines := strings.Split(got, "\n")
	var at int
	for i, l := range lines {
		if strings.Contains(l, "[[findings:flaky]]") {
			at = i
		}
	}
	if at == 0 {
		t.Fatalf("the line is missing:\n%s", got)
	}
	if !strings.HasPrefix(lines[at-1], "- **\"What may I change") {
		t.Errorf("the line must land after the LAST bullet, got it after %q", lines[at-1])
	}
	if !strings.Contains(lines[at+1], "") || strings.HasPrefix(lines[at+1], "- ") {
		t.Errorf("the line must land before the trailing prose, got %q next", lines[at+1])
	}
	if strings.Contains(got, "\n\n- **\"To fix a flaky test\"") {
		t.Error("appending to an existing list must not insert a blank line — that splits it in two")
	}
}

func TestPlanRoutingLineSectionedRouter(t *testing.T) {
	line := routingLine("To do a thing", "findings:x", "Because")
	_, err := planRoutingLine(sectionedRouter, "", line, wikiLink("findings:x"))
	if err == nil {
		t.Fatal("a router with several routing sections must refuse to guess")
	}
	if code := exitcode.FromError(err); code != exitcode.Usage {
		t.Errorf("an ambiguous router is a usage error (2), got %d", code)
	}
	if !strings.Contains(err.Error(), "Database / Prisma") || !strings.Contains(err.Error(), "--section") {
		t.Errorf("the error must name the headings to choose between, got %q", err.Error())
	}

	plan, err := planRoutingLine(sectionedRouter, "mcp", line, wikiLink("findings:x"))
	if err != nil || plan.Skipped {
		t.Fatalf("--section should match a heading case-insensitively by substring: %v", err)
	}
	if plan.Section != "MCP and OAuth" {
		t.Errorf("the plan must report the heading it resolved to, got %q", plan.Section)
	}
	got := plan.Body
	lines := strings.Split(got, "\n")
	for i, l := range lines {
		if strings.Contains(l, "[[findings:x]]") {
			if !strings.Contains(lines[i-1], "mcp-stale-session") {
				t.Errorf("the line landed outside the named section, after %q", lines[i-1])
			}
			return
		}
	}
	t.Fatalf("the line is missing:\n%s", got)
}

func TestPlanRoutingLineEdgeOnlyRouter(t *testing.T) {
	line := routingLine("To do a thing", "findings:x", "Because")
	_, err := planRoutingLine(edgeOnlyRouter, "", line, wikiLink("findings:x"))
	if err == nil {
		t.Fatal("a router with no routing list must refuse rather than invent one")
	}
	if code := exitcode.FromError(err); code != exitcode.Usage {
		t.Errorf("want a usage error (2), got %d", code)
	}
	if !strings.Contains(err.Error(), "--no-body-line") {
		t.Errorf("the error must offer the edge-only escape hatch, got %q", err.Error())
	}

	// Naming a section explicitly starts a list there.
	plan, err := planRoutingLine(edgeOnlyRouter, "How to use this index", line, wikiLink("findings:x"))
	if err != nil || plan.Skipped {
		t.Fatalf("--section should start a list in a section that has none: %v", err)
	}
	got := plan.Body
	if !strings.Contains(got, "then follow it.\n\n- **\"To do a thing\"**") {
		t.Errorf("a new list needs a blank line after the prose above it, got:\n%s", got)
	}
}

// A section whose only list is ordinary prose bullets is NOT a routing list.
// Treating it as one filed a route among unrelated items and reported success
// — the exact silent-wrong outcome the planner exists to avoid (Codex on #376).
func TestPlanRoutingLineIgnoresOrdinaryLists(t *testing.T) {
	body := `# Preflight

This node is a **router**: each outgoing edge is labeled with a planned action.

## How to use this index

- **Match your intent to an edge.** The outgoing edges read as actions.
- **No matching edge?** Search Hadron with symptom keywords.
`
	line := routingLine("To do a thing", "findings:x", "Because")
	_, err := planRoutingLine(body, "", line, wikiLink("findings:x"))
	if err == nil {
		t.Fatal("one ordinary bullet list is not a routing list — the planner must refuse, not append to it")
	}
	if !strings.Contains(err.Error(), "--no-body-line") {
		t.Errorf("want the no-routing-list refusal, got %q", err.Error())
	}

	// Naming it explicitly starts a NEW list at the section's end rather than
	// joining the instructions.
	plan, err := planRoutingLine(body, "How to use this index", line, wikiLink("findings:x"))
	if err != nil || plan.Skipped {
		t.Fatalf("--section should still work: %v", err)
	}
	if !strings.Contains(plan.Body, "symptom keywords.\n\n- **\"To do a thing\"**") {
		t.Errorf("the route must start its own list after the ordinary one, got:\n%s", plan.Body)
	}
}

// A routing bullet may run to several paragraphs. A blank line inside one does
// not end it, or the new line lands mid-entry (Copilot on #376).
func TestPlanRoutingLineMultiParagraphBullet(t *testing.T) {
	body := "# R\n\n- **\"A\"** → [[a]] — first.\n" +
		"- **\"B\"** → [[b]] — second:\n  para one.\n\n  para two.\n\nTrailing prose.\n"
	plan, err := planRoutingLine(body, "", routingLine("C", "c", "third"), wikiLink("c"))
	if err != nil || plan.Skipped {
		t.Fatalf("planRoutingLine: %v", err)
	}
	if !strings.Contains(plan.Body, "  para two.\n- **\"C\"** → [[c]] — third.") {
		t.Errorf("the line must follow the WHOLE multi-paragraph entry, got:\n%s", plan.Body)
	}
}

// A line written by hand before the node existed is not a defect to duplicate.
func TestPlanRoutingLineAlreadyLinked(t *testing.T) {
	line := routingLine("To fix it", "conventions:output-contract", "d")
	plan, err := planRoutingLine(flatRouter, "", line, wikiLink("conventions:output-contract"))
	if err != nil {
		t.Fatalf("an existing link is not an error: %v", err)
	}
	if !plan.Skipped {
		t.Error("a loc the body already links must not get a second bullet")
	}
	if plan.Body != flatRouter {
		t.Error("the body must come back untouched")
	}
}

func TestPlanRoutingLineAmbiguousSection(t *testing.T) {
	// "database" is a substring of both "Database / Prisma" and "When in doubt
	// about the database".
	_, err := planRoutingLine(sectionedRouter, "database", routingLine("s", "findings:x", "d"), wikiLink("findings:x"))
	if err == nil {
		t.Fatal("a --section matching two headings must be rejected, not silently resolved")
	}
	if !strings.Contains(err.Error(), "ambiguous") {
		t.Errorf("unexpected error: %v", err)
	}
	if _, err := planRoutingLine(sectionedRouter, "nope", routingLine("s", "findings:x", "d"), wikiLink("findings:x")); err == nil {
		t.Fatal("a --section matching nothing must be rejected")
	}
}

// A bullet may wrap onto indented lines; the new one belongs after the whole
// block, not spliced into the middle of the last entry.
func TestPlanRoutingLineWrappedBullet(t *testing.T) {
	body := "# R\n\n- **\"A\"** → [[a]] — first.\n- **\"B\"** → [[b]] — second,\n  which continues here.\n\nTrailing prose.\n"
	plan, err := planRoutingLine(body, "", routingLine("C", "c", "third"), wikiLink("c"))
	if err != nil || plan.Skipped {
		t.Fatalf("planRoutingLine: %v", err)
	}
	got := plan.Body
	if !strings.Contains(got, "which continues here.\n- **\"C\"** → [[c]] — third.") {
		t.Errorf("the line must follow the wrapped bullet's continuation, got:\n%s", got)
	}
}

func TestRoutingLineFormat(t *testing.T) {
	got := routingLine("To fix a flaky test", "findings:flaky", "The countdown starts before the await")
	want := `- **"To fix a flaky test"** → [[findings:flaky]] — The countdown starts before the await.`
	if got != want {
		t.Errorf("routingLine = %q, want %q", got, want)
	}
	// An already-punctuated description is not double-punctuated.
	if got := routingLine("S", "x", "Ends already."); !strings.HasSuffix(got, "Ends already.") {
		t.Errorf("routingLine = %q, want a single terminator", got)
	}
	if got := routingLine("S", "x", "A question?"); !strings.HasSuffix(got, "A question?") {
		t.Errorf("routingLine = %q, want the question mark kept", got)
	}
	// A multi-byte terminator: indexing the last BYTE misses it and appends a
	// stray period (Copilot on #376).
	if got := routingLine("S", "x", "and so on…"); !strings.HasSuffix(got, "and so on…") {
		t.Errorf("routingLine = %q, want no period after a multi-byte terminator", got)
	}
}
