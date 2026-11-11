package agent

import (
	"fmt"
	"io"
	"strings"

	"github.com/spf13/cobra"

	"github.com/hadron-memory/hadron-cli/internal/api"
	"github.com/hadron-memory/hadron-cli/internal/api/gen"
	"github.com/hadron-memory/hadron-cli/internal/cmdutil"
	"github.com/hadron-memory/hadron-cli/internal/exitcode"
	"github.com/hadron-memory/hadron-cli/internal/output"
)

// cli#1853: `agent boot-prompt` reads Query.agentServedBootPrompt
// (hadron-server#3846, hrn:node:hadronmemory.com:specs:cor:agt:040:05/:10).
// One provider call; the text is printed exactly as returned.

// bootPromptDecisionDTO is the served revision's identity, a stable public shape.
type bootPromptDecisionDTO struct {
	PointerLevel        string `json:"pointerLevel"`
	CompiledNodeID      string `json:"compiledNodeId"`
	CompiledContentHash string `json:"compiledContentHash"`
	ProvenanceRevSeq    int    `json:"provenanceRevSeq"`
	ServedRevSeq        int    `json:"servedRevSeq"`
}

type agentBootPromptDTO struct {
	Decision bootPromptDecisionDTO `json:"decision"`
	Text     string                `json:"text"`
}

func newCmdAgentBootPrompt(f *cmdutil.Factory) *cobra.Command {
	var model string
	cmd := &cobra.Command{
		Use:   "boot-prompt <agent> [--app <app>] [--model <llm-model>]",
		Short: "Read the boot prompt an Agent is served now, without a worker binding",
		Long: `Print the compiled boot prompt a chat or run with this Agent would be given now,
rendered, and the revision served. It binds no worker, starts no session and
records nothing; the answer is the current serve, so an approval between two
calls can change it.

Without --app this is what the Agent's own pointer alone would serve. An App's
install, or a customised worker, may be served a different prompt: pass --app
to read what a chat in that App would be given. The configured default App is
never used here, only an explicit --app.

--model is the model name filled into session.llmModel; it is rendering text,
never an identity claim. Worker-only variables (name, reports-to, signature) are
filled as a chat fills them; the worker briefing's derived blocks are not added.

The server refuses with a typed code and nothing is printed as the prompt:
AGENT_NOT_FOUND (missing, unreadable and not-installed read the same; exit 4);
BOOT_PROMPT_NOT_SET, BOOT_PROMPT_TARGET_DELETED, BOOT_PROMPT_NOT_APPROVED and
BOOT_PROMPT_NOT_SIGNED exit 1 (configuration state; the code in the message is
the signal to branch on).

Output: a first line "served boot prompt: <LEVEL> <compiledNodeId> revision <N>",
then the served text raw. --json prints exactly {decision, text}.`,
		Example: `  hadron agent boot-prompt hrn:agent:acme.com:bot
  hadron agent boot-prompt hrn:agent:acme.com:bot --app hrn:app:acme.com:team --model claude-opus-5-5
  hadron agent boot-prompt hrn:agent:acme.com:bot --json`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ref, err := nonBlankAgentRef(args[0])
			if err != nil {
				return err
			}
			var appRef *string
			if fl := cmd.Flag("app"); fl != nil && fl.Changed {
				if strings.TrimSpace(f.AppFlag) == "" {
					return exitcode.Newf(exitcode.Usage, "--app was given a blank value — name the App, or omit --app")
				}
				a, err := cmdutil.CanonicalAppRef("--app", f.AppFlag)
				if err != nil {
					return err
				}
				appRef = &a
			}
			var llm *string
			if cmd.Flags().Changed("model") {
				if strings.TrimSpace(model) == "" {
					return exitcode.Newf(exitcode.Usage, "--model was given a blank value — name the model, or omit --model")
				}
				llm = &model
			}
			client, err := f.GraphQLClient()
			if err != nil {
				return err
			}
			resp, err := gen.AgentServedBootPrompt(cmd.Context(), client, ref, appRef, llm)
			if err != nil {
				return api.MapErrorKeepingCode(err)
			}
			// R7: the decision proves the serve. A null or malformed one is refused,
			// never shown as an "unknown" header over served text; a valid decision
			// with empty text is a success.
			r := resp.AgentServedBootPrompt
			if r == nil || r.Decision == nil {
				return exitcode.Newf(exitcode.Error, "the server returned a malformed boot-prompt decision; nothing to print")
			}
			d := r.Decision
			// R7(a): the decision proves the serve of THIS read (the agent-read). A
			// WORKER-scope decision proves a worker serve, so showing its text as the
			// Agent's prompt would be wrong data: refuse it, exit 1, nothing printed.
			if d.PointerLevel == gen.ServedPromptPointerLevelWorker {
				return exitcode.Newf(exitcode.Error, "the server returned a WORKER-scope decision for an Agent read; that is not the Agent's served prompt, nothing to print")
			}
			if !validPointerLevel(d.PointerLevel) || strings.TrimSpace(d.CompiledNodeId) == "" || d.ServedRevSeq < 1 {
				return exitcode.Newf(exitcode.Error, "the server returned a malformed boot-prompt decision; nothing to print")
			}
			dto := agentBootPromptDTO{
				Decision: bootPromptDecisionDTO{
					PointerLevel:        string(d.PointerLevel),
					CompiledNodeID:      d.CompiledNodeId,
					CompiledContentHash: d.CompiledContentHash,
					ProvenanceRevSeq:    d.ProvenanceRevSeq,
					ServedRevSeq:        d.ServedRevSeq,
				},
				Text: r.Text,
			}
			return output.Write(f.IOStreams, f.JSON, dto, func(w io.Writer) error {
				// R6: header line, then the served text raw; a final newline is added
				// on a terminal only, so a pipe receives the text byte for byte.
				if _, err := fmt.Fprintf(w, "served boot prompt: %s %s revision %d\n",
					output.SafeCell(dto.Decision.PointerLevel), output.SafeCell(dto.Decision.CompiledNodeID), dto.Decision.ServedRevSeq); err != nil {
					return err
				}
				if _, err := io.WriteString(w, dto.Text); err != nil {
					return err
				}
				if f.IOStreams.IsTerminal() && !strings.HasSuffix(dto.Text, "\n") {
					_, err := io.WriteString(w, "\n")
					return err
				}
				return nil
			})
		},
	}
	cmd.Flags().StringVar(&model, "model", "", "the model name filled into session.llmModel (omitted: empty)")
	return cmd
}

// validPointerLevel accepts only the levels the server's ServedPromptPointerLevel names.
func validPointerLevel(l gen.ServedPromptPointerLevel) bool {
	switch l {
	case gen.ServedPromptPointerLevelAgent, gen.ServedPromptPointerLevelAppAgent, gen.ServedPromptPointerLevelWorker:
		return true
	}
	return false
}
