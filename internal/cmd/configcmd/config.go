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
		Short: "Read and write CLI configuration",
	}
	cmd.AddCommand(newCmdGet(f))
	cmd.AddCommand(newCmdSet(f))
	cmd.AddCommand(newCmdList(f))
	return cmd
}

func newCmdGet(f *cmdutil.Factory) *cobra.Command {
	return &cobra.Command{
		Use:   "get <key>",
		Short: "Print a configuration value",
		Args:  cobra.ExactArgs(1),
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
		Short: "Set a configuration value",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := f.Config()
			if err != nil {
				return err
			}
			if err := cfg.Set(args[0], args[1]); err != nil {
				return err
			}
			return output.Write(f.IOStreams, f.JSON, map[string]string{args[0]: args[1]}, func(w io.Writer) error {
				_, err := fmt.Fprintf(w, "✓ %s = %s\n", args[0], args[1])
				return err
			})
		},
	}
}

func newCmdList(f *cmdutil.Factory) *cobra.Command {
	return &cobra.Command{
		Use:     "list",
		Aliases: []string{"ls"},
		Short:   "List all configuration values",
		Args:    cobra.NoArgs,
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
					t.Row(k, all[k])
				}
				return t.Flush()
			})
		},
	}
}
