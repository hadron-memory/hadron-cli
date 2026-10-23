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
		Long: `Read the node's authorized canonical portal URL, never a guessed host or route.
Refs may be node ids or qualified URNs; -m composes a bare loc. A qualified
ref already names its memory, even alongside -m.

Interactive text output requests the platform browser launcher and prints the
URL. --print, --json and non-terminal stdout only print the pointer and never
launch. JSON is {id,urn,portalUrl}. A successful launcher request is not proof
that a browser loaded the page. A launcher failure is an error, not success.

Missing or unreadable nodes share exit4. A missing/null or invalid canonical
URL exits7 (unavailable), without constructing a fallback. No content is read.
Only node open is delivered here; memory/agent/app open wait for their provider
canonical URL fields (hadron-server#2309).`,
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
				return exitcode.Newf(exitcode.Unavailable, "canonical portal URL unavailable: the server did not supply one")
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
		return exitcode.Newf(exitcode.Unavailable, "canonical portal URL unavailable: the server returned an invalid http(s) URL")
	}
	if !f.JSON && !printOnly && terminal {
		if err := open(dto.PortalURL); err != nil {
			return exitcode.Newf(exitcode.Error, "browser launcher failed: %v; use --print to read the URL without navigation", err)
		}
	}
	return output.Write(f.IOStreams, f.JSON, dto, func(w io.Writer) error { _, err := fmt.Fprintln(w, dto.PortalURL); return err })
}
