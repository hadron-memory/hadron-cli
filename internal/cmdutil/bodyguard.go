package cmdutil

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/hadron-memory/hadron-cli/internal/exitcode"
)

// The write guards on a proposed node body (cli#873). Both exist because a
// body written back from the wrong read corrupts the node silently:
//
//   - A text-mode `node get` or `spec get` prints a metadata header above the
//     body. Saving that output with `--content-file` stores the header INTO the
//     body; three runnable task nodes were damaged that way in the field.
//   - A rendered read compiles Mustache, so it drops every {{…}} placeholder and
//     every {{=…=}} delimiter change. Saving it deletes them; one spec lost its
//     delimiter line, and its literal {{name}} text then rendered as empty.
//
// The header is refused (a body never legitimately starts with one), the
// dropped tags only warned about (removing a placeholder can be the edit).

// GetHeaderPrefix reports whether body begins with the header a text-mode
// `node get` or `spec get` prints: a title line, then `  loc: <loc>` (node
// get) or the node's bare `hrn:node:` URN (spec get, possibly after its
// PLACEHOLDER line). A markdown heading as the first line is never the spec
// header, which prints the plain name.
func GetHeaderPrefix(body string) bool {
	lines := strings.SplitN(body, "\n", 4)
	if len(lines) < 2 {
		return false
	}
	for i := range lines {
		lines[i] = strings.TrimSuffix(lines[i], "\r")
	}
	title := lines[0]
	if strings.TrimSpace(title) == "" || strings.TrimLeft(title, " \t") != title {
		return false
	}
	if loc, ok := strings.CutPrefix(lines[1], "  loc: "); ok && strings.TrimSpace(loc) != "" {
		return true
	}
	if strings.HasPrefix(title, "#") {
		return false
	}
	urnLine := lines[1]
	if urnLine == "PLACEHOLDER — reserved, not yet written" && len(lines) > 2 {
		urnLine = lines[2]
	}
	return strings.HasPrefix(urnLine, "hrn:node:") && !strings.ContainsAny(urnLine, " \t")
}

// RefuseGetHeaderPrefix is the one refusal for a header-prefixed body, so
// every write that guards says the same thing. forceFlag names the override.
func RefuseGetHeaderPrefix(body, forceFlag string) error {
	if !GetHeaderPrefix(body) {
		return nil
	}
	first, _, _ := strings.Cut(body, "\n")
	return exitcode.Newf(exitcode.Usage,
		"the new body starts with the metadata header a text-mode `node get` / `spec get` prints (%q, then its loc or URN line), so saving it would store the header IN the body — nothing was written. Read the body to edit with `hadron node get <ref> --body-only` (or `hadron spec get <citation> --body-only`), which prints the stored body and nothing else. If the header really is meant to be content, pass %s",
		strings.TrimSuffix(first, "\r"), forceFlag)
}

// templateTag matches one Mustache tag in the default delimiters: a variable
// ({{name}}), an unescaped one ({{{name}}}), a section or comment
// ({{#x}}, {{/x}}, {{!x}}), and a delimiter change ({{=<% %>=}}).
var templateTag = regexp.MustCompile(`\{\{\{?[^{}]*\}?\}\}`)

// DroppedTemplateTags lists, in first-seen order, the distinct {{…}} tags the
// stored body has and the proposed one does not.
func DroppedTemplateTags(stored, proposed string) []string {
	kept := map[string]bool{}
	for _, t := range templateTag.FindAllString(proposed, -1) {
		kept[t] = true
	}
	seen := map[string]bool{}
	dropped := []string{}
	for _, t := range templateTag.FindAllString(stored, -1) {
		if !kept[t] && !seen[t] {
			seen[t] = true
			dropped = append(dropped, t)
		}
	}
	return dropped
}

// droppedTagsShown caps how many dropped tags a warning names.
const droppedTagsShown = 8

// TemplateDropWarning is the warning for a proposed body that drops template
// tags the stored body has, or "" when it drops none.
func TemplateDropWarning(stored, proposed string) string {
	dropped := DroppedTemplateTags(stored, proposed)
	if len(dropped) == 0 {
		return ""
	}
	shown := dropped
	more := ""
	if len(shown) > droppedTagsShown {
		more = fmt.Sprintf(" and %d more", len(shown)-droppedTagsShown)
		shown = shown[:droppedTagsShown]
	}
	msg := fmt.Sprintf("the new body drops %d template tag(s) the stored body has: %s%s.", len(dropped), strings.Join(shown, " "), more)
	for _, t := range dropped {
		if strings.HasPrefix(t, "{{=") {
			msg += " A dropped {{=…=}} delimiter change makes later literal {{…}} text render as placeholders."
			break
		}
	}
	return msg + " If the edit started from a rendered read, re-read the stored body with `--body-only`."
}
