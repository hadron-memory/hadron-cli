package spec

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"

	"github.com/Khan/genqlient/graphql"
	"github.com/aymanbagabas/go-udiff"
	"github.com/spf13/cobra"

	"github.com/hadron-memory/hadron-cli/internal/api"
	"github.com/hadron-memory/hadron-cli/internal/api/gen"
	"github.com/hadron-memory/hadron-cli/internal/cmdutil"
	"github.com/hadron-memory/hadron-cli/internal/exitcode"
	"github.com/hadron-memory/hadron-cli/internal/output"
)

// editResultDTO is the --json shape for `spec edit`. Changed is the overall
// "anything written" flag (body, abstract or description); the field flags break
// it down. Changed could only mean "body changed" before the abstract became
// editable here, so the broadening is backward-compatible.
type editResultDTO struct {
	Citation           string `json:"citation"`
	MemoryID           string `json:"memoryId"`
	NodeID             string `json:"nodeId"`
	Name               string `json:"name"`
	Changed            bool   `json:"changed"`
	BodyChanged        bool   `json:"bodyChanged"`
	AbstractChanged    bool   `json:"abstractChanged"`
	DescriptionChanged bool   `json:"descriptionChanged"`
	// AbstractReaffirmed reports that the abstract was re-sent UNCHANGED to
	// re-fingerprint it against the current body (#612). Additive, and it is
	// counted into Changed because the call really does write — a `changed:
	// false` beside a write would be the DTO lying to the agent parsing it.
	AbstractReaffirmed bool `json:"abstractReaffirmed"`
	DryRun             bool `json:"dryRun"`
	// Changes is the proposal itself, one entry per field it writes (cli#737):
	// the flags above say THAT a field changes, and a reviewer approving an
	// edit needs to see WHAT. Always a list, `[]` on a no-op. Built from the
	// same editProposal the write is built from: the changes a run reports are
	// exactly what that run writes. (A dry run and a later real run are two
	// reads of the spec; closing that gap is cli#738's.)
	Changes []fieldChangeDTO `json:"changes"`
	// Revision and NodeID identify the snapshot this proposal is based on.
	// Carry both from a dry run to an approved save: a citation can be reused
	// by a different node at the same revision.
	Revision int `json:"revision"`
	// ProposalHash binds a later approved save to the exact write input shown
	// in the preview, including which fields are written and their text.
	ProposalHash string `json:"proposalHash"`
}

// fieldChangeDTO is one field of a proposed edit: the text as stored (read
// raw, placeholders intact), the text proposed, and a unified diff of the two.
type fieldChangeDTO struct {
	Field string `json:"field"` // "content", "abstract" or "description"
	// Change is "replaced", "cleared" (the proposed text is empty) or
	// "reaffirmed" (--abstract-still-accurate: the same text re-sent so it is
	// re-fingerprinted against the body; Before == After and Diff is "").
	Change string `json:"change"`
	Before string `json:"before"`
	After  string `json:"after"`
	Diff   string `json:"diff"`
}

// editProposal is the one computed change `spec edit` makes. The dry-run
// renders it and the write is built from it; nothing between the two
// recomputes anything.
type editProposal struct {
	curBody, curAbstract, curDescription string
	newBody, newAbstract, newDescription string
	reaffirm                             bool
	// originHash is the stored abstract's fingerprint (spec 032), nil when it
	// was never fingerprinted.
	originHash *string
	// The write targets the immutable ID read with this revision. A loc can be
	// vacated and reused by another node at the same revision.
	baseNodeID   string
	baseRevision int
}

func (p editProposal) bodyChanged() bool        { return p.newBody != p.curBody }
func (p editProposal) descriptionChanged() bool { return p.newDescription != p.curDescription }

// abstractChanged: the server stores an empty or whitespace-only abstract as
// null, so blank → blank is no change (and writes nothing).
func (p editProposal) abstractChanged() bool {
	if strings.TrimSpace(p.newAbstract) == "" && strings.TrimSpace(p.curAbstract) == "" {
		return false
	}
	return p.newAbstract != p.curAbstract
}

// armsAbstractStale reports whether a body-only save leaves the kept abstract
// STALE: answered by abstractVerification (citations.go) over the state the
// save produces, the same function `spec lint` uses, so the message cannot
// disagree with the marker. A spec with no abstract or an emptied body is
// not-applicable, one never fingerprinted stays unverified, and a body edited
// back to its fingerprinted text becomes verified; none of those is stale
// (#740 review, Codex and Copilot).
func (p editProposal) armsAbstractStale() bool {
	if !p.bodyChanged() || p.abstractChanged() || p.reaffirm {
		return false
	}
	abstract, body := p.curAbstract, p.newBody
	after := specNode{ContentIsRaw: true, Abstract: &abstract, Content: &body, AbstractOriginHash: p.originHash}
	return abstractVerification(after) == abstractStale
}

// changes lists the proposal field by field, body first.
func (p editProposal) changes() []fieldChangeDTO {
	out := []fieldChangeDTO{}
	if p.bodyChanged() {
		out = append(out, fieldChange("content", p.curBody, p.newBody))
	}
	if p.descriptionChanged() {
		out = append(out, fieldChange("description", p.curDescription, p.newDescription))
	}
	switch {
	case p.abstractChanged():
		out = append(out, fieldChange("abstract", p.curAbstract, p.newAbstract))
	case p.reaffirm:
		out = append(out, fieldChangeDTO{Field: "abstract", Change: "reaffirmed", Before: p.curAbstract, After: p.curAbstract})
	}
	return out
}

func fieldChange(field, before, after string) fieldChangeDTO {
	change := "replaced"
	// The server stores an empty or whitespace-only ABSTRACT as null (spec
	// 031), so sending one clears it: `--abstract-file` on a file holding only
	// a newline is a clear, and must say so. A body is stored as sent.
	if after == "" || (field == "abstract" && strings.TrimSpace(after) == "") {
		change = "cleared"
	}
	return fieldChangeDTO{
		Field: field, Change: change, Before: before, After: after,
		Diff: udiff.Unified(field+" (stored)", field+" (proposed)", before, after),
	}
}

// input is the write. Omitted fields are preserved; only what changed is set.
// An abstract changed to empty sends "" — the server normalizes that to null
// (clear), which is the intended "I removed the abstract". The node is
// targeted by immutable node ID; updateNode never creates.
func (p editProposal) input() gen.UpdateNodeInput {
	// ALWAYS guarded (cli#738): the server applies the write only while the
	// spec is still at the revision this proposal was computed against, and
	// refuses with NODE_WRITE_CONFLICT otherwise. There is no unguarded path.
	rev := p.baseRevision
	id := p.baseNodeID
	in := gen.UpdateNodeInput{Id: &id, ExpectedRevision: &rev}
	if p.bodyChanged() {
		body := p.newBody
		in.Content = &body
	}
	if p.descriptionChanged() {
		description := p.newDescription
		in.Description = &description
	}
	switch {
	case p.abstractChanged():
		abstract := p.newAbstract
		in.Abstract = &abstract
	case p.reaffirm:
		// Spec 032, verified against hadron-server `origin/main` d7ef615
		// (resolvers.mutation.node.ts): an `abstract` supplied as a STRING
		// is re-fingerprinted against the post-update content — `input.content`
		// when supplied, the stored body otherwise. So re-sending the
		// stored text verbatim is exactly the assertion, and it works on
		// the GraphQL path with no server change.
		//
		// It is NOT `abstractStillAccurate`: that argument exists only on
		// the MCP surface (#1126) and has no GraphQL equivalent, which is
		// the parity gap reported on #612.
		abstract := p.curAbstract
		in.Abstract = &abstract
	}
	return in
}

// proposalHash fingerprints the exact mutation input computed from a preview.
// A file or stdin can change between preview and save without changing the
// node's revision, so the baseline guard alone cannot protect approval.
func (p editProposal) proposalHash() (string, error) {
	b, err := json.Marshal(p.input())
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("sha256:%x", sha256.Sum256(b)), nil
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
		memory          string
		content         string
		contentFile     string
		abstract        string
		abstractFile    string
		description     string
		descriptionFile string
		stillAccurate   bool
		dryRun          bool
		expectedRev     int
		expectedID      string
		expectedHash    string
	)
	cmd := &cobra.Command{
		Use:   "edit <citation>",
		Short: "Edit a spec's body, abstract and description with guarded writes",
		// "update" is the intuitive verb for this and reads nothing
		// like "edit", so distance-based suggestion never finds it.
		SuggestFor: []string{"update", "modify", "change", "set"},
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
nothing. The body is read as stored, {{…}} placeholders intact, never rendered.

The description shown by spec list and search is edited with --description or
--description-file, through the same guarded spec write. It is not part of the
default editor buffer; supply its flag to edit it alone or together with the
body and abstract. The description is preserved when its flag is omitted.

An abstract replacement may contain at most 2000 characters as counted by the
server (UTF-16 code units, including surrounding whitespace and newlines).
Both --dry-run and a save refuse an over-limit proposal before writing.

--dry-run writes nothing and shows the change itself: a unified diff per field
(--json: "changes", each with the stored text, the proposed text and the diff).
It is a preview, not an approval; applying it is a separate run without
--dry-run, recomputed against the spec as stored at that moment.

EVERY SAVE IS GUARDED (cli#738). The write targets the immutable node ID read
with the body and carries that node's revision. A changed node is refused by
the server, and a replacement at the same citation cannot receive the write.
To save a proposal previewed and APPROVED earlier (another turn or process),
pass ALL THREE values the dry run reported: --expected-revision N,
--expected-node-id ID and --expected-proposal-hash HASH. If the node, revision
or proposed write changed, your proposed edit is not applied (exit 5). A
pre-write refusal writes nothing. On a server-reported write-time conflict,
the server may record out-of-band drift as a revision while refusing your edit;
your proposed text is saved to a file when possible. A failed file save is
reported explicitly. Re-read, reconcile, and get the proposal approved again.
A server that cannot guard a save (predating hadron-server#1339 or #1352) is
refused, never written to unguarded.

Editing the body alone ARMS the abstract-stale marker: the abstract was
fingerprinted against the old content, so every later read flags it as a
possibly-outdated preview. That is intended — but preserving an unchanged field
by omission is also what makes it unclearable here, because re-running with the
same abstract writes nothing. --abstract-still-accurate is the way out: it
asserts you re-read the abstract and it still describes the spec, and re-sends it
unchanged so the server re-fingerprints it against the new body. Use it with a
body edit, or on its own to settle a marker a previous edit left behind.

It is an assertion, not a formality: the marker is a prompt to check, and
re-affirming an abstract you have not re-read is the one thing it must not be
used for. It is refused alongside --abstract/--abstract-file (replacing the
abstract re-fingerprints it anyway), on a spec with no abstract at all, and on
a legacy abstract past the server cap — re-affirming replaces it, and a
replacement over the cap is rejected.`,
		Example: `  hadron spec edit cor:dmo:060:02 -m hrn:mem:hadronmemory.com:specs
  hadron spec edit msg:010:02 -m hrn:mem:micromentor.org:platform-specs --dry-run
  hadron spec edit msg:010:02 -m hrn:mem:micromentor.org:platform-specs --content-file body.md --expected-revision 7 --expected-node-id <id-from-preview> --expected-proposal-hash <hash-from-preview>
  cat rewrite.md | hadron spec edit msg:010:02 -m hrn:mem:micromentor.org:platform-specs --content -
  hadron spec edit msg:010:02 -m hrn:mem:micromentor.org:platform-specs --abstract-file abstract.md
  hadron spec edit msg:010:02 -m hrn:mem:micromentor.org:platform-specs --description-file description.md
  hadron spec edit cor:agt:020 -m hrn:mem:hadronmemory.com:specs --content-file body.md --abstract-still-accurate
  hadron spec edit cor:agt:020 -m hrn:mem:hadronmemory.com:specs --abstract-still-accurate`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if _, err := validateSpecLoc(args[0]); err != nil {
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
			if changed("description") && changed("description-file") {
				return exitcode.Newf(exitcode.Usage, "--description and --description-file are mutually exclusive")
			}
			// The flag ASSERTS that the stored abstract survives this edit, so
			// replacing that abstract in the same call contradicts it. Caught at
			// parse time, before the node is read or an editor opens.
			if stillAccurate && (changed("abstract") || changed("abstract-file")) {
				return exitcode.Newf(exitcode.Usage,
					"--abstract-still-accurate asserts the STORED abstract survives this edit, so it cannot be combined with --abstract/--abstract-file (replacing the abstract re-fingerprints it anyway)")
			}
			// Body and abstract can each read stdin via "-", but stdin is
			// consumable only once.
			stdinInputs := 0
			for _, value := range []string{content, abstract, description} {
				if value == "-" {
					stdinInputs++
				}
			}
			if stdinInputs > 1 {
				return exitcode.Newf(exitcode.Usage, "only one of --content -, --abstract -, and --description - can read stdin")
			}
			if err := refuseDocumentStdin(f.IOStreams.IsInputTerminal(), content, abstract); err != nil {
				return err
			}
			if description == "-" {
				if err := cmdutil.RefuseDocumentStdinFromTerminal(f.IOStreams.IsInputTerminal(), "--description -", "--description-file"); err != nil {
					return err
				}
			}
			if changed("expected-revision") && expectedRev < 1 {
				return exitcode.Newf(exitcode.Usage,
					"--expected-revision must be a positive revision number — use it with --expected-node-id and --expected-proposal-hash from the same `spec edit --dry-run`")
			}
			if changed("expected-revision") != changed("expected-node-id") ||
				changed("expected-revision") != changed("expected-proposal-hash") ||
				(changed("expected-node-id") && strings.TrimSpace(expectedID) == "") ||
				(changed("expected-proposal-hash") && strings.TrimSpace(expectedHash) == "") {
				return exitcode.Newf(exitcode.Usage,
					"--expected-revision, --expected-node-id and --expected-proposal-hash must be passed together, using the values from the same `spec edit --dry-run`")
			}
			contentProvided := changed("content") || changed("content-file")
			abstractProvided := changed("abstract") || changed("abstract-file")
			descriptionProvided := changed("description") || changed("description-file")
			// The assertion on its own is a complete, non-interactive operation:
			// it is the way to clear an `abstract-stale` marker without editing
			// anything, which is the half of #612 the command had no answer for.
			nonInteractive := contentProvided || abstractProvided || descriptionProvided || stillAccurate

			client, err := f.GraphQLClient()
			if err != nil {
				return err
			}
			memURN, err := specMemoryURN(f, cmd, client, memory)
			if err != nil {
				return err
			}
			// RAW: the stored body, placeholders intact. Everything below —
			// the editor buffer, the preview and the write — starts from it.
			node, err := fetchSpecForEdit(cmd, client, memURN, args[0])
			if err != nil {
				return err
			}
			// The guard's base (cli#738). With --expected-revision the caller
			// names the node and revision their proposal was approved against;
			// a replacement at the same citation or a changed node is refused HERE, before
			// an editor opens or a preview is computed against text nobody
			// approved. The server enforces the same comparison on the write.
			base := node.Revision
			if changed("expected-revision") {
				if node.Id != expectedID {
					return exitcode.Newf(exitcode.Conflict,
						"%s now names node %s, not the previewed node %s — nothing was written. Re-read the spec and get the proposal approved again before saving",
						args[0], node.Id, expectedID)
				}
				if node.Revision != expectedRev {
					return exitcode.Newf(exitcode.Conflict,
						"%s changed since revision %d (it is at revision %d now) — nothing was written. Re-read it (`spec edit --dry-run` shows the stored text), reconcile your proposal with the change, and get it approved again before saving",
						node.Loc, expectedRev, node.Revision)
				}
				base = expectedRev
			}
			curBody, curAbstract, curDescription := derefStr(node.Content), derefStr(node.Abstract), derefStr(node.Description)

			// A field defaults to its stored value (preserved); only a field the
			// caller actually supplies is replaced. CRLF→LF normalization is
			// applied solely to caller-supplied text — never to a preserved field,
			// or an abstract-only edit could flip a CRLF body to LF and write it,
			// breaking omit-to-preserve. (parseEditBuffer normalizes the editor
			// buffer itself, so both interactive fields arrive LF.)
			newBody, newAbstract, newDescription := curBody, curAbstract, curDescription
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
				if descriptionProvided {
					d, rerr := cmdutil.ResolveTextInput("description", description, descriptionFile, f.IOStreams.In)
					if rerr != nil {
						return rerr
					}
					newDescription = strings.ReplaceAll(d, "\r\n", "\n")
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

			// Re-affirming is only meaningful when the abstract is NOT also
			// being replaced — a replacement is fingerprinted on its own.
			proposal := editProposal{
				curBody: curBody, curAbstract: curAbstract, curDescription: curDescription,
				newBody: newBody, newAbstract: newAbstract, newDescription: newDescription,
				reaffirm:     stillAccurate && newAbstract == curAbstract,
				originHash:   node.AbstractOriginHash,
				baseNodeID:   node.Id,
				baseRevision: base,
			}
			if proposal.abstractChanged() && abstractLength(&newAbstract) > abstractHardMax {
				return exitcode.Newf(exitcode.Usage,
					"abstract exceeds the %d-character cap (%d characters); shorten --abstract/--abstract-file before previewing or saving",
					abstractHardMax, abstractLength(&newAbstract))
			}
			proposalHash, err := proposal.proposalHash()
			if err != nil {
				return fmt.Errorf("fingerprint spec edit proposal: %w", err)
			}
			if changed("expected-proposal-hash") && proposalHash != expectedHash {
				return exitcode.Newf(exitcode.Conflict,
					"%s has a different proposed write than the approved dry run — nothing was written. Re-run `spec edit --dry-run` and get this proposal approved again", node.Loc)
			}
			result := editResultDTO{
				Citation:           node.Loc,
				MemoryID:           node.MemoryId,
				NodeID:             node.Id,
				Name:               node.Name,
				BodyChanged:        proposal.bodyChanged(),
				AbstractChanged:    proposal.abstractChanged(),
				DescriptionChanged: proposal.descriptionChanged(),
				AbstractReaffirmed: proposal.reaffirm,
				DryRun:             dryRun,
				Changes:            proposal.changes(),
				Revision:           base,
				ProposalHash:       proposalHash,
			}
			// A legacy abstract PAST the server's cap cannot be re-sent
			// (@codex on #613). Omitting it preserves it — which is exactly why
			// such a node still works today — but re-affirming REPLACES it, and
			// the server rejects a replacement over the cap. So the assertion
			// would fail at write time on precisely the nodes `abstract-length`
			// already calls out as legacy data.
			//
			// Refused with the remedy rather than attempted and mapped: the fix
			// is to shorten the abstract, which is an edit the author has to make
			// anyway, and `--abstract-file` re-fingerprints it in the same call.
			if result.AbstractReaffirmed && abstractLength(&curAbstract) > abstractHardMax {
				return exitcode.Newf(exitcode.Usage,
					"%s has a %d-char abstract, past the %d-char cap — re-affirming REPLACES it and the server rejects a replacement over the cap (omitting it, which is what preserves it today, is what armed the marker). Shorten it with --abstract/--abstract-file, which re-fingerprints it in the same write",
					node.Loc, abstractLength(&curAbstract), abstractHardMax)
			}
			if result.AbstractReaffirmed && strings.TrimSpace(curAbstract) == "" {
				// REFUSE rather than send "". The server normalizes an empty
				// abstract to null, so the assertion would CLEAR the field it
				// claims to be vouching for — a destructive read of a flag whose
				// whole point is "leave it as it is".
				return exitcode.Newf(exitcode.Usage,
					"%s has no abstract, so there is nothing to re-affirm — --abstract-still-accurate would clear the field rather than re-fingerprint it; write one with --abstract/--abstract-file instead", node.Loc)
			}
			result.Changed = result.BodyChanged || result.AbstractChanged || result.DescriptionChanged || result.AbstractReaffirmed

			if !result.Changed {
				return output.Write(f.IOStreams, f.JSON, result, func(w io.Writer) error {
					fmt.Fprintf(w, "no changes — %s left untouched\n", node.Loc)
					if dryRun {
						// The same closing line as every other dry run, so a
						// no-op preview reads no differently as to what it is.
						fmt.Fprintln(w, dryRunDisclaimer)
						fmt.Fprintln(w, revisionLine(result.Revision, result.NodeID, result.ProposalHash))
					}
					return nil
				})
			}
			render := func() error {
				return output.Write(f.IOStreams, f.JSON, result, func(w io.Writer) error {
					return renderEditResult(w, result, curBody, newBody, proposal.armsAbstractStale())
				})
			}
			if dryRun {
				// Zero writes: the preview returns before any mutation is built.
				return render()
			}

			input := proposal.input()
			if _, err := api.UpdateSpecNode(cmd.Context(), client, &input); err != nil {
				if api.HasErrorCode(err, "NODE_WRITE_CONFLICT") {
					proposalText := assembleEditBuffer(newAbstract, newBody)
					if proposal.descriptionChanged() {
						proposalText = "<!-- Proposed description (--description-file): -->\n" + newDescription + "\n\n" + proposalText
					}
					return conflictRefusal(node.Loc, base, proposalText)
				}
				if unsupportedGuard(err) {
					return exitcode.Newf(exitcode.Usage,
						"this server does not support guarded saves (UpdateNodeInput.expectedRevision, hadron-server#1352), so %s cannot be saved without risking a silent overwrite of a concurrent change — nothing was written; spec edit needs a server with #1352",
						node.Loc)
				}
				return api.MapError(err)
			}
			return render()
		},
	}
	cmd.Flags().StringVarP(&memory, "memory", "m", "", "memory ID or fully-qualified URN (defaults to the memory set by hadron spec use, then the active memory)")
	cmd.Flags().StringVarP(&content, "content", "c", "", `replace the body with this value ("-" reads piped stdin, refused from a terminal) instead of opening $EDITOR`)
	cmd.Flags().StringVar(&contentFile, "content-file", "", "replace the body with a file's contents instead of opening $EDITOR")
	cmd.Flags().StringVar(&abstract, "abstract", "", `replace the abstract (max 2000 UTF-16 code units incl whitespace/newlines); "-" reads piped stdin, refused from a terminal`)
	cmd.Flags().StringVar(&abstractFile, "abstract-file", "", "replace the abstract with a file's contents (max 2000 UTF-16 code units incl whitespace/newlines)")
	cmd.Flags().StringVar(&description, "description", "", `replace the description with this value ("-" reads piped stdin, refused from a terminal) through the guarded spec write`)
	cmd.Flags().StringVar(&descriptionFile, "description-file", "", "replace the description with a file's contents through the guarded spec write")
	// No backticks in this usage string: cobra's UnquoteUsage reads backquoted
	// text as the flag's placeholder name, so "clearing `abstract-stale`" would
	// rename the flag's argument in --help (review:backticks-in-flag-usage-become-the-placeholder).
	cmd.Flags().BoolVar(&stillAccurate, "abstract-still-accurate", false,
		"assert you re-read the abstract and it still describes the spec: re-sends it unchanged so it is re-fingerprinted against the body, refreshing its verification")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "show the proposed change as a diff of the stored text, without writing (a preview, not an approval)")
	cmd.Flags().IntVar(&expectedRev, "expected-revision", 0, "the revision from an approved dry run; use with --expected-node-id and --expected-proposal-hash")
	cmd.Flags().StringVar(&expectedID, "expected-node-id", "", "the immutable node ID from the same dry run as --expected-revision and --expected-proposal-hash")
	cmd.Flags().StringVar(&expectedHash, "expected-proposal-hash", "", "the proposalHash from the same dry run as --expected-node-id and --expected-revision; refused if the proposed write changed")
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

// fetchRawSpec is fetchSpecTaggedNode over the RAW read (GetSpecNodeRaw): the
// body as stored, never Mustache-rendered. The same address resolution and the
// same not-a-spec refusal; only the read differs. Every command that WRITES a
// body it read uses a raw read (cli#737 edit, cli#742 supersede and extract).
func fetchRawSpec(cmd *cobra.Command, client graphql.Client, memoryURN, loc string) (*gen.GetSpecNodeRawNode, error) {
	loc, err := validateSpecLoc(loc)
	if err != nil {
		return nil, err
	}
	id, err := resolveSpecNode(cmd, client, memoryURN, loc)
	if err != nil {
		return nil, err
	}
	resp, err := gen.GetSpecNodeRaw(cmd.Context(), client, id)
	if err != nil {
		return nil, api.MapError(err)
	}
	if resp.Node == nil {
		return nil, exitcode.Newf(exitcode.NotFound, "spec %q not found", loc)
	}
	if !isSpec(resp.Node.Tags, resp.Node.Role) {
		return nil, exitcode.Newf(exitcode.Usage, "%s is not a spec (no \"spec\" tag or spec role)", resp.Node.Loc)
	}
	return resp.Node, nil
}

// fetchSpecForEdit is fetchRawSpec plus the node's live revision, from ONE
// read (GetSpecNodeForEdit, cli#738): the guarded save needs the revision the
// body was read at, never a second read's. A server predating Node.revision
// (hadron-server#1339) cannot guard a save, so it is refused plainly rather
// than edited unguarded.
func fetchSpecForEdit(cmd *cobra.Command, client graphql.Client, memoryURN, loc string) (*gen.GetSpecNodeForEditNode, error) {
	loc, err := validateSpecLoc(loc)
	if err != nil {
		return nil, err
	}
	id, err := resolveSpecNode(cmd, client, memoryURN, loc)
	if err != nil {
		return nil, err
	}
	resp, err := gen.GetSpecNodeForEdit(cmd.Context(), client, id)
	if err != nil {
		if m := err.Error(); strings.Contains(m, "revision") &&
			(strings.Contains(m, "Cannot query field") || strings.Contains(m, "Unknown field") || api.HasErrorCode(err, "GRAPHQL_VALIDATION_FAILED")) {
			return nil, exitcode.Newf(exitcode.Usage,
				"this server predates node revisions (hadron-server#1339), so spec edit cannot guard a save against a concurrent change — refusing rather than write unguarded")
		}
		return nil, api.MapError(err)
	}
	if resp.Node == nil {
		return nil, exitcode.Newf(exitcode.NotFound, "spec %q not found", loc)
	}
	if !isSpec(resp.Node.Tags, resp.Node.Role) {
		return nil, exitcode.Newf(exitcode.Usage, "%s is not a spec (no \"spec\" tag or spec role)", resp.Node.Loc)
	}
	return resp.Node, nil
}

// withRawBody replaces a GetNode read's body with the STORED one, read raw by
// id. For a command that needs GetNode's structure (edges, role) and also
// writes the body back: GetNode renders Mustache, and a rendered body written
// back deletes every `{{…}}` placeholder (cli#742). The abstract, its
// fingerprint and the tags come from the SAME read: callers write them
// together (the retirement replaces content AND tags; --copy-body copies body
// AND abstract), so they must be one snapshot, never a fresh body beside a
// stale abstract or tags an intervening edit changed (#743 review).
func withRawBody(cmd *cobra.Command, client graphql.Client, n *gen.GetNodeNode) error {
	resp, err := gen.GetSpecNodeRaw(cmd.Context(), client, n.Id)
	if err != nil {
		return api.MapError(err)
	}
	if resp.Node == nil {
		return exitcode.Newf(exitcode.NotFound, "spec %q not found", n.Loc)
	}
	n.Content = resp.Node.Content
	n.Abstract = resp.Node.Abstract
	n.AbstractOriginHash = resp.Node.AbstractOriginHash
	n.Tags = tagsOrEmpty(resp.Node.Tags)
	return nil
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

// dryRunDisclaimer closes every dry run, a no-op included.
// revisionLine tells a dry-run reader how to save EXACTLY what they reviewed:
// the immutable node ID and revision must both match on the later run.
func revisionLine(rev int, id, hash string) string {
	return fmt.Sprintf("based on node %s at revision %d: to save exactly this proposal later, add --expected-node-id %s --expected-revision %d --expected-proposal-hash %s — a changed node, revision or proposal is refused. A pre-write refusal writes nothing; on a server-reported write-time conflict, your proposed edit is not applied but the server may record out-of-band drift as a revision.", id, rev, id, rev, hash)
}

// conflictRefusal is the NODE_WRITE_CONFLICT answer (cli#738): the proposed
// edit was not applied, but the server may have recorded drift. It saves the
// proposal to a file when possible, since it may exist only in an editor buffer
// or piped stdin. A failed spill is reported plainly; the message must not
// promise a copy that does not exist.
func conflictRefusal(loc string, base int, proposal string) error {
	path, err := spillEditProposal(proposal)
	if err != nil {
		return exitcode.Newf(exitcode.Conflict,
			"%s changed since revision %d — your proposed edit was not applied, but the server may have recorded out-of-band drift as a revision. The proposed text could not be saved to a file (%v). Do not assume it was kept; recover it from its original input, then re-read the spec, reconcile your proposal, and get it approved again before saving",
			loc, base, err)
	}
	return exitcode.Newf(exitcode.Conflict,
		"%s changed since revision %d, which this edit was based on — your proposed edit was not applied, but the server may have recorded out-of-band drift as a revision. Your proposed text is kept (saved at %s). Re-read the spec, reconcile your proposal with the change, and get it approved again before saving",
		loc, base, path)
}

func spillEditProposal(proposal string) (string, error) {
	f, err := os.CreateTemp("", "hadron-spec-edit-*.md")
	if err != nil {
		return "", err
	}
	path := f.Name()
	n, err := f.WriteString(proposal)
	if err == nil && n != len(proposal) {
		err = io.ErrShortWrite
	}
	if err != nil {
		_ = f.Close()
		_ = os.Remove(path)
		return "", err
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(path)
		return "", err
	}
	return path, nil
}

// unsupportedGuard reports the server refusing the guard itself: a build
// predating UpdateNodeInput.expectedRevision (hadron-server#1352) rejects the
// field at validation, before anything runs.
func unsupportedGuard(err error) bool {
	msg := err.Error()
	return strings.Contains(msg, "expectedRevision") &&
		(strings.Contains(msg, "is not defined") || strings.Contains(msg, "Unknown field") || strings.Contains(msg, "GRAPHQL_VALIDATION_FAILED"))
}

const dryRunDisclaimer = "dry run: nothing was written, and this preview is not an approval. Applying it is a separate `spec edit` run without --dry-run, which recomputes the change against the spec as stored at that moment."

func renderEditResult(w io.Writer, r editResultDTO, beforeBody, afterBody string, armsStale bool) error {
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
	if r.DescriptionChanged {
		fmt.Fprintln(w, "  description: updated")
	}
	if r.AbstractReaffirmed {
		fmt.Fprintln(w, "  abstract: unchanged, re-fingerprinted against this body (verification refreshed)")
	}
	// Only nudge about the abstract when the body changed but a kept abstract
	// didn't (armsAbstractStale) — now that the abstract is editable here, a
	// meaning shift is easy to fold into the same command. With no abstract, or
	// an emptied body, nothing can go stale and the reaffirm remedy would be
	// refused, so there is nothing true to say.
	//
	// SUPPRESSED once re-affirmed, and that is the #612 fix as much as the flag
	// is: the reminder asks the author to decide whether the abstract survived
	// the edit, and --abstract-still-accurate is them answering it. Printing it
	// anyway would ask a question they just answered, and leave the command
	// still appearing to have no way to settle the marker.
	if armsStale {
		if r.DryRun {
			// A consequence of saving, so the reviewer sees it before approving.
			fmt.Fprintf(w, "  note: saving this changes the body and not the abstract, so it would arm abstract-stale on %s — add --abstract/--abstract-file if the rule's meaning changed, or --abstract-still-accurate if you re-read the abstract and it still describes the spec\n", r.Citation)
		} else {
			fmt.Fprintf(w, "  reminder: refresh the abstract on %s with --abstract/--abstract-file if the rule's meaning changed — or, if you re-read it and it still describes the spec, re-affirm it with --abstract-still-accurate (the body edit has armed abstract-stale either way)\n", r.Citation)
		}
	}
	if r.DryRun {
		// The change itself, not only its summary (cli#737): a reviewer asked
		// to approve an edit has to be able to read it. Printed verbatim and
		// unindented, so the diff stays a diff (and pastes into `patch`).
		for _, c := range r.Changes {
			if c.Diff == "" {
				continue
			}
			fmt.Fprintf(w, "\n%s", c.Diff)
		}
		fmt.Fprintln(w, "\n"+dryRunDisclaimer)
		fmt.Fprintln(w, revisionLine(r.Revision, r.NodeID, r.ProposalHash))
	}
	return nil
}
