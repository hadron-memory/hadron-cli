package team

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"

	"github.com/spf13/cobra"

	"github.com/hadron-memory/hadron-cli/internal/api"
	"github.com/hadron-memory/hadron-cli/internal/api/gen"
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
		Long: `Declare the worklog collection in the team App memory's property schema.
Idempotent and convergent: the worklog entry is (re)set to its canonical
definition; every other collection in the schema is preserved.

-m must name an ` + "`app`" + `-class memory — the team App's own shared memory.
Any other class is refused: an Agent's system memory in particular is
read-only from every App that runs it (cor:dmo:050:03), so a collection
declared there could never be written through the App (#384).

The team group chat needs no init: it is a platform operation
(hadron-server#939) that the server bootstraps in the team App's shared
memory on the first ` + "`team chat post`" + `.

Messages are deliberately NOT a property-schema collection: the group chat
speaks the canonical chat-NODE shape (body in content, envelope in data,
chat-message nodeType — D-2026-08-07-004), owned server-side.`,
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
			if err := requireAppClass(resp.Memory.Urn, resp.Memory.Class); err != nil {
				return err
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

			// The group chat needs no materialization here: the server
			// bootstraps chats:team in the App's shared memory on the first
			// post (hadron-server#939) and restores a tombstoned root itself.

			result := struct {
				Memory      string   `json:"memory"`
				Collections []string `json:"collections"`
				Status      string   `json:"status"`
			}{resp.Memory.Urn, []string{"worklog"}, status}
			return output.Write(f.IOStreams, f.JSON, result, func(w io.Writer) error {
				_, err := fmt.Fprintf(w, "✓ worklog collection %s in %s\n", status, resp.Memory.Urn)
				return err
			})
		},
	}
	cmd.Flags().StringVarP(&memory, "memory", "m", "", "the team App memory (ID or URN)")
	_ = cmd.MarkFlagRequired("memory")
	return cmd
}

// requireAppClass refuses any memory that is not the team App's own
// app-class memory (#384). The worklog is a collection in the team APP
// memory (#369 D13/D14), and the mistake this catches is not exotic: on a
// fresh team App the correct memory does not exist yet (created lazily on
// the first `team chat post`, hadron-server#951), so the Team Agent's
// system memory is the only team-shaped memory in sight — and a system
// memory is read-only from every App that runs it (cor:dmo:050:03), which
// makes a worklog collection declared there permanently unwritable. Writing
// it anyway and reporting success is the whole defect.
func requireAppClass(urn string, class gen.MemoryClass) error {
	if class == gen.MemoryClassApp {
		return nil
	}
	hint := ""
	if class == gen.MemoryClassSystem {
		hint = " — a system memory is an Agent's design, read-only from every App that runs it (cor:dmo:050:03), so `recordWork` could never write rows against a collection declared there"
	}
	return exitcode.Newf(exitcode.Usage,
		"memory %s is class %q, not \"app\"%s. Pass the team App's own shared memory; on a brand-new App it is created by the server on the first `hadron team chat post`",
		urn, string(class), hint)
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
