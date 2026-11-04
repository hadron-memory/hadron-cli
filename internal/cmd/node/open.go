package node

import (
	"fmt"
	"io"
	"net/url"
	"strings"

	"github.com/hadron-memory/hadron-cli/internal/api"
	"github.com/hadron-memory/hadron-cli/internal/api/gen"
	"github.com/hadron-memory/hadron-cli/internal/auth"
	"github.com/hadron-memory/hadron-cli/internal/cmdutil"
	"github.com/hadron-memory/hadron-cli/internal/exitcode"
	"github.com/hadron-memory/hadron-cli/internal/output"
	"github.com/spf13/cobra"
)

type nodePortalDTO struct {
	ID        string `json:"id"`
	URN       string `json:"urn"`
	PortalURL string `json:"portalUrl"`
}

func newCmdOpen(f *cmdutil.Factory) *cobra.Command {
	var memory string
	var printOnly bool
	cmd := &cobra.Command{
		Use:   "open <node-ref> [-m <memory>] [--print]",
		Short: "Open a node's server-returned canonical portal URL",
		Long: `Open a node in the web portal, using the link the server gives for it (never a
guessed address). Name the node by its id or URN, or by its loc with -m. A
URN already names its memory, even alongside -m.

Interactive text output requests the platform browser launcher and prints the
URL. --print, --json and non-terminal stdout only print the pointer and never
launch. JSON is {id,urn,portalUrl}. A successful launcher request is not proof
that a browser loaded the page. A launcher failure is an error, not success.

Missing or unreadable nodes share exit 4. A missing/null or invalid canonical
URL exits 1: the server answered, so retrying blindly will not fix it. No
fallback is constructed. This query selects only id, urn and portalUrl; the
server still records a node read and may decrypt internally.`,
		Example: `  hadron node open findings:flaky-ci -m hrn:mem:acme.com:kb
  hadron node open hrn:node:acme.com:kb:findings:flaky-ci --print`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if cmd.Flags().Changed("memory") && strings.TrimSpace(memory) == "" {
				return exitcode.Newf(exitcode.Usage, "-m/--memory must not be empty")
			}
			scope := memory
			if cmdutil.IsQualifiedNodeRef(args[0]) {
				scope = ""
			}
			ref, err := cmdutil.BatchNodeRef(scope, args[0])
			if err != nil {
				return err
			}
			client, err := f.GraphQLClient()
			if err != nil {
				return err
			}
			resp, err := gen.NodePortal(cmd.Context(), client, ref)
			if err != nil {
				return api.MapError(err)
			}
			if resp.Node == nil {
				return exitcode.Newf(exitcode.NotFound, "node is missing or not readable")
			}
			if resp.Node.PortalUrl == nil {
				return exitcode.Newf(exitcode.Error, "canonical portal URL unavailable: the server did not supply one")
			}
			dto := nodePortalDTO{ID: resp.Node.Id, URN: resp.Node.Urn, PortalURL: *resp.Node.PortalUrl}
			return writeNodePortal(f, dto, printOnly, f.IOStreams.IsTerminal(), auth.OpenInBrowser)
		},
	}
	cmd.Flags().StringVarP(&memory, "memory", "m", "", "memory for a bare node loc")
	cmd.Flags().BoolVar(&printOnly, "print", false, "print the pointer without requesting a browser launcher")
	return cmd
}

// The opener is injected here so tests never navigate. JSON/headless/print paths
// are checked before touching it; the production helper uses argv, not a shell.
func writeNodePortal(f *cmdutil.Factory, dto nodePortalDTO, printOnly, terminal bool, open func(string) error) error {
	parsed, err := url.Parse(dto.PortalURL)
	if err != nil || parsed.Host == "" || parsed.Hostname() == "" || parsed.User != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Opaque != "" {
		return exitcode.Newf(exitcode.Error, "canonical portal URL unavailable: the server returned an invalid http(s) URL")
	}
	if !f.JSON && !printOnly && terminal {
		if err := open(dto.PortalURL); err != nil {
			return exitcode.Newf(exitcode.Error, "browser launcher failed: %v; canonical URL: %s (use --print for pointer-only output)", err, dto.PortalURL)
		}
	}
	return output.Write(f.IOStreams, f.JSON, dto, func(w io.Writer) error { _, err := fmt.Fprintln(w, dto.PortalURL); return err })
}
