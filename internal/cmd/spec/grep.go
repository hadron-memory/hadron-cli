package spec

import (
	"fmt"
	"io"
	"regexp"
	"sort"
	"strings"

	"github.com/spf13/cobra"

	"github.com/hadron-memory/hadron-cli/internal/api"
	"github.com/hadron-memory/hadron-cli/internal/api/gen"
	"github.com/hadron-memory/hadron-cli/internal/cmdutil"
	"github.com/hadron-memory/hadron-cli/internal/exitcode"
	"github.com/hadron-memory/hadron-cli/internal/output"
)

// specMatchDTO is the stable --json shape for one grep hit: a citation, which
// field it matched in, the 1-based line within that field, and the full line.
type specMatchDTO struct {
	Citation string `json:"citation"`
	Field    string `json:"field"` // "content" | "abstract"
	Line     int    `json:"line"`
	Text     string `json:"text"`
}

func newCmdGrep(f *cmdutil.Factory) *cobra.Command {
	var memory, prefix, field string
	var useRegex, ignoreCase bool
	cmd := &cobra.Command{
		Use:   "grep <pattern> [-m <memory>]",
		Short: "Search spec bodies + abstracts across the whole corpus",
		Long: `Search the body and abstract of every spec in the memory for a
pattern, printing each match as ` + "`citation:line: text`" + ` — line-oriented,
grep-style, and exhaustive (every occurrence, not a ranked/limited page).

Unlike ` + "`spec find`" + `, which ranks by relevance over name/loc/description/tags,
grep reads each spec's full text server-side (one bulk fetch, not a per-spec
loop) and matches client-side, so it finds where a token actually lives in the
prose. Matching is a literal substring by default; --regex treats <pattern> as
an RE2 regular expression, and -i folds case. By default both the body and the
abstract are searched; restrict with --field content|abstract.

grep is deliberately broad (no word-boundary): use it to discover every place a
token appears — including inside longer tokens — then rewrite precisely with
` + "`hadron spec replace`" + `, which is word-boundary-aware by default.`,
		Example: `  hadron spec grep h-read-node -m hadronmemory.com::specs
  hadron spec grep 'hadron_[a-z_]+' -m hadronmemory.com::specs --regex --json
  hadron spec grep TODO -m hadronmemory.com::specs --prefix cor:api --field content`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			pattern := args[0]
			if pattern == "" {
				return exitcode.Newf(exitcode.Usage, "<pattern> must not be empty")
			}
			searchContent, searchAbstract, err := parseGrepFields(field)
			if err != nil {
				return err
			}
			re, err := compileMatcher(pattern, useRegex, ignoreCase)
			if err != nil {
				return err
			}

			client, err := f.GraphQLClient()
			if err != nil {
				return err
			}
			memURN, err := specMemoryURN(f, cmd, client, memory)
			if err != nil {
				return err
			}
			var prefixPtr *string
			if prefix != "" {
				prefixPtr = &prefix
			}
			// List every spec-tagged citation node in scope (paged to exhaustion —
			// #23), then bulk-read their full content/abstract via nodeBatch (200/1
			// MB per call) rather than a per-spec GetNode loop, which timed out over
			// the whole corpus (#240). Scoping to the "spec" tag keeps grep to the
			// corpus proper — never the register or other non-spec nodes.
			all, err := scanAllNodes(cmd.Context(), client, &memURN, prefixPtr, []string{"spec"})
			if err != nil {
				return err
			}
			ids := make([]string, 0, len(all))
			for _, n := range all {
				if n == nil {
					continue
				}
				if _, perr := ParseCitation(n.Loc); perr != nil {
					continue // skip any non-citation-shaped node
				}
				ids = append(ids, n.Id)
			}
			// Nothing to read — skip the nodeBatch round-trip entirely.
			if len(ids) == 0 {
				return output.Write(f.IOStreams, f.JSON, []specMatchDTO{}, func(w io.Writer) error { return nil })
			}
			nodes, unavailable, err := api.CollectNodeBatch(ids, func(chunk []string) (*gen.NodeBatchNodeBatchNodeBatchResult, error) {
				resp, err := gen.NodeBatch(cmd.Context(), client, chunk)
				if err != nil {
					return nil, api.MapError(err)
				}
				if resp == nil {
					return nil, nil // CollectNodeBatch turns this into a clear error
				}
				return resp.NodeBatch, nil
			})
			if err != nil {
				return err
			}

			matches := []specMatchDTO{}
			for _, n := range nodes {
				if n == nil {
					continue
				}
				if searchContent && n.Content != nil {
					matches = append(matches, grepField(n.Loc, "content", *n.Content, re)...)
				}
				if searchAbstract && n.Abstract != nil {
					matches = append(matches, grepField(n.Loc, "abstract", *n.Abstract, re)...)
				}
			}
			sort.Slice(matches, func(i, j int) bool {
				a, b := matches[i], matches[j]
				if a.Citation != b.Citation {
					return a.Citation < b.Citation
				}
				if a.Field != b.Field {
					return a.Field < b.Field
				}
				return a.Line < b.Line
			})

			// A node that lists but is unreadable by nodeBatch is a real gap —
			// surface it (as the whole-corpus-read contract requires) rather than
			// silently under-reporting.
			if len(unavailable) > 0 {
				fmt.Fprintf(f.IOStreams.ErrOut, "note: %d spec(s) listed but unreadable — not searched\n", len(unavailable))
			}

			return output.Write(f.IOStreams, f.JSON, matches, func(w io.Writer) error {
				for _, m := range matches {
					// Body lines get a bare line number; abstract lines are tagged
					// so the two fields never look like the same coordinate space.
					if m.Field == "content" {
						fmt.Fprintf(w, "%s:%d: %s\n", m.Citation, m.Line, m.Text)
					} else {
						fmt.Fprintf(w, "%s:%s:%d: %s\n", m.Citation, m.Field, m.Line, m.Text)
					}
				}
				return nil
			})
		},
	}
	cmd.Flags().StringVarP(&memory, "memory", "m", "", "memory ID or fully-qualified URN (defaults to `hadron spec use`, then the active memory)")
	cmd.Flags().StringVar(&prefix, "prefix", "", "restrict to a citation prefix (that node + its descendants)")
	cmd.Flags().StringVar(&field, "field", "", "restrict to one field: content or abstract (default: both)")
	cmd.Flags().BoolVar(&useRegex, "regex", false, "treat <pattern> as an RE2 regular expression instead of a literal")
	cmd.Flags().BoolVarP(&ignoreCase, "ignore-case", "i", false, "match case-insensitively")
	return cmd
}

// parseGrepFields maps the --field flag to (searchContent, searchAbstract). An
// empty flag searches both; "content"/"abstract" restrict; anything else is a
// usage error.
func parseGrepFields(field string) (content, abstract bool, err error) {
	switch strings.ToLower(strings.TrimSpace(field)) {
	case "":
		return true, true, nil
	case "content", "body":
		return true, false, nil
	case "abstract":
		return false, true, nil
	default:
		return false, false, exitcode.Newf(exitcode.Usage, "unknown --field %q (valid: content, abstract, or omit for both)", field)
	}
}

// compileMatcher builds the RE2 matcher: a literal pattern is quoted so its
// metacharacters are inert; --regex passes it through; -i prepends the
// case-insensitive flag. A bad --regex is a usage error, not a crash.
func compileMatcher(pattern string, useRegex, ignoreCase bool) (*regexp.Regexp, error) {
	src := pattern
	if !useRegex {
		src = regexp.QuoteMeta(pattern)
	}
	if ignoreCase {
		src = "(?i)" + src
	}
	re, err := regexp.Compile(src)
	if err != nil {
		return nil, exitcode.Newf(exitcode.Usage, "invalid --regex pattern %q: %v", pattern, err)
	}
	return re, nil
}

// grepField returns one match per line of text that the matcher hits. Line
// numbers are 1-based within the field; a trailing carriage return is trimmed so
// CRLF bodies don't leave a stray \r in the reported line.
func grepField(loc, field, text string, re *regexp.Regexp) []specMatchDTO {
	var out []specMatchDTO
	for i, line := range strings.Split(text, "\n") {
		line = strings.TrimSuffix(line, "\r")
		if re.MatchString(line) {
			out = append(out, specMatchDTO{Citation: loc, Field: field, Line: i + 1, Text: line})
		}
	}
	return out
}
