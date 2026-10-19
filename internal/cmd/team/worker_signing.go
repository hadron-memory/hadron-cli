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

// signingSession resolves, BEFORE the write, the worker session a signature or
// trailer edit must go out through (#946 BLIND P2). The generic
// AuthoredWriteContext is best-effort: it attaches nothing when it can't
// verify a binding, which for these edits would silently make a bound worker's
// edit the person's. So: no worktree or no binding is an unbound shell ("");
// a binding that verifies for this request is its session; a binding that
// doesn't refuses, and nothing is written.
func signingSession(ctx context.Context, f *cmdutil.Factory) (string, error) {
	b, _, err := readBindingFile(ctx)
	if errors.Is(err, errNoWorktree) {
		return "", nil
	}
	if err != nil {
		return "", exitcode.Newf(exitcode.Usage, "cannot read this worktree's session binding (%v), so the edit can't be attributed; nothing was written", err)
	}
	if b == nil || b.SessionID == "" {
		return "", nil
	}
	refuse := func(why string) error {
		return exitcode.Newf(exitcode.Usage,
			"this worktree is bound to worker session %s, but %s, so this signature/trailer edit can't go out through it; nothing was written. Make the edit from an unbound shell, or run it against the binding's server and App",
			b.SessionID, why)
	}
	if b.WorkerID == "" || b.AppID == "" || b.Server == "" {
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
	resp, err := gen.UpdateWorkerSigning(ctx, client, workerID, e.override, e.signature, e.trailer)
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
