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

// projectionWriter renders the consent projection. It remembers the first write
// error and whether anything was cut for display: a caller must not ask for
// consent after either (the person has not been shown what they are approving).
type projectionWriter struct {
	w          io.Writer
	err        error
	incomplete bool
}

func (p *projectionWriter) printf(format string, a ...any) {
	if p.err != nil {
		return
	}
	_, p.err = fmt.Fprintf(p.w, format, a...)
}

// fact is one server string, sanitized and bounded; a cut marks the display incomplete.
func (p *projectionWriter) fact(s string, max int) string {
	out, cut := safeTextCut(s, max)
	if cut {
		p.incomplete = true
	}
	return out
}

func (p *projectionWriter) list(label string, items []string) {
	if len(items) == 0 {
		p.printf("%s: none listed\n", label)
		return
	}
	p.printf("%s:\n", label)
	for i, it := range items {
		if i == maxFacts {
			p.incomplete = true
			p.printf("  … and %d more\n", len(items)-maxFacts)
			break
		}
		p.printf("  - %s\n", p.fact(it, maxFact))
	}
}

func (p *projectionWriter) flowList(label string, items []string) {
	if len(items) == 0 {
		return
	}
	shown := items
	more := ""
	if len(items) > maxFacts {
		shown, more = items[:maxFacts], fmt.Sprintf(", … and %d more", len(items)-maxFacts)
		p.incomplete = true
	}
	out := make([]string, len(shown))
	for i, it := range shown {
		out[i] = p.fact(it, maxFact)
	}
	p.printf("      %s: %s%s\n", label, joinComma(out), more)
}

// writeProjection renders what an adopter reviews. Every server string passes
// safeText; the hash and graph appear only with the depth. It returns the first
// write error, and incomplete=true when any fact or list was cut to fit.
func writeProjection(w io.Writer, display string, d *approvalDTO, withDepth bool) (incomplete bool, err error) {
	p := &projectionWriter{w: w}
	p.printf("Automation %s\n", display)
	p.printf("State: %s\n", stateWord(d.State))
	s := d.Summary
	p.printf("Name: %s\n", p.fact(s.Name, maxName))
	if s.Schedule != nil {
		p.printf("Schedule: %s\n", p.fact(*s.Schedule, maxFact))
	}
	p.list("Reads", s.Reads)
	p.list("Writes", s.Writes)
	p.list("Tools", s.Tools)
	p.list("Limits", s.Limits)
	p.list("Cannot do", s.CannotDo)
	if len(s.Flows) == 0 {
		p.printf("Flows: none listed\n")
	} else {
		p.printf("Flows:\n")
		for i, fl := range s.Flows {
			if i == maxFacts {
				p.incomplete = true
				p.printf("  … and %d more\n", len(s.Flows)-maxFacts)
				break
			}
			p.printf("  - %s (%s)\n", p.fact(fl.Name, maxName), p.fact(fl.URN, maxFact))
			p.flowList("tools", fl.Tools)
			p.flowList("writes", fl.Writes)
		}
	}
	if withDepth {
		p.printf("Hash: %s\n", p.fact(d.Hash, maxFact))
		var pretty bytes.Buffer
		if err := json.Indent(&pretty, d.Depth, "", "  "); err != nil {
			pretty.Reset()
			pretty.Write(d.Depth)
		}
		block := safeBlock(pretty.String(), maxDepth)
		if len([]rune(pretty.String())) > maxDepth {
			p.incomplete = true
		}
		p.printf("Graph:\n%s\n", block)
	}
	if p.incomplete {
		p.printf("(some of this was cut for display; `--json` has all of it)\n")
	}
	return p.incomplete, p.err
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

An empty list is printed "none listed": it is what the server listed, not a
guarantee that nothing applies. --depth adds the graph and the hash. State is one of: not approved, approved, or
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
				_, err := writeProjection(w, sel.Display+inApp(sel, app), dto, depth)
				return err
			})
		},
	}
	addSelectionArgs(cmd, &run)
	cmd.Flags().BoolVar(&depth, "depth", false, "also show the graph and the hash")
	return cmd
}
