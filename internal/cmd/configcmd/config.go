// Package configcmd implements `hadron config ...`.
package configcmd

import (
	"fmt"
	"io"
	"sort"

	"github.com/spf13/cobra"

	"github.com/hadron-memory/hadron-cli/internal/cmdutil"
	"github.com/hadron-memory/hadron-cli/internal/output"
)

func NewCmdConfig(f *cmdutil.Factory) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "config <command>",
		Short: "Read and change this CLI's saved settings (server, default App, memory)",
		Long: `Read and change the settings this CLI saves on your computer, in
~/.config/hadron/config.toml (or $XDG_CONFIG_HOME/hadron/config.toml). They
are local to this machine and user, not stored on the server, and every later
hadron command uses them until you change them.

The keys:
  server       the Hadron server to talk to (default https://srv.hadronmemory.com)
  app          default App sent with requests (none by default)
  memory       default memory for commands that take -m/--memory
  spec_memory  default memory for 'hadron spec' commands
  org          active organization
  scope        default search scope for 'hadron search'

Start with 'hadron config list' to see every key and its current value. To
remove a saved value, set it to an empty string: hadron config set app ""

Your sign-in token is not a setting and is never stored here: see 'hadron
auth'. Several keys have a dedicated command that checks the value first
('app set-active', 'memory set-active', 'org use', 'scope use', 'spec use');
'config set' stores what you give it without checking it against the server.`,
	}
	cmd.AddCommand(newCmdGet(f))
	cmd.AddCommand(newCmdSet(f))
	cmd.AddCommand(newCmdList(f))
	return cmd
}

func newCmdGet(f *cmdutil.Factory) *cobra.Command {
	return &cobra.Command{
		Use:   "get <key>",
		Short: "Print one saved setting",
		Long: `Print the current value of one setting: server, app, memory, spec_memory,
org or scope ('hadron config --help' says what each means).

A key that was never set prints an empty line, except server, which prints
the default (https://srv.hadronmemory.com). It prints the saved value only: the
HADRON_SERVER environment variable and the --server flag are not applied. An
unknown key is refused with the list of known keys (exit 2).`,
		Example: `  hadron config get server
  hadron config get app --json`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := f.Config()
			if err != nil {
				return err
			}
			value, err := cfg.Get(args[0])
			if err != nil {
				return err
			}
			return output.Write(f.IOStreams, f.JSON, map[string]string{args[0]: value}, func(w io.Writer) error {
				_, err := fmt.Fprintln(w, value)
				return err
			})
		},
	}
}

func newCmdSet(f *cmdutil.Factory) *cobra.Command {
	return &cobra.Command{
		Use:   "set <key> <value>",
		Short: "Save a setting for every later command",
		Long: `Save a setting in ~/.config/hadron/config.toml. Every later hadron command
on this machine uses it until you change it; the --server and --app flags still
override it for a single command.

<key> is one of server, app, memory, spec_memory, org or scope ('hadron config
--help' says what each means); an unknown key is refused with the list of
known keys (exit 2). <value> is stored as given: a server URL, an App id or
hrn:app:<root>:<slug>, a memory id or URN, an organization root, or a scope
name. Only app is checked for shape; nothing is checked against the server.
The dedicated commands ('app set-active', 'memory set-active', 'org use',
'scope use', 'spec use') check the value first; prefer them.

To remove a saved value, set it to an empty string (""): the key is dropped
from the file, and server returns to its default.`,
		Example: `  hadron config set server https://hadron.internal.example
  hadron config set app hrn:app:acme.com:support
  hadron config set app ""`,
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := f.Config()
			if err != nil {
				return err
			}
			key, value := args[0], args[1]
			if key == "app" && value != "" {
				// The same gate `hadron app set-active` applies: a value that
				// cannot name an App must not be stored to fail on every later
				// command (#540).
				if value, err = cmdutil.CanonicalAppRef("<value>", value); err != nil {
					return err
				}
			}
			if err := cfg.Set(key, value); err != nil {
				return err
			}
			return output.Write(f.IOStreams, f.JSON, map[string]string{key: value}, func(w io.Writer) error {
				_, err := fmt.Fprintf(w, "✓ %s = %s\n", key, value)
				return err
			})
		},
	}
}

func newCmdList(f *cmdutil.Factory) *cobra.Command {
	return &cobra.Command{
		Use:     "list",
		Aliases: []string{"ls"},
		Short:   "List every setting and its current value",
		Long: `List every setting (server, app, memory, spec_memory, org, scope) with its
saved value. A key that was never set shows empty, except server, which shows
the default. It shows the saved values only: the HADRON_SERVER environment
variable and the --server flag are not applied.

The output never contains your sign-in token or any other credential (those
are kept by 'hadron auth', not here), so it is safe to paste into a bug report.
It does show the server URL and the App, memory and org you work with.`,
		Example: `  hadron config list
  hadron config list --json`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := f.Config()
			if err != nil {
				return err
			}
			all := cfg.All()
			return output.Write(f.IOStreams, f.JSON, all, func(w io.Writer) error {
				keys := make([]string, 0, len(all))
				for k := range all {
					keys = append(keys, k)
				}
				sort.Strings(keys)
				t := output.NewTable(w)
				for _, k := range keys {
					t.Row(output.SafeCell(k), output.SafeCell(all[k]))
				}
				return t.Flush()
			})
		},
	}
}
