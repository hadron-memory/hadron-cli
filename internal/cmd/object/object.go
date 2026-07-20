// Package object implements `hadron object …` — the CLI surface for the object
// store (cor:api:170, server #745). An object IS a node with an objectType,
// presented as a flat record { id, type, ...fields }. These commands are the
// legible, collection-oriented sugar over the structured-storage node flags
// (`node --object-type`/`--properties`, `search --where`/`--sort-property`).
package object

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/hadron-memory/hadron-cli/internal/cmdutil"
	"github.com/hadron-memory/hadron-cli/internal/exitcode"
	"github.com/hadron-memory/hadron-cli/internal/output"
)

// NewCmdObject wires the `object` command group.
func NewCmdObject(f *cmdutil.Factory) *cobra.Command {
	cmd := &cobra.Command{
		Use:     "object <command>",
		Aliases: []string{"objects", "obj"},
		Short:   "Create, read, update, and query objects (structured records)",
		Long: `Work with the object store — the collection-oriented surface over structured
storage. An object is a node with an objectType, presented as a flat record
{ id, type, ...fields }: id/type are the reserved envelope, and the node's typed
properties are the top-level fields. loc and name are auto-derived and hidden.

Objects live in a memory and belong to a collection (their type). On a memory
with a declared schema, writes are validated against the collection's fields.`,
	}
	cmd.AddCommand(newCmdCreate(f))
	cmd.AddCommand(newCmdGet(f))
	cmd.AddCommand(newCmdUpdate(f))
	cmd.AddCommand(newCmdDelete(f))
	cmd.AddCommand(newCmdFind(f))
	return cmd
}

// resolveJSON reads a JSON value from an inline flag or its `--<flag>-file`
// companion, validates it is well-formed, and returns it as a raw message.
// An empty/absent value yields (nil, nil) so an optional flag stays omitted from
// the wire. The two sources are mutually exclusive — the caller enforces that on
// cmd.Flags().Changed() (an explicit "" is a set value that a value check can't
// tell from unset). Deep validation (object shape, reserved fields, the where /
// sort grammar) is the server's job and surfaces as BAD_USER_INPUT.
func resolveJSON(flag, inline, file string) (*json.RawMessage, error) {
	raw := strings.TrimSpace(inline)
	name := flag
	if file != "" {
		b, err := os.ReadFile(file)
		if err != nil {
			return nil, exitcode.Newf(exitcode.Usage, "reading %s-file: %v", flag, err)
		}
		raw = strings.TrimSpace(string(b))
		name = flag + "-file"
	}
	if raw == "" {
		return nil, nil
	}
	if !json.Valid([]byte(raw)) {
		return nil, exitcode.Newf(exitcode.Usage, "%s must contain valid JSON", name)
	}
	msg := json.RawMessage(raw)
	return &msg, nil
}

// writeObject renders a single flat object: verbatim JSON under --json (the
// server's record is the stable contract), pretty-printed otherwise.
func writeObject(f *cmdutil.Factory, obj json.RawMessage) error {
	return output.Write(f.IOStreams, f.JSON, obj, func(w io.Writer) error {
		var buf bytes.Buffer
		if err := json.Indent(&buf, obj, "", "  "); err != nil {
			_, werr := fmt.Fprintln(w, string(obj))
			return werr
		}
		_, werr := fmt.Fprintln(w, buf.String())
		return werr
	})
}
