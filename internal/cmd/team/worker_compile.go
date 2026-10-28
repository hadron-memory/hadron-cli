package team

import (
	"fmt"
	"strings"

	"github.com/hadron-memory/hadron-cli/internal/api"
	"github.com/hadron-memory/hadron-cli/internal/api/gen"
	"github.com/hadron-memory/hadron-cli/internal/cmd/bootcompile"
	"github.com/hadron-memory/hadron-cli/internal/cmdutil"
	"github.com/hadron-memory/hadron-cli/internal/exitcode"
	"github.com/hadron-memory/hadron-cli/internal/output"
	"github.com/spf13/cobra"
)

func newCmdWorkerCompile(f *cmdutil.Factory) *cobra.Command {
	return bootcompile.NewWorker(f, func(cmd *cobra.Command, ref string) (string, error) {
		ref = strings.TrimSpace(ref)
		client, err := f.GraphQLClient()
		if err != nil {
			return "", err
		}
		source := "explicit worker reference"
		var worker gen.WorkerFields
		// An explicit casting identity is independent of every ambient App/binding,
		// including an unreadable binding or one for another server.
		if cmdutil.IsEntityID(ref) || strings.HasPrefix(strings.ToLower(ref), "hrn:worker:") || strings.HasPrefix(strings.ToLower(ref), "urn:worker:") {
			r, err := gen.GetWorker(cmd.Context(), client, ref)
			if err != nil {
				return "", api.MapError(err)
			}
			if r.Worker == nil {
				return "", exitcode.Newf(exitcode.NotFound, "worker is not available")
			}
			worker = r.Worker.WorkerFields
		} else {
			// A name needs one usable App. Read the binding only if an explicit/configured
			// App did not answer; do not hide a binding or scope failure with an id lookup.
			appRef, err := f.App()
			if err != nil {
				return "", err
			}
			var b *binding
			if appRef == "" {
				b, err = readBindingOrNilWithApp(cmd.Context(), f)
				if err != nil {
					return "", err
				}
				if b != nil {
					server, err := f.Server()
					if err != nil {
						return "", err
					}
					if b.Server == "" || api.Endpoint(b.Server) != api.Endpoint(server) {
						return "", exitcode.Newf(exitcode.Usage, "cannot resolve a worker name from an unknown or different-server binding; pass --app <ref> for this server or a fully-qualified worker URN")
					}
				}
			}
			scope, err := resolveTeamAppScope(cmd.Context(), f, b)
			if err != nil {
				return "", err
			}
			if scope.Ref == "" {
				return "", exitcode.Newf(exitcode.Usage, "resolving a worker name needs --app <ref>, an App context or a binding for this server")
			}
			worker, err = resolveWorker(cmd.Context(), client, scope.Ref, ref)
			if err != nil {
				return "", err
			}
			source = scope.Source
		}
		if !f.JSON {
			label := worker.Id
			if worker.Urn != nil && *worker.Urn != "" {
				label = *worker.Urn
			}
			if _, err := fmt.Fprintf(f.IOStreams.Out, "worker: %s (%s)\n", output.SafeCell(label), output.SafeCell(source)); err != nil {
				return "", err
			}
		}
		return worker.Id, nil
	})
}
