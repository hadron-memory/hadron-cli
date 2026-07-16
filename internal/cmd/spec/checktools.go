package spec

import (
	_ "embed"
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

// The registered-tool manifest and the non-tool ignore-list, embedded at build
// time. mcp-tools.txt is generated from hadron-server (`make tools-manifest`,
// CI-drift-checked); mcp-tools-ignore.txt is hand-maintained.
//
//go:embed mcp-tools.txt
var toolManifestRaw string

//go:embed mcp-tools-ignore.txt
var toolIgnoreRaw string

// reToolToken matches a hadron_* tool-shaped token: an underscore-joined run of
// lowercase-alphanumeric segments (digits included, so a name like
// hadron_s3_v2 matches whole; a trailing "_" in "hadron_chatbot_*" is not
// captured, keeping the token comparable to a registered name).
var reToolToken = regexp.MustCompile(`hadron_[a-z0-9]+(?:_[a-z0-9]+)*`)

// specToolFindingDTO is the stable --json shape for one finding. Kind is
// "unregistered-tool" (Token names it) or "unavailable" (a spec listed by the
// scan but unreadable by the batch read — reported so a check-tools gate can't
// pass while part of the corpus went unchecked).
type specToolFindingDTO struct {
	Citation string `json:"citation"`
	Kind     string `json:"kind"`
	Field    string `json:"field,omitempty"` // "content" | "abstract"
	Line     int    `json:"line,omitempty"`
	Token    string `json:"token,omitempty"`
	Text     string `json:"text,omitempty"`
}

const (
	kindUnregistered = "unregistered-tool"
	kindUnavailable  = "unavailable"
)

func newCmdCheckTools(f *cmdutil.Factory) *cobra.Command {
	var memory, prefix string
	cmd := &cobra.Command{
		Use:   "check-tools [-m <memory>]",
		Short: "Flag hadron_* tool references in specs that aren't real registered tools",
		Long: `Scan every spec's body and abstract for ` + "`hadron_*`" + ` MCP/runner tool
references and flag any that are not a real registered tool — the drift that let
stale ` + "`h-*`" + ` shorthand rot silently (issue #240).

The set of real tools is a manifest baked into this binary — the union of
hadron-server's two tool registries (the MCP ` + "`server.tool()`" + ` names and the
runner ` + "`RunToolDef`" + ` names), regenerated with ` + "`make tools-manifest`" + ` and
kept honest by CI. A small hand-maintained ignore-list carries the known
non-tool ` + "`hadron_*`" + ` identifiers that legitimately appear in prose (e.g. the
` + "`hadron_token`" + ` sign-in cookie), so they are never flagged.

Exits 5 when any unregistered token is found (like ` + "`spec lint`" + `), so it can
gate CI; ` + "`--json`" + ` emits the findings. A clean corpus prints a one-line OK and
exits 0.`,
		Example: `  hadron spec check-tools -m hadronmemory.com::specs
  hadron spec check-tools -m hadronmemory.com::specs --prefix cor:api --json`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			registered := parseToolList(toolManifestRaw)
			ignored := parseToolList(toolIgnoreRaw)

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
			// Same spec-scoped bulk read as `spec grep`: list the spec-tagged
			// citation nodes, then read their content/abstract in one nodeBatch.
			all, err := scanAllNodes(cmd.Context(), client, &memURN, prefixPtr, []string{"spec"})
			if err != nil {
				return err
			}
			ids := make([]string, 0, len(all))
			locByID := map[string]string{}
			for _, n := range all {
				if n == nil {
					continue
				}
				if _, perr := ParseCitation(n.Loc); perr != nil {
					continue
				}
				ids = append(ids, n.Id)
				locByID[n.Id] = n.Loc
			}
			findings := []specToolFindingDTO{}
			if len(ids) > 0 {
				nodes, unavailable, err := api.CollectNodeBatch(ids, func(chunk []string) (*gen.NodeBatchNodeBatchNodeBatchResult, error) {
					resp, err := gen.NodeBatch(cmd.Context(), client, chunk)
					if err != nil {
						return nil, api.MapError(err)
					}
					if resp == nil {
						return nil, nil
					}
					return resp.NodeBatch, nil
				})
				if err != nil {
					return err
				}
				// A spec that lists but can't be read went UNCHECKED — surface it as
				// a finding (not just a note) so a check-tools gate can't report
				// success while part of the corpus was skipped (matches spec lint's
				// treatment of unavailable nodes).
				for _, id := range unavailable {
					loc := locByID[id]
					if loc == "" {
						loc = id
					}
					findings = append(findings, specToolFindingDTO{Citation: loc, Kind: kindUnavailable, Text: "listed but unreadable — not checked"})
				}
				for _, n := range nodes {
					if n == nil {
						continue
					}
					if n.Content != nil {
						findings = append(findings, checkToolField(n.Loc, "content", *n.Content, registered, ignored)...)
					}
					if n.Abstract != nil {
						findings = append(findings, checkToolField(n.Loc, "abstract", *n.Abstract, registered, ignored)...)
					}
				}
			}
			sort.Slice(findings, func(i, j int) bool {
				a, b := findings[i], findings[j]
				if a.Citation != b.Citation {
					return a.Citation < b.Citation
				}
				if a.Field != b.Field {
					return a.Field < b.Field
				}
				if a.Line != b.Line {
					return a.Line < b.Line
				}
				return a.Token < b.Token
			})

			if err := output.Write(f.IOStreams, f.JSON, findings, func(w io.Writer) error {
				if len(findings) == 0 {
					fmt.Fprintf(w, "✓ no unregistered hadron_* tool references (checked against %d registered tools)\n", len(registered))
					return nil
				}
				t := output.NewTable(w, "CITATION", "KIND", "LOCATION", "DETAIL")
				for _, fnd := range findings {
					loc, detail := "-", fnd.Token
					if fnd.Kind == kindUnregistered {
						loc = fmt.Sprintf("%s:%d", fnd.Field, fnd.Line)
					} else {
						detail = fnd.Text
					}
					t.Row(fnd.Citation, fnd.Kind, loc, detail)
				}
				return t.Flush()
			}); err != nil {
				return err
			}
			// Any finding — an unregistered token OR a spec that couldn't be
			// checked — is a drift/coverage signal: exit 5 (Conflict), matching
			// `spec lint`, so CI can gate on it.
			if len(findings) > 0 {
				return exitcode.Silent(exitcode.Conflict)
			}
			return nil
		},
	}
	cmd.Flags().StringVarP(&memory, "memory", "m", "", "memory ID or fully-qualified URN (defaults to `hadron spec use`, then the active memory)")
	cmd.Flags().StringVar(&prefix, "prefix", "", "restrict to a citation prefix (that node + its descendants)")
	return cmd
}

// parseToolList reads a manifest/ignore file into a set: one identifier per
// line, blank lines and #-comments (whole-line or trailing) skipped.
func parseToolList(raw string) map[string]bool {
	set := map[string]bool{}
	for _, line := range strings.Split(raw, "\n") {
		if i := strings.IndexByte(line, '#'); i >= 0 {
			line = line[:i]
		}
		if tok := strings.TrimSpace(line); tok != "" {
			set[tok] = true
		}
	}
	return set
}

// checkToolField returns one finding per line for each hadron_* token that is
// neither a registered tool nor on the ignore-list. A token seen twice on the
// same line is reported once.
func checkToolField(loc, field, text string, registered, ignored map[string]bool) []specToolFindingDTO {
	var out []specToolFindingDTO
	for i, line := range strings.Split(text, "\n") {
		line = strings.TrimSuffix(line, "\r")
		seen := map[string]bool{}
		for _, tok := range reToolToken.FindAllString(line, -1) {
			if registered[tok] || ignored[tok] || seen[tok] {
				continue
			}
			seen[tok] = true
			out = append(out, specToolFindingDTO{Citation: loc, Kind: kindUnregistered, Field: field, Line: i + 1, Token: tok, Text: line})
		}
	}
	return out
}
