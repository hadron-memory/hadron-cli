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

// editResultDTO is the --json shape for `spec edit`. Changed is the overall
// "anything written" flag (body or abstract); BodyChanged/AbstractChanged break
// it down. Changed could only mean "body changed" before the abstract became
// editable here, so the broadening is backward-compatible.
type editResultDTO struct {
	Citation        string `json:"citation"`
	MemoryID        string `json:"memoryId"`
	Name            string `json:"name"`
	Changed         bool   `json:"changed"`
	BodyChanged     bool   `json:"bodyChanged"`
	AbstractChanged bool   `json:"abstractChanged"`
	DryRun          bool   `json:"dryRun"`
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
		memory       string
		content      string
		contentFile  string
		abstract     string
		abstractFile string
		dryRun       bool
	)
	cmd := &cobra.Command{
		Use:   "edit <citation>",
		Short: "Edit a spec's body and abstract in $EDITOR (or from flags)",
		Long: `Edit a spec's markdown body and its abstract in place — they are
one logical unit, so this command maintains both. By default it opens the
current abstract and body together in your $EDITOR (then $VISUAL, else vi)
pre-loaded — so you change the few lines you mean to, instead of reconstructing
them in a temp file and risking a transcription slip on a full replace. The two
fields are divided by sentinel comment lines; keep the body divider in place
(it's how the buffer is split back apart on save).

Pass any of --content -/--content-file/--abstract -/--abstract-file to replace a
field non-interactively (and skip the editor); supply both kinds to update body
and abstract in one call. A field whose flag is omitted is preserved untouched,
and a field that didn't actually change is not rewritten. Nothing changed writes
nothing.`,
		Example: `  hadron spec edit cor:dmo:060:02 -m hadronmemory.com::specs
  hadron spec edit msg:010:02 -m micromentor.org::platform-specs --dry-run
  cat rewrite.md | hadron spec edit msg:010:02 -m micromentor.org::platform-specs --content -
  hadron spec edit msg:010:02 -m micromentor.org::platform-specs --abstract-file abstract.md`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if _, err := ParseCitation(args[0]); err != nil {
				return err
			}
			changed := cmd.Flags().Changed
			// Each field's two input flags are mutually exclusive. Guard on
			// Changed() so an explicit empty value (clear the field) is caught too,
			// not just the value-based check inside ResolveTextInput.
			if changed("content") && changed("content-file") {
				return exitcode.Newf(exitcode.Usage, "--content and --content-file are mutually exclusive")
			}
			if changed("abstract") && changed("abstract-file") {
				return exitcode.Newf(exitcode.Usage, "--abstract and --abstract-file are mutually exclusive")
			}
			// Body and abstract can each read stdin via "-", but stdin is
			// consumable only once.
			if content == "-" && abstract == "-" {
				return exitcode.Newf(exitcode.Usage, "--content - and --abstract - cannot both read stdin")
			}
			contentProvided := changed("content") || changed("content-file")
			abstractProvided := changed("abstract") || changed("abstract-file")
			nonInteractive := contentProvided || abstractProvided

			client, err := f.GraphQLClient()
			if err != nil {
				return err
			}
			memURN, err := resolveSpecMemoryURN(cmd, client, memory)
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
			curBody, curAbstract := derefStr(node.Content), derefStr(node.Abstract)

			// A field defaults to its stored value (preserved); only a field the
			// caller actually supplies is replaced. CRLF→LF normalization is
			// applied solely to caller-supplied text — never to a preserved field,
			// or an abstract-only edit could flip a CRLF body to LF and write it,
			// breaking omit-to-preserve. (parseEditBuffer normalizes the editor
			// buffer itself, so both interactive fields arrive LF.)
			newBody, newAbstract := curBody, curAbstract
			if nonInteractive {
				if contentProvided {
					b, rerr := cmdutil.ResolveTextInput("content", content, contentFile, f.IOStreams.In)
					if rerr != nil {
						return rerr
					}
					newBody = strings.ReplaceAll(b, "\r\n", "\n")
				}
				if abstractProvided {
					a, rerr := cmdutil.ResolveTextInput("abstract", abstract, abstractFile, f.IOStreams.In)
					if rerr != nil {
						return rerr
					}
					newAbstract = strings.ReplaceAll(a, "\r\n", "\n")
				}
			} else {
				// The default seam (launchEditor) enforces the TTY requirement, so
				// overriding it in tests bypasses the terminal check cleanly. The
				// buffer carries both fields so body+abstract are one edit.
				edited, eerr := editorFunc(f.IOStreams, assembleEditBuffer(curAbstract, curBody))
				if eerr != nil {
					return eerr
				}
				if newAbstract, newBody, err = parseEditBuffer(edited); err != nil {
					return err
				}
			}

			result := editResultDTO{
				Citation:        node.Loc,
				MemoryID:        node.MemoryId,
				Name:            node.Name,
				BodyChanged:     newBody != curBody,
				AbstractChanged: newAbstract != curAbstract,
				DryRun:          dryRun,
			}
			result.Changed = result.BodyChanged || result.AbstractChanged

			if !result.Changed {
				return output.Write(f.IOStreams, f.JSON, result, func(w io.Writer) error {
					fmt.Fprintf(w, "no changes — %s left untouched\n", node.Loc)
					return nil
				})
			}
			render := func() error {
				return output.Write(f.IOStreams, f.JSON, result, func(w io.Writer) error {
					return renderEditResult(w, result, curBody, newBody)
				})
			}
			if dryRun {
				return render()
			}

			// Omitted fields are preserved; we set only what changed. An abstract
			// changed to empty sends "" — the server normalizes that to null
			// (clear), which is the intended "I removed the abstract". The node
			// is targeted by (memoryId, loc); updateNode never creates.
			input := gen.UpdateNodeInput{
				MemoryId: &node.MemoryId,
				Loc:      &node.Loc,
			}
			if result.BodyChanged {
				input.Content = &newBody
			}
			if result.AbstractChanged {
				input.Abstract = &newAbstract
			}
			if _, err := gen.UpdateNode(cmd.Context(), client, &input); err != nil {
				return api.MapError(err)
			}
			return render()
		},
	}
	cmd.Flags().StringVarP(&memory, "memory", "m", "", "memory ID or fully-qualified URN (required)")
	cmd.Flags().StringVarP(&content, "content", "c", "", `replace the body with this value ("-" reads stdin) instead of opening $EDITOR`)
	cmd.Flags().StringVar(&contentFile, "content-file", "", "replace the body with a file's contents instead of opening $EDITOR")
	cmd.Flags().StringVar(&abstract, "abstract", "", `replace the abstract with this value ("-" reads stdin) instead of opening $EDITOR`)
	cmd.Flags().StringVar(&abstractFile, "abstract-file", "", "replace the abstract with a file's contents instead of opening $EDITOR")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "show what would change without writing")
	_ = cmd.MarkFlagRequired("memory")
	return cmd
}

// edit-buffer sentinels. The interactive editor sees the abstract and body in
// one buffer split by these comment lines; the body divider is load-bearing
// (parseEditBuffer refuses to write if it's gone), the abstract divider just
// labels the top region.
const (
	abstractDivider = "<!-- === ABSTRACT === one paragraph; the spec's RAG retrieval surface. Edit below this line. -->"
	bodyDivider     = "<!-- === BODY === the spec markdown. Edit below this line. -->"
)

// assembleEditBuffer lays out the abstract above the body, divided by the
// sentinels, for a single $EDITOR pass. The body is written verbatim and last
// so an untouched buffer round-trips exactly (a no-op).
func assembleEditBuffer(abstract, body string) string {
	return abstractDivider + "\n" + abstract + "\n" + bodyDivider + "\n" + body
}

// parseEditBuffer splits an edited buffer back into (abstract, body). The body
// divider is the only load-bearing split: everything below it is the body
// (verbatim), everything above it — minus the abstract-divider label line — is
// the abstract (trimmed), so any stray text the author leaves above the label
// is kept rather than silently dropped. A missing body divider is a hard error:
// we refuse to guess where the body begins rather than truncate. CRLF is
// normalized up front so the matched fields are LF and divider detection is an
// exact line equality — an indented marker-like line inside the prose is real
// content, not a divider.
func parseEditBuffer(s string) (abstract, body string, err error) {
	lines := strings.Split(strings.ReplaceAll(s, "\r\n", "\n"), "\n")
	bodyIdx := -1
	for i, ln := range lines {
		if ln == bodyDivider {
			bodyIdx = i
			break
		}
	}
	if bodyIdx == -1 {
		return "", "", exitcode.Newf(exitcode.Usage,
			"the body divider was removed from the buffer — aborting without writing; keep the %q line so the abstract and body can be split apart", bodyDivider)
	}
	var absLines []string
	for _, ln := range lines[:bodyIdx] {
		if ln != abstractDivider {
			absLines = append(absLines, ln)
		}
	}
	abstract = strings.TrimSpace(strings.Join(absLines, "\n"))
	body = strings.Join(lines[bodyIdx+1:], "\n")
	return abstract, body, nil
}

// derefStr returns the string a *string points at, or "" if nil.
func derefStr(s *string) string {
	if s == nil {
		return ""
	}
	return *s
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

func renderEditResult(w io.Writer, r editResultDTO, beforeBody, afterBody string) error {
	verb := "✓ updated"
	if r.DryRun {
		verb = "would update"
	}
	fmt.Fprintf(w, "%s %s\n", verb, r.Name)
	if r.BodyChanged {
		fmt.Fprintf(w, "  body: %d → %d lines\n", countLines(beforeBody), countLines(afterBody))
	}
	if r.AbstractChanged {
		fmt.Fprintln(w, "  abstract: updated")
	}
	// Only nudge about the abstract when the body changed but the abstract
	// didn't — now that the abstract is editable here, a meaning shift is easy
	// to fold into the same command.
	if !r.DryRun && r.BodyChanged && !r.AbstractChanged {
		fmt.Fprintf(w, "  reminder: refresh the abstract on %s with --abstract/--abstract-file if the rule's meaning changed\n", r.Citation)
	}
	return nil
}
