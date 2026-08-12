package team

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"

	"github.com/spf13/cobra"

	"github.com/hadron-memory/hadron-cli/internal/api"
	"github.com/hadron-memory/hadron-cli/internal/api/gen"
	chatpkg "github.com/hadron-memory/hadron-cli/internal/cmd/chat"
	"github.com/hadron-memory/hadron-cli/internal/cmdutil"
	"github.com/hadron-memory/hadron-cli/internal/exitcode"
	"github.com/hadron-memory/hadron-cli/internal/output"
)

// worklogCollection is the canonical schema of the worklog collection
// (#369 D13/D14): append-only external-artifact milestones, flat queried
// fields so the provenance query needs no joins, plus an undeclared optional
// `detail` JSON bag (strict stays false, so extra keys are allowed).
// `ref` is the canonical normalized artifact string (see refnorm.go) — the
// equality-lookup key of `session list --pr`.
const worklogCollectionJSON = `{
  "description": "Append-only external-artifact milestones per session (hadron-cli#369 D13/D14) - the PR/session provenance join. ref is the canonical normalized artifact string (owner/repo#N, owner/repo@sha).",
  "fields": {
    "sessionId":   {"type": "text", "required": true},
    "personaName": {"type": "text", "required": true},
    "tool":        {"type": "text", "required": true},
    "kind":        {"type": "enum", "required": true, "values": ["pr", "issue", "commit", "branch"]},
    "ref":         {"type": "text", "required": true},
    "action":      {"type": "text", "required": true},
    "at":          {"type": "datetime", "required": true}
  }
}`

func newCmdInit(f *cmdutil.Factory) *cobra.Command {
	var memory string
	cmd := &cobra.Command{
		Use:   "init -m <team-memory>",
		Short: "Declare the team collection schemas in the team App memory",
		Long: `Declare the worklog collection in the team App memory's property schema and
materialize the group-chat parent node (` + defaultMessagesLoc + `). Idempotent and
convergent: the worklog entry is (re)set to its canonical definition; every
other collection in the schema is preserved.

Messages are deliberately NOT a property-schema collection: the group chat
speaks the canonical chat-NODE shape (body in content, envelope in data,
chat-message nodeType — D-2026-08-07-004) that ` + "`hadron chat`" + ` shares —
moving it into the object store would break the storage model #921
formalizes. See docs/plans/team-chat.md.`,
		Example: `  hadron team init -m acme.com::eng-team`,
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			memRef := cmdutil.CanonicalMemoryRef(memory)
			client, err := f.GraphQLClient()
			if err != nil {
				return err
			}
			resp, err := gen.GetMemory(ctx, client, memRef)
			if err != nil {
				return api.MapError(err)
			}
			if resp.Memory == nil {
				return exitcode.Newf(exitcode.NotFound, "memory %q not found", memory)
			}

			// Merge: preserve every other collection, converge worklog onto
			// the canonical definition.
			schema := map[string]json.RawMessage{}
			objectTypes := map[string]json.RawMessage{}
			if resp.Memory.Schema != nil && len(*resp.Memory.Schema) > 0 && !bytes.Equal(*resp.Memory.Schema, []byte("null")) {
				if err := json.Unmarshal(*resp.Memory.Schema, &schema); err != nil {
					return fmt.Errorf("memory %s has an unparseable schema (%v) — fix it with `hadron memory set --schema` first", resp.Memory.Urn, err)
				}
				if raw, ok := schema["objectTypes"]; ok {
					if err := json.Unmarshal(raw, &objectTypes); err != nil {
						return fmt.Errorf("memory %s schema.objectTypes is unparseable (%v)", resp.Memory.Urn, err)
					}
				}
			}
			canonical := normalizeJSON([]byte(worklogCollectionJSON))
			status := "created"
			if existing, ok := objectTypes["worklog"]; ok {
				if bytes.Equal(normalizeJSON(existing), canonical) {
					status = "unchanged"
				} else {
					status = "updated"
				}
			}
			if status != "unchanged" {
				objectTypes["worklog"] = json.RawMessage(canonical)
				otRaw, err := json.Marshal(objectTypes)
				if err != nil {
					return err
				}
				schema["objectTypes"] = otRaw
				schemaRaw, err := json.Marshal(schema)
				if err != nil {
					return err
				}
				raw := json.RawMessage(schemaRaw)
				if _, err := gen.UpdateMemory(ctx, client, resp.Memory.Id, nil, nil, nil, nil, nil, nil, nil, nil, &raw); err != nil {
					return api.MapError(err)
				}
			}

			// Materialize the chat parent so the group chat is a real, copyable
			// node from day one. Best-effort like every EnsureChatParent call —
			// an existing parent conflicts harmlessly. Converge then retypes a
			// structure materialized by an older CLI (whose container was
			// typed `chat`) onto the canonical D-2026-08-07-004 types — init
			// is the explicit, idempotent migration point, so posts stay one
			// round-trip.
			chatCoords := chatpkg.Coords{Memory: resp.Memory.Id, MessagesLoc: defaultMessagesLoc}
			chatpkg.EnsureChatParent(ctx, client, chatCoords)
			chatpkg.ConvergeChatParent(ctx, client, chatCoords)

			result := struct {
				Memory      string   `json:"memory"`
				Collections []string `json:"collections"`
				Chat        string   `json:"chat"`
				Status      string   `json:"status"`
			}{resp.Memory.Urn, []string{"worklog"}, defaultMessagesLoc, status}
			return output.Write(f.IOStreams, f.JSON, result, func(w io.Writer) error {
				_, err := fmt.Fprintf(w, "✓ worklog collection %s in %s (chat parent: %s)\n", status, resp.Memory.Urn, defaultMessagesLoc)
				return err
			})
		},
	}
	cmd.Flags().StringVarP(&memory, "memory", "m", "", "the team App memory (ID or URN)")
	_ = cmd.MarkFlagRequired("memory")
	return cmd
}

// normalizeJSON re-marshals raw with sorted keys so semantically equal
// definitions compare equal regardless of key order/whitespace. Unparseable
// input returns the input itself (compares unequal, forcing an update).
func normalizeJSON(raw []byte) []byte {
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		return raw
	}
	out, err := json.Marshal(v)
	if err != nil {
		return raw
	}
	return out
}
