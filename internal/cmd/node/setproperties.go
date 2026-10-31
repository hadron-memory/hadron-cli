package node

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/hadron-memory/hadron-cli/internal/api"
	"github.com/hadron-memory/hadron-cli/internal/api/gen"
	"github.com/hadron-memory/hadron-cli/internal/cmdutil"
	"github.com/hadron-memory/hadron-cli/internal/exitcode"
	"github.com/hadron-memory/hadron-cli/internal/output"
)

// setPropertiesDTO is the stable --json shape of node set-properties: the
// node and its properties as stored after the merge.
type setPropertiesDTO struct {
	ID         string          `json:"id"`
	MemoryID   string          `json:"memoryId"`
	Loc        string          `json:"loc"`
	Name       string          `json:"name"`
	Properties json.RawMessage `json:"properties"`
	UpdatedAt  string          `json:"updatedAt"`
}

// newCmdSetProperties is node set-properties (cli#1341): updateNodeProperties,
// a server-side shallow merge into the node's properties.
func newCmdSetProperties(f *cmdutil.Factory) *cobra.Command {
	var memory, patch, patchFile, reason string
	cmd := &cobra.Command{
		Use:   "set-properties <node-urn> | <loc> -m <memory>",
		Short: "Merge fields into a node's properties",
		Long: `Merge a JSON object into a node's properties (the record's fields, which
--where and --sort-property search on; not its data object). The server does
the merge: each top-level key you give overwrites that key, and keys you leave
out stay as they are. A node with no properties takes the object as given.

One exception: an object under "exports" (the per-host skill export settings)
is merged as a JSON Merge Patch (RFC 7396): hosts and their fields merge
recursively, and a null value there removes that host or field,
so {"exports":{"codexSkill":{"enable":false}}} changes one setting and keeps
the rest. Let the server do this merge rather than reading, editing and
writing the whole object back, which can lose another writer's change.

On a memory with a schema, the merged result is checked against it and a
record that does not fit is refused. --reason is recorded in the node's
version history. node update --properties replaces every field instead.`,
		Example: `  hadron node set-properties competitors:acme -m hrn:mem:acme.com:research --patch '{"tier":"enterprise"}'
  hadron node set-properties hrn:node:acme.com:kb:skills:triage --patch '{"exports":{"codexSkill":{"enable":false}}}' --reason "disable on Codex"
  hadron node set-properties competitors:acme -m hrn:mem:acme.com:research --patch-file patch.json --json`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			changed := cmd.Flags().Changed
			if changed("patch") == changed("patch-file") {
				return exitcode.Newf(exitcode.Usage, "pass exactly one of --patch or --patch-file. Nothing was sent")
			}
			raw := patch
			if changed("patch-file") {
				b, err := os.ReadFile(patchFile)
				if err != nil {
					return exitcode.Newf(exitcode.Usage, "reading --patch-file: %v", err)
				}
				raw = string(b)
			}
			raw = strings.TrimSpace(raw)
			var obj map[string]json.RawMessage
			if err := json.Unmarshal([]byte(raw), &obj); err != nil || obj == nil {
				return exitcode.Newf(exitcode.Usage, "the patch must be a JSON object, such as {\"tier\":\"enterprise\"}. Nothing was sent")
			}
			var reasonArg *string
			if r := strings.TrimSpace(reason); r != "" {
				reasonArg = &r
			}
			client, err := f.GraphQLClient()
			if err != nil {
				return err
			}
			id, err := cmdutil.ResolveNodeRef(cmd, client, memory, args[0])
			if err != nil {
				return err
			}
			resp, err := gen.UpdateNodeProperties(cmd.Context(), client, id, json.RawMessage(raw), reasonArg)
			if err != nil {
				return api.MapError(err)
			}
			n := resp.UpdateNodeProperties
			dto := setPropertiesDTO{ID: n.Id, MemoryID: n.MemoryId, Loc: n.Loc, Name: n.Name, Properties: cmdutil.ProjectedColumn(n.Properties), UpdatedAt: n.UpdatedAt}
			return output.Write(f.IOStreams, f.JSON, dto, func(w io.Writer) error {
				_, err := fmt.Fprintf(w, "✓ updated properties of %s\n%s\n", output.SafeCell(dto.Loc), string(dto.Properties))
				return err
			})
		},
	}
	cmd.Flags().StringVarP(&memory, "memory", "m", "", "memory (hrn:mem:<root>:<slug>) to resolve a bare <loc> against")
	cmd.Flags().StringVar(&patch, "patch", "", "the JSON object to merge into the properties")
	cmd.Flags().StringVar(&patchFile, "patch-file", "", "read the JSON object to merge from a file")
	cmd.Flags().StringVar(&reason, "reason", "", "why this change was made (recorded in version history)")
	return cmd
}
