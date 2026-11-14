package team

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/Khan/genqlient/graphql"
	"github.com/spf13/cobra"

	"github.com/hadron-memory/hadron-cli/internal/api"
	"github.com/hadron-memory/hadron-cli/internal/api/gen"
	"github.com/hadron-memory/hadron-cli/internal/cmd/lintwarn"
	"github.com/hadron-memory/hadron-cli/internal/cmdutil"
	"github.com/hadron-memory/hadron-cli/internal/exitcode"
	"github.com/hadron-memory/hadron-cli/internal/output"
)

// signingEdit is one `worker update` that touches the signature or trailer
// template (cli#916 C4, hadron-server#2120). Each field is presence-preserving:
// nil is omitted on the wire (the server keeps the stored value) and a pointer
// to "" is an explicit empty string. Null is never sent.
type signingEdit struct {
	signature, trailer *string
}

func optionalFlag(set bool, v string) *string {
	if !set {
		return nil
	}
	return &v
}

const boundSigningHint = "note: this edit was sent from your bound worker session; if you manage this App, an edit made through a worker session may be what was refused — try from an unbound shell"

// signingSession resolves, BEFORE the write, the worker session a signature or
// trailer edit must go out through (#946 BLIND P2). The generic
// AuthoredWriteContext is best-effort: it attaches nothing when it can't
// verify a binding, which for these edits would silently make a bound worker's
// edit the person's. So: no worktree, or no binding file, is a genuinely
// unbound shell (""); a binding that verifies for this request is its session;
// any other binding (unreadable, incomplete, or for another server or App)
// refuses, and nothing is written.
func signingSession(ctx context.Context, f *cmdutil.Factory) (string, error) {
	b, _, err := readBindingFile(ctx)
	if errors.Is(err, errNoWorktree) {
		return "", nil
	}
	if err != nil {
		return "", exitcode.Newf(exitcode.Usage, "cannot read this worktree's session binding (%v), so the edit can't be attributed; nothing was written", err)
	}
	if b == nil {
		return "", nil
	}
	refuse := func(why string) error {
		return exitcode.Newf(exitcode.Usage,
			"this worktree has a session binding (%q), but %s, so this signature/trailer edit can't go out through it; nothing was written. Make the edit from an unbound shell, or run it against the binding's server and App",
			b.SessionID, why)
	}
	if b.SessionID == "" || b.WorkerID == "" || b.AppID == "" || b.Server == "" {
		return "", refuse("the binding is incomplete")
	}
	server, err := f.Server()
	if err != nil {
		return "", err
	}
	// Endpoint is the client's own normalization: equal endpoints are the
	// same server, whatever the spelling.
	if api.Endpoint(server) != api.Endpoint(b.Server) {
		return "", refuse("it belongs to " + b.Server + ", not " + server)
	}
	app, err := f.App()
	if err != nil {
		return "", err
	}
	if app != "" {
		same, err := bindingsAppIdentity(ctx, f, app, b.AppID)
		if err != nil {
			return "", exitcode.Newf(exitcode.FromError(err), "cannot confirm the binding's App (%v); nothing was written", err)
		}
		if !same {
			return "", refuse("it belongs to a different App than --app")
		}
	}
	noteEndedBinding(ctx, b)
	return b.SessionID, nil
}

func updateWorkerSigning(cmd *cobra.Command, f *cmdutil.Factory, client graphql.Client, workerID string, e signingEdit) error {
	ctx := cmd.Context()
	session, err := signingSession(ctx, f)
	if err != nil {
		return err
	}
	if session != "" {
		ctx = api.WithSession(ctx, session)
	}
	resp, err := gen.UpdateWorkerSigning(ctx, client, workerID, e.signature, e.trailer)
	if err != nil {
		mapped := api.MapError(err)
		// FORBIDDEN covers a non-manager AND a session-bound edit, and the CLI
		// can't tell which: the server's sentence stays whole, and the hedge is
		// added only when this edit really went out through a session.
		if session != "" && exitcode.FromError(mapped) == exitcode.Forbidden {
			return exitcode.Newf(exitcode.Forbidden, "%s\n%s", mapped.Error(), boundSigningHint)
		}
		return mapped
	}
	if resp.UpdateWorker == nil {
		return exitcode.Newf(exitcode.Error, "server returned no worker")
	}
	return writeSigningReceipt(cmd, ctx, f, client, e, resp.UpdateWorker.WorkerFields)
}

// updateWorkerSigningInstructed is the same edit carrying the holder's
// instruction claim (cli#1592). It goes out through the binding only when
// that binding is verified for the TARGET worker's App; anything else refuses,
// as the plain edit does, and never becomes the person's write. With no
// binding at all it is the signed-in person's call (the server validates the
// claim but does not record it). One mutation, never retried.
func updateWorkerSigningInstructed(cmd *cobra.Command, f *cmdutil.Factory, client graphql.Client, snap instructionSnapshot, w gen.WorkerFields, e signingEdit, instr *gen.ActInstructionInput) error {
	ctx := cmd.Context()
	session, why, err := instructedSession(ctx, f, snap, w.AppId)
	if err != nil {
		return err
	}
	if why != "" {
		return instructionRefusal(snap, why)
	}
	if session != "" {
		noteEndedBinding(ctx, snap.b)
		ctx = api.WithSession(ctx, session)
	}
	resp, err := gen.UpdateWorkerSigningInstructed(ctx, client, w.Id, e.signature, e.trailer, instr)
	if err != nil {
		return api.MapErrorKeepingCode(err)
	}
	if resp.UpdateWorker == nil {
		return exitcode.Newf(exitcode.Error, "server returned no worker; outcome unknown, inspect the worker before retrying")
	}
	return writeSigningReceipt(cmd, ctx, f, client, e, resp.UpdateWorker.WorkerFields)
}

// workerUpdateReceiptDTO is the stable --json shape of the signature/trailer
// update receipt: workerDTO plus signatureLint (cli#2007). Update-only, so the
// cast, list and roster shapes (workerDTO) do not move. signatureLint is the
// CURRENT check read after the save, not a snapshot taken at write time; null
// means unavailable (or a masked template), never "no warnings".
type workerUpdateReceiptDTO struct {
	workerDTO
	SignatureLint *lintwarn.Page `json:"signatureLint"`
}

// writeSigningReceipt prints the receipt of a successful edit and then reads
// signatureLint with the write's own context (its session header, if any). The
// read is advisory: it starts its own budget after the write, never fails the
// command and never changes its exit code.
func writeSigningReceipt(cmd *cobra.Command, ctx context.Context, f *cmdutil.Factory, client graphql.Client, e signingEdit, fields gen.WorkerFields) error {
	dto := workerUpdateReceiptDTO{workerDTO: workerDTOFromFields(fields)}
	// signature is NOT NULL in the store, so null means the caller may not see
	// the worker's internals (maySeeWorkerInternals), never "unset" (contract,
	// cli#2007); revisit if the server ever nulls an unset template.
	masked := dto.Signature == nil
	if !masked {
		budget := lintwarn.NewBudget(ctx)
		defer budget.Close()
		dto.SignatureLint = lintwarn.ReadSignatureLint(budget, client, dto.ID)
		if dto.SignatureLint == nil {
			lintwarn.ReportUnavailable(cmd)
		}
	}
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
		if !masked {
			if err := lintwarn.WriteSection(out, "signature lint (current check)", "the signature and trailer", dto.SignatureLint, false); err != nil {
				return err
			}
		}
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
	return strings.Join(parts, ", ")
}
