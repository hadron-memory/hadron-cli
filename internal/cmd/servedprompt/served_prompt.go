// Package servedprompt renders the immutable scalar-only served record. It
// never fetches source bodies, current pointers or live edges to reconstruct it.
package servedprompt

import (
	"encoding/json"
	"fmt"
	"io"
	"math"
	"strings"

	"github.com/hadron-memory/hadron-cli/internal/api"
	"github.com/hadron-memory/hadron-cli/internal/api/gen"
	"github.com/hadron-memory/hadron-cli/internal/cmdutil"
	"github.com/hadron-memory/hadron-cli/internal/exitcode"
	"github.com/hadron-memory/hadron-cli/internal/output"
	"github.com/spf13/cobra"
)

type entryDTO struct {
	NodeID string `json:"nodeId"`
	RevSeq int    `json:"revSeq"`
	Hash   string `json:"hash"`
	Pruned bool   `json:"pruned"`
}
type provenanceDTO struct {
	Items  []entryDTO `json:"items"`
	Total  int        `json:"total"`
	Limit  int        `json:"limit"`
	Offset int        `json:"offset"`
}
type recordDTO struct {
	ID                  string          `json:"id"`
	Kind                string          `json:"kind"`
	Ordinal             int             `json:"ordinal"`
	PointerLevel        string          `json:"pointerLevel"`
	CompiledNodeID      string          `json:"compiledNodeId"`
	CompiledContentHash string          `json:"compiledContentHash"`
	ServedRevSeq        int             `json:"servedRevSeq"`
	ProvenanceRevSeq    int             `json:"provenanceRevSeq"`
	ServedAt            string          `json:"servedAt"`
	Variables           json.RawMessage `json:"variables"`
	Pruned              bool            `json:"pruned"`
	Provenance          provenanceDTO   `json:"provenance"`
}
type envelopeDTO struct {
	ServedPrompt *recordDTO `json:"servedPrompt"`
}

// NewWorker reads only an explicit worker ref, without ambient App/name lookup.
func NewWorker(f *cmdutil.Factory) *cobra.Command { return newCommand(f, true) }

// NewRun reads the served record of an explicit run ref.
func NewRun(f *cmdutil.Factory) *cobra.Command { return newCommand(f, false) }

func newCommand(f *cmdutil.Factory, worker bool) *cobra.Command {
	var limit, offset int
	var all bool
	owner := "run"
	if worker {
		owner = "worker"
	}
	cmd := &cobra.Command{
		Use:   "served-prompt <" + owner + "-ref>",
		Short: "Show the recorded boot-prompt revision and provenance",
		Long: `Read the recorded boot-prompt revision, hash, pointer level and filled-in
variables, with one page of scalar-only provenance. This never reconstructs a
prompt from current pointers, source bodies or live edges. Revision numbers are
revSeq, not contentRevSeq. Pruned revisions stay recorded; no replacement text
is fetched. Variables are the recorded values and may contain sensitive text.

A null record is unavailable: it may be unrecorded, unreadable or serving may
be off. Null does not prove which. Worker refs must be ids or fully-qualified
worker URNs; names and ambient App/worktree selection are not used. Run refs
are sent to the provider as given. Provider authorization remains authoritative.

--limit defaults to 50, caps at 200; 0 reads counts only. --offset selects the
provenance page. --all buffers every provenance page before output and refuses
an inconsistent record or failed page without printing a partial result. It
cannot guarantee one database snapshot across requests.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ref := strings.TrimSpace(args[0])
			if ref == "" || (worker && !cmdutil.IsEntityID(ref) && !strings.HasPrefix(strings.ToLower(ref), "hrn:worker:") && !strings.HasPrefix(strings.ToLower(ref), "urn:worker:")) {
				return exitcode.Newf(exitcode.Usage, "pass an explicit %s id or fully-qualified URN", owner)
			}
			if limit < 0 || limit > 200 || offset < 0 || offset > math.MaxInt32 || (all && (limit == 0 || cmd.Flags().Changed("offset"))) {
				return exitcode.Newf(exitcode.Usage, "use --limit 0..200 and --offset 0..2147483647; --all excludes --offset and --limit 0")
			}
			client, err := f.GraphQLClient()
			if err != nil {
				return err
			}
			fetch := func(at int) (*gen.ServedPromptRecord, error) {
				if worker {
					r, e := gen.WorkerServedPrompt(cmd.Context(), client, ref, limit, at)
					if e != nil {
						return nil, api.MapError(e)
					}
					if r == nil || r.Worker == nil || r.Worker.ServedPrompt == nil {
						return nil, nil
					}
					return &r.Worker.ServedPrompt.ServedPromptRecord, nil
				}
				r, e := gen.RunServedPrompt(cmd.Context(), client, ref, limit, at)
				if e != nil {
					return nil, api.MapError(e)
				}
				if r == nil || r.AppRun == nil || r.AppRun.ServedPrompt == nil {
					return nil, nil
				}
				return &r.AppRun.ServedPrompt.ServedPromptRecord, nil
			}
			var result *recordDTO
			var signature string
			for at := offset; ; {
				r, e := fetch(at)
				if e != nil {
					return e
				}
				if r == nil {
					if result != nil {
						return exitcode.Newf(exitcode.Error, "served record became unavailable during pagination; no partial result shown")
					}
					break
				}
				page, e := recordFrom(r, limit, at)
				if e != nil {
					return e
				}
				if result == nil {
					result = page
					signature = identity(page)
				} else {
					if identity(page) != signature || page.Provenance.Total != result.Provenance.Total {
						return exitcode.Newf(exitcode.Error, "served record changed during pagination; no partial result shown")
					}
					result.Provenance.Items = append(result.Provenance.Items, page.Provenance.Items...)
				}
				count := len(page.Provenance.Items)
				if !all || at >= page.Provenance.Total-count {
					break
				}
				if count == 0 || at > math.MaxInt32-count {
					return exitcode.Newf(exitcode.Error, "served provenance page did not advance; no partial result shown")
				}
				at += count
			}
			return output.Write(f.IOStreams, f.JSON, envelopeDTO{ServedPrompt: result}, func(w io.Writer) error {
				if result == nil {
					_, e := fmt.Fprintln(w, "served prompt: unavailable (unrecorded, unreadable or serving off)")
					return e
				}
				r := result
				if _, e := fmt.Fprintf(w, "record: %s\nkind: %s; ordinal: %d; pointer: %s\ncompiled node: %s\nserved revision: %d; provenance revision: %d\nhash: %s\nserved at: %s\npruned: %t\nvariables: %s\nprovenance: %d of %d (offset %d)\n", output.SafeCell(r.ID), output.SafeCell(r.Kind), r.Ordinal, output.SafeCell(r.PointerLevel), output.SafeCell(r.CompiledNodeID), r.ServedRevSeq, r.ProvenanceRevSeq, output.SafeCell(r.CompiledContentHash), output.SafeCell(r.ServedAt), r.Pruned, output.SafeCell(string(r.Variables)), len(r.Provenance.Items), r.Provenance.Total, offset); e != nil {
					return e
				}
				t := output.NewTable(w)
				t.Row("NODE ID", "REVISION", "HASH", "PRUNED")
				for _, p := range r.Provenance.Items {
					t.Row(output.SafeCell(p.NodeID), fmt.Sprint(p.RevSeq), output.SafeCell(p.Hash), fmt.Sprint(p.Pruned))
				}
				return t.Flush()
			})
		},
	}
	cmd.Flags().IntVar(&limit, "limit", 50, "provenance entries per page (0 counts only, maximum 200)")
	cmd.Flags().IntVar(&offset, "offset", 0, "provenance entries to skip")
	cmd.Flags().BoolVar(&all, "all", false, "read all provenance pages before output")
	return cmd
}

func identity(r *recordDTO) string {
	copy := *r
	copy.Provenance = provenanceDTO{}
	b, _ := json.Marshal(copy)
	return string(b)
}

func recordFrom(r *gen.ServedPromptRecord, limit, offset int) (*recordDTO, error) {
	p := r.Provenance
	if r.Id == "" || r.CompiledNodeId == "" || r.ServedRevSeq <= 0 || r.ProvenanceRevSeq <= 0 || !json.Valid(r.Variables) || p == nil || p.Items == nil || p.Total < 0 || len(p.Items) > limit || len(p.Items) > max(0, p.Total-offset) {
		return nil, exitcode.Newf(exitcode.Error, "server returned an invalid served record/provenance page")
	}
	dto := &recordDTO{ID: r.Id, Kind: string(r.Kind), Ordinal: r.Ordinal, PointerLevel: string(r.PointerLevel), CompiledNodeID: r.CompiledNodeId, CompiledContentHash: r.CompiledContentHash, ServedRevSeq: r.ServedRevSeq, ProvenanceRevSeq: r.ProvenanceRevSeq, ServedAt: r.ServedAt, Variables: r.Variables, Pruned: r.Pruned, Provenance: provenanceDTO{Items: []entryDTO{}, Total: p.Total, Limit: limit, Offset: offset}}
	for _, e := range p.Items {
		if e == nil {
			return nil, exitcode.Newf(exitcode.Error, "server returned a null served-provenance entry")
		}
		dto.Provenance.Items = append(dto.Provenance.Items, entryDTO{NodeID: e.NodeId, RevSeq: e.RevSeq, Hash: e.Hash, Pruned: e.Pruned})
	}
	return dto, nil
}
