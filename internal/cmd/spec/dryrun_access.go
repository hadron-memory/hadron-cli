package spec

import (
	"github.com/Khan/genqlient/graphql"
	"github.com/spf13/cobra"

	"github.com/hadron-memory/hadron-cli/internal/api"
	"github.com/hadron-memory/hadron-cli/internal/api/gen"
	"github.com/hadron-memory/hadron-cli/internal/cmdutil"
	"github.com/hadron-memory/hadron-cli/internal/exitcode"
)

// noWriteAccessRefusal is the sentence the server gives a real write to a
// memory the caller cannot write (pinned in internal/api errors_test.go).
const noWriteAccessRefusal = "Forbidden: no write access to this memory"

// requireWriteAccessForDryRun makes a spec dry run answer as the real run
// would for a caller who cannot write (cli#1912, cor:api:290:01's precedent:
// a dry run reports the refusal the real call would give). A dry run is a
// client-computed plan, so without this a Reader gets a full plan and exit 0,
// then the real run is refused.
//
// The verdict is the server's own: effectiveAccess for the calling user on the
// memory (cor:acl, hadron-server#323), never a client-side role table. It is
// called only AFTER the target has been read, so a memory or spec the caller
// cannot read already gave the real run's not-found: this refusal never
// confirms that an unreadable target exists (cor:api:140:03). Any failure to
// decide (an App key with no user, an error) leaves the dry run as it was;
// the real write's gate stays the authority.
func requireWriteAccessForDryRun(cmd *cobra.Command, client graphql.Client, memURN string) error {
	ctx := cmd.Context()
	auth, err := gen.AuthContext(ctx, client)
	if err != nil || auth.AuthContext == nil || auth.AuthContext.User == nil {
		return nil
	}
	resp, err := gen.EffectiveAccess(ctx, client, auth.AuthContext.User.Id, cmdutil.CanonicalMemoryRef(memURN))
	if err != nil {
		if api.HasErrorCode(err, "NOT_FOUND") {
			return api.MapError(err)
		}
		return nil
	}
	if !resp.EffectiveAccess.CanWrite {
		return exitcode.Newf(exitcode.Forbidden, "%s", noWriteAccessRefusal)
	}
	return nil
}
