package team

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/Khan/genqlient/graphql"
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
		Use:   "init [-m <team-memory>]",
		Short: "Declare the team collection schemas in the team App memory",
		Long: `Declare the team collections in the team App memory's property schema.
Idempotent and convergent: the server (re)sets each collection it owns to its
canonical definition; every other collection in the schema is preserved.

The definitions are the SERVER's (hadron-server#958) — this command asks it to
converge them, so a memory declared by an older CLI is repaired rather than
left on whatever that version happened to ship.

The team App resolves like the rest of the group: --app, the configured App
context, or the worktree binding (#400) — the server locates the App's ONE
team shared memory itself (the worklog's home), so no memory needs naming.
-m stays as an explicit override; it must name an ` + "`app`" + `-class memory (an
Agent's system memory in particular is read-only from every App that runs
it — cor:dmo:050:03 — so a collection declared there could never be written
through the App, #384), and if it names some other app-class memory the
declaration still lands on the team memory, reported honestly.

The team group chat needs no init: it is a platform operation
(hadron-server#939) that the server bootstraps in the team App's shared
memory on the first ` + "`team chat post`" + `.

Messages are deliberately NOT a property-schema collection: the group chat
speaks the canonical chat-NODE shape (body in content, envelope in data,
chat-message nodeType — D-2026-08-07-004), owned server-side.`,
		Example: `  hadron team init --app acme.com:eng-team
  hadron team init -m hrn:mem:acme.com:eng-team`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			client, err := f.GraphQLClient()
			if err != nil {
				return err
			}
			if memory != "" {
				return initViaMemory(cmd, f, client, cmdutil.CanonicalMemoryRef(memory), memory)
			}
			b, berr := readBindingOrNilWithApp(ctx, f)
			if berr != nil {
				return berr
			}
			appRef, err := resolveTeamApp(ctx, f, b)
			if err != nil {
				return err
			}
			declared, preID, preURN, err := sharedMemoryStatus(ctx, client, appRef)
			if err != nil {
				return err
			}
			return convergeAndReport(cmd, f, client, appRef, declared, preID, preURN, "", "")
		},
	}
	cmd.Flags().StringVarP(&memory, "memory", "m", "", "explicit team App memory override (ID or URN); defaults to the App's own shared memory")
	return cmd
}

// initViaMemory is the -m override path: precheck the named memory's class,
// then converge via its App. Kept because an explicit memory deserves the
// early, specific refusals (#384) — the App path needs none of them.
//
// The STATUS pre-read deliberately does NOT come from the named memory: the
// write always lands on the App's canonical shared memory, and when -m names
// some other app-class memory a `declared` verdict read from IT would lie
// (an already-correct target reporting "created", a fresh one "updated" —
// PR #437 review). The named memory contributes only the class check and
// the where-it-landed note.
func initViaMemory(cmd *cobra.Command, f *cmdutil.Factory, client graphql.Client, memRef, asTyped string) error {
	ctx := cmd.Context()
	resp, err := gen.GetMemory(ctx, client, memRef)
	if err != nil {
		return api.MapError(err)
	}
	if resp.Memory == nil {
		return exitcode.Newf(exitcode.NotFound, "memory %q not found", asTyped)
	}
	if err := requireAppClass(resp.Memory.Urn, resp.Memory.Class); err != nil {
		return err
	}
	appRef, err := appForTeamMemory(ctx, client, memRef)
	if err != nil {
		return err
	}
	declared, preID, preURN, err := sharedMemoryStatus(ctx, client, appRef)
	if err != nil {
		return err
	}
	return convergeAndReport(cmd, f, client, appRef, declared, preID, preURN, resp.Memory.Id, resp.Memory.Urn)
}

// sharedMemoryStatus pre-reads the App's canonical shared memory — the
// memory the convergence writes to — for the three-value status contract. A
// null sharedMemory IS the answer: nothing declared (the server provisions
// on write).
func sharedMemoryStatus(ctx context.Context, client graphql.Client, appRef string) (declared bool, preID, preURN string, err error) {
	pre, err := gen.GetAppSharedMemory(ctx, client, appRef)
	if err != nil {
		return false, "", "", api.MapError(err)
	}
	if pre.App == nil {
		return false, "", "", exitcode.Newf(exitcode.NotFound, "App %q not found", appRef)
	}
	if pre.App.SharedMemory == nil {
		return false, "", "", nil
	}
	declared, err = worklogDeclared(pre.App.SharedMemory.Urn, pre.App.SharedMemory.Schema)
	return declared, pre.App.SharedMemory.Id, pre.App.SharedMemory.Urn, err
}

// worklogDeclared reports whether the schema already carries a worklog
// collection. Presence, not content (#401): the definition itself is the
// server's, and knowing whether one existed is what keeps the three-value
// `status` contract intact.
func worklogDeclared(urn string, schema *json.RawMessage) (bool, error) {
	if schema == nil || len(*schema) == 0 || bytes.Equal(*schema, []byte("null")) {
		return false, nil
	}
	var parsed struct {
		ObjectTypes map[string]json.RawMessage `json:"objectTypes"`
	}
	if err := json.Unmarshal(*schema, &parsed); err != nil {
		return false, fmt.Errorf("memory %s has an unparseable schema (%v) — fix it with `hadron memory set --schema` first", urn, err)
	}
	_, declared := parsed.ObjectTypes["worklog"]
	return declared, nil
}

// convergeAndReport runs the convergence and reports where the declaration
// actually landed. preID/preURN identify the App's shared memory as
// pre-read (empty on a fresh App); namedID/namedURN identify the -m memory
// when one was given — an -m naming some other app-class memory still
// declares on the team memory, and reporting the named one would be the lie
// (Codex P2 on PR #413), so that mismatch gets the note.
func convergeAndReport(cmd *cobra.Command, f *cmdutil.Factory, client graphql.Client, appRef string, declared bool, preID, preURN, namedID, namedURN string) error {
	ctx := cmd.Context()
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
	targetURN := preURN
	if converged.UpdateTeamCollections.MemoryId != preID {
		actual, aerr := gen.GetMemory(ctx, client, converged.UpdateTeamCollections.MemoryId)
		if aerr != nil {
			return api.MapError(aerr)
		}
		if actual.Memory == nil {
			return exitcode.Newf(exitcode.NotFound,
				"the collections were declared on memory %s, which could not be read back",
				converged.UpdateTeamCollections.MemoryId)
		}
		targetURN = actual.Memory.Urn
	}
	if namedURN != "" && converged.UpdateTeamCollections.MemoryId != namedID {
		fmt.Fprintf(f.IOStreams.ErrOut,
			"note: %s is not this App's team memory — the collections were declared on %s, where the worklog lives\n",
			namedURN, targetURN)
	}

	result := struct {
		Memory      string   `json:"memory"`
		Collections []string `json:"collections"`
		Status      string   `json:"status"`
	}{targetURN, collections, status}
	return output.Write(f.IOStreams, f.JSON, result, func(w io.Writer) error {
		_, err := fmt.Fprintf(w, "✓ %s collection(s) %s in %s\n",
			strings.Join(collections, ", "), status, targetURN)
		return err
	})
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
