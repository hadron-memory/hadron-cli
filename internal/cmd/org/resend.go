package org

import (
	"fmt"
	"io"
	"strings"

	"github.com/hadron-memory/hadron-cli/internal/api"
	"github.com/hadron-memory/hadron-cli/internal/api/gen"
	"github.com/hadron-memory/hadron-cli/internal/cmdutil"
	"github.com/hadron-memory/hadron-cli/internal/exitcode"
	"github.com/hadron-memory/hadron-cli/internal/output"
	"github.com/spf13/cobra"
)

type resendSenderDTO struct {
	ID             string  `json:"id"`
	OrganizationID string  `json:"organizationId"`
	From           string  `json:"from"`
	Enabled        bool    `json:"enabled"`
	HasAPIKey      bool    `json:"hasApiKey"`
	CreatedAt      string  `json:"createdAt"`
	UpdatedAt      string  `json:"updatedAt"`
	CreatedBy      *string `json:"createdBy"`
	UpdatedBy      *string `json:"updatedBy"`
}

func senderDTO(s gen.ResendSenderFields) resendSenderDTO {
	return resendSenderDTO{s.Id, s.OrganizationId, s.From, s.Enabled, s.HasApiKey, s.CreatedAt, s.UpdatedAt, s.CreatedBy, s.UpdatedBy}
}

func emitSender(f *cmdutil.Factory, d resendSenderDTO) error {
	return output.Write(f.IOStreams, f.JSON, d, func(w io.Writer) error {
		_, err := fmt.Fprintf(w, "sender %s\n  org: %s\n  from: %s\n  enabled: %t\n  has API key: %t\n", output.SafeCell(d.ID), output.SafeCell(d.OrganizationID), output.SafeCell(d.From), d.Enabled, d.HasAPIKey)
		return err
	})
}

// resendDomainVerifiedFloor is the server floor: the UTC day hadron-server's ResendSender.domainVerified
// (hadron-server#4865) merged. `get` asks for it in its only query, so an older server rejects the whole
// read as an unknown field, which the help says (cli#2588).
const resendDomainVerifiedFloor = "2026-11-22"

// resendSenderGetDTO is what `get` prints: the sender plus Resend's domain-verified state (cli#2588, report
// only). DomainVerified is a pointer and has no omitempty on purpose: true and false are Resend's answer, and
// null means the state could not be read, which says nothing about the domain, so it is a present key and
// never a false.
type resendSenderGetDTO struct {
	resendSenderDTO
	DomainVerified *bool `json:"domainVerified"`
}

func domainVerifiedText(v *bool) string {
	switch {
	case v == nil:
		return "unknown (could not be read; says nothing about the domain)"
	case *v:
		return "yes"
	default:
		return "no (Resend reports it not verified, or has no record of the domain)"
	}
}

func emitSenderGet(f *cmdutil.Factory, d resendSenderGetDTO) error {
	return output.Write(f.IOStreams, f.JSON, d, func(w io.Writer) error {
		_, err := fmt.Fprintf(w, "sender %s\n  org: %s\n  from: %s\n  enabled: %t\n  has API key: %t\n  domain verified: %s\n", output.SafeCell(d.ID), output.SafeCell(d.OrganizationID), output.SafeCell(d.From), d.Enabled, d.HasAPIKey, domainVerifiedText(d.DomainVerified))
		return err
	})
}

// A write-only credential must not return in a backend/transport diagnostic.
// Keep the classified exit and server code, but never retain the raw error chain.
func senderWriteError(err error, secret *string) error {
	if secret == nil {
		return api.MapErrorKeepingCode(err)
	}
	mapped := api.MapError(err)
	safe := exitcode.Newf(exitcode.FromError(mapped), "Resend sender write failed; API key and server diagnostic withheld (check access, sender input and server availability)")
	// Even an invalid server code must not reflect the credential.
	if strings.Contains(api.ServerCodeOf(err), *secret) {
		return safe
	}
	return api.WithServerCode(safe, err)
}

func newCmdResendSender(f *cmdutil.Factory) *cobra.Command {
	cmd := &cobra.Command{Use: "resend-sender <command>", Short: "Manage an organization's Resend sender (write-only API key)", Long: `A Resend sender is the address and Resend API key an organization sends its
email from. Resend is an outside email-sending service; an organization has at
most one sender, and its AI agents' runs send mail through it (the
hadron_send_email tool, when the App's policy allows). A disabled sender stops
that mail. You need a Resend account with a verified sending domain first.

Create, inspect, update or delete the sender with the commands below. Org
members can read sanitized sender details; org ADMIN/OWNER can write. Only
hasApiKey is returned, never the API key. Creating or updating a sender does
not send mail. Other commands take the sender's ID, which "create" and "list"
print.

Keys are read from --api-key-file (a path or - for stdin), never argv values.
For create, omit that flag to read piped stdin or use a masked terminal prompt.
For update, pass the flag explicitly to rotate the key.`, Example: "  hadron org resend-sender list\n  hadron org resend-sender create --org acme.com --from 'Robot <robot@example.com>' --api-key-file ./resend.key"}
	cmd.AddCommand(newCmdSenderWrite(f, false), newCmdSenderWrite(f, true), newCmdSenderGet(f), newCmdSenderList(f), newCmdSenderRm(f))
	return cmd
}

const senderCreateExample = "  hadron org resend-sender create --org acme.com --from 'Robot <robot@example.com>' --api-key-file ./resend.key"

const senderUpdateExample = "  hadron org resend-sender update <sender-id> --enabled=false\n  hadron org resend-sender update <sender-id> --api-key-file ./new-resend.key"

const senderCreateLong = `Create the organization's Resend sender: the address its mail is sent from and
the Resend API key that sends it. The server checks org ADMIN/OWNER access.

Before you start, in Resend: verify the sending domain (Resend, not Hadron,
checks it; mail from an unverified domain fails when it is sent, not here) and
create an API key, ideally one limited to sending from that domain. --from is
that address, for example Robot <robot@example.com>.

Give the key with --api-key-file (a path, or - for stdin), never inline;
without the flag, create reads piped stdin or prompts with input hidden. The
new sender is on unless you pass --enabled=false. Prints the sender: its ID
(what get, update and rm take), organization, address, enabled, and
whether a key is stored (hasApiKey), never the key. Creating a sender sends
no mail.`

const senderUpdateLong = `Change a sender's address, its enabled state or its API key. Anything you do
not pass keeps its current value; pass at least one of --from, --enabled or
--api-key-file. The server checks org ADMIN/OWNER access. Find the sender ID
with "hadron org resend-sender list".

--enabled=false switches the sender off, which stops the organization's mail
until you pass --enabled again. --api-key-file rotates the key (a path, or -
for stdin, never inline); omit it and the stored key is kept. Prints the
sender as it now is (hasApiKey, never the key).`

func newCmdSenderWrite(f *cmdutil.Factory, update bool) *cobra.Command {
	var org, from, keyFile string
	var enabled bool
	use := "create --org <ref> --from <address>"
	short := "Create an organization's sender with a write-only API key"
	argsCheck := cobra.NoArgs
	if update {
		use = "update <sender-id>"
		short = "Update supplied sender fields or rotate its API key"
		argsCheck = cobra.ExactArgs(1)
	}
	longText := senderCreateLong
	example := senderCreateExample
	if update {
		longText = senderUpdateLong
		example = senderUpdateExample
	}
	cmd := &cobra.Command{Use: use, Short: short, Args: argsCheck, Long: longText, Example: example, RunE: func(cmd *cobra.Command, args []string) error {
		changed := cmd.Flags().Changed
		org = strings.TrimSpace(org)
		from = strings.TrimSpace(from)
		if update {
			args[0] = strings.TrimSpace(args[0])
		}
		if update && strings.TrimSpace(args[0]) == "" {
			return exitcode.Newf(exitcode.Usage, "sender ID must not be empty")
		}
		if !update && strings.TrimSpace(org) == "" {
			return exitcode.Newf(exitcode.Usage, "--org must not be empty")
		}
		if (!update || changed("from")) && strings.TrimSpace(from) == "" {
			return exitcode.Newf(exitcode.Usage, "--from must not be empty")
		}
		if update && !changed("from") && !changed("enabled") && !changed("api-key-file") {
			return exitcode.Newf(exitcode.Usage, "nothing to update; pass --from, --enabled or --api-key-file")
		}
		if changed("api-key-file") && keyFile == "" {
			return exitcode.Newf(exitcode.Usage, "--api-key-file requires a path or -")
		}
		var key *string
		if !update || changed("api-key-file") {
			value, err := cmdutil.ReadSecretInput(f.IOStreams, keyFile, "api-key-file", "Resend API key", "Resend API key")
			if err != nil {
				return api.MapError(err)
			}
			key = &value
		}
		client, err := f.GraphQLClient()
		if err != nil {
			return err
		}
		var en *bool
		if changed("enabled") {
			en = &enabled
		}
		var fields gen.ResendSenderFields
		if update {
			var fr *string
			if changed("from") {
				fr = &from
			}
			resp, e := gen.UpdateResendSender(cmd.Context(), client, args[0], fr, key, en)
			if e != nil {
				return senderWriteError(e, key)
			}
			if resp == nil || resp.UpdateResendSender == nil {
				return exitcode.Newf(exitcode.Error, "server returned no sender")
			}
			fields = resp.UpdateResendSender.ResendSenderFields
		} else {
			resp, e := gen.CreateResendSender(cmd.Context(), client, org, from, *key, en)
			if e != nil {
				return senderWriteError(e, key)
			}
			if resp == nil || resp.CreateResendSender == nil {
				return exitcode.Newf(exitcode.Error, "server returned no sender")
			}
			fields = resp.CreateResendSender.ResendSenderFields
		}
		d := senderDTO(fields)
		// Defend against an erroneous server reflecting the credential in metadata.
		if key != nil {
			redact := func(s string) string { return strings.ReplaceAll(s, *key, "[REDACTED]") }
			d.ID = redact(d.ID)
			d.OrganizationID = redact(d.OrganizationID)
			d.From = redact(d.From)
			d.CreatedAt = redact(d.CreatedAt)
			d.UpdatedAt = redact(d.UpdatedAt)
			if d.CreatedBy != nil {
				s := redact(*d.CreatedBy)
				d.CreatedBy = &s
			}
			if d.UpdatedBy != nil {
				s := redact(*d.UpdatedBy)
				d.UpdatedBy = &s
			}
		}
		return emitSender(f, d)
	}}
	if !update {
		cmd.Flags().StringVar(&org, "org", "", "organization ref: ID, slug or URN (required)")
		_ = cmd.MarkFlagRequired("org")
	}
	fromUsage, enabledUsage, enabledDefault := "the address mail is sent from, such as 'Robot <robot@example.com>' (required)", "--enabled=false creates the sender switched off", true
	if update {
		fromUsage, enabledUsage, enabledDefault = "the sender's new address; leave it out to keep the current one", "turn the sender on; --enabled=false turns it off; leave it out to keep it as it is", false
	}
	cmd.Flags().StringVar(&from, "from", "", fromUsage)
	if !update {
		_ = cmd.MarkFlagRequired("from")
	}
	cmd.Flags().StringVar(&keyFile, "api-key-file", "", "read write-only key from file or - (stdin); never an inline key")
	cmd.Flags().BoolVar(&enabled, "enabled", enabledDefault, enabledUsage)
	return cmd
}

func newCmdSenderGet(f *cmdutil.Factory) *cobra.Command {
	return &cobra.Command{Use: "get <sender-id>", Short: "Show a sanitized sender; missing or inaccessible exits 4", Long: `Read one sender by its sender ID (from list or create). The server checks
organization access; missing and inaccessible senders both exit 4. Text shows
ID, organization, sender identity, enabled, hasApiKey and domain verified;
--json adds creation and update metadata. No API key is returned.

Domain verified is Resend's answer for the domain of the sender address, read
live on each get, so it is current as of that read: yes, no (Resend reports it
not verified, or has no record of the domain), or unknown (the lookup could not
be made, for example it failed or timed out); unknown says nothing about the
domain. In --json it is domainVerified: true, false or null. It only reports:
get sends no mail and changes nothing at Resend. To verify the domain, do it
in Resend.

Needs server ` + resendDomainVerifiedFloor + ` or later: the query asks for domain verified, which
an older server rejects as an unknown field, so get fails there.`, Example: "  hadron org resend-sender get <sender-id> --json", Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
		client, err := f.GraphQLClient()
		if err != nil {
			return err
		}
		resp, err := gen.GetResendSender(cmd.Context(), client, args[0])
		if err != nil {
			return api.MapErrorKeepingCode(err)
		}
		if resp == nil || resp.ResendSender == nil {
			return exitcode.Newf(exitcode.NotFound, "Resend sender not found or inaccessible")
		}
		return emitSenderGet(f, resendSenderGetDTO{senderDTO(resp.ResendSender.ResendSenderFields), resp.ResendSender.DomainVerified})
	}}
}

func newCmdSenderList(f *cmdutil.Factory) *cobra.Command {
	var orgID string
	var limit, offset int
	cmd := &cobra.Command{Use: "list", Aliases: []string{"ls"}, Short: "List sanitized senders in accessible organizations", Long: `List senders across the caller's accessible organizations. --org-id filters
by a raw organization ID (not a slug or URN). By default page to exhaustion;
--limit or --offset selects one explicit page. --json is an array (empty: []).
API keys are never returned; hasApiKey indicates whether one is stored.`, Example: "  hadron org resend-sender list --json\n  hadron org resend-sender list --org-id <org-id> --limit 20", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, args []string) error {
		if limit < 0 || offset < 0 {
			return exitcode.Newf(exitcode.Usage, "limit and offset must be non-negative")
		}
		orgID = strings.TrimSpace(orgID)
		var org *string
		if cmd.Flags().Changed("org-id") {
			if strings.TrimSpace(orgID) == "" {
				return exitcode.Newf(exitcode.Usage, "--org-id must not be empty")
			}
			org = &orgID
		}
		client, err := f.GraphQLClient()
		if err != nil {
			return err
		}
		fetch := func(lim, off *int) ([]*gen.ListResendSendersResendSendersResendSendersPageItemsResendSender, int, error) {
			resp, e := gen.ListResendSenders(cmd.Context(), client, org, lim, off)
			if e != nil {
				return nil, 0, api.MapErrorKeepingCode(e)
			}
			if resp == nil || resp.ResendSenders == nil {
				return nil, 0, exitcode.Newf(exitcode.Error, "server returned no sender page")
			}
			return resp.ResendSenders.Items, resp.ResendSenders.Total, nil
		}
		var items []*gen.ListResendSendersResendSendersResendSendersPageItemsResendSender
		if cmd.Flags().Changed("limit") || cmd.Flags().Changed("offset") {
			var lim, off *int
			if cmd.Flags().Changed("limit") {
				lim = &limit
			}
			if cmd.Flags().Changed("offset") {
				off = &offset
			}
			items, _, err = fetch(lim, off)
		} else {
			items, err = api.CollectAll(func(lim, off int) ([]*gen.ListResendSendersResendSendersResendSendersPageItemsResendSender, int, error) {
				return fetch(&lim, &off)
			})
		}
		if err != nil {
			return err
		}
		dtos := []resendSenderDTO{}
		for _, s := range items {
			if s == nil {
				return exitcode.Newf(exitcode.Error, "server returned a null sender")
			}
			dtos = append(dtos, senderDTO(s.ResendSenderFields))
		}
		return output.Write(f.IOStreams, f.JSON, dtos, func(w io.Writer) error {
			t := output.NewTable(w, "ID", "ORG", "FROM", "ENABLED", "HAS KEY")
			for _, d := range dtos {
				t.Row(output.SafeCell(d.ID), output.SafeCell(d.OrganizationID), output.SafeCell(d.From), fmt.Sprint(d.Enabled), fmt.Sprint(d.HasAPIKey))
			}
			return t.Flush()
		})
	}}
	cmd.Flags().StringVar(&orgID, "org-id", "", "filter by raw organization ID")
	cmd.Flags().IntVar(&limit, "limit", 0, "single-page size (default: page to exhaustion)")
	cmd.Flags().IntVar(&offset, "offset", 0, "single-page offset")
	return cmd
}

func newCmdSenderRm(f *cmdutil.Factory) *cobra.Command {
	var yes bool
	cmd := &cobra.Command{Use: "rm <sender-id>", Aliases: []string{"delete"}, Short: "Delete a sender and its stored API key (requires confirmation)", Long: `Permanently delete the sender registry entry and its stored API key.
Use a sender ID from list or create. The server requires org ADMIN/OWNER.
This removes the configuration, not any previously delivered mail.

Prompts on a terminal; pass --yes to confirm non-interactively. Text prints
the deleted ID; --json returns {id,deleted}. A missing/inaccessible sender
exits 4; a false server result is a failure, never a success.`, Example: "  hadron org resend-sender rm <sender-id> --yes", Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
		if strings.TrimSpace(args[0]) == "" {
			return exitcode.Newf(exitcode.Usage, "sender ID must not be empty")
		}
		if err := cmdutil.ConfirmDeletion(f.IOStreams, yes, "Resend sender "+args[0]); err != nil {
			return err
		}
		client, err := f.GraphQLClient()
		if err != nil {
			return err
		}
		resp, err := gen.DeleteResendSender(cmd.Context(), client, args[0])
		if err != nil {
			return api.MapErrorKeepingCode(err)
		}
		if resp == nil || !resp.DeleteResendSender {
			return exitcode.Newf(exitcode.Error, "sender was not deleted")
		}
		dto := struct {
			ID      string `json:"id"`
			Deleted bool   `json:"deleted"`
		}{args[0], true}
		return output.Write(f.IOStreams, f.JSON, dto, func(w io.Writer) error {
			_, e := fmt.Fprintf(w, "deleted sender %q\n", dto.ID)
			return e
		})
	}}
	cmd.Flags().BoolVar(&yes, "yes", false, "confirm deletion without prompting")
	return cmd
}
