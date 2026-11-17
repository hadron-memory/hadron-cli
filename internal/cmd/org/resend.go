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
	cmd := &cobra.Command{Use: "resend-sender <command>", Short: "Manage an organization's Resend sender (write-only API key)", Long: `Create, inspect, update or delete a Resend sender. The server checks access:
org members can read sanitized sender details; org ADMIN/OWNER can write.
Only hasApiKey is returned, never the API key. Creating or updating the
registry does not send mail. Sender references are sender IDs.

Keys are read from --api-key-file (a path or - for stdin), never argv values.
For create, omit that flag to read piped stdin or use a masked terminal prompt.
For update, pass the flag explicitly to rotate the key.`, Example: "  hadron org resend-sender list\n  hadron org resend-sender create --org acme.com --from 'Robot <robot@example.com>' --api-key-file ./resend.key"}
	cmd.AddCommand(newCmdSenderWrite(f, false), newCmdSenderWrite(f, true), newCmdSenderGet(f), newCmdSenderList(f), newCmdSenderRm(f))
	return cmd
}

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
	cmd := &cobra.Command{Use: use, Short: short, Args: argsCheck, Long: `Write the sender registry; the server enforces org ADMIN/OWNER access.
--from is the fixed sender identity (for example Robot <robot@example.com>).
Resend must verify its domain. --enabled=false disables this sender.

Create defaults enabled to true on the server. Update changes only supplied
flags; omission preserves each field. --api-key-file reads a file (or - for
stdin), never an inline key. Create without it reads piped stdin or prompts
with input hidden. Update rotates the key only when --api-key-file is given.
The response shows hasApiKey and sender metadata, never key material.`, Example: "  hadron org resend-sender create --org acme.com --from 'Robot <robot@example.com>' --api-key-file ./resend.key\n  hadron org resend-sender update <sender-id> --enabled=false", RunE: func(cmd *cobra.Command, args []string) error {
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
	cmd.Flags().StringVar(&from, "from", "", "fixed sender identity; required on create")
	if !update {
		_ = cmd.MarkFlagRequired("from")
	}
	cmd.Flags().StringVar(&keyFile, "api-key-file", "", "read write-only key from file or - (stdin); never an inline key")
	cmd.Flags().BoolVar(&enabled, "enabled", true, "enable or disable the sender (update: only when passed)")
	return cmd
}

func newCmdSenderGet(f *cmdutil.Factory) *cobra.Command {
	return &cobra.Command{Use: "get <sender-id>", Short: "Show a sanitized sender; missing or inaccessible exits 4", Long: `Read one sender by its sender ID (from list or create). The server checks
organization access; missing and inaccessible senders both exit 4. Text shows
ID, organization, sender identity, enabled and hasApiKey; --json adds creation
and update metadata. No API key is returned.`, Example: "  hadron org resend-sender get <sender-id> --json", Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
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
		return emitSender(f, senderDTO(resp.ResendSender.ResendSenderFields))
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
