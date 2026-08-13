package team

import (
	"bytes"
	"encoding/json"
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

func newCmdInit(f *cmdutil.Factory) *cobra.Command {
	var memory string
	cmd := &cobra.Command{
		Use:   "init -m <team-memory>",
		Short: "Declare the team collection schemas in the team App memory",
		Long: `Declare the team collections in the team App memory's property schema.
Idempotent and convergent: the server (re)sets each collection it owns to its
canonical definition; every other collection in the schema is preserved.

The definitions are the SERVER's (hadron-server#958) — this command asks it to
converge them, so a memory declared by an older CLI is repaired rather than
left on whatever that version happened to ship.

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

			// #401: the SCHEMA is the server's. It declares the worklog
			// collection when it provisions the team memory and converges a
			// drifted declaration on request — this command asks for that
			// convergence. The CLI used to carry the definition as a Go
			// constant and merge it in from here, which is how its `kind` enum
			// came to lag the server's (no `repo`), silently refusing a kind
			// recordTeamWork accepts on every CLI-bootstrapped memory.
			//
			// Presence, not content: knowing whether a declaration EXISTED is
			// what keeps the three-value `status` contract intact, and that is
			// not knowledge of the schema itself.
			declared := false
			if resp.Memory.Schema != nil && len(*resp.Memory.Schema) > 0 && !bytes.Equal(*resp.Memory.Schema, []byte("null")) {
				var schema struct {
					ObjectTypes map[string]json.RawMessage `json:"objectTypes"`
				}
				if err := json.Unmarshal(*resp.Memory.Schema, &schema); err != nil {
					return fmt.Errorf("memory %s has an unparseable schema (%v) — fix it with `hadron memory set --schema` first", resp.Memory.Urn, err)
				}
				_, declared = schema.ObjectTypes["worklog"]
			}
			appRef, err := appForTeamMemory(ctx, client, memRef)
			if err != nil {
				return err
			}
			converged, err := gen.UpdateTeamCollections(ctx, client, appRef)
			if err != nil {
				return api.MapError(err)
			}
			status := "unchanged"
			switch {
			case !declared:
				status = "created"
			case converged.UpdateTeamCollections.Changed:
				status = "updated"
			}
			collections := converged.UpdateTeamCollections.Collections
			if collections == nil {
				collections = []string{}
			}

			// The group chat needs no materialization here: the server
			// bootstraps chats:team in the App's shared memory on the first
			// post (hadron-server#939) and restores a tombstoned root itself.

			result := struct {
				Memory      string   `json:"memory"`
				Collections []string `json:"collections"`
				Status      string   `json:"status"`
			}{resp.Memory.Urn, collections, status}
			return output.Write(f.IOStreams, f.JSON, result, func(w io.Writer) error {
				_, err := fmt.Fprintf(w, "✓ %s collection(s) %s in %s\n",
					strings.Join(collections, ", "), status, resp.Memory.Urn)
				return err
			})
		},
	}
	cmd.Flags().StringVarP(&memory, "memory", "m", "", "the team App memory (ID or URN)")
	_ = cmd.MarkFlagRequired("memory")
	return cmd
}

// requireAppClass refuses any memory that is not the team App's own
// app-class memory (#384). The worklog is a collection in the team App's
// OWN memory (#369 D13/D14), and the mistake this catches is not exotic: on a
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
