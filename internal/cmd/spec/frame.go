package spec

import (
	"fmt"
	"regexp"
	"slices"
	"strings"

	"github.com/Khan/genqlient/graphql"
	"github.com/spf13/cobra"

	"github.com/hadron-memory/hadron-cli/internal/api"
	"github.com/hadron-memory/hadron-cli/internal/api/gen"
	"github.com/hadron-memory/hadron-cli/internal/cmdutil"
	"github.com/hadron-memory/hadron-cli/internal/exitcode"
)

const platformTemplates = "hrn:mem:hadronmemory.com:specs-engineer-system"

var frameH1 = regexp.MustCompile(`(?m)^# ([A-Za-z]+) Spec: (.+)\r?$`)
var frameHeading = regexp.MustCompile(`(?m)^## (.+?)\r?$`)
var frameLink = regexp.MustCompile(`\[\[([^\[\]\n|]+?)(?:\|([^\[\]\n]*))?\]\]`)
var frameSuffix = regexp.MustCompile(` \[[a-z0-9:]+\]$`)
var frameType = regexp.MustCompile(`^[a-z]+$`)

type frameTemplate struct {
	kind                                string
	sections, required, defining, below []string
	top, bottom                         string
	built                               bool
}

func frameSections(body string) []string {
	out := []string{}
	for _, m := range frameHeading.FindAllStringSubmatch(body, -1) {
		out = append(out, m[1])
	}
	return out
}

// Read the actual Markdown and optional-section declaration. No per-type
// section lists or caps are copied into the CLI. Unknown source formats fail.
func parseFrameTemplate(source string) (frameTemplate, error) {
	var out frameTemplate
	_, code, ok := strings.Cut(source, "```markdown\n")
	if !ok {
		return out, fmt.Errorf("template has no markdown block")
	}
	code, _, ok = strings.Cut(code, "```")
	if !ok {
		return out, fmt.Errorf("template markdown block is not closed")
	}
	h := frameH1.FindStringSubmatch(code)
	if h == nil {
		return out, fmt.Errorf("template has no typed Spec heading")
	}
	out.kind = h[1]
	out.sections = frameSections(code)
	_, optional, ok := strings.Cut(source, "**Optional sections:** ")
	if !ok {
		return out, fmt.Errorf("template has no optional-section declaration")
	}
	optional, _, ok = strings.Cut(optional, ". Every other section")
	if !ok {
		return out, fmt.Errorf("template optional-section declaration is unrecognized")
	}
	optionalSet := map[string]bool{}
	for _, s := range strings.Split(optional, ",") {
		optionalSet[strings.TrimSpace(s)] = true
	}
	for s := range optionalSet {
		if !slices.Contains(out.sections, s) {
			return out, fmt.Errorf("unknown optional section %q", s)
		}
	}
	for _, s := range out.sections {
		if !optionalSet[s] {
			out.required = append(out.required, s)
		}
	}
	caps := regexp.MustCompile(`(?m)^<div class="spec-core"[^\n]*</div>\r?$`).FindAllStringIndex(code, -1)
	if len(caps) != 2 {
		return out, fmt.Errorf("template needs two defining-block caps")
	}
	out.top = code[caps[0][0]:caps[0][1]]
	out.bottom = code[caps[1][0]:caps[1][1]]
	out.defining = frameSections(code[caps[0][1]:caps[1][0]])
	divider := regexp.MustCompile(`(?m)^---\s*$`).FindAllStringIndex(code, -1)
	if len(divider) != 1 {
		return out, fmt.Errorf("template needs one horizontal rule")
	}
	out.below = frameSections(code[divider[0][1]:])
	out.built = strings.Contains(code, "- **Built:** ")
	if len(out.sections) == 0 || len(out.defining) == 0 {
		return out, fmt.Errorf("template has no section contract")
	}
	return out, nil
}

func frameBasics(n specNode) []lintFindingDTO {
	fs := []lintFindingDTO{}
	if hasTag(n.Tags, supersededTag) {
		return fs
	}
	h := frameH1.FindStringSubmatch(derefStr(n.Content))
	if h == nil {
		return fs
	}
	add := func(rule, msg string) {
		fs = append(fs, lintFindingDTO{Citation: n.Loc, Rule: rule, Severity: sevError, Message: msg})
	}
	want := h[1] + ": " + strings.TrimSpace(h[2]) + " [" + n.Loc + "]"
	if n.Name != want {
		add("frame-name", "name must be "+want)
	}
	if len(n.Tags) > 0 {
		add("frame-tags", "a spec in the authoring frame has no tags")
	}
	return fs
}

func lintFrame(n specNode, t frameTemplate) []lintFindingDTO {
	fs := []lintFindingDTO{}
	add := func(rule, msg string) {
		fs = append(fs, lintFindingDTO{Citation: n.Loc, Rule: rule, Severity: sevError, Message: msg})
	}
	body := strings.ReplaceAll(derefStr(n.Content), "\r\n", "\n")
	h := frameH1.FindStringSubmatch(body)
	if h == nil || h[1] != t.kind {
		add("frame-heading", "H1 must be '# "+t.kind+" Spec: <title>'")
	}
	if !regexp.MustCompile(`(?m)^- \*\*Status:\*\* .+`).MatchString(body) {
		add("frame-status", "missing '- **Status:**' line")
	}
	built := regexp.MustCompile(`(?m)^- \*\*Built:\*\* .+`).MatchString(body)
	if built != t.built {
		add("frame-built", fmt.Sprintf("Built-line presence must match the %s template", t.kind))
	}
	hs := frameSections(body)
	prev := -1
	seen := map[string]bool{}
	for _, h := range hs {
		idx := slices.Index(t.sections, h)
		if idx < 0 {
			add("frame-section", "unexpected section: "+h)
			continue
		}
		if idx < prev || seen[h] {
			add("frame-section-order", "duplicate or out-of-order section: "+h)
		}
		prev = idx
		seen[h] = true
	}
	for _, h := range t.required {
		if !seen[h] {
			add("frame-required", "missing section: "+h)
		}
	}
	top, bot := strings.Index(body, t.top), strings.Index(body, t.bottom)
	if strings.Count(body, t.top) != 1 || strings.Count(body, t.bottom) != 1 || top >= bot {
		add("frame-caps", "expected exactly one top and bottom cap, in template order")
	} else {
		inside := frameSections(body[top+len(t.top) : bot])
		want := []string{}
		for _, h := range hs {
			if slices.Contains(t.defining, h) {
				want = append(want, h)
			}
		}
		if !slices.Equal(inside, want) {
			add("frame-caps", "caps must enclose exactly the defining sections from the template")
		}
	}
	dividers := regexp.MustCompile(`(?m)^---\s*$`).FindAllStringIndex(body, -1)
	if len(dividers) != 1 {
		add("frame-divider", "expected exactly one horizontal rule")
	} else {
		for _, h := range frameSections(body[dividers[0][1]:]) {
			if !slices.Contains(t.below, h) {
				add("frame-divider", "section does not belong below the line: "+h)
			}
		}
	}
	if strings.TrimSpace(derefStr(n.Abstract)) == "" {
		add("frame-abstract", "a framed spec needs an abstract")
	} else if len([]rune(strings.TrimSpace(derefStr(n.Abstract)))) > abstractSoftMax {
		add("frame-abstract", "abstract exceeds 1,600 characters")
	}
	return fs
}

// Load only types in scope, once per invocation. An unreadable or malformed
// template is an error, never a clean frame verdict. This is a read-only check.
func lintFrames(cmd *cobra.Command, client graphql.Client, mem string, nodes []specNode) ([]lintFindingDTO, error) {
	fs := []lintFindingDTO{}
	templates := map[string]frameTemplate{}
	byLoc := map[string]specNode{}
	for _, n := range nodes {
		byLoc[n.Loc] = n
	}
	for _, n := range nodes {
		if n.Unavailable || hasTag(n.Tags, supersededTag) {
			continue
		}
		if n.Role == nil || !strings.HasPrefix(*n.Role, "spec.") {
			if frameH1.MatchString(derefStr(n.Content)) {
				fs = append(fs, lintFindingDTO{Citation: n.Loc, Rule: "frame-role", Severity: sevError, Message: "a framed spec needs its spec.<type> role"})
			}
			continue
		}
		if !frameH1.MatchString(derefStr(n.Content)) && n.UpdatedAt != "" && n.UpdatedAt < "2026-10-04" {
			continue
		}
		typ := strings.TrimPrefix(*n.Role, "spec.")
		if !frameType.MatchString(typ) {
			return nil, exitcode.Newf(exitcode.Usage, "invalid spec type %q", typ)
		}
		t, ok := templates[typ]
		if !ok {
			ref := cmdutil.NodeURN(platformTemplates, "authoring:"+typ+":template")
			got, err := gen.GetSpecNodeRaw(cmd.Context(), client, ref)
			if err != nil {
				return nil, api.MapError(err)
			}
			if got.Node == nil || got.Node.Content == nil {
				return nil, exitcode.Newf(exitcode.Unavailable, "authoring template %s is unavailable; frame checks did not complete", ref)
			}
			t, err = parseFrameTemplate(*got.Node.Content)
			if err != nil {
				return nil, exitcode.Newf(exitcode.Error, "cannot read authoring template %s: %v", ref, err)
			}
			if strings.ToLower(t.kind) != typ {
				return nil, exitcode.Newf(exitcode.Error, "authoring template %s describes %s", ref, t.kind)
			}
			templates[typ] = t
		}
		fs = append(fs, lintFrame(n, t)...)
		for _, u := range regexp.MustCompile(`\]\((https?://[^)\s]*/hrn:node:[^)\s]+)\)`).FindAllStringSubmatch(derefStr(n.Content)+"\n"+derefStr(n.Abstract), -1) {
			fs = append(fs, lintFindingDTO{Citation: n.Loc, Rule: "frame-link", Severity: sevError, Message: "host-specific portal URL; use a corpus wikilink: " + u[1]})
		}
		// Match speccheck's corpus links, including bare labels and qualified refs.
		for _, m := range frameLink.FindAllStringSubmatch(derefStr(n.Content)+"\n"+derefStr(n.Abstract), -1) {
			target := strings.TrimSpace(m[1])
			prefix := strings.TrimSuffix(cmdutil.NodeURN(mem, "cor"), "cor")
			if prefix != "" && strings.HasPrefix(target, prefix) {
				target = strings.TrimPrefix(target, prefix)
			}
			if target != "cor" && !strings.HasPrefix(target, "cor:") {
				continue
			}
			dest, ok := byLoc[target]
			if !ok {
				got, err := gen.GetSpecNodeRaw(cmd.Context(), client, cmdutil.NodeURN(mem, target))
				if err != nil {
					return nil, api.MapError(err)
				}
				if got.Node == nil {
					fs = append(fs, lintFindingDTO{Citation: n.Loc, Rule: "frame-link", Severity: sevError, Message: "unresolved link: " + target})
					continue
				}
				dest = specNode{Name: got.Node.Name}
				byLoc[target] = dest
			}
			want := frameSuffix.ReplaceAllString(dest.Name, "")
			if m[2] != want {
				fs = append(fs, lintFindingDTO{Citation: n.Loc, Rule: "frame-link", Severity: sevError, Message: fmt.Sprintf("label %q for %s must be %q", m[2], target, want)})
			}
		}
	}
	return fs, nil
}
