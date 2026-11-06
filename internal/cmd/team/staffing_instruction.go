package team

import (
	"github.com/Khan/genqlient/graphql"
	"github.com/hadron-memory/hadron-cli/internal/api"
	"github.com/hadron-memory/hadron-cli/internal/api/gen"
	"github.com/hadron-memory/hadron-cli/internal/cmdutil"
	"github.com/hadron-memory/hadron-cli/internal/exitcode"
	"github.com/hadron-memory/hadron-cli/internal/instruction"
	"github.com/spf13/cobra"
)

// staffingInstruction reads cast/retire/rm's --instruction (cli#1594). It
// runs before any prompt or request: a bad kind or blank ref is exit 2 with
// nothing sent. With the flag it also snapshots the binding, so call it
// before the command's first lookup. Without the flag both results are nil
// and the command's documents and attribution stay as they were.
func staffingInstruction(cmd *cobra.Command, f *cmdutil.Factory, raw string) (*gen.ActInstructionInput, InstructedWrite, error) {
	if !cmd.Flags().Changed("instruction") {
		return nil, nil, nil
	}
	claim, err := instruction.Parse(raw)
	if err != nil {
		return nil, nil, err
	}
	return claim, BeginInstructedWrite(f)(cmd.Context()), nil
}

// castWorker makes the one cast: the unchanged document without a claim,
// else the instructed one in the context instructed decides.
func castWorker(cmd *cobra.Command, client graphql.Client, appRef, agentRef, role, name string, reportsTo *string, noTeam *bool, claim *gen.ActInstructionInput, instructed InstructedWrite) (*gen.WorkerFields, error) {
	if claim == nil {
		resp, err := gen.CastWorker(cmd.Context(), client, appRef, optStr(agentRef), optStr(role), optStr(name), reportsTo, noTeam)
		if err != nil || resp == nil || resp.CastWorker == nil {
			return nil, err
		}
		return &resp.CastWorker.WorkerFields, nil
	}
	ctx, _, err := instructed(cmd.Context(), "", appRef)
	if err != nil {
		return nil, err
	}
	resp, err := gen.CastWorkerInstructed(ctx, client, appRef, optStr(agentRef), optStr(role), optStr(name), reportsTo, noTeam, claim)
	if err != nil || resp == nil || resp.CastWorker == nil {
		return nil, err
	}
	return &resp.CastWorker.WorkerFields, nil
}

// castPreview is castWorker for --dry-run. claimed is true when an
// instructed preview went out through the worker session and was admitted:
// the server then recorded one claim event.
func castPreview(cmd *cobra.Command, client graphql.Client, appRef, agentRef, role, name string, reportsTo *string, noTeam *bool, claim *gen.ActInstructionInput, instructed InstructedWrite) (p *gen.CastWorkerPreviewCastWorkerPreview, claimed bool, err error) {
	if claim == nil {
		resp, err := gen.CastWorkerPreview(cmd.Context(), client, appRef, optStr(agentRef), optStr(role), optStr(name), reportsTo, noTeam)
		if err != nil {
			return nil, false, castChoiceRemedy(err)
		}
		if resp == nil || resp.CastWorkerPreview == nil {
			return nil, false, exitcode.Newf(exitcode.Error, "server returned no preview")
		}
		return resp.CastWorkerPreview, false, nil
	}
	ctx, worker, err := instructed(cmd.Context(), "", appRef)
	if err != nil {
		return nil, false, err
	}
	resp, err := gen.CastWorkerPreviewInstructed(ctx, client, appRef, optStr(agentRef), optStr(role), optStr(name), reportsTo, noTeam, claim)
	if err != nil {
		return nil, false, castChoiceRemedy(err)
	}
	if resp == nil || resp.CastWorkerPreview == nil {
		return nil, false, exitcode.Newf(exitcode.Error, "server returned no preview")
	}
	q := resp.CastWorkerPreview
	return &gen.CastWorkerPreviewCastWorkerPreview{
		Name: q.Name, Role: q.Role, AgentId: q.AgentId, AgentName: q.AgentName,
		Prompt: q.Prompt, TeamPlacement: q.TeamPlacement, ReportsToWorkerId: q.ReportsToWorkerId,
	}, worker, nil
}

// retireWorker makes the one retire; the target App is the worker's own.
func retireWorker(cmd *cobra.Command, client graphql.Client, w *gen.WorkerFields, claim *gen.ActInstructionInput, instructed InstructedWrite) (*gen.WorkerFields, error) {
	if claim == nil {
		resp, err := gen.RetireWorker(cmd.Context(), client, w.Id)
		if err != nil || resp == nil || resp.RetireWorker == nil {
			return nil, err
		}
		return &resp.RetireWorker.WorkerFields, nil
	}
	ctx, _, err := instructed(cmd.Context(), w.AppId, w.AppId)
	if err != nil {
		return nil, err
	}
	resp, err := gen.RetireWorkerInstructed(ctx, client, w.Id, claim)
	if err != nil || resp == nil || resp.RetireWorker == nil {
		return nil, err
	}
	return &resp.RetireWorker.WorkerFields, nil
}

// deleteWorker makes the one delete; the target App is the worker's own.
func deleteWorker(cmd *cobra.Command, client graphql.Client, w *gen.WorkerFields, attestation *bool, claim *gen.ActInstructionInput, instructed InstructedWrite) (bool, error) {
	if claim == nil {
		resp, err := gen.DeleteWorker(cmd.Context(), client, w.Id, attestation)
		if err != nil || resp == nil {
			return false, err
		}
		return resp.DeleteWorker, nil
	}
	ctx, _, err := instructed(cmd.Context(), w.AppId, w.AppId)
	if err != nil {
		return false, err
	}
	resp, err := gen.DeleteWorkerInstructed(ctx, client, w.Id, attestation, claim)
	if err != nil || resp == nil {
		return false, err
	}
	return resp.DeleteWorker, nil
}

// deleteWorkerPreview makes the one delete preview (cli#1735): the plain
// document without a claim, else the instructed one in the context instructed
// decides. claimed is true when an instructed preview went out through the
// worker session: the server then records one claim event. The reply is
// checked before it is returned: a null or empty result, one naming another
// worker, or one whose attestationRequired is not true, is a failure, never a success to print. It is one request: no retry
// and no other operation to fall back on.
func deleteWorkerPreview(cmd *cobra.Command, client graphql.Client, w *gen.WorkerFields, claim *gen.ActInstructionInput, instructed InstructedWrite) (attestationRequired, claimed bool, err error) {
	var workerID string
	if claim == nil {
		resp, err := gen.DeleteWorkerPreview(cmd.Context(), client, w.Id)
		if err != nil {
			return false, false, err
		}
		if resp == nil || resp.DeleteWorkerPreview == nil {
			return false, false, exitcode.Newf(exitcode.Error, "server returned no delete preview")
		}
		workerID, attestationRequired = resp.DeleteWorkerPreview.WorkerId, resp.DeleteWorkerPreview.AttestationRequired
	} else {
		ctx, worker, err := instructed(cmd.Context(), w.AppId, w.AppId)
		if err != nil {
			return false, false, err
		}
		// An instructed preview is a Query that writes (one claim event), so a
		// 307/308 must not resend it: refuse the redirect as a delete does.
		resp, err := gen.DeleteWorkerPreviewInstructed(api.WithNoRedirect(ctx), client, w.Id, claim)
		if err != nil {
			return false, false, err
		}
		if resp == nil || resp.DeleteWorkerPreview == nil {
			return false, false, exitcode.Newf(exitcode.Error, "server returned no delete preview")
		}
		workerID, attestationRequired, claimed = resp.DeleteWorkerPreview.WorkerId, resp.DeleteWorkerPreview.AttestationRequired, worker
	}
	if workerID != w.Id {
		return false, false, exitcode.Newf(exitcode.Error, "server returned a delete preview for a different worker (wanted %s)", w.Id)
	}
	// The schema documents attestationRequired as always true, and a Go bool
	// decodes an omitted or null field to false without an error. A false
	// reply is therefore malformed, never a preview that hides the
	// required-attestation note.
	if !attestationRequired {
		return false, false, exitcode.Newf(exitcode.Error, "server returned a malformed delete preview (attestationRequired is not true)")
	}
	return attestationRequired, claimed, nil
}
