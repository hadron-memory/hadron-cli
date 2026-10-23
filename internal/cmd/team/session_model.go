package team

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/Khan/genqlient/graphql"
	"github.com/spf13/cobra"

	"github.com/hadron-memory/hadron-cli/internal/api"
	"github.com/hadron-memory/hadron-cli/internal/api/gen"
	"github.com/hadron-memory/hadron-cli/internal/cmdutil"
	"github.com/hadron-memory/hadron-cli/internal/exitcode"
	"github.com/hadron-memory/hadron-cli/internal/modelreport"
	"github.com/hadron-memory/hadron-cli/internal/output"
)

// hookInputLimit bounds the hook JSON read from stdin.
const hookInputLimit = 1 << 20

// hookDiagnostic is the ONLY thing hook mode ever prints on a failure: fixed,
// with no path, no transcript text and no id. Hook stdout can be fed back to
// the agent by the native tool, so hook mode writes nothing there at all.
const hookDiagnostic = "hadron: team session sync-model: model not reported (unavailable)"

type modelReportDTO struct {
	SessionID string `json:"sessionId"`
	Model     string `json:"model"`
	Reported  bool   `json:"reported"`
}

func newCmdSessionSyncModel(f *cmdutil.Factory) *cobra.Command {
	var hook bool
	cmd := &cobra.Command{
		Use:   "sync-model [--hook]",
		Short: "Report this worktree's session model, read from its recorded transcript (hook entry)",
		Long: `Report the model the bound worker session is running, as the model reporting
slice of hadron-server#2252 (hadron-cli#995). Built to be a native tool's hook
command: the tool pipes its hook JSON ({"session_id","transcript_path","cwd"})
on stdin.

WHAT IT WILL AND WILL NOT READ. The only transcript it reads is the one the
bound Hadron session ALREADY RECORDED as its transcriptPath (` + "`session start|resume --transcript`" + `).
The hook's path, native session id and cwd are claims it verifies against
that file — never an association source. It does not pick a "newest" file, and
a session with no recorded transcript reports nothing. The file must sit under
the tool's transcript root, be the same file the recorded path names, carry
the hook's native session id in its own header, and stay unchanged while read.
Anything unproven means UNAVAILABLE: nothing is sent.

ONE write: updateSession with the model only — never family or tier (the
server derives them), never clearing a stored model. The worktree binding is
re-read just before the write; if it changed meanwhile the observation is
discarded, not retried against either session.

--hook  hook mode: prints NOTHING on stdout (a hook's stdout can reach the
        agent), exits 0 on unavailable or any failure with one fixed line on
        stderr. Never inferred from a terminal.
Without --hook an unavailable model exits ` + fmt.Sprint(modelUnavailableExit) + ` (nothing reported, nothing
refused); server/auth failures use the usual codes.

LIMITS, DISCLOSED. Last arrival wins: a delayed hook or a manual
` + "`set-model`" + ` can overwrite a newer report; there is no monotonic or
latest-observation guarantee. This command parses Claude/GLM and Codex
transcript formats; it does not install, or claim, any native hook — wiring
it into a tool's settings is the operator's step.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			err := runSyncModel(cmd.Context(), f, cmd, hook)
			if err != nil && hook {
				_, _ = fmt.Fprintln(f.IOStreams.ErrOut, hookDiagnostic)
				return nil
			}
			return err
		},
	}
	cmd.Flags().BoolVar(&hook, "hook", false, "hook mode: no stdout, exit 0 on unavailable or failure")
	return cmd
}

type hookInput struct {
	SessionID      string `json:"session_id"`
	TranscriptPath string `json:"transcript_path"`
	Cwd            string `json:"cwd"`
}

// modelUnavailableExit is `sync-model`'s own code for "could not prove a model;
// nothing reported, nothing refused". Command-specific like `chat check`'s 9
// and 10, so it is not one of the global exitcode constants, and 9/10 are not
// reused for a different meaning.
const modelUnavailableExit = 11

func errModelUnavailable() error {
	return exitcode.Newf(modelUnavailableExit, "model unavailable: nothing was reported")
}

func runSyncModel(ctx context.Context, f *cmdutil.Factory, cmd *cobra.Command, hook bool) error {
	raw, err := io.ReadAll(io.LimitReader(f.IOStreams.In, hookInputLimit+1))
	var in hookInput
	if err != nil || len(raw) > hookInputLimit || json.Unmarshal(raw, &in) != nil {
		return errModelUnavailable()
	}
	b, _, err := readBindingFile(ctx)
	if err != nil || b == nil || b.SessionID == "" {
		return errModelUnavailable()
	}
	if checkBindingServer(f, b) != nil || !sameTree(in.Cwd) {
		return errModelUnavailable()
	}
	client, err := f.GraphQLClient()
	if err != nil {
		return api.MapError(err)
	}
	resp, err := gen.GetTeamSession(ctx, client, b.SessionID)
	if err != nil {
		return api.MapError(err)
	}
	if resp.Session == nil || resp.Session.TranscriptPath == nil {
		return errModelUnavailable()
	}
	model, err := modelreport.Read(modelreport.Options{
		Recorded: *resp.Session.TranscriptPath,
		Claim:    modelreport.Claim{SessionID: in.SessionID, TranscriptPath: in.TranscriptPath, Cwd: in.Cwd},
		Roots:    modelreport.DefaultRoots(),
	})
	if err != nil {
		return errModelUnavailable()
	}
	// Re-read the binding right before the one write. A replaced or removed
	// binding discards the observation: no retry against either session. This
	// narrows the race; it is not an atomic filesystem/server transaction.
	again, _, err := readBindingFile(ctx)
	if err != nil || again == nil || again.SessionID != b.SessionID {
		return errModelUnavailable()
	}
	return reportModel(ctx, f, cmd, client, b.SessionID, model, hook)
}

// sameTree reports whether the hook's cwd is the process's worktree, or inside
// it or above it (a hook may run from a subdirectory).
func sameTree(hookCwd string) bool {
	if hookCwd == "" {
		return false
	}
	here, err := os.Getwd()
	if err != nil {
		return false
	}
	a, err1 := filepath.EvalSymlinks(hookCwd)
	b, err2 := filepath.EvalSymlinks(here)
	if err1 != nil || err2 != nil {
		return false
	}
	within := func(p, root string) bool {
		return p == root || strings.HasPrefix(p, strings.TrimRight(root, string(filepath.Separator))+string(filepath.Separator))
	}
	return within(a, b) || within(b, a)
}

func reportModel(ctx context.Context, f *cmdutil.Factory, _ *cobra.Command, client graphql.Client, sessionID, model string, quiet bool) error {
	if _, err := gen.UpdateTeamSession(ctx, client, sessionID, nil, nil, &model); err != nil {
		return api.MapError(err)
	}
	if quiet {
		return nil
	}
	return output.Write(f.IOStreams, f.JSON, modelReportDTO{SessionID: sessionID, Model: model, Reported: true}, func(w io.Writer) error {
		_, err := fmt.Fprintf(w, "reported model: %s (last arrival wins; the server derives family and tier)\n", model)
		return err
	})
}

func newCmdSessionSetModel(f *cmdutil.Factory) *cobra.Command {
	return &cobra.Command{
		Use:   "set-model <model>",
		Short: "Report the model this worktree's session is running (manual)",
		Long: `Report <model> for the worker session bound to this worktree, through the same
updateSession as ` + "`sync-model`" + ` — model only; the server derives family and
tier, and never clears family on a model it does not map. The model is an
exact string of at most 128 printable characters. It uses the worktree's
binding only: there is no --session, and it starts or rebinds nothing.

Last arrival wins: a later report from a hook or another run can overwrite
this one.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			model := args[0]
			if !modelreport.ValidModel(model) {
				return exitcode.Newf(exitcode.Usage, "model must be 1–128 printable characters with no surrounding space")
			}
			b, _, err := readBinding(cmd.Context())
			if err != nil {
				return err
			}
			if b == nil {
				return exitcode.Newf(exitcode.NotFound, "no active session in this worktree — start one first")
			}
			if err := checkBindingServer(f, b); err != nil {
				return err
			}
			client, err := f.GraphQLClient()
			if err != nil {
				return api.MapError(err)
			}
			return reportModel(cmd.Context(), f, cmd, client, b.SessionID, model, false)
		},
	}
}
