package spec

import (
	"fmt"
	"io"
	"strings"

	"github.com/spf13/cobra"

	"github.com/hadron-memory/hadron-cli/internal/api"
	"github.com/hadron-memory/hadron-cli/internal/api/gen"
	"github.com/hadron-memory/hadron-cli/internal/cmdutil"
	"github.com/hadron-memory/hadron-cli/internal/config"
	"github.com/hadron-memory/hadron-cli/internal/exitcode"
	"github.com/hadron-memory/hadron-cli/internal/output"
)

// specDocumentDTO is the --json shape of `spec document` (cli#1692): the
// server's Markdown plus every entry in the server's document order. Field
// names are the merged SDL's (SpecDocument / SpecDocumentEntry); lists are
// [], never null.
type specDocumentDTO struct {
	Loc        string                 `json:"loc"`
	MemoryID   string                 `json:"memoryId"`
	RenderedAt string                 `json:"renderedAt"`
	Markdown   string                 `json:"markdown"`
	Entries    []specDocumentEntryDTO `json:"entries"`
}

type specDocumentEntryDTO struct {
	Loc            string           `json:"loc"`
	URN            string           `json:"urn"`
	Name           string           `json:"name"`
	Revision       int              `json:"revision"`
	Content        *string          `json:"content"`
	IsPlaceholder  bool             `json:"isPlaceholder"`
	ApprovalStatus *specApprovalDTO `json:"approvalStatus"`
	MintStatus     *specMintDTO     `json:"mintStatus"`
}

const specDocumentUnsupported = "this server does not provide the assembled spec document yet (hadron-server#1302); nothing was fetched and no fallback was tried"

func newCmdDocument(f *cmdutil.Factory) *cobra.Command {
	var memory, outFile string
	cmd := &cobra.Command{
		Use:   "document <citation>",
		Short: "Print a feature's specs as one server-assembled document",
		Long: `Print the specs of one feature as ONE document, exactly as the server
assembles it: type groups, state lines and rewritten links, the same document
the portal renders (cor:spc:030:02). The <citation> names the feature; the
document covers it and its live descendants.

This differs from ` + "`spec get --prefix`" + `, which dumps the stored specs node by node
with no assembly. ` + "`spec document`" + ` is read-only and does no rendering of its own:
the CLI prints the server's Markdown byte for byte. A newline is added only on a
terminal when the text lacks one, never into a pipe or file.

-o <file> writes exactly those bytes to the file (nothing added) and prints one
receipt line to stderr. The document is fetched first, so a refusal or a network
failure never creates, truncates or replaces the file; the write is atomic and
refuses a symlink. -o - means stdout. --json prints the Markdown plus every entry
in document order (citation, URN, name, revision, body, approval record, mint
record, placeholder flag); --json with -o <file> is a usage error, because the
file's format would be a guess.

The bytes depend on the caller's access (a spec you cannot read is not included)
and on the server's portal origin, so two callers can get different documents.
A feature over the server's spec cap is refused, never returned in part. A server
without the assembled document answers exit 7.`,
		Example: `  hadron spec document cor:spc:030 -m hrn:mem:hadronmemory.com:specs
  hadron spec document cor:spc:030 -o feature.md
  hadron spec document cor:spc:030 --json`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			loc, err := validateSpecLoc(args[0])
			if err != nil {
				return err
			}
			toFile := outFile != "" && outFile != "-"
			if f.JSON && toFile {
				return exitcode.Newf(exitcode.Usage, "--json cannot be combined with -o <file>: say whether the file should hold the Markdown (drop --json) or the JSON (redirect stdout)")
			}
			client, err := f.GraphQLClient()
			if err != nil {
				return err
			}
			memID, _, err := specMemoryID(f, cmd, client, memory)
			if err != nil {
				return err
			}
			resp, err := gen.SpecDocument(cmd.Context(), client, memID, loc)
			if err != nil {
				return specDocumentError(err)
			}
			// A success reply with no document ({}, null, no loc) is a malformed
			// answer (a real document always starts with its header, so empty Markdown is
			// never one): refuse it before anything is printed or a file is replaced.
			if resp == nil || resp.SpecDocument == nil || resp.SpecDocument.Loc == "" || resp.SpecDocument.Markdown == "" {
				return exitcode.Newf(exitcode.Error, "the server answered without a spec document for %s; nothing was printed or written", loc)
			}
			doc := resp.SpecDocument
			markdown := doc.Markdown

			if toFile {
				if err := config.WriteFileAtomic(outFile, []byte(markdown), 0o644); err != nil {
					return exitcode.Newf(exitcode.Error, "cannot write %s: %v", outFile, err)
				}
				fmt.Fprintf(f.IOStreams.ErrOut, "✓ wrote %s to %s (%d bytes)\n", loc, outFile, len(markdown))
				return nil
			}

			dto := specDocumentDTO{Loc: doc.Loc, MemoryID: doc.MemoryId, RenderedAt: doc.RenderedAt, Markdown: markdown, Entries: []specDocumentEntryDTO{}}
			for _, e := range doc.Entries {
				if e == nil {
					continue
				}
				ent := specDocumentEntryDTO{Loc: e.Loc, URN: e.Urn, Name: e.Name, Revision: e.Revision, Content: e.Content, IsPlaceholder: e.IsPlaceholder}
				if e.ApprovalStatus != nil {
					ent.ApprovalStatus = specApprovalFrom(&e.ApprovalStatus.NodeApprovalStatusFields)
				}
				if e.MintStatus != nil {
					ent.MintStatus = specMintFrom(&e.MintStatus.NodeMintStatusFields)
				}
				dto.Entries = append(dto.Entries, ent)
			}
			return output.Write(f.IOStreams, f.JSON, dto, func(w io.Writer) error {
				if _, err := io.WriteString(w, markdown); err != nil {
					return err
				}
				if f.IOStreams.IsTerminal() && markdown != "" && !strings.HasSuffix(markdown, "\n") {
					_, err := fmt.Fprintln(w)
					return err
				}
				return nil
			})
		},
	}
	cmd.Flags().StringVarP(&memory, "memory", "m", "", memoryFlagHelp)
	cmd.Flags().StringVarP(&outFile, "out", "o", "", `write the Markdown to this file ("-" or unset writes to stdout)`)
	return cmd
}

// specDocumentError maps the server's refusals: a server without the query is
// exit 7 with one fixed sentence (no fallback, no retry); over the cap is a
// usage error naming the cap, never a partial document; every other refusal
// goes through api.MapError unchanged, so an unreadable and a missing memory
// stay one answer.
func specDocumentError(err error) error {
	if api.IsGraphQLValidationFor(err, "specDocument") {
		return exitcode.Newf(exitcode.Unavailable, "%s", specDocumentUnsupported)
	}
	if api.HasErrorCode(err, "SPEC_DOCUMENT_TOO_LARGE") {
		if limit, ok := api.ExtensionInt(err, "cap"); ok {
			return exitcode.Newf(exitcode.Usage, "this feature has more specs than the document cap of %d: ask for a narrower citation", limit)
		}
		return exitcode.Newf(exitcode.Usage, "this feature has more specs than the document cap: ask for a narrower citation")
	}
	return api.MapError(err) // non-nil in, non-nil out: the cause is never dropped
}
