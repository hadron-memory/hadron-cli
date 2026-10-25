package automation

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"

	"github.com/spf13/cobra"

	"github.com/hadron-memory/hadron-cli/internal/cmdutil"
	"github.com/hadron-memory/hadron-cli/internal/output"
)

func printList(w io.Writer, label string, items []string) {
	if len(items) == 0 {
		fmt.Fprintf(w, "%s: (none)\n", label)
		return
	}
	fmt.Fprintf(w, "%s:\n", label)
	for i, it := range items {
		if i == maxItems {
			fmt.Fprintf(w, "  … and %d more (`--json` has them all)\n", len(items)-maxItems)
			break
		}
		fmt.Fprintf(w, "  - %s\n", safeText(it, maxLine))
	}
}

// writeProjection renders what an adopter reviews. Every server string passes
// safeText; the hash and graph appear only with the depth.
func writeProjection(w io.Writer, display string, d *approvalDTO, withDepth bool) {
	fmt.Fprintf(w, "Automation %s in %s\n", display, d.App)
	fmt.Fprintf(w, "State: %s\n", stateWord(d.State))
	s := d.Summary
	fmt.Fprintf(w, "Name: %s\n", safeText(s.Name, maxName))
	if s.Schedule != nil {
		fmt.Fprintf(w, "Schedule: %s\n", safeText(*s.Schedule, maxLine))
	}
	printList(w, "Reads", s.Reads)
	printList(w, "Writes", s.Writes)
	printList(w, "Tools", s.Tools)
	printList(w, "Limits", s.Limits)
	printList(w, "Cannot do", s.CannotDo)
	if len(s.Flows) == 0 {
		fmt.Fprintln(w, "Flows: (none)")
	} else {
		fmt.Fprintln(w, "Flows:")
		for i, fl := range s.Flows {
			if i == maxItems {
				fmt.Fprintf(w, "  … and %d more (`--json` has them all)\n", len(s.Flows)-maxItems)
				break
			}
			fmt.Fprintf(w, "  - %s (%s)\n", safeText(fl.Name, maxName), safeText(fl.URN, maxLine))
			printFlowList(w, "tools", fl.Tools)
			printFlowList(w, "writes", fl.Writes)
		}
	}
	if withDepth {
		fmt.Fprintf(w, "Hash: %s\n", safeText(d.Hash, maxLine))
		var pretty bytes.Buffer
		if err := json.Indent(&pretty, d.Depth, "", "  "); err != nil {
			pretty.Reset()
			pretty.Write(d.Depth)
		}
		fmt.Fprintf(w, "Graph:\n%s\n", safeBlock(pretty.String(), maxDepth))
	}
}

func printFlowList(w io.Writer, label string, items []string) {
	if len(items) == 0 {
		return
	}
	shown := items
	more := ""
	if len(items) > maxItems {
		shown, more = items[:maxItems], fmt.Sprintf(", … and %d more", len(items)-maxItems)
	}
	out := make([]string, len(shown))
	for i, it := range shown {
		out[i] = safeText(it, maxLine)
	}
	fmt.Fprintf(w, "      %s: %s%s\n", label, joinComma(out), more)
}

func joinComma(s []string) string {
	var b bytes.Buffer
	for i, x := range s {
		if i > 0 {
			b.WriteString(", ")
		}
		b.WriteString(x)
	}
	return b.String()
}

func newCmdApproval(f *cmdutil.Factory) *cobra.Command {
	var run string
	var depth bool
	cmd := &cobra.Command{
		Use:   "approval [<name>]",
		Short: "Show what approving an automation would approve",
		Long: `Show the plain summary of one automation (--app, else the active App): its state,
what it reads, writes and calls, and what it cannot do. Every sentence is the
server's; a fact the server cannot show says "not stated".

--depth adds the graph and the hash. State is one of: not approved, approved, or
changed since it was approved (its next run fails until it is approved again).`,
		Example: `  hadron automation approval schedule/nightly
  hadron automation approval nightly --depth --json
  hadron automation approval --run <run-id>`,
		RunE: func(cmd *cobra.Command, args []string) error {
			app, _, err := cmdutil.TargetApp(cmd, f)
			if err != nil {
				return err
			}
			client, err := f.GraphQLClient()
			if err != nil {
				return err
			}
			sel, err := resolveSelection(cmd.Context(), client, app, args, run, cmd.Flags().Changed("run"))
			if err != nil {
				return err
			}
			dto, err := fetchApproval(cmd.Context(), client, app, sel.Trigger, sel.Kind, depth)
			if err != nil {
				return err
			}
			return output.Write(f.IOStreams, f.JSON, dto, func(w io.Writer) error {
				writeProjection(w, sel.Display, dto, depth)
				return nil
			})
		},
	}
	addSelectionArgs(cmd, &run)
	cmd.Flags().BoolVar(&depth, "depth", false, "also show the graph and the hash")
	return cmd
}
