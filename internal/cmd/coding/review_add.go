package coding

import (
	"context"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"

	"github.com/Khan/genqlient/graphql"
	"github.com/spf13/cobra"

	"github.com/hadron-memory/hadron-cli/internal/api"
	"github.com/hadron-memory/hadron-cli/internal/api/gen"
	"github.com/hadron-memory/hadron-cli/internal/cmdutil"
	"github.com/hadron-memory/hadron-cli/internal/exitcode"
	"github.com/hadron-memory/hadron-cli/internal/output"
)

// Default tags for a new check, matching what the live checklists carry.
// `review` is not what MAKES it a check — membership is the loc prefix (see
// membership.go) — but it is the tag callers filter on.
var defaultCheckTags = []string{"review", "review-criteria"}

// defaultLinkLabel is the relationship a --link edge gets when none is given.
// The convention in the live trees is that a check points at the canonical
// convention/finding node that explains the rule in full, rather than
// duplicating it.
const defaultLinkLabel = "details"

// newCheckDTO is the --json contract for `coding review create`.
type newCheckDTO struct {
	Loc     string    `json:"loc"`
	ID      string    `json:"id"`
	Name    string    `json:"name"`
	Trigger string    `json:"trigger"`
	Tags    []string  `json:"tags"`
	Seq     *int      `json:"seq"`
	Parent  string    `json:"parent"`
	EdgeID  string    `json:"edgeId"`
	Links   []linkDTO `json:"links"`
}

type linkDTO struct {
	Target string `json:"target"`
	Label  string `json:"label"`
}

// newLink is one --link value: the target ref as given, plus its edge label.
type newLink struct {
	Ref   string
	Label string
}

func newCmdReviewAdd(f *cmdutil.Factory) *cobra.Command {
	var (
		memory, root string
		trigger      string
		description  string
		scope        string
		content      string
		contentFile  string
		tags         []string
		links        []string
		seq          int
	)
	cmd := &cobra.Command{
		Use:     "create <check-name>",
		Aliases: []string{"add"},
		Short:   "Create a review check, wired to the review parent",
		Long: `Create a checklist node under the review parent, with the edge that
makes it fire.

The edge back to the parent is what ` + "`tasks:review-changes`" + ` discovers
the check by, and its label is the trigger matched against a diff. Creating the
node and that edge separately is how checks end up invisible, so both are
written in a single mutation and the result is read back to confirm the edge
landed.

--trigger is the condition. "Applies when" is prepended when you leave it off,
so ` + "`--trigger \"a resolver changes\"`" + ` and
` + "`--trigger \"Applies when a resolver changes\"`" + ` are the same label.

Without --content/--content-file the body is scaffolded Scope-first: a
` + "`> **Scope.**`" + ` blockquote (from --scope, or the trigger) that lets a
reviewer skip the check without reading it, then TODO sections to fill in.

A check created this way passes ` + "`coding review lint`" + ` as written.
Creating a check that already exists fails rather than overwriting it — edit an
existing one with ` + "`hadron node update`" + ` / ` + "`hadron edge update`" + `.`,
		Example: `  hadron coding review create thin-resolver-field -m acme.com::kb \
    --trigger "adding or modifying a GraphQL resolver" \
    --description "Resolver fields stay thin — applies when adding or modifying a resolver."
  hadron coding review create stable-json-dto -m acme.com::kb --trigger "a command's --json output changes" \
    --description "Commands marshal explicit DTOs — applies when --json output changes." \
    --link conventions:output-contract --tag json`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			mem, err := codingMemoryURN(memory)
			if err != nil {
				return err
			}
			loc, err := checkLoc(root, args[0])
			if err != nil {
				return err
			}
			label, err := normalizeTrigger(trigger)
			if err != nil {
				return err
			}
			if strings.TrimSpace(description) == "" {
				return exitcode.Newf(exitcode.Usage,
					"--description is required — list and search output show the description, so a check without one is invisible there")
			}
			parsedLinks, err := parseLinks(links)
			if err != nil {
				return err
			}
			body, err := resolveCheckBody(content, contentFile, f.IOStreams.In)
			if err != nil {
				return err
			}
			if body == "" {
				body = scaffoldCheckBody(loc, label, scope, description)
			}

			client, err := f.GraphQLClient()
			if err != nil {
				return err
			}
			ctx := cmd.Context()

			// Resolve the parent first: a check wired to nothing is the defect
			// this command exists to prevent, so fail before writing rather
			// than leaving an orphan behind.
			parentRef, err := mem.nodeRef(root)
			if err != nil {
				return err
			}
			parent, err := gen.GetNode(ctx, client, parentRef)
			if err != nil {
				return api.MapError(err)
			}
			if parent.Node == nil {
				return exitcode.Newf(exitcode.NotFound,
					"no %q node in %s — create the parent first, or pass --root", root, mem.raw)
			}

			edges := []*gen.NodeEdgeInput{{TargetId: parent.Node.Id, Name: &label}}
			outLinks := []linkDTO{}
			for _, l := range parsedLinks {
				// A --link commonly points at the canonical convention/finding
				// node, which often lives in ANOTHER memory (a repo's checks
				// cross-linking ::dev). -m is required here because it names
				// where the check is created, and ResolveNodeRef reads its ref
				// as a bare loc whenever a memory is given — so a qualified ref
				// must be resolved without it, or it gets composed into the
				// check's memory and resolves to nothing.
				linkMemory := memory
				if cmdutil.IsQualifiedNodeRef(l.Ref) {
					linkMemory = ""
				}
				targetID, rerr := cmdutil.ResolveNodeRef(cmd, client, linkMemory, l.Ref)
				if rerr != nil {
					return rerr
				}
				lbl := l.Label
				edges = append(edges, &gen.NodeEdgeInput{TargetId: targetID, Name: &lbl})
				outLinks = append(outLinks, linkDTO{Target: l.Ref, Label: l.Label})
			}

			input := gen.CreateNodeInput{
				MemoryId:    mem.Ref,
				Loc:         loc,
				Name:        checkName(loc),
				Description: &description,
				Content:     &body,
				Tags:        mergeTags(tags),
				Edges:       edges,
			}
			if cmd.Flags().Changed("seq") {
				input.Seq = &seq
			}

			resp, err := gen.CreateNode(ctx, client, &input)
			if err != nil {
				return api.MapError(err)
			}
			if resp.CreateNode == nil {
				return exitcode.Newf(exitcode.Error, "createNode returned no node")
			}

			// Step 6 of tasks:add-review-node — verify discoverability. The
			// create response carries no edges, and an unwired check looks
			// exactly like a wired one until a reviewer silently skips it.
			edgeID, wired, err := confirmParentEdge(ctx, client, resp.CreateNode.Id, parent.Node.Id)
			if err != nil {
				return err
			}

			dto := newCheckDTO{
				Loc: resp.CreateNode.Loc, ID: resp.CreateNode.Id, Name: resp.CreateNode.Name,
				Trigger: label, Tags: resp.CreateNode.Tags, Seq: resp.CreateNode.Seq,
				Parent: root, EdgeID: edgeID, Links: outLinks,
			}
			if dto.Tags == nil {
				dto.Tags = []string{}
			}
			if err := output.Write(f.IOStreams, f.JSON, dto, func(w io.Writer) error {
				t := output.NewTable(w)
				t.Row("✓ created", dto.Loc, "("+label+")")
				return t.Flush()
			}); err != nil {
				return err
			}
			if !wired {
				// A partial write exits 1 by contract: the node exists but is
				// under-linked, and a caller branching on the exit code must
				// not read that as complete.
				return exitcode.Newf(exitcode.Error,
					"created %s but its edge to %q is missing — the check is invisible to tasks:review-changes; wire it with `hadron edge create -m %s --from %s --to %s --name %q`",
					dto.Loc, root, mem.raw, dto.Loc, root, label)
			}
			return nil
		},
	}
	cmd.Flags().StringVarP(&memory, "memory", "m", "", "memory to add the check to (org::memory)")
	cmd.Flags().StringVar(&root, "root", reviewRootLoc, "loc of the review parent node")
	cmd.Flags().StringVar(&trigger, "trigger", "", `the condition the check fires on ("Applies when" is prepended if absent)`)
	cmd.Flags().StringVar(&description, "description", "", "one-line description (what it checks — applies when …)")
	cmd.Flags().StringVar(&scope, "scope", "", "the Scope blockquote's condition (default: the trigger)")
	cmd.Flags().StringVarP(&content, "content", "c", "", `node body ("-" reads stdin; default: a Scope-first scaffold)`)
	cmd.Flags().StringVar(&contentFile, "content-file", "", "read the node body from a file")
	cmd.Flags().StringArrayVar(&tags, "tag", nil, "extra tag (repeatable; review and review-criteria are always set)")
	cmd.Flags().StringArrayVar(&links, "link", nil,
		"cross-link to a canonical node: <node-ref>[=<label>], a bare loc in this memory or a URN/id anywhere (repeatable)")
	cmd.Flags().IntVar(&seq, "seq", 0, "explicit sibling ordering")
	_ = cmd.MarkFlagRequired("trigger")
	return cmd
}

// checkLoc composes the check's loc from the root and the name the caller gave.
// The fully-prefixed spelling is accepted too, so `review:foo` and `foo` under
// --root review are the same check.
func checkLoc(root, name string) (string, error) {
	n := strings.TrimSpace(name)
	n = strings.TrimPrefix(n, childPrefix(root))
	if n == "" {
		return "", exitcode.Newf(exitcode.Usage, "check name is empty")
	}
	if strings.ContainsAny(n, ": /") {
		return "", exitcode.Newf(exitcode.Usage,
			"check name %q must be a single kebab-case segment — the %q prefix is added for you", name, childPrefix(root))
	}
	loc := childPrefix(root) + n
	if err := cmdutil.ValidateURNPath("<check-name>", loc); err != nil {
		return "", err
	}
	return loc, nil
}

// checkName is the node name: the kebab segment, as the live checks spell it.
func checkName(loc string) string {
	if i := strings.LastIndex(loc, ":"); i >= 0 {
		return loc[i+1:]
	}
	return loc
}

// normalizeTrigger turns what the caller passed into the edge label the linter
// requires: the "Applies when" stem plus a condition. The checks here are the
// same ones lintReview applies, so a check created by this command is
// lint-clean the moment it exists.
func normalizeTrigger(trigger string) (string, error) {
	t := strings.Join(strings.Fields(trigger), " ")
	if t == "" {
		return "", exitcode.Newf(exitcode.Usage, "--trigger is required — it becomes the label the check fires on")
	}
	if !strings.HasPrefix(strings.ToLower(t), triggerStem) {
		return "Applies when " + t, nil
	}
	if strings.TrimSpace(t[len(triggerStem):]) == "" {
		return "", exitcode.Newf(exitcode.Usage,
			"--trigger %q is the bare stem — say what the check applies TO (e.g. \"a resolver changes\")", trigger)
	}
	// Keep the caller's spelling of the stem; the rule is case-insensitive.
	return t, nil
}

// parseLinks parses the repeatable --link values, each `<ref>` or
// `<ref>=<label>`.
func parseLinks(raw []string) ([]newLink, error) {
	out := []newLink{}
	for _, r := range raw {
		ref, label, found := strings.Cut(r, "=")
		ref, label = strings.TrimSpace(ref), strings.TrimSpace(label)
		if ref == "" {
			return nil, exitcode.Newf(exitcode.Usage, "--link %q has no target ref", r)
		}
		if !found || label == "" {
			label = defaultLinkLabel
		}
		out = append(out, newLink{Ref: ref, Label: label})
	}
	return out, nil
}

// mergeTags adds the caller's tags to the defaults, de-duplicated
// case-insensitively and ordered so the defaults come first.
func mergeTags(extra []string) []string {
	out := append([]string{}, defaultCheckTags...)
	seen := map[string]bool{}
	for _, t := range out {
		seen[strings.ToLower(t)] = true
	}
	rest := []string{}
	for _, t := range extra {
		t = strings.TrimSpace(t)
		if t == "" || seen[strings.ToLower(t)] {
			continue
		}
		seen[strings.ToLower(t)] = true
		rest = append(rest, t)
	}
	sort.Strings(rest)
	return append(out, rest...)
}

// resolveCheckBody reads an explicitly supplied body, or "" to scaffold one.
func resolveCheckBody(content, contentFile string, stdin io.Reader) (string, error) {
	if content != "" && contentFile != "" {
		return "", exitcode.Newf(exitcode.Usage, "--content and --content-file are mutually exclusive")
	}
	if contentFile != "" {
		b, err := os.ReadFile(contentFile)
		if err != nil {
			return "", exitcode.Newf(exitcode.Usage, "reading --content-file: %v", err)
		}
		return string(b), nil
	}
	if content == "-" {
		b, err := io.ReadAll(stdin)
		if err != nil {
			return "", err
		}
		return string(b), nil
	}
	return content, nil
}

// scaffoldCheckBody writes the Scope-first skeleton the review trees use. The
// Scope blockquote is the one part that must be real on day one: the runner
// reads only it before deciding to skip or walk the check, and `review lint`
// quotes it when the edge label goes missing (#331). The rest is TODO markers,
// spelled as the spec scaffolds spell them.
func scaffoldCheckBody(loc, trigger, scope, description string) string {
	if strings.TrimSpace(scope) == "" {
		// The trigger already reads as a condition; strip the stem so the
		// sentence reads "ONLY when <condition>".
		scope = strings.TrimSpace(trigger[len(triggerStem):])
	}
	var b strings.Builder
	fmt.Fprintf(&b, "> **Scope.** Run this checklist ONLY when %s. Skip otherwise.\n\n", strings.TrimRight(strings.TrimSpace(scope), "."))
	fmt.Fprintf(&b, "## Review: %s\n\n%s\n\n", checkName(loc), strings.TrimSpace(description))
	b.WriteString("### The Problem\n\nTODO(problem): why this is easy to miss — compiles fine, tests pass, only bites at runtime / on the wire / in CI.\n\n")
	b.WriteString("### Checklist\n\n- TODO(checklist): the specific things to verify.\n\n")
	b.WriteString("### Pass / fail / n-a\n\n- **Pass**: TODO(pass).\n- **Flag**: TODO(flag).\n- **N/a**: TODO(n-a).\n")
	return b.String()
}

// confirmParentEdge re-reads the created check and reports the id of its edge
// to the parent, and whether one exists at all.
func confirmParentEdge(ctx context.Context, client graphql.Client, nodeID, parentID string) (string, bool, error) {
	resp, err := gen.GetNode(ctx, client, nodeID)
	if err != nil {
		return "", false, api.MapError(err)
	}
	if resp.Node == nil {
		return "", false, nil
	}
	for _, e := range resp.Node.OutgoingEdges {
		if e == nil || e.Target == nil {
			continue
		}
		if e.Target.Id == parentID {
			return e.Id, true, nil
		}
	}
	return "", false, nil
}
