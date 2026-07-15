package spec

import (
	"fmt"
	"io"
	"strings"

	"github.com/spf13/cobra"

	"github.com/hadron-memory/hadron-cli/internal/api"
	"github.com/hadron-memory/hadron-cli/internal/api/gen"
	"github.com/hadron-memory/hadron-cli/internal/cmdutil"
	"github.com/hadron-memory/hadron-cli/internal/exitcode"
	"github.com/hadron-memory/hadron-cli/internal/output"
)

// extractResultDTO is the --json shape for `spec extract`: the scaffolded spec
// (mirroring newResultDTO) plus the source it was carved from and the
// --strip-source outcome. Slices are initialized to [] so empty renders as [].
type extractResultDTO struct {
	Citation     string           `json:"citation"`
	Source       string           `json:"source"`
	MemoryID     string           `json:"memoryId"`
	Name         string           `json:"name"`
	Tags         []string         `json:"tags"`
	Abstract     string           `json:"abstract"`
	Edges        []plannedEdgeDTO `json:"edges"`
	StripSource  bool             `json:"stripSource"`
	StripMatched bool             `json:"stripMatched"`
	DryRun       bool             `json:"dryRun"`
	// Content is the new spec body; included only in --dry-run for preview.
	Content string `json:"content,omitempty"`
}

func newCmdExtract(f *cmdutil.Factory) *cobra.Command {
	var (
		memory, toFeature, rule, title string
		content, contentFile           string
		abstract, abstractFile         string
		refLabel                       string
		tags                           []string
		stripSource, noEdges, dryRun   bool
	)
	cmd := &cobra.Command{
		Use:   "extract <source-citation>",
		Short: "Split a sub-rule out of a spec into its own citation",
		Long: `Carve a durable sub-rule out of a fat parent spec into its own
citation under another feature, auto-wiring the cross-reference edge back to
the parent — the "split" move, in one command instead of five manual
full-body replaces.

The new rule lands under <source-product>:<source-module>:<to-feature> (the
source's product and module, your --to-feature). Pipe the moved chunk in as
the new body via --content -/--content-file; it scaffolds the rule (abstract +
rubric default), wires the table-of-contents and inheritance edges like
` + "`spec new`" + `, and adds the cross-ref edge new→source.

By default the source is left untouched and you're reminded to trim the moved
chunk out of it. --strip-source also removes the chunk from the source body,
but only when it matches verbatim (exactly once) — a reformatted or absent
chunk leaves the source alone with a warning.`,
		Example: `  # Carve the "Node type" field out of the Node entity spec into feature 020.
  hadron spec get cor:dmo:060:02 -m hadronmemory.com::specs --body-only \
    | sed -n '/## Node type/,/## /p' \
    | hadron spec extract cor:dmo:060:02 -m hadronmemory.com::specs \
        --to-feature 020 --title "Node type" --content - --strip-source \
        --ref-label "documents the nodeType field of Node"

  hadron spec extract cor:dmo:060:02 -m hadronmemory.com::specs --to-feature 020 --rule 04 --title "Node type" --dry-run`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			source, err := ParseCitation(args[0])
			if err != nil {
				return err
			}
			if title == "" {
				return exitcode.Newf(exitcode.Usage, "--title is required")
			}
			if toFeature == "" {
				return exitcode.Newf(exitcode.Usage, "--to-feature <fff> is required")
			}
			// Body and abstract can each read stdin via "-", but only once.
			if content == "-" && abstract == "-" {
				return exitcode.Newf(exitcode.Usage, "--content - and --abstract - cannot both read stdin")
			}
			// --abstract and --abstract-file are mutually exclusive — guard on
			// Changed() so an explicit empty --abstract is caught too (mirrors new).
			if cmd.Flags().Changed("abstract") && cmd.Flags().Changed("abstract-file") {
				return exitcode.Newf(exitcode.Usage, "--abstract and --abstract-file are mutually exclusive")
			}
			chunkProvided := content != "" || contentFile != ""
			if stripSource && !chunkProvided {
				return exitcode.Newf(exitcode.Usage, "--strip-source needs the moved chunk via --content/--content-file")
			}

			client, err := f.GraphQLClient()
			if err != nil {
				return err
			}
			memURN, err := specMemoryURN(f, cmd, client, memory)
			if err != nil {
				return err
			}

			// Fetch the source: existence + name (for the default ref-label) +
			// body (for --strip-source). A typo fails fast here.
			srcNode, _, err := fetchSpecTaggedNode(cmd, client, memURN, source.Format())
			if err != nil {
				return err
			}

			// One scan of the source's product/module subtree for allocation,
			// paged to exhaustion (a truncated tail would reuse a live number, #23).
			prefix := source.Module
			if source.Product != "" {
				prefix = source.Product
			}
			all, err := scanAllNodes(cmd.Context(), client, &memURN, &prefix, nil)
			if err != nil {
				return err
			}
			locs := make(map[string]bool, len(all))
			allLocs := make([]string, 0, len(all)) // len(all) is the upper bound after filtering
			for _, n := range all {
				if n == nil {
					continue
				}
				if n.Loc != prefix && !strings.HasPrefix(n.Loc, prefix+":") {
					continue
				}
				if _, perr := ParseCitation(n.Loc); perr != nil {
					continue
				}
				locs[n.Loc] = true
				allLocs = append(allLocs, n.Loc)
			}

			target, parentLoc, inheritLoc, err := planExtract(source, toFeature, rule, locs, allLocs)
			if err != nil {
				return err
			}

			body, err := resolveBody(content, contentFile, f.IOStreams.In, target, title)
			if err != nil {
				return err
			}
			abs, err := cmdutil.ResolveTextInput("abstract", abstract, abstractFile, f.IOStreams.In)
			if err != nil {
				return err
			}
			if abs == "" {
				abs = placeholderAbstract(target, title)
			}
			name := specName(target, title)
			tagSet := specTags(tags)
			if refLabel == "" {
				refLabel = defaultRefLabel(title, srcNode.Name)
			}

			result := extractResultDTO{
				Citation:    target.Format(),
				Source:      source.Format(),
				MemoryID:    memURN,
				Name:        name,
				Tags:        tagSet,
				Abstract:    abs,
				Edges:       []plannedEdgeDTO{},
				StripSource: stripSource,
				DryRun:      dryRun,
			}
			if !noEdges {
				if parentLoc != "" {
					result.Edges = append(result.Edges, plannedEdgeDTO{Label: title, Target: parentLoc})
				}
				if inheritLoc != "" {
					result.Edges = append(result.Edges, plannedEdgeDTO{Label: inheritEdgeLabel, Target: inheritLoc})
				}
				// The cross-ref edge — the one thing extract adds over `spec new`.
				result.Edges = append(result.Edges, plannedEdgeDTO{Label: refLabel, Target: source.Format()})
			}

			// Compute the strip outcome up front (read-only) so --dry-run can
			// preview it and the executed path can apply it.
			var strippedBody string
			if stripSource {
				srcBody := ""
				if srcNode.Content != nil {
					srcBody = *srcNode.Content
				}
				strippedBody, result.StripMatched = stripChunk(srcBody, body)
			}

			if dryRun {
				result.Content = body
				return output.Write(f.IOStreams, f.JSON, result, func(w io.Writer) error {
					return renderExtractResult(w, result)
				})
			}

			// Create the new spec (identical to the `spec new` create path).
			nodeType := "info"
			input := gen.CreateNodeInput{
				MemoryId: memURN,
				Loc:      target.Format(),
				Name:     name,
				Tags:     tagSet,
				NodeType: &nodeType,
				Abstract: &abs,
				Content:  &body,
				Data:     specDataRaw(),
				Seq:      specSeq(target),
			}
			up, err := gen.CreateNode(cmd.Context(), client, &input)
			if err != nil {
				return api.MapError(err)
			}
			newID := up.CreateNode.Id

			var edgeFailures []string
			if !noEdges {
				for _, e := range result.Edges {
					targetID, rerr := resolveSpecNode(cmd, client, memURN, e.Target)
					if rerr != nil {
						fmt.Fprintf(f.IOStreams.ErrOut, "warning: skipped edge %q → %s: %v\n", e.Label, e.Target, rerr)
						edgeFailures = append(edgeFailures, e.Target)
						continue
					}
					if _, cerr := gen.CreateEdge(cmd.Context(), client, newID, targetID, e.Label, nil, nil, nil, nil, nil, nil); cerr != nil {
						fmt.Fprintf(f.IOStreams.ErrOut, "warning: edge %q → %s failed: %v\n", e.Label, e.Target, api.MapError(cerr))
						edgeFailures = append(edgeFailures, e.Target)
					}
				}
			}

			// Best-effort source trim, AFTER the additive create — so a miss or a
			// failed update never costs the extraction.
			if stripSource {
				if result.StripMatched {
					srcInput := gen.UpdateNodeInput{
						MemoryId: &srcNode.MemoryId,
						Loc:      &srcNode.Loc,
						Content:  &strippedBody, // content-only update; omitted fields preserved
					}
					if _, serr := gen.UpdateNode(cmd.Context(), client, &srcInput); serr != nil {
						result.StripMatched = false
						fmt.Fprintf(f.IOStreams.ErrOut, "warning: --strip-source: trimming %s failed: %v\n", source.Format(), api.MapError(serr))
					}
				} else {
					fmt.Fprintf(f.IOStreams.ErrOut, "warning: --strip-source: chunk not found verbatim (or ambiguous) in %s — source left untouched\n", source.Format())
				}
			}

			if err := output.Write(f.IOStreams, f.JSON, result, func(w io.Writer) error {
				return renderExtractResult(w, result)
			}); err != nil {
				return err
			}
			// The spec was created but one or more of its edges (ToC, inheritance,
			// or the cross-ref back to the source) could not be wired. Exit non-zero
			// so the partial write isn't read as a clean extract (#127). Note the
			// gap is a partial edge outcome — a lone cross-ref miss still leaves the
			// spec attached to the ToC, so this doesn't claim "orphaned" outright.
			if len(edgeFailures) > 0 {
				return exitcode.Newf(exitcode.Error,
					"extracted %s but %d edge(s) could not be wired to %s (see the warnings above); fix the target(s) and wire with `hadron edge add`",
					target.Format(), len(edgeFailures), strings.Join(edgeFailures, ", "))
			}
			return nil
		},
	}
	cmd.Flags().StringVarP(&memory, "memory", "m", "", "memory ID or fully-qualified URN (defaults to `hadron spec use`, then the active memory)")
	cmd.Flags().StringVar(&toFeature, "to-feature", "", "existing feature the extracted rule lands under (3 digits, required)")
	cmd.Flags().StringVar(&rule, "rule", "", "create this exact rule number (2 digits; default: allocate the next)")
	cmd.Flags().StringVar(&title, "title", "", "human title for the extracted spec (required)")
	cmd.Flags().StringVarP(&content, "content", "c", "", `the moved chunk = the new spec body ("-" reads stdin; default: the rubric)`)
	cmd.Flags().StringVar(&contentFile, "content-file", "", "read the moved chunk from a file")
	cmd.Flags().StringVar(&abstract, "abstract", "", `the new spec's abstract ("-" reads stdin; default: a placeholder lint flags)`)
	cmd.Flags().StringVar(&abstractFile, "abstract-file", "", "read the abstract from a file")
	cmd.Flags().StringVar(&refLabel, "ref-label", "", "label for the cross-ref edge new→source (default: synthesized from titles)")
	cmd.Flags().BoolVar(&stripSource, "strip-source", false, "also trim the moved chunk out of the source body (verbatim match only)")
	cmd.Flags().StringArrayVar(&tags, "tag", nil, "extra semantic tag (repeatable)")
	cmd.Flags().BoolVar(&noEdges, "no-edges", false, "do not create the ToC / inheritance / cross-ref edges")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "print the planned extraction without writing anything")
	_ = cmd.MarkFlagRequired("to-feature")
	_ = cmd.MarkFlagRequired("title")
	return cmd
}

// planExtract derives the destination citation for an extraction — a rule under
// <source product>:<source module>:<to-feature> — plus its ToC parent (the
// feature root) and inheritance target (the feature's :00 contract, if present).
// It forwards to planTarget, so allocation, frozen-code, and parent-exists rules
// are byte-for-byte identical to `spec new`.
func planExtract(source Citation, toFeature, rule string, locs map[string]bool, allLocs []string) (target Citation, parentLoc, inheritLoc string, err error) {
	if !reFeature.MatchString(toFeature) {
		return Citation{}, "", "", exitcode.Newf(exitcode.Usage, "--to-feature %q must be 3 digits", toFeature)
	}
	if rule != "" && !re2digit.MatchString(rule) {
		return Citation{}, "", "", exitcode.Newf(exitcode.Usage, "--rule %q must be 2 digits", rule)
	}
	return planTarget(planInput{
		product: source.Product,
		module:  source.Module,
		feature: toFeature,
		rule:    rule,
		locs:    locs,
		allLocs: allLocs,
	})
}

// titleFromName returns the human title portion of a spec node name
// ("<citation> — <title>" → "<title>"), or the whole trimmed name when there is
// no citation separator.
func titleFromName(name string) string {
	if i := strings.Index(name, "— "); i >= 0 {
		return strings.TrimSpace(name[i+len("— "):])
	}
	return strings.TrimSpace(name)
}

// defaultRefLabel synthesizes a sentence-style cross-ref label in the corpus
// convention ("documents <title> on the <entity> entity"); the author refines
// it with `edge update`. sourceName is the source node's full name
// ("<citation> — <entity title>"), so the entity is its title portion.
func defaultRefLabel(title, sourceName string) string {
	return fmt.Sprintf("documents %s on the %s entity", strings.TrimSpace(title), titleFromName(sourceName))
}

// stripChunk removes the moved chunk from a source body for --strip-source. It
// matches the chunk verbatim (after trimming surrounding whitespace) and acts
// only when the chunk occurs EXACTLY ONCE: a miss or an ambiguous multiple
// match returns ok=false and the caller leaves the source untouched. On a hit
// it removes the span and tidies the seam (one blank line between the kept
// halves, a single trailing newline) without touching the rest of the body.
func stripChunk(sourceBody, chunk string) (string, bool) {
	needle := strings.TrimSpace(chunk)
	if needle == "" {
		return "", false
	}
	idx := strings.Index(sourceBody, needle)
	if idx < 0 {
		return "", false
	}
	if strings.Contains(sourceBody[idx+len(needle):], needle) {
		return "", false // ambiguous — refuse to guess which occurrence
	}
	before := strings.TrimRight(sourceBody[:idx], " \t\n")
	after := strings.TrimLeft(sourceBody[idx+len(needle):], "\n") // keep indentation on the next line
	var out string
	switch {
	case before == "" && after == "":
		out = ""
	case before == "":
		out = after
	case after == "":
		out = before
	default:
		out = before + "\n\n" + after
	}
	if out != "" && !strings.HasSuffix(out, "\n") {
		out += "\n"
	}
	return out, true
}

func renderExtractResult(w io.Writer, r extractResultDTO) error {
	verb := "✓ extracted"
	if r.DryRun {
		verb = "would extract"
	}
	// r.Name already includes the citation ("<cit> — <title>"), so print it as-is.
	fmt.Fprintf(w, "%s %s  (from %s)\n", verb, r.Name, r.Source)
	fmt.Fprintf(w, "  tags: %v\n", r.Tags)
	for _, e := range r.Edges {
		fmt.Fprintf(w, "  edge: %s → %s\n", e.Label, e.Target)
	}
	if r.StripSource {
		switch {
		case r.DryRun && r.StripMatched:
			fmt.Fprintf(w, "  strip: chunk found in %s — would trim\n", r.Source)
		case r.DryRun:
			fmt.Fprintf(w, "  strip: chunk NOT found verbatim in %s — would skip\n", r.Source)
		case r.StripMatched:
			fmt.Fprintf(w, "  strip: trimmed the moved chunk out of %s\n", r.Source)
			// An executed miss is reported via a stderr warning, not here.
		}
	}
	if r.DryRun && r.Content != "" {
		fmt.Fprintf(w, "\n%s\n", r.Content)
	}
	if !r.DryRun {
		fmt.Fprintf(w, "  reminder: refresh the abstract on %s and %s", r.Citation, r.Source)
		if r.StripSource && r.StripMatched {
			fmt.Fprintln(w)
		} else {
			fmt.Fprintf(w, "; trim the moved chunk out of %s (spec get %s --body-only | node update --content -)\n", r.Source, r.Source)
		}
	}
	return nil
}
