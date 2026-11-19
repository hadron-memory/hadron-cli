// Package aiconfig implements `hadron ai-config ...` — the AI service
// config management surface: masked reads, CRUD, endpoints and saved probes.
package aiconfig

import (
	"github.com/spf13/cobra"

	"github.com/hadron-memory/hadron-cli/internal/cmdutil"
)

func NewCmdAiConfig(f *cmdutil.Factory) *cobra.Command {
	cmd := &cobra.Command{
		Use:     "ai-config <command>",
		Aliases: []string{"ai-configs"},
		Short:   "Work with Hadron AI service configs",
		Long: `Manage AI service configs: the saved provider, model and API key that Hadron
uses when it calls an AI model for you. Each config has a name and one owner
(an App, an Agent, an Organization or the Hadron server). A chat selects a
config by name; when several owners have a config of that name, the innermost
owner wins (App, then Agent, then Organization, then server).

A typical path: see which providers and base URLs exist ('endpoints'), 'create'
a config, check it works ('test', and 'test --chat-probe' for chat use),
then 'list', 'get', 'update' or 'rm' it. 'tool-choice' answers, before you save
anything, how chat would offer its respond tool to a provider and model pair.
API keys are never printed: reads show only whether a key is stored.`,
	}
	cmd.AddCommand(newCmdLs(f))
	cmd.AddCommand(newCmdGet(f))
	cmd.AddCommand(newCmdEndpoints(f))
	cmd.AddCommand(newCmdTest(f))
	cmd.AddCommand(newCmdCreate(f))
	cmd.AddCommand(newCmdUpdate(f))
	cmd.AddCommand(newCmdRm(f))
	cmd.AddCommand(newCmdToolChoice(f))
	return cmd
}
