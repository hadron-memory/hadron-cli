package team

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
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

// modelReportDTO names only what was SENT. Model is null on a family-only
// report (an omitted model is never implied changed); Family is omitted unless
// `set-model --family` sent one, so sync-model's shape is unchanged. Neither
// is the server's normalized answer.
type modelReportDTO struct {
	SessionID string  `json:"sessionId"`
	Model     *string `json:"model"`
	Family    *string `json:"family,omitempty"`
	Reported  bool    `json:"reported"`
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
server derives them; only set-model --family ever sends a family), never clearing a stored model. The worktree binding is
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
it into a tool's settings is the operator's step.

SETUP, IN ORDER: start the session WITH --transcript
(` + "`session start --as <w> --transcript <file>`" + `), so there is a recorded file
to check; then add this
command to the tool's hook settings. For Claude Code, a Stop hook in
.claude/settings.json:
  {"hooks":{"Stop":[{"hooks":[{"type":"command",
    "command":"hadron team session sync-model --hook"}]}]}}`,
		Example: `  # what a hook sends: stdin is the tool's JSON, not a terminal
  echo '{"session_id":"4f0c…","transcript_path":"/home/me/.claude/projects/x/4f0c….jsonl","cwd":"/work/widgets"}' \
    | hadron team session sync-model
  # exit 0: reported; exit ` + fmt.Sprint(modelUnavailableExit) + `: unavailable, nothing sent

  # if your tool has no hook, report by hand instead
  hadron team session set-model claude-opus-5-5`,
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
	if f.IOStreams.IsInputTerminal() {
		return exitcode.Newf(exitcode.Usage, "sync-model reads the hook JSON from stdin; pipe it in (a terminal would wait forever)")
	}
	raw, err := io.ReadAll(io.LimitReader(f.IOStreams.In, hookInputLimit+1))
	var in hookInput
	if err != nil || len(raw) > hookInputLimit || json.Unmarshal(raw, &in) != nil {
		return errModelUnavailable()
	}
	b, _, err := readBindingFile(ctx)
	if err != nil || b == nil || b.SessionID == "" {
		return errModelUnavailable()
	}
	if checkBindingServer(f, b) != nil || !sameTree(ctx, in.Cwd) {
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

// sameTree reports whether the hook's cwd is the worktree this binding lives
// in, or inside it. The worktree is the git top level of the process cwd; when
// that cannot be read the process cwd stands in. A cwd ABOVE the worktree
// (including /) and a cwd in a nested, independent git repo are refused.
func sameTree(ctx context.Context, hookCwd string) bool {
	if hookCwd == "" {
		return false
	}
	root, err := os.Getwd()
	if err != nil {
		return false
	}
	a, err1 := filepath.EvalSymlinks(hookCwd)
	if err1 != nil {
		return false
	}
	if out, err := exec.CommandContext(ctx, "git", "rev-parse", "--show-toplevel").Output(); err == nil {
		root = strings.TrimSpace(string(out))
		// A nested independent repo sits under the worktree's path but is not
		// the worktree: the hook cwd's OWN git top level must be this one.
		hookOut, herr := exec.CommandContext(ctx, "git", "-C", a, "rev-parse", "--show-toplevel").Output()
		if herr != nil {
			return false
		}
		hookTop, e1 := filepath.EvalSymlinks(strings.TrimSpace(string(hookOut)))
		cliTop, e2 := filepath.EvalSymlinks(root)
		if e1 != nil || e2 != nil || hookTop != cliTop {
			return false
		}
	}
	b, err2 := filepath.EvalSymlinks(root)
	if err2 != nil {
		return false
	}
	return a == b || strings.HasPrefix(a, strings.TrimRight(b, string(filepath.Separator))+string(filepath.Separator))
}

func reportModel(ctx context.Context, f *cmdutil.Factory, _ *cobra.Command, client graphql.Client, sessionID, model string, quiet bool) error {
	return reportSessionModel(ctx, f, client, sessionID, &model, nil, quiet)
}

// reportSessionModel is the one updateSession write for model metadata. Each
// of model and family is sent only when non-nil: an omitted field preserves
// the stored value, and nothing here ever sends an explicit null.
func reportSessionModel(ctx context.Context, f *cmdutil.Factory, client graphql.Client, sessionID string, model, family *string, quiet bool) error {
	if _, err := gen.UpdateTeamSession(ctx, client, sessionID, nil, nil, model, family); err != nil {
		return api.MapError(err)
	}
	if quiet {
		return nil
	}
	return output.Write(f.IOStreams, f.JSON, modelReportDTO{SessionID: sessionID, Model: model, Family: family, Reported: true}, func(w io.Writer) error {
		var parts []string
		if model != nil {
			parts = append(parts, "model: "+*model)
		}
		if family != nil {
			parts = append(parts, "family: "+*family+" (as sent; the server normalizes it)")
		}
		note := "last arrival wins"
		if family == nil {
			note += "; the server derives family and tier"
		}
		_, err := fmt.Fprintf(w, "reported %s (%s)\n", strings.Join(parts, ", "), note)
		return err
	})
}

func newCmdSessionSetModel(f *cmdutil.Factory) *cobra.Command {
	var family string
	cmd := &cobra.Command{
		Use:   "set-model [<model>] [--family <token>]",
		Short: "Report the model and/or family this worktree's session is running (manual)",
		Long: `Report <model> and/or an explicit --family for the worker session bound to this
worktree, through the same updateSession as ` + "`sync-model`" + `. At least one is
required; only what you give is sent, so an omitted model or family is
PRESERVED (never cleared). With a model alone the server derives family and
tier, and never clears family on a model it does not map.

--family is the deliberate way to state a family (hadron-server#1931: an
explicit family wins over the derivation from the model). The token is passed
to the server as typed — the CLI normalizes and infers nothing, and a host
such as claude-code is not a family. The server owns what it accepts; its
refusal is shown and no success receipt is printed. The receipt names only
what was sent. ` + "`sync-model`" + ` (the hook) stays model-only and has no --family.

The model is an exact string of at most 128 printable characters, and so is
the family; an empty or blank value is a usage error, not a clear. It uses the
worktree's binding only: there is no --session, and it starts or rebinds
nothing.

Last arrival wins: a later report from a hook or another run can overwrite
this one.`,
		Example: `  hadron team session set-model claude-opus-5-5
  hadron team session set-model glm-4.6 --family glm
  hadron team session set-model --family codex`,
		Args: func(_ *cobra.Command, args []string) error {
			if len(args) > 1 {
				return exitcode.Newf(exitcode.Usage, "set-model takes at most one <model>; got %d", len(args))
			}
			return nil
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			familyGiven := cmd.Flags().Changed("family")
			if len(args) == 0 && !familyGiven {
				return exitcode.Newf(exitcode.Usage, "give a model, a --family, or both (nothing to report)")
			}
			var model, fam *string
			if len(args) == 1 {
				if !modelreport.ValidModel(args[0]) {
					return exitcode.Newf(exitcode.Usage, "model must be 1–128 printable characters with no surrounding space")
				}
				model = &args[0]
			}
			if familyGiven {
				if !modelreport.ValidModel(family) {
					return exitcode.Newf(exitcode.Usage, "--family must be 1–128 printable characters with no surrounding space (an empty value does not clear it)")
				}
				fam = &family
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
			// readBinding's ended-session check above is a server round trip; a
			// rebind can land during it. Re-read and refuse a changed binding
			// rather than write the model onto the stale session.
			again, _, err := readBindingFile(cmd.Context())
			if err != nil || again == nil || again.SessionID != b.SessionID {
				return exitcode.Newf(exitcode.Conflict, "this worktree's session binding changed while reporting — nothing was sent; rerun")
			}
			return reportSessionModel(cmd.Context(), f, client, b.SessionID, model, fam, false)
		},
	}
	cmd.Flags().StringVar(&family, "family", "", "state the model family explicitly (passed to the server as typed; wins over its derivation)")
	return cmd
}
