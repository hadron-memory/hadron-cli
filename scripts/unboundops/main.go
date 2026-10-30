// Command unboundops writes and checks the CLI coverage matrix,
// internal/api/coveragematrix.txt (hadron-cli#397, #1334, #66).
//
// The gap it exists to catch: a server operation ships, the schema snapshot is
// refreshed, and nothing notices that no CLI command reaches it. That is not
// hypothetical — installAgentIntoApp had been in the schema since spec 023 when
// #389 was filed as "no surface attaches an existing Agent to an existing App",
// and the worklog contract sat duplicated in the client for months after
// recordTeamWork shipped (#396).
//
// The matrix maps every Query and Mutation field to the command that reaches
// it, the issue that tracks the gap, or an exclusion whose class
// cor:api:240:02 allows. It also lists, for the operations the CLI reaches,
// every argument and input field it leaves out, and every field of a list
// query's filter input with the flag that sets it.
//
// What is REACHED is derived from the code, never claimed: an operation
// document selects the field and production code calls that operation's
// generated function, passing the argument (see facts). The human-authored
// part is only the disposition, and the check refuses one that contradicts the
// facts — a gap on a field a command now reaches, a command on a field nothing
// calls. A new field the generator has never seen is written UNCLASSIFIED,
// which the check refuses until someone decides it. A field the server has
// deprecated, that the CLI does not reach, is excluded with the server's own
// reason unless a line already says otherwise.
//
//	go run ./scripts/unboundops            # print the matrix to stdout
//	go run ./scripts/unboundops -check     # fail if it is stale or contradicts the code
//
// That every `command` and `flag` names a real command and flag is checked by
// TestCoverageMatrixNamesRealCommands in internal/cmd, which has the tree.
package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	gql "github.com/vektah/gqlparser/v2/ast"

	"github.com/hadron-memory/hadron-cli/internal/coveragematrix"
)

const (
	schemaPath  = "schema/schema.graphql"
	queriesGlob = "internal/api/queries/*.graphql"
)

// keys lists every matrix key by section, from the facts.
func keys(f *facts) map[string][]item {
	out := map[string][]item{}
	for _, root := range []string{"Query", "Mutation"} {
		def := f.types[root]
		if def == nil {
			continue
		}
		for _, fld := range def.Fields {
			out[root] = append(out[root], item{
				key:        fld.Name,
				reached:    f.roots[root+"."+fld.Name],
				deprecated: deprecation(fld.Directives),
			})
		}
	}
	out["Arguments"], out["Filters"] = f.argumentItems()
	for _, s := range out {
		sort.Slice(s, func(i, j int) bool { return s[i].key < s[j].key })
	}
	return out
}

// build renders the matrix from the facts, carrying each existing
// disposition across, and lists every problem the check refuses.
func build(f *facts, existing []coveragematrix.Entry) (string, []string) {
	var problems []string
	prior := map[string]coveragematrix.Entry{}
	for _, e := range existing {
		id := e.Section + " " + e.Key
		if _, dup := prior[id]; dup {
			problems = append(problems, fmt.Sprintf("[%s] %s: listed twice", e.Section, e.Key))
		}
		prior[id] = e
	}

	all := keys(f)
	var b strings.Builder
	b.WriteString(header)
	for _, section := range coveragematrix.Sections {
		var rows []string
		counts := map[coveragematrix.Kind]int{}
		for _, l := range all[section] {
			id := section + " " + l.key
			e, had := prior[id]
			delete(prior, id)
			if !had {
				e = coveragematrix.Entry{Section: section, Key: l.key, Kind: coveragematrix.Unclassified}
				if l.deprecated != "" && !l.reached {
					e.Kind, e.Class, e.Reason = coveragematrix.Excluded, "deprecated", "@deprecated: "+oneLine(l.deprecated)
				}
			}
			if p := contradiction(section, l, e); p != "" {
				problems = append(problems, fmt.Sprintf("[%s] %s: %s", section, l.key, p))
			}
			counts[e.Kind]++
			rows = append(rows, l.key+"  "+e.Disposition())
		}
		fmt.Fprintf(&b, "\n[%s] %d: %s\n", section, len(rows), summary(counts))
		for _, r := range rows {
			b.WriteString(r + "\n")
		}
	}
	// What is left was in the file and the facts no longer produce: the
	// field left the schema, or the gap or filter it named is gone.
	for _, e := range existing {
		if _, orphan := prior[e.Section+" "+e.Key]; orphan {
			problems = append(problems, fmt.Sprintf("[%s] %s: orphan (line %d) — no longer in the schema, or no longer a gap; the regenerated file drops it", e.Section, e.Key, e.Line))
			delete(prior, e.Section+" "+e.Key)
		}
	}
	return b.String(), problems
}

// contradiction is why a disposition cannot stand against the facts, or "".
func contradiction(section string, l item, e coveragematrix.Entry) string {
	switch {
	case e.Kind == coveragematrix.Unclassified && l.reached && (section == "Query" || section == "Mutation"):
		return "UNCLASSIFIED — production code reaches it: name the command, `command <path>`"
	case e.Kind == coveragematrix.Unclassified && l.reached:
		return "UNCLASSIFIED — production code sets it: name the flag, `flag <path> --<flag>`"
	case e.Kind == coveragematrix.Unclassified:
		return "UNCLASSIFIED — the CLI does not reach it: `gap #<issue>` or `excluded <class>: <reason>`"
	case section == "Query" || section == "Mutation":
		if e.Kind == coveragematrix.Flag {
			return "a root field is reached by a `command`, not a `flag`"
		}
		if l.reached && e.Kind != coveragematrix.Command {
			return fmt.Sprintf("production code now reaches it, so `%s` is stale — name the command that does", e.Disposition())
		}
		if !l.reached && e.Kind == coveragematrix.Command {
			return "no production code calls an operation selecting it, so no command reaches it — wire it up, or mark it `gap`/`excluded`"
		}
	case section == "Filters":
		if e.Kind == coveragematrix.Command {
			return "a filter field is reached by a `flag`, not a `command`"
		}
		// A field the code sets for its own use (a filter a command applies
		// internally) can still lack a flag, so only a `flag` is checked
		// against the facts here.
		if !l.reached && e.Kind == coveragematrix.Flag {
			return "production code never sets it, so no flag reaches it — mark it `gap`/`excluded`"
		}
	case section == "Arguments":
		if e.Kind == coveragematrix.Command || e.Kind == coveragematrix.Flag {
			return "the Arguments section lists only what the CLI does not reach — `gap` or `excluded`"
		}
	}
	return ""
}

func summary(c map[coveragematrix.Kind]int) string {
	var parts []string
	for _, k := range []coveragematrix.Kind{coveragematrix.Command, coveragematrix.Flag, coveragematrix.Gap, coveragematrix.Excluded, coveragematrix.Unclassified} {
		if c[k] > 0 {
			parts = append(parts, fmt.Sprintf("%d %s", c[k], k))
		}
	}
	if len(parts) == 0 {
		return "none"
	}
	return strings.Join(parts, ", ")
}

func oneLine(s string) string { return strings.Join(strings.Fields(s), " ") }

const header = `# CLI coverage matrix (hadron-cli#1334, #66; cor:api:240:02): every field hadron-server
# exposes, and how the CLI reaches it. Regenerate with ` + "`make coverage`" + `; ` + "`make coverage-check`" + ` fails when
# the file is stale, a line is UNCLASSIFIED, or a disposition contradicts the code.
#
# What is reached is DERIVED from the code: a reached field has a document selecting it and
# production code calling that operation. Only the disposition after the two spaces is written
# by hand, and a regeneration keeps it:
#   command <path>             a command reaches it (Query, Mutation)
#   flag <path> --<flag>       a flag sets this filter field (Filters)
#   gap #<issue>[: <note>]     not reached yet; the issue tracks it
#   excluded <class>: <reason> deliberately not in the CLI. Classes: internal, host-protocol,
#                              deprecated, not-a-capability, graphical (name what is graphical).
#                              "Not built yet" and "rarely used" are gaps, never exclusions.
#
# [Query] and [Mutation] list every root field. [Arguments] lists each argument or input field
# a reached operation never passes (the shallowest one). [Filters] lists every field of a
# reached list query's filter input. A gap here is not a claim of full parity: while any line
# says gap, the CLI does not cover the platform.
`

func main() {
	check := flag.Bool("check", false, "fail if the committed matrix is stale or contradicts the code")
	flag.Parse()
	if err := run(*check); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(check bool) error {
	f, err := loadFacts()
	if err != nil {
		return err
	}
	var existing []coveragematrix.Entry
	committed, err := os.ReadFile(coveragematrix.Path)
	switch {
	case err == nil:
		if existing, err = coveragematrix.Parse(string(committed)); err != nil {
			return err
		}
	case !os.IsNotExist(err) || check:
		return fmt.Errorf("✗ read %s: %w", coveragematrix.Path, err)
	}
	out, problems := build(f, existing)

	if !check {
		fmt.Print(out)
		for _, p := range problems {
			fmt.Fprintln(os.Stderr, "! "+p)
		}
		return nil
	}
	if len(problems) > 0 {
		return fmt.Errorf("✗ %s: %d problem(s):\n  %s", coveragematrix.Path, len(problems), strings.Join(problems, "\n  "))
	}
	if string(committed) != out {
		return fmt.Errorf("✗ %s is stale: the schema or the code changed what the CLI reaches.\n  Run `make coverage` and commit; then decide every UNCLASSIFIED line", coveragematrix.Path)
	}
	fmt.Printf("✓ %s in sync (%s)\n", coveragematrix.Path, firstLines(out))
	return nil
}

// firstLines is the per-section summary, for the success line.
func firstLines(out string) string {
	var parts []string
	for _, l := range strings.Split(out, "\n") {
		if strings.HasPrefix(l, "[") {
			parts = append(parts, l)
		}
	}
	return strings.Join(parts, "; ")
}

func loadFacts() (*facts, error) {
	src, err := os.ReadFile(schemaPath)
	if err != nil {
		return nil, fmt.Errorf("read schema: %w", err)
	}
	types, err := parseSchema(string(src))
	if err != nil {
		return nil, fmt.Errorf("parse schema: %w", err)
	}
	paths, err := filepath.Glob(queriesGlob)
	if err != nil || len(paths) == 0 {
		return nil, fmt.Errorf("no operation documents found at %s", queriesGlob)
	}
	ops, err := parseDocuments(paths)
	if err != nil {
		return nil, fmt.Errorf("parse operations: %w", err)
	}
	inputs := map[string]bool{}
	for name, d := range types {
		if d.Kind == gql.InputObject {
			inputs[name] = true
		}
	}
	bindings, err := genqlientBindings(genqlientCfg)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", genqlientCfg, err)
	}
	uses, err := loadGoUses(".", productionPackages, inputs, bindings)
	if err != nil {
		return nil, err
	}
	return deriveFacts(types, ops, uses), nil
}
