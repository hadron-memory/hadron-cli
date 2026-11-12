package org

import (
	"context"
	"fmt"
	"io"
	"strconv"

	"github.com/Khan/genqlient/graphql"
	"github.com/spf13/cobra"

	"github.com/hadron-memory/hadron-cli/internal/api"
	"github.com/hadron-memory/hadron-cli/internal/api/gen"
	"github.com/hadron-memory/hadron-cli/internal/cmdutil"
	"github.com/hadron-memory/hadron-cli/internal/exitcode"
	"github.com/hadron-memory/hadron-cli/internal/output"
)

// hadron-cli#1879 / hadron-server#2973 PR 2: `hadron org signing`.
//
// Invariants this file exists to keep (each has a pin in
// internal/cmd/org_signing_2973_test.go):
//   - `on` ALWAYS fetches and shows every page of the preview before it sends
//     the mutation; no flag reaches the mutation any other way (#2973: no
//     surface switches on blind).
//   - `on` sends the FIRST page's token (R6): any change after page 1 makes the
//     server's full-preview digest differ, so the switch answers STALE.
//   - No verb looks the org up first, and no verb ever opts in to the worker
//     session header: the server answers an unknown org and a non-admin with the
//     same FORBIDDEN, and refuses a worker session outright.

// signingAllPagesSize is the server's page cap; all-pages reads use it to keep
// the number of snapshots low.
const signingAllPagesSize = 200

const (
	codeSigningPreviewStale = "BOOT_PROMPT_SIGNING_PREVIEW_STALE"

	signingForbiddenMsg = "forbidden: only the organisation's current ADMINs and OWNERs, signed in as themselves " +
		"(not a worker session or an App key), can read or change its boot-prompt signing switch"

	signingStaleMsg = "the preview changed or expired (it is valid for 10 minutes) before the switch was sent; " +
		"nothing changed. Run `hadron org signing on <org>` again to review the current preview"

	signingDriftMsg = "the preview changed while it was being read; nothing was sent"

	signingNotEnforcedNote = "this deployment does not ship boot-prompt signing, so the switch has no effect here yet"
)

// signingStatusDTO is the stable --json shape of get / off. Org is the ref as
// typed, not canonicalised: canonicalising needs a lookup, and a lookup would
// make an unknown org distinguishable from a non-admin.
type signingStatusDTO struct {
	Org             string `json:"org"`
	Required        bool   `json:"required"`
	Enforced        bool   `json:"enforced"`
	DeploymentSigns bool   `json:"deploymentSigns"`
}

type signingRowDTO struct {
	AppID           string  `json:"appId"`
	AppURN          string  `json:"appUrn"`
	AgentID         string  `json:"agentId"`
	AgentName       string  `json:"agentName"`
	WorkerID        *string `json:"workerId"`
	WorkerName      *string `json:"workerName"`
	CompiledNodeID  string  `json:"compiledNodeId"`
	CompiledNodeURN *string `json:"compiledNodeUrn"`
	ServedRevSeq    int     `json:"servedRevSeq"`
	State           string  `json:"state"`
	ChangesToRevSeq *int    `json:"changesToRevSeq"`
	// AgentURN and WorkerURN are the server's display URNs, printed as served:
	// this CLI builds and re-canonicalises none (cli#1949). Null (an explicit key)
	// when the server could not derive one, and WorkerURN on an install-level row.
	AgentURN  *string `json:"agentUrn"`
	WorkerURN *string `json:"workerUrn"`
}

// signingPreviewDTO is the stable --json shape of preview. It deliberately has
// no previewToken (R10): no verb consumes one, and `on` takes no token flag, so
// the CLI offers no "preview now, switch later from a script" path.
type signingPreviewDTO struct {
	Org             string          `json:"org"`
	Enforced        bool            `json:"enforced"`
	DeploymentSigns bool            `json:"deploymentSigns"`
	Total           int             `json:"total"`
	Rows            []signingRowDTO `json:"rows"`
	HasMore         bool            `json:"hasMore"`
	EndCursor       *string         `json:"endCursor"`
}

type signingShownDTO struct {
	Total int             `json:"total"`
	Rows  []signingRowDTO `json:"rows"`
}

// signingOnDTO is the stdout document of `on`: the status plus the preview that
// was acted on (R2), so an agent's record of the switch includes what it saw.
type signingOnDTO struct {
	Org             string          `json:"org"`
	Required        bool            `json:"required"`
	Enforced        bool            `json:"enforced"`
	DeploymentSigns bool            `json:"deploymentSigns"`
	Preview         signingShownDTO `json:"preview"`
}

// previewPage is one server page plus the token it minted. The token stays out
// of every DTO.
type previewPage struct {
	dto   signingPreviewDTO
	token string
}

func newCmdSigning(f *cmdutil.Factory) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "signing <command>",
		Short: "Read, preview and switch an organization's boot-prompt signing",
		Long: `An organization can require that the boot prompts its Apps serve are signed
(passkey) as well as approved. These commands read that switch, preview what
turning it on would change, and set it. They are for the organization's
current ADMINs and OWNERs, signed in as themselves: a worker session, an App
key or an impersonation is refused, and so is anyone else.

  get      the switch: required, enforced, deploymentSigns
  preview  the served boot prompts the switch would stop or change
  on       show the full preview, confirm, then switch on
  off      switch off (no preview needed)

"enforced" is the switch AND the deployment shipping signing, so an
organization never reads as protected where it is not. <org> is passed to the
server as given (an id, URN or root such as acme.com) and is never looked up
first: an unknown organization and a non-admin get the same refusal (exit 8).`,
		Example: `  hadron org signing get acme.com
  hadron org signing preview acme.com --all
  hadron org signing on acme.com`,
	}
	cmd.AddCommand(newCmdSigningGet(f))
	cmd.AddCommand(newCmdSigningPreview(f))
	cmd.AddCommand(newCmdSigningOn(f))
	cmd.AddCommand(newCmdSigningOff(f))
	return cmd
}

func newCmdSigningGet(f *cmdutil.Factory) *cobra.Command {
	return &cobra.Command{
		Use:   "get <org>",
		Short: "Show an organization's boot-prompt signing switch",
		Long: `Show whether the organization requires signed boot prompts (required), and
whether that is actually in force (enforced). When required is on but enforced
is off, this deployment does not ship signing and the switch has no effect yet.
Needs ADMIN or OWNER of the organization, signed in as yourself.`,
		Example: `  hadron org signing get acme.com
  hadron org signing get acme.com --json`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := f.GraphQLClient()
			if err != nil {
				return err
			}
			resp, err := gen.OrgBootPromptSigning(cmd.Context(), client, args[0])
			if err != nil {
				return signingError(err)
			}
			s := resp.OrgBootPromptSigning
			if s == nil {
				return exitcode.Newf(exitcode.Error, "server returned an empty orgBootPromptSigning result")
			}
			dto := signingStatusDTO{Org: args[0], Required: s.Required, Enforced: s.Enforced, DeploymentSigns: s.DeploymentSigns}
			return output.Write(f.IOStreams, f.JSON, dto, func(w io.Writer) error {
				return writeSigningStatus(w, dto)
			})
		},
	}
}

func newCmdSigningPreview(f *cmdutil.Factory) *cobra.Command {
	var first, after string
	var all bool
	cmd := &cobra.Command{
		Use:   "preview <org>",
		Short: "Preview what switching boot-prompt signing on would change",
		Long: `List the served boot prompts that switching signing on would change, with the
App, Agent and worker each belongs to, the revision served today, and what
would happen: SIGNATURE_PENDING (not served until the signature's cooling-off
ends), REVISION_CHANGES (falls back to an older signed revision) or
UNSIGNED_STOPS (would be refused). The Agent and Worker URNs are the server's,
printed as served (empty when it could not derive one; --json shows null).

A plain preview reads ONE server page (20 rows) and prints how to get more.
--all reads every page; --first and --after choose a page by hand (--all
cannot be combined with them). This is read-only: to switch on, use
hadron org signing on, which fetches and shows the full preview itself.`,
		Example: `  hadron org signing preview acme.com
  hadron org signing preview acme.com --all
  hadron org signing preview acme.com --after <cursor>`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			var firstN *int
			var afterC *string
			if all && (cmd.Flags().Changed("first") || cmd.Flags().Changed("after")) {
				return exitcode.Newf(exitcode.Usage, "--all cannot be combined with --first or --after")
			}
			if cmd.Flags().Changed("first") {
				n, err := strconv.Atoi(first)
				if err != nil {
					return exitcode.Newf(exitcode.Usage, "--first must be an integer, got %q", output.SafeCell(first))
				}
				firstN = &n
			}
			if cmd.Flags().Changed("after") {
				afterC = &after
			}
			client, err := f.GraphQLClient()
			if err != nil {
				return err
			}
			var dto signingPreviewDTO
			if all {
				page, err := collectSigningPreview(cmd.Context(), client, args[0])
				if err != nil {
					return err
				}
				dto = page.dto
			} else {
				page, err := fetchSigningPreviewPage(cmd.Context(), client, args[0], firstN, afterC)
				if err != nil {
					return err
				}
				dto = page.dto
			}
			return output.Write(f.IOStreams, f.JSON, dto, func(w io.Writer) error {
				if err := writeSigningPreview(w, dto); err != nil {
					return err
				}
				if dto.HasMore && dto.EndCursor != nil {
					_, err := fmt.Fprintf(w, "\nMore rows: --after %s for the next page, or --all for every page.\n", output.SafeCell(*dto.EndCursor))
					return err
				}
				return nil
			})
		},
	}
	cmd.Flags().StringVar(&first, "first", "", "page size to request (the server serves 20 by default and at most 200)")
	cmd.Flags().StringVar(&after, "after", "", "cursor from a previous page's endCursor")
	cmd.Flags().BoolVar(&all, "all", false, "read every page")
	return cmd
}

func newCmdSigningOn(f *cmdutil.Factory) *cobra.Command {
	var yes bool
	cmd := &cobra.Command{
		Use:   "on <org>",
		Short: "Switch an organization's boot-prompt signing on",
		Long: `Switch boot-prompt signing on for the organization. The command first reads
EVERY page of the preview and shows it on stderr, then asks you to confirm,
then switches on with the preview's token. There is no way to skip the fetch or
the display; --yes skips only the confirmation prompt, and a non-interactive
run without --yes is refused (exit 2). An empty preview is still confirmed:
the switch also governs prompts compiled and approved later.

If the preview changed or expired (10 minutes) before the switch was sent, it
answers exit 5 and nothing changes: run this command again. Needs ADMIN or
OWNER of the organization, signed in as yourself. stdout carries one document:
the new status plus the preview you were shown.`,
		Example: `  hadron org signing on acme.com
  hadron org signing on acme.com --yes --json`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := f.GraphQLClient()
			if err != nil {
				return err
			}
			// Unconditional: no flag, global flag or environment variable reaches
			// the mutation without this read.
			page, err := collectSigningPreview(cmd.Context(), client, args[0])
			if err != nil {
				return err
			}
			// Written BEFORE the prompt, to stderr so stdout stays one document.
			if f.JSON {
				if err := output.WriteJSON(f.IOStreams.ErrOut, page.dto); err != nil {
					return err
				}
			} else {
				if err := writeSigningPreview(f.IOStreams.ErrOut, page.dto); err != nil {
					return err
				}
				if len(page.dto.Rows) == 0 {
					if _, err := fmt.Fprintln(f.IOStreams.ErrOut, "Nothing served today would change."); err != nil {
						return err
					}
				}
			}
			prompt := fmt.Sprintf("Switch boot-prompt signing ON for %s? The %d prompt(s) listed above would change or stop being served; prompts compiled and approved later must be signed too", output.SafeCell(args[0]), page.dto.Total)
			if page.dto.Total == 0 {
				prompt = fmt.Sprintf("Switch boot-prompt signing ON for %s? Nothing served today would change; prompts compiled and approved later must be signed", output.SafeCell(args[0]))
			}
			if err := cmdutil.Confirm(f.IOStreams, yes, prompt); err != nil {
				return err
			}
			token := page.token
			resp, err := gen.SetOrgBootPromptSigning(cmd.Context(), client, args[0], true, &token)
			if err != nil {
				return signingError(err)
			}
			s := resp.SetOrgBootPromptSigning
			if s == nil {
				return exitcode.Newf(exitcode.Error, "server returned an empty setOrgBootPromptSigning result; run `hadron org signing get` to see the switch")
			}
			dto := signingOnDTO{
				Org: args[0], Required: s.Required, Enforced: s.Enforced, DeploymentSigns: s.DeploymentSigns,
				Preview: signingShownDTO{Total: page.dto.Total, Rows: page.dto.Rows},
			}
			return output.Write(f.IOStreams, f.JSON, dto, func(w io.Writer) error {
				return writeSigningStatus(w, signingStatusDTO{Org: dto.Org, Required: dto.Required, Enforced: dto.Enforced, DeploymentSigns: dto.DeploymentSigns})
			})
		},
	}
	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "skip the confirmation prompt (the preview is still fetched and shown)")
	return cmd
}

func newCmdSigningOff(f *cmdutil.Factory) *cobra.Command {
	var yes bool
	cmd := &cobra.Command{
		Use:   "off <org>",
		Short: "Switch an organization's boot-prompt signing off",
		Long: `Switch boot-prompt signing off for the organization: its Apps may again serve
boot prompts that are approved but not signed. No preview or token is needed,
and switching off an already-off switch succeeds. It asks you to confirm;
--yes skips the prompt, and a non-interactive run without --yes is refused
(exit 2). Needs ADMIN or OWNER of the organization, signed in as yourself.`,
		Example: `  hadron org signing off acme.com
  hadron org signing off acme.com --yes --json`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := f.GraphQLClient()
			if err != nil {
				return err
			}
			prompt := fmt.Sprintf("Switch boot-prompt signing OFF for %s? Its Apps may again serve boot prompts that are not signed", output.SafeCell(args[0]))
			if err := cmdutil.Confirm(f.IOStreams, yes, prompt); err != nil {
				return err
			}
			resp, err := gen.SetOrgBootPromptSigning(cmd.Context(), client, args[0], false, nil)
			if err != nil {
				return signingError(err)
			}
			s := resp.SetOrgBootPromptSigning
			if s == nil {
				return exitcode.Newf(exitcode.Error, "server returned an empty setOrgBootPromptSigning result; run `hadron org signing get` to see the switch")
			}
			dto := signingStatusDTO{Org: args[0], Required: s.Required, Enforced: s.Enforced, DeploymentSigns: s.DeploymentSigns}
			return output.Write(f.IOStreams, f.JSON, dto, func(w io.Writer) error {
				return writeSigningStatus(w, dto)
			})
		},
	}
	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "skip the confirmation prompt")
	return cmd
}

// signingError maps a server error for these verbs. FORBIDDEN gets ONE constant
// message whether the org exists or not, and the code makes no extra call to
// find out. The server's code rides along for the --json envelope's serverCode.
func signingError(err error) error {
	mapped := api.MapError(err)
	switch api.ServerCodeOf(err) {
	case "FORBIDDEN":
		mapped = exitcode.Newf(exitcode.Forbidden, "%s", signingForbiddenMsg)
	case codeSigningPreviewStale:
		mapped = exitcode.Newf(exitcode.Conflict, "%s", signingStaleMsg)
	}
	return api.WithServerCode(mapped, err)
}

// previewURNFields are the two fields the preview document selects that a
// server older than previewMinServerCommit lacks (cli#1949).
var previewURNFields = []string{"agentUrn", "workerUrn"}

// previewMinServerCommit is the hadron-server commit that serves them, from the
// settled cli#1949 gate. It is guidance in the message only, never a support
// decision: the CLI learns a field is missing from the server's own refusal.
const previewMinServerCommit = "cac6c05c6"

// signingOlderServerMsg: the preview request is what the server refused, so it
// says no signing change was sent, not that nothing was.
const signingOlderServerMsg = "this server is older than this hadron build needs for the boot-prompt signing preview: " +
	"it does not serve agentUrn and workerUrn (needs hadron-server at or after " + previewMinServerCommit + "). " +
	"The preview was refused and no signing change was sent. Upgrade the server, or use an older hadron build"

func fetchSigningPreviewPage(ctx context.Context, client graphql.Client, org string, first *int, after *string) (previewPage, error) {
	resp, err := gen.OrgBootPromptSigningPreview(ctx, client, org, first, after)
	if err != nil {
		// cli#1955: a server without the two URN fields refuses this document.
		// Only that exact refusal (every parsed error is a validation rejection
		// naming agentUrn or workerUrn, with the framework code) gets its own sentence; anything else
		// keeps the existing mapping. Exit 2, the framework code rides along.
		if api.IsFrameworkValidationOnlyFor(err, previewURNFields...) {
			return previewPage{}, api.WithServerCode(exitcode.Newf(exitcode.Usage, "%s", signingOlderServerMsg), err)
		}
		return previewPage{}, signingError(err)
	}
	p := resp.OrgBootPromptSigningPreview
	if p == nil {
		return previewPage{}, exitcode.Newf(exitcode.Error, "server returned an empty orgBootPromptSigningPreview result; nothing was sent")
	}
	dto := signingPreviewDTO{
		Org: org, Enforced: p.Enforced, DeploymentSigns: p.DeploymentSigns, Total: p.Total,
		Rows: []signingRowDTO{}, HasMore: p.HasMore, EndCursor: p.EndCursor,
	}
	for _, r := range p.Rows {
		if r == nil {
			continue
		}
		dto.Rows = append(dto.Rows, signingRowDTO{
			AppID: r.AppId, AppURN: r.AppUrn, AgentID: r.AgentId, AgentName: r.AgentName,
			WorkerID: r.WorkerId, WorkerName: r.WorkerName,
			CompiledNodeID: r.CompiledNodeId, CompiledNodeURN: r.CompiledNodeUrn,
			ServedRevSeq: r.ServedRevSeq, State: string(r.State), ChangesToRevSeq: r.ChangesToRevSeq,
			AgentURN: r.AgentUrn, WorkerURN: r.WorkerUrn,
		})
	}
	return previewPage{dto: dto, token: p.PreviewToken}, nil
}

// collectSigningPreview reads every page at the server's cap and returns the
// whole preview with the FIRST page's token (R6). Any sign the preview moved
// while it was being read is refused with no partial output: the user must
// never agree to rows they were not shown.
func collectSigningPreview(ctx context.Context, client graphql.Client, org string) (previewPage, error) {
	size := signingAllPagesSize
	var out previewPage
	var after *string
	seenCursor := map[string]bool{}
	seenRow := map[[3]string]bool{}
	for n := 0; ; n++ {
		page, err := fetchSigningPreviewPage(ctx, client, org, &size, after)
		if err != nil {
			return previewPage{}, err
		}
		if n == 0 {
			out = page
			out.dto.Rows = []signingRowDTO{}
		} else if page.dto.Total != out.dto.Total {
			return previewPage{}, exitcode.Newf(exitcode.Error, "%s", signingDriftMsg)
		}
		for _, r := range page.dto.Rows {
			key := [3]string{r.AppID, r.AgentID, ""}
			if r.WorkerID != nil {
				key[2] = "w:" + *r.WorkerID
			}
			if seenRow[key] {
				return previewPage{}, exitcode.Newf(exitcode.Error, "%s", signingDriftMsg)
			}
			seenRow[key] = true
			out.dto.Rows = append(out.dto.Rows, r)
		}
		if !page.dto.HasMore {
			break
		}
		c := page.dto.EndCursor
		if c == nil || *c == "" || seenCursor[*c] {
			return previewPage{}, exitcode.Newf(exitcode.Error, "%s", signingDriftMsg)
		}
		seenCursor[*c] = true
		after = c
	}
	if len(out.dto.Rows) != out.dto.Total {
		return previewPage{}, exitcode.Newf(exitcode.Error, "%s", signingDriftMsg)
	}
	out.dto.HasMore = false
	out.dto.EndCursor = nil
	return out, nil
}

func yesNo(b bool) string {
	if b {
		return "yes"
	}
	return "no"
}

// writeSigningStatus and writeSigningPreview return every write error: on's
// display-before-mutation guarantee is worthless if a failed display is
// swallowed and the mutation goes anyway.
func writeSigningStatus(w io.Writer, st signingStatusDTO) error {
	if _, err := fmt.Fprintf(w, "required: %s\nenforced: %s\ndeploymentSigns: %s\n", yesNo(st.Required), yesNo(st.Enforced), yesNo(st.DeploymentSigns)); err != nil {
		return err
	}
	if st.Required && !st.Enforced {
		if _, err := fmt.Fprintf(w, "enforced: no — %s\n", signingNotEnforcedNote); err != nil {
			return err
		}
	}
	return nil
}

func writeSigningPreview(w io.Writer, p signingPreviewDTO) error {
	if _, err := fmt.Fprintf(w, "total: %d\nenforced: %s\ndeploymentSigns: %s\n", p.Total, yesNo(p.Enforced), yesNo(p.DeploymentSigns)); err != nil {
		return err
	}
	if !p.DeploymentSigns {
		if _, err := fmt.Fprintf(w, "enforced: no — %s\n", signingNotEnforcedNote); err != nil {
			return err
		}
	}
	if len(p.Rows) == 0 {
		return nil
	}
	t := output.NewTable(w, "APP", "AGENT", "WORKER", "COMPILED NODE", "SERVED", "STATE", "FALLS BACK TO", "AGENT URN", "WORKER URN")
	for _, r := range p.Rows {
		worker := "(install)"
		if r.WorkerName != nil {
			worker = *r.WorkerName
		} else if r.WorkerID != nil {
			worker = *r.WorkerID
		}
		node := r.CompiledNodeID
		if r.CompiledNodeURN != nil {
			node = *r.CompiledNodeURN
		}
		falls := ""
		if r.ChangesToRevSeq != nil {
			falls = "rev " + strconv.Itoa(*r.ChangesToRevSeq)
		}
		t.Row(output.SafeCell(r.AppURN), output.SafeCell(r.AgentName), output.SafeCell(worker), output.SafeCell(node),
			"rev "+strconv.Itoa(r.ServedRevSeq), r.State, falls,
			output.SafeCell(derefOrEmpty(r.AgentURN)), output.SafeCell(derefOrEmpty(r.WorkerURN)))
	}
	return t.Flush()
}

// derefOrEmpty renders an absent server URN as an empty cell: the text view
// omits what the server did not send rather than printing a placeholder.
func derefOrEmpty(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}
