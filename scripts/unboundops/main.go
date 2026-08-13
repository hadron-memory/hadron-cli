// Command unboundops reports GraphQL operations that hadron-server exposes and
// the CLI does not wrap (hadron-cli#397).
//
// The gap it exists to catch: a server operation ships, the schema snapshot is
// refreshed, and nothing notices that no CLI command reaches it. That is not
// hypothetical — installAgentIntoApp had been in the schema since spec 023 when
// #389 was filed as "no surface attaches an existing Agent to an existing App",
// and the worklog contract sat duplicated in the client for months after
// recordTeamWork shipped (#396).
//
// Why a committed BASELINE rather than a plain report: the CLI wraps roughly
// half of the server's surface (148 of 284 root fields when this landed), so an
// unfiltered list is noise nobody reads, and an allowlist of 148 hand-written
// reasons is worse. The baseline turns the inventory into a DIFF — a schema
// refresh that adds an unbound operation shows up as an added line in review,
// and accepting it is one commit. Deliberate omissions carry their reason in
// the file itself.
//
//	go run ./scripts/unboundops            # print the baseline to stdout
//	go run ./scripts/unboundops -check     # diff against the committed baseline
package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/vektah/gqlparser/v2/ast"
	"github.com/vektah/gqlparser/v2/parser"
)

const (
	schemaPath   = "schema/schema.graphql"
	queriesGlob  = "internal/api/queries/*.graphql"
	baselinePath = "internal/api/unbound-ops.txt"
)

// rootFields returns the field names declared on `type Query` / `type Mutation`.
func rootFields(schemaSrc string) (map[string][]string, error) {
	doc, err := parser.ParseSchema(&ast.Source{Name: schemaPath, Input: schemaSrc})
	if err != nil {
		return nil, err
	}
	out := map[string][]string{}
	for _, def := range doc.Definitions {
		if def.Kind != ast.Object || (def.Name != "Query" && def.Name != "Mutation") {
			continue
		}
		for _, f := range def.Fields {
			out[def.Name] = append(out[def.Name], f.Name)
		}
	}
	return out, nil
}

// selectedRootFields returns every root field the CLI's operations select —
// the operation NAME is irrelevant, what matters is the server field it hits.
func selectedRootFields(paths []string) (map[string]bool, error) {
	used := map[string]bool{}
	for _, p := range paths {
		src, err := os.ReadFile(p)
		if err != nil {
			return nil, err
		}
		doc, err := parser.ParseQuery(&ast.Source{Name: p, Input: string(src)})
		if err != nil {
			return nil, fmt.Errorf("%s: %w", p, err)
		}
		for _, op := range doc.Operations {
			for _, sel := range op.SelectionSet {
				if f, ok := sel.(*ast.Field); ok {
					used[f.Name] = true
				}
			}
		}
	}
	return used, nil
}

func render(unbound map[string][]string, reasons map[string]string) string {
	var b strings.Builder
	b.WriteString("# GraphQL operations hadron-server exposes that the CLI does not wrap.\n")
	b.WriteString("# Regenerate with `make unbound-ops`; `make unbound-ops-check` fails when stale.\n")
	b.WriteString("#\n")
	b.WriteString("# A line ADDED here by a schema refresh is the signal: the server shipped an\n")
	b.WriteString("# operation and no command reaches it yet. Decide, then commit — either wire it\n")
	b.WriteString("# up (the line disappears) or leave it with a `# reason` noting why not.\n")
	for _, root := range []string{"Query", "Mutation"} {
		names := unbound[root]
		sort.Strings(names)
		fmt.Fprintf(&b, "\n[%s] %d unbound\n", root, len(names))
		for _, n := range names {
			if r := reasons[n]; r != "" {
				fmt.Fprintf(&b, "%s  # %s\n", n, r)
			} else {
				fmt.Fprintf(&b, "%s\n", n)
			}
		}
	}
	return b.String()
}

// existingReasons preserves the hand-written `# reason` annotations across a
// regeneration — they are the only part of this file a human authors.
func existingReasons(path string) map[string]string {
	reasons := map[string]string{}
	data, err := os.ReadFile(path)
	if err != nil {
		return reasons
	}
	for _, line := range strings.Split(string(data), "\n") {
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, "[") {
			continue
		}
		name, reason, found := strings.Cut(line, "#")
		if !found {
			continue
		}
		reasons[strings.TrimSpace(name)] = strings.TrimSpace(reason)
	}
	return reasons
}

func main() {
	check := flag.Bool("check", false, "fail if the committed baseline is stale")
	flag.Parse()

	schemaSrc, err := os.ReadFile(schemaPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, "read schema:", err)
		os.Exit(1)
	}
	roots, err := rootFields(string(schemaSrc))
	if err != nil {
		fmt.Fprintln(os.Stderr, "parse schema:", err)
		os.Exit(1)
	}
	paths, err := filepath.Glob(queriesGlob)
	if err != nil || len(paths) == 0 {
		fmt.Fprintln(os.Stderr, "no operation documents found at", queriesGlob)
		os.Exit(1)
	}
	used, err := selectedRootFields(paths)
	if err != nil {
		fmt.Fprintln(os.Stderr, "parse operations:", err)
		os.Exit(1)
	}

	unbound := map[string][]string{}
	for root, names := range roots {
		for _, n := range names {
			if !used[n] {
				unbound[root] = append(unbound[root], n)
			}
		}
	}
	out := render(unbound, existingReasons(baselinePath))

	if !*check {
		fmt.Print(out)
		return
	}
	committed, err := os.ReadFile(baselinePath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "✗ %s is missing — run `make unbound-ops` and commit it.\n", baselinePath)
		os.Exit(1)
	}
	if string(committed) != out {
		fmt.Fprintf(os.Stderr, "✗ %s is stale: the set of unwrapped server operations changed.\n", baselinePath)
		fmt.Fprintln(os.Stderr, "  Run `make unbound-ops` and commit — an ADDED line means the server")
		fmt.Fprintln(os.Stderr, "  shipped an operation no command reaches yet; wire it up or annotate why not.")
		os.Exit(1)
	}
	fmt.Printf("✓ %s in sync (%d Query + %d Mutation operations unwrapped)\n",
		baselinePath, len(unbound["Query"]), len(unbound["Mutation"]))
}
