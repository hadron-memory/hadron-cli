package team

import (
	"fmt"
	"io"
	"strings"

	"github.com/Khan/genqlient/graphql"
	"github.com/spf13/cobra"

	"github.com/hadron-memory/hadron-cli/internal/api"
	"github.com/hadron-memory/hadron-cli/internal/api/gen"
	"github.com/hadron-memory/hadron-cli/internal/cmdutil"
	"github.com/hadron-memory/hadron-cli/internal/exitcode"
	"github.com/hadron-memory/hadron-cli/internal/output"
)

// signingEdit is one `worker update` that touches the signature or trailer
// template (cli#916 C4, hadron-server#2120). Each field is presence-preserving:
// nil is omitted on the wire (the server keeps the stored value) and a pointer
// to "" is an explicit empty string. Null is never sent.
type signingEdit struct {
	override, signature, trailer *string
}

func optionalFlag(set bool, v string) *string {
	if !set {
		return nil
	}
	return &v
}

// overrideEdit: on this operation a clear is an explicit "" (the server's
// blank-clears rule), since omitempty cannot carry a null.
func overrideEdit(set, clear bool, v string) *string {
	switch {
	case set:
		return &v
	case clear:
		empty := ""
		return &empty
	}
	return nil
}

const boundSigningHint = "note: this edit was sent from your bound worker session; if you manage this App, an edit made through a worker session may be what was refused — try from an unbound shell"

func updateWorkerSigning(cmd *cobra.Command, f *cmdutil.Factory, client graphql.Client, workerID string, e signingEdit) error {
	ctx, attached := withSessionAttachRecorder(cmd.Context())
	resp, err := gen.UpdateWorkerSigning(ctx, client, workerID, e.override, e.signature, e.trailer)
	if err != nil {
		mapped := api.MapError(err)
		// FORBIDDEN covers a non-manager AND a session-bound edit, and the CLI
		// can't tell which: the server's sentence stays whole, and the hedge is
		// added only when this edit really went out through a session.
		if attached() && exitcode.FromError(mapped) == exitcode.Forbidden {
			return exitcode.Newf(exitcode.Forbidden, "%s\n%s", mapped.Error(), boundSigningHint)
		}
		return mapped
	}
	if resp.UpdateWorker == nil {
		return exitcode.Newf(exitcode.Error, "server returned no worker")
	}
	dto := workerDTOFromFields(resp.UpdateWorker.WorkerFields)
	return output.Write(f.IOStreams, f.JSON, dto, func(out io.Writer) error {
		if _, err := fmt.Fprintf(out, "✓ updated the %s for worker %s (%s)\n", changedFields(e), dto.Name, dto.ID); err != nil {
			return err
		}
		// Only what this call changed: printing an untouched template would
		// read as a claim that it was set.
		var sig, trl *string
		if e.signature != nil {
			sig = dto.Signature
		}
		if e.trailer != nil {
			trl = dto.Trailer
		}
		writeSigningTemplates(out, sig, trl)
		if dto.Prompt != nil && *dto.Prompt != "" {
			if _, err := fmt.Fprintf(out, "\nBoot briefing at the next bind:\n\n%s\n", *dto.Prompt); err != nil {
				return err
			}
		}
		return nil
	})
}

func changedFields(e signingEdit) string {
	var parts []string
	if e.signature != nil {
		parts = append(parts, "signature template")
	}
	if e.trailer != nil {
		parts = append(parts, "trailer template")
	}
	switch {
	case e.override != nil && *e.override == "":
		parts = append(parts, "prompt override (cleared)")
	case e.override != nil:
		parts = append(parts, "prompt override")
	}
	return strings.Join(parts, ", ")
}
