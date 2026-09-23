package agent

import "github.com/hadron-memory/hadron-cli/internal/cmdutil"

// refusePromptStdin refuses `--system-prompt -` / `--persona-prompt -` from an
// interactive terminal (#648). A prompt is a DOCUMENT, not a secret — often
// long, and read on every run of the agent — so a copy truncated by the
// terminal's line discipline would be stored and then followed. Called with
// refuseMultiStdin, before the prompt is read or the agent written.
func refusePromptStdin(stdinIsTerminal bool, systemPrompt, personaPrompt string) error {
	if systemPrompt == "-" {
		if err := cmdutil.RefuseDocumentStdinFromTerminal(stdinIsTerminal, "--system-prompt -", "--system-prompt-file"); err != nil {
			return err
		}
	}
	if personaPrompt == "-" {
		return cmdutil.RefuseDocumentStdinFromTerminal(stdinIsTerminal, "--persona-prompt -", "--persona-prompt-file")
	}
	return nil
}
