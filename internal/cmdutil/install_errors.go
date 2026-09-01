package cmdutil

import (
	"errors"
	"fmt"

	"github.com/hadron-memory/hadron-cli/internal/api"
	"github.com/hadron-memory/hadron-cli/internal/exitcode"
)

// InstallForbiddenGuidance turns the server's bare FORBIDDEN on an
// install/uninstall into the actual rule (#389). The gate is NOT App
// membership: installing an existing Agent is itself a read grant on that
// Agent's design — its system memory becomes readable from every App context
// and the returned Agent carries its systemPrompt — so it sits at the level
// that can already read the org's Agents. hadron-server#952 proposed admitting
// non-reader AppMembers and that was declined for exactly this reason, which
// means a team member who is not an org member will hit this and deserves
// better than one word.
//
// It lives in cmdutil rather than in the `app` command package because two
// command groups now reach the same mutation — `app agent add/remove` and
// `agent create --install-into` — and a second copy of this prose would drift
// from the first the next time the rule moves.
func InstallForbiddenGuidance(err error) error {
	if !api.HasErrorCode(err, "FORBIDDEN") {
		return api.MapError(err)
	}
	mapped := api.MapError(err)
	return exitcode.Newf(exitcode.FromError(mapped),
		"%v — installing or removing an Agent needs CONTRIBUTOR+ on the App's owning org "+
			"(or ownership of a user-owned App). App membership is deliberately not enough: "+
			"attaching an Agent grants read access to its design, including its system prompt",
		unwrapMessage(mapped))
}

// unwrapMessage strips the exitcode wrapper so the server's own sentence can be
// interpolated without its code prefix.
func unwrapMessage(err error) error {
	var coded *exitcode.CodedError
	if errors.As(err, &coded) {
		return coded.Err
	}
	return err
}

// NoteAgentNotInstalled says on stderr that a freshly-created agent is in no
// App's cast pool yet (#535). "✓ created" reads as done, but an agent that is
// not installed anywhere cannot be cast as a worker — and the failure surfaces
// later, at `team worker cast`, as WORKER_AGENT_NOT_FOUND, which points at the
// role rather than at the missing install.
//
// Stderr, so the --json stdout contract is untouched; and only when
// --install-into was NOT passed, since having done the install is the answer.
func (f *Factory) NoteAgentNotInstalled(agentRef string) {
	fmt.Fprintf(f.IOStreams.ErrOut,
		"note: not yet installed in any App, so it cannot be cast as a worker. "+
			"Install it with `hadron app agent add <app> %s`, or pass --install-into <app> at create time.\n",
		agentRef)
}
