package spec

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"

	"github.com/spf13/cobra"

	"github.com/hadron-memory/hadron-cli/internal/api"
	"github.com/hadron-memory/hadron-cli/internal/api/gen"
	"github.com/hadron-memory/hadron-cli/internal/cmdutil"
	"github.com/hadron-memory/hadron-cli/internal/exitcode"
	"github.com/hadron-memory/hadron-cli/internal/output"
)

// editResultDTO is the --json shape for `spec edit`.
type editResultDTO struct {
	Citation string `json:"citation"`
	MemoryID string `json:"memoryId"`
	Name     string `json:"name"`
	Changed  bool   `json:"changed"`
	DryRun   bool   `json:"dryRun"`
}

// editorFunc is the editor-launch seam. Production uses launchEditor ($EDITOR on
// a temp file); SetEditorFuncForTest swaps it so command-level tests exercise
// the interactive path without spawning a real editor.
var editorFunc = launchEditor

// SetEditorFuncForTest replaces the editor seam and returns a restore func.
// Test-only: it lets sibling-package tests drive `spec edit`'s interactive path
// deterministically. The fake receives the current body and returns the edited
// one.
func SetEditorFuncForTest(fn func(io *output.IOStreams, current string) (string, error)) func() {
	prev := editorFunc
	editorFunc = fn
	return func() { editorFunc = prev }
}

func newCmdEdit(f *cmdutil.Factory) *cobra.Command {
	var (
		memory      string
		content     string
		contentFile string
		dryRun      bool
	)
	cmd := &cobra.Command{
		Use:   "edit <citation>",
		Short: "Edit a spec's body in $EDITOR (or from --content/--content-file)",
		Long: `Edit a spec's markdown body in place. By default it opens the
current body in your $EDITOR (then $VISUAL, else vi) pre-loaded — so you change
the few lines you mean to, instead of reconstructing the whole body in a temp
file and risking a transcription slip on a full-body replace.

Pass --content -/--content-file to replace the body non-interactively (the same
path agents already use via ` + "`spec get --body-only | node update --content -`" + `,
but spec-scoped: it validates the target is a spec and reminds you about the
abstract). An unchanged body writes nothing. The abstract is a separate field
and is preserved untouched — refresh it with ` + "`node update --abstract-file`" + ` if
the rule's meaning changed.`,
		Example: `  hadron spec edit cor:dmo:060:02 -m hadronmemory.com::specs
  hadron spec edit msg:010:02 -m micromentor.org::platform-specs --dry-run
  cat rewrite.md | hadron spec edit msg:010:02 -m micromentor.org::platform-specs --content -`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			memURN, err := memoryURNFromFlag(memory)
			if err != nil {
				return err
			}
			if _, err := ParseCitation(args[0]); err != nil {
				return err
			}
			changed := cmd.Flags().Changed
			// --content and --content-file are mutually exclusive. Guard on
			// Changed() so an explicit empty --content (clear the body) is caught
			// too, not just the value-based check inside ResolveTextInput.
			if changed("content") && changed("content-file") {
				return exitcode.Newf(exitcode.Usage, "--content and --content-file are mutually exclusive")
			}
			nonInteractive := changed("content") || changed("content-file")

			client, err := f.GraphQLClient()
			if err != nil {
				return err
			}
			node, err := fetchSpecNode(cmd, client, memURN, args[0])
			if err != nil {
				return err
			}
			if !hasTag(node.Tags, "spec") {
				return exitcode.Newf(exitcode.Usage,
					"%s is not a spec (no \"spec\" tag) — use `hadron node update` to edit arbitrary nodes", node.Loc)
			}
			current := ""
			if node.Content != nil {
				current = *node.Content
			}

			var edited string
			if nonInteractive {
				edited, err = cmdutil.ResolveTextInput("content", content, contentFile, f.IOStreams.In)
			} else {
				// The default seam (launchEditor) enforces the TTY requirement, so
				// overriding it in tests bypasses the terminal check cleanly.
				edited, err = editorFunc(f.IOStreams, current)
			}
			if err != nil {
				return err
			}

			result := editResultDTO{
				Citation: node.Loc,
				MemoryID: node.MemoryId,
				Name:     node.Name,
				Changed:  edited != current,
				DryRun:   dryRun,
			}

			if !result.Changed {
				return output.Write(f.IOStreams, f.JSON, result, func(w io.Writer) error {
					fmt.Fprintf(w, "no changes — %s left untouched\n", node.Loc)
					return nil
				})
			}
			if dryRun {
				return output.Write(f.IOStreams, f.JSON, result, func(w io.Writer) error {
					return renderEditResult(w, result, current, edited)
				})
			}

			// Content-only update: omitted fields (abstract, tags, …) are preserved.
			input := gen.NodeInput{
				MemoryId: node.MemoryId,
				Loc:      node.Loc,
				Name:     node.Name,
				Content:  &edited,
			}
			if _, err := gen.UpsertNode(cmd.Context(), client, &input); err != nil {
				return api.MapError(err)
			}
			return output.Write(f.IOStreams, f.JSON, result, func(w io.Writer) error {
				return renderEditResult(w, result, current, edited)
			})
		},
	}
	cmd.Flags().StringVarP(&memory, "memory", "m", "", "memory ID or fully-qualified URN (required)")
	cmd.Flags().StringVarP(&content, "content", "c", "", `replace the body with this value ("-" reads stdin) instead of opening $EDITOR`)
	cmd.Flags().StringVar(&contentFile, "content-file", "", "replace the body with a file's contents instead of opening $EDITOR")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "show what would change without writing")
	_ = cmd.MarkFlagRequired("memory")
	return cmd
}

// editorArgv resolves the editor command: $VISUAL, then $EDITOR, else vi. The
// value may carry args (e.g. "code --wait"), split on whitespace.
func editorArgv() []string {
	for _, env := range []string{os.Getenv("VISUAL"), os.Getenv("EDITOR")} {
		if fields := strings.Fields(env); len(fields) > 0 {
			return fields
		}
	}
	return []string{"vi"}
}

// launchEditor writes the current body to a temp .md file, opens it in the
// resolved editor wired to the real terminal, and returns the saved contents.
// It requires an interactive terminal — a non-TTY caller is told to use
// --content/--content-file instead.
func launchEditor(io *output.IOStreams, current string) (string, error) {
	if !io.IsTerminal() || !io.IsInputTerminal() {
		return "", exitcode.Newf(exitcode.Usage,
			"not a terminal: pass --content -/--content-file to edit non-interactively")
	}
	tmp, err := os.CreateTemp("", "hadron-spec-*.md")
	if err != nil {
		return "", err
	}
	path := tmp.Name()
	defer func() { _ = os.Remove(path) }()
	if _, err := tmp.WriteString(current); err != nil {
		_ = tmp.Close()
		return "", err
	}
	if err := tmp.Close(); err != nil {
		return "", err
	}

	argv := append(editorArgv(), path)
	ed := exec.Command(argv[0], argv[1:]...) // #nosec G204 — editor is the user's own $EDITOR
	ed.Stdin, ed.Stdout, ed.Stderr = os.Stdin, os.Stdout, os.Stderr
	if err := ed.Run(); err != nil {
		return "", exitcode.Newf(exitcode.Error, "editor %q exited: %v", argv[0], err)
	}

	edited, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	return string(edited), nil
}

// countLines counts body lines for the change summary, treating a final
// newline as a terminator rather than an extra empty line.
func countLines(s string) int {
	if s == "" {
		return 0
	}
	n := strings.Count(s, "\n")
	if !strings.HasSuffix(s, "\n") {
		n++
	}
	return n
}

func renderEditResult(w io.Writer, r editResultDTO, before, after string) error {
	verb := "✓ updated"
	if r.DryRun {
		verb = "would update"
	}
	fmt.Fprintf(w, "%s %s  (body: %d → %d lines)\n", verb, r.Name, countLines(before), countLines(after))
	if !r.DryRun {
		fmt.Fprintf(w, "  reminder: refresh the abstract on %s if the rule's meaning changed\n", r.Citation)
	}
	return nil
}
