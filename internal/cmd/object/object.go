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
		Example: `  hadron object create -m hrn:mem:acme.com:market --type competitor --key letta \
    --fields '{"name":"Letta","stage":"series-a","fundingUsd":12000000}'
  hadron object find -m hrn:mem:acme.com:market --type competitor --match '{"stage":"series-a"}'
  hadron object update hrn:node:acme.com:market:competitor:letta --fields '{"stage":"series-b"}'
  hadron object delete hrn:node:acme.com:market:competitor:letta --yes
  hadron node restore hrn:node:acme.com:market:competitor:letta`,
		Long: `Keep structured records, such as competitors with their stage and funding,
and find them by field. Each record prints as one flat JSON object:

  {"name":"Letta","stage":"series-a","fundingUsd":12000000,"id":"01a189a817267abc9b0f43cdde9b9382","type":"competitor"}

type names the collection the record belongs to: every record of one kind
shares it (competitor). id is the record's own id, which get, update and delete
take. The other keys are your fields; id and type cannot be field names.

Use object for records you add, change and filter by field. Use node for notes
(text you write and read), and search to look for text. A record is stored as
a node, so node and search commands see it too.

Without a schema, a collection accepts any fields. Someone who manages the
memory can declare the fields each collection takes (hadron memory set <memory>
--schema-file <file>; hadron memory get <memory> shows it). Then a missing
required field or a value of the wrong type is refused (exit 2), naming the
field; fields the schema does not list are still accepted.

A deleted record can be brought back with hadron node restore <id or URN>,
unless it was deleted with --hard.`,
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
