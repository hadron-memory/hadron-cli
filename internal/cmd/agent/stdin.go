package agent

import "github.com/hadron-memory/hadron-cli/internal/cmdutil"

// refusePromptStdin refuses `--system-prompt -` from an interactive terminal
// (#648). A prompt is a document, often long; terminal line discipline could
// truncate it. Run before reading the prompt or contacting the server.
func refusePromptStdin(stdinIsTerminal bool, systemPrompt string) error {
	if systemPrompt == "-" {
		return cmdutil.RefuseDocumentStdinFromTerminal(stdinIsTerminal, "--system-prompt -", "--system-prompt-file")
	}
	return nil
}
