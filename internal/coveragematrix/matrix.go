// Package coveragematrix reads the CLI coverage matrix, internal/api/coverage-matrix.txt
// (hadron-cli#1334, #66): every Query and Mutation field hadron-server
// exposes, mapped to the command that reaches it, the issue that tracks the
// gap, or an exclusion with its reason (cor:api:240:02).
//
// scripts/unboundops writes the file and checks it against the schema and the
// code; a test in internal/cmd checks that every named command and flag
// exists. This package is only the shared grammar, so both sides read the
// same lines the same way.
package coveragematrix

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// Path is where the matrix lives, relative to the repository root.
const Path = "internal/api/coverage-matrix.txt"

// Kind is a line's disposition.
type Kind string

const (
	// Command: a command reaches it ("command agent ls").
	Command Kind = "command"
	// Flag: a flag sets a filter field ("flag agent list --owned-by-me"); a
	// field several flags set names each ("flag scope list --owner-org
	// --owner-app"), and a positional argument is named as the command's Use
	// line spells it ("flag user search [query]").
	Flag Kind = "flag"
	// Gap: not reached yet; the issue tracks it ("gap #1340").
	Gap Kind = "gap"
	// Excluded: deliberately not in the CLI ("excluded internal: <reason>").
	Excluded Kind = "excluded"
	// Unclassified is what the generator writes for a field nobody has
	// decided yet. The check refuses it.
	Unclassified Kind = "UNCLASSIFIED"
)

// ExclusionClasses are the only reasons cor:api:240:02 leaves room for. "Not
// built yet" and "rarely used" are not among them: those are gaps.
var ExclusionClasses = map[string]string{
	"internal":         "an internal or capability-tool plane, not a user surface",
	"host-protocol":    "a host-poller or service-to-service protocol",
	"deprecated":       "deprecated, with a named replacement",
	"not-a-capability": "not a user capability",
	"graphical":        "a graphical capability that does not work well in a terminal (the reason names what is graphical)",
}

// Sections, in file order. Query and Mutation list every root field;
// Arguments lists what a reached operation leaves out; Filters lists every
// field of a reached list query's filter input.
var Sections = []string{"Query", "Mutation", "Arguments", "Filters"}

// Entry is one line of the matrix.
type Entry struct {
	Section string
	Key     string // "agents", "Query.agents(filter.ownedByMe)"
	Kind    Kind
	Command string   // Command, Flag: the command path without "hadron"
	Flags   []string // Flag: "--owned-by-me", or a positional "[query]"
	Issue   int      // Gap
	Class   string   // Excluded
	Reason  string   // Excluded: why; Gap: an optional note
	Line    int
}

// Disposition renders the entry's right-hand side, the inverse of Parse.
func (e Entry) Disposition() string {
	switch e.Kind {
	case Command:
		return "command " + e.Command
	case Flag:
		return "flag " + e.Command + " " + strings.Join(e.Flags, " ")
	case Gap:
		if e.Reason != "" {
			return fmt.Sprintf("gap #%d: %s", e.Issue, e.Reason)
		}
		return fmt.Sprintf("gap #%d", e.Issue)
	case Excluded:
		return "excluded " + e.Class + ": " + e.Reason
	}
	return string(Unclassified)
}

var (
	sectionRe = regexp.MustCompile(`^\[([A-Za-z]+)\]`)
	gapRe     = regexp.MustCompile(`^#([1-9][0-9]*)(?::\s*(.*))?$`)
	flagRe    = regexp.MustCompile(`^(--[a-z0-9][a-z0-9-]*|\[[a-z0-9-]+\]|<[a-z0-9-]+>)$`)
	cmdRe     = regexp.MustCompile(`^[a-z0-9][a-z0-9-]*( [a-z0-9][a-z0-9-]*)*$`)
)

// Parse reads the matrix. A malformed line is an error naming its line: the
// grammar is strict so a typo cannot read as a disposition.
func Parse(src string) ([]Entry, error) {
	var out []Entry
	var errs []string
	section := ""
	for i, line := range strings.Split(src, "\n") {
		n := i + 1
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if m := sectionRe.FindStringSubmatch(line); m != nil {
			section = m[1]
			if !known(section) {
				errs = append(errs, fmt.Sprintf("line %d: unknown section [%s]", n, section))
			}
			continue
		}
		if section == "" {
			errs = append(errs, fmt.Sprintf("line %d: entry before any section", n))
			continue
		}
		e, err := parseEntry(line)
		if err != nil {
			errs = append(errs, fmt.Sprintf("line %d: %v", n, err))
			continue
		}
		e.Section, e.Line = section, n
		out = append(out, e)
	}
	if len(errs) > 0 {
		return nil, fmt.Errorf("%s: %s", Path, strings.Join(errs, "; "))
	}
	return out, nil
}

func known(section string) bool {
	for _, s := range Sections {
		if s == section {
			return true
		}
	}
	return false
}

func parseEntry(line string) (Entry, error) {
	key, rest, ok := strings.Cut(line, "  ")
	if !ok || key == "" || strings.ContainsAny(key, " \t") {
		return Entry{}, fmt.Errorf("want `<field>  <disposition>` (two spaces), got %q", line)
	}
	rest = strings.TrimSpace(rest)
	e := Entry{Key: key}
	word, arg, _ := strings.Cut(rest, " ")
	switch Kind(word) {
	case Command:
		if !cmdRe.MatchString(arg) {
			return e, fmt.Errorf("%s: `command` wants a command path such as `agent ls`, got %q", key, arg)
		}
		e.Kind, e.Command = Command, arg
	case Flag:
		toks := strings.Fields(arg)
		i := 0
		for i < len(toks) && !flagRe.MatchString(toks[i]) {
			i++
		}
		path := strings.Join(toks[:i], " ")
		e.Kind, e.Command, e.Flags = Flag, path, toks[i:]
		for _, fl := range e.Flags {
			if !flagRe.MatchString(fl) {
				return e, fmt.Errorf("%s: `flag` wants `<command path> --<flag> [--<flag>…]`, got %q", key, arg)
			}
		}
		if !cmdRe.MatchString(path) || len(e.Flags) == 0 {
			return e, fmt.Errorf("%s: `flag` wants `<command path> --<flag> [--<flag>…]`, got %q", key, arg)
		}
	case Gap:
		m := gapRe.FindStringSubmatch(arg)
		if m == nil {
			return e, fmt.Errorf("%s: `gap` wants an issue `#<n>`, got %q", key, arg)
		}
		e.Kind, e.Reason = Gap, m[2]
		e.Issue, _ = strconv.Atoi(m[1])
	case Excluded:
		class, reason, ok := strings.Cut(arg, ": ")
		if !ok || strings.TrimSpace(reason) == "" {
			return e, fmt.Errorf("%s: `excluded` wants `<class>: <reason>`, got %q", key, arg)
		}
		if _, ok := ExclusionClasses[class]; !ok {
			return e, fmt.Errorf("%s: %q is not an exclusion class (want one of %s)", key, class, classList())
		}
		e.Kind, e.Class, e.Reason = Excluded, class, strings.TrimSpace(reason)
	case Unclassified:
		e.Kind = Unclassified
	default:
		return e, fmt.Errorf("%s: unknown disposition %q (want command, flag, gap or excluded)", key, word)
	}
	return e, nil
}

func classList() string {
	return "internal, host-protocol, deprecated, not-a-capability, graphical"
}
