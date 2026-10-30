package app

// `app key revoke` and `app key rotate` — the end of an App key's life
// (cli#1333, cor:dmo:050:05). Both are irreversible and confirm first; neither
// can be retried safely after a lost answer, so neither is.

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/spf13/cobra"

	"github.com/hadron-memory/hadron-cli/internal/api"
	"github.com/hadron-memory/hadron-cli/internal/api/gen"
	"github.com/hadron-memory/hadron-cli/internal/cmdutil"
	"github.com/hadron-memory/hadron-cli/internal/exitcode"
	"github.com/hadron-memory/hadron-cli/internal/output"
	"github.com/hadron-memory/hadron-cli/internal/secretfile"
)

type appKeyRevokedDTO struct {
	ID      string `json:"id"`
	Revoked bool   `json:"revoked"`
}

type appKeyRotatedDTO struct {
	AppID               string `json:"appId"`
	AppURN              string `json:"appUrn"`
	Out                 string `json:"out"`
	AllOtherKeysRevoked bool   `json:"allOtherKeysRevoked"`
}

// codedError carries the server's code to the --json error envelope (the root
// handler reads it through ServerCode), so a script can tell FORBIDDEN from
// UNAUTHENTICATED behind the same message. The message is the mapped error's,
// untouched.
type codedError struct {
	error
	code string
}

func (c *codedError) Unwrap() error      { return c.error }
func (c *codedError) ServerCode() string { return c.code }

// withServerCode maps a raw failure and keeps the server's code on it.
func withServerCode(mapped, raw error) error {
	if mapped == nil {
		return nil
	}
	if code := api.ServerCodeOf(raw); code != "" {
		return &codedError{error: mapped, code: code}
	}
	return mapped
}

func newCmdKeyRevoke(f *cmdutil.Factory) *cobra.Command {
	var yes bool
	cmd := &cobra.Command{
		Use:   "revoke <key-id> [--yes]",
		Short: "Revoke an App key (irreversible)",
		Long: `Revoke one App key. A revoked key is refused from its next call and cannot be
restored: mint a new one with hadron app key create. Find the id with
hadron app key list <app-ref>.

It asks for confirmation on a terminal; scripts and agents pass --yes, and
without it a non-interactive run exits 2 with nothing sent.

Only a person who manages the App can revoke, as themselves: the server refuses
an App key, an impersonated session and a bound worker's session (exit 8).`,
		Example: `  hadron app key revoke key_123
  hadron app key revoke key_123 --yes --json`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			id := strings.TrimSpace(args[0])
			if id == "" {
				return exitcode.Newf(exitcode.Usage, "pass the key id (see `hadron app key list <app-ref>`)")
			}
			if err := cmdutil.Confirm(f.IOStreams, yes, fmt.Sprintf("Revoke App key %s? This cannot be undone.", id)); err != nil {
				return err
			}
			client, err := f.GraphQLClient()
			if err != nil {
				return err
			}
			resp, err := gen.RevokeAppKey(cmd.Context(), client, id)
			if err != nil {
				return withServerCode(api.MapError(err), err)
			}
			if resp == nil || !resp.RevokeAppKey {
				return exitcode.Newf(exitcode.Error, "the server did not confirm the revocation of key %s; run `hadron app key list <app-ref>` to see whether it is still active", id)
			}
			dto := appKeyRevokedDTO{ID: id, Revoked: true}
			return output.Write(f.IOStreams, f.JSON, dto, func(w io.Writer) error {
				_, err := fmt.Fprintf(w, "✓ revoked key %s\n", output.SafeCell(id))
				return err
			})
		},
	}
	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "skip the confirmation prompt (required when non-interactive)")
	return cmd
}

func newCmdKeyRotate(f *cmdutil.Factory) *cobra.Command {
	var out string
	var force, printKey, yes bool
	cmd := &cobra.Command{
		Use:   "rotate <app-ref> --out <file> [--yes] [--force] [--print-key]",
		Short: "Revoke EVERY active key of an App and mint one replacement",
		Long: `Rotate an App's keys: the server revokes EVERY active key of the App and mints
one replacement. The new key holds NO scopes, so every scope and host grant
(attention:deliver, host:operate) must be granted again:
  hadron app key scope grant <key-id> <scope> [--host-id <host>]
Anything still running on an old key stops working at its next call.

The new raw key is shown by the server once, so it goes into --out under the
same contract as app key create: a new file at mode 0600, opened before anything
is sent (an existing path is refused with exit 2 and nothing sent; --force
replaces it via a temp file), never printed unless --print-key (refused with
--json). If the server refuses, the file this command created is removed.

Pass an App ID, hrn:app:<root>:<slug>, or the <root>:<slug> short form.

It asks for confirmation on a terminal; scripts and agents pass --yes, and
without it a non-interactive run exits 2 with nothing sent.

regenerateAppKey is never retried. If its answer is lost (timeout, gateway
error), the old keys may already be revoked and a new key may exist whose value
nobody has: run hadron app key list <app-ref> to see what is active, and rotate
again if you need a key you hold.

On Windows, mode 0600 only clears the read-only attribute; keep key files under
your user profile (see app key create).

Only a person who manages the App can rotate, as themselves: the server refuses
an App key, an impersonated session and a bound worker's session (exit 8).`,
		Example: `  hadron app key rotate acme.com:ops --out ./ops.key
  hadron app key rotate acme.com:ops --out ./ops.key --yes --json`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ref, err := requiredReadAppRef(args[0])
			if err != nil {
				return err
			}
			if out == "" {
				return exitcode.Newf(exitcode.Usage, "--out is required: name the file the new key is written to")
			}
			if printKey && f.JSON {
				return exitcode.Newf(exitcode.Usage, "--print-key cannot be combined with --json")
			}
			abs, err := filepath.Abs(out)
			if err != nil {
				return exitcode.Newf(exitcode.Usage, "cannot resolve --out: %v", err)
			}
			// Confirmed before --out is touched, so a refusal leaves nothing behind.
			if err := cmdutil.Confirm(f.IOStreams, yes, fmt.Sprintf("Rotate the keys of %s? EVERY active key is revoked and the new key holds no scopes. This cannot be undone.", ref)); err != nil {
				return err
			}
			client, err := f.GraphQLClient()
			if err != nil {
				return err
			}
			pending, err := secretfile.Open(abs, force)
			if err != nil {
				switch {
				case errors.Is(err, secretfile.ErrExists):
					return exitcode.Newf(exitcode.Usage, "%s already exists; nothing was rotated (--force replaces it)", abs)
				case errors.Is(err, secretfile.ErrIsDir):
					return exitcode.Newf(exitcode.Usage, "%s is a directory; name a file", abs)
				case errors.Is(err, os.ErrNotExist):
					return exitcode.Newf(exitcode.Usage, "the directory of %s does not exist; nothing was rotated", abs)
				}
				return exitcode.Newf(exitcode.Error, "cannot create %s: %v; nothing was rotated", abs, err)
			}
			if fi, err := os.Stat(filepath.Dir(abs)); err == nil && runtime.GOOS != "windows" && fi.Mode().Perm()&0o077 != 0 {
				fmt.Fprintf(f.IOStreams.ErrOut, "warning: %s is accessible to group or others (mode %o); the key file is 0600 but keep it in a private directory\n", filepath.Dir(abs), fi.Mode().Perm())
			}

			resp, err := gen.RegenerateAppKey(cmd.Context(), client, ref)
			if err != nil {
				return withCleanup(rotateFailure(err, ref), pending.Abort())
			}
			if resp == nil || resp.RegenerateAppKey == nil || resp.RegenerateAppKey.RawKey == "" {
				return withCleanup(exitcode.Newf(exitcode.Error, "the server returned no key: every key of %s may already be revoked. Run `hadron app key list %s` to see what is active, and rotate again if you need a key you hold", ref, ref), pending.Abort())
			}
			rotated := resp.RegenerateAppKey
			if err := pending.Commit([]byte(rotated.RawKey + "\n")); err != nil {
				var lb *secretfile.LeftBehindError
				if errors.As(err, &lb) {
					return exitcode.Newf(exitcode.Error, "could not write the key file, and %v; every old key of %s is revoked and the new key's value may be partly on disk: rotate again for a key you hold", err, ref)
				}
				return exitcode.Newf(exitcode.Error, "%v after the keys were rotated: every old key of %s is revoked and the new key's value is lost; rotate again for a key you hold", err, ref)
			}

			// The SDL makes `app` non-null, but the key is already written and the
			// old keys already revoked, so a missing app must not turn a good
			// rotation into a crash: report what we were asked about instead.
			dto := appKeyRotatedDTO{AppURN: ref, Out: abs, AllOtherKeysRevoked: true}
			if rotated.App != nil {
				dto.AppID, dto.AppURN = rotated.App.Id, rotated.App.Urn
			}
			return output.Write(f.IOStreams, f.JSON, dto, func(w io.Writer) error {
				if _, err := fmt.Fprintf(w, "✓ rotated the keys of %s: EVERY other active key is revoked\n  the new key holds no scopes: grant them again (hadron app key scope grant <key-id> <scope>)\n  written to %s (mode 0600)\n",
					output.SafeCell(dto.AppURN), output.SafeCell(abs)); err != nil {
					return err
				}
				if printKey {
					_, err := fmt.Fprintln(w, rotated.RawKey)
					return err
				}
				return nil
			})
		},
	}
	cmd.Flags().StringVar(&out, "out", "", "file to write the new key to, a new file at mode 0600 (required)")
	cmd.Flags().BoolVar(&force, "force", false, "replace an existing --out (written via a temp file and renamed)")
	cmd.Flags().BoolVar(&printKey, "print-key", false, "also print the raw key to stdout (refused with --json)")
	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "skip the confirmation prompt (required when non-interactive)")
	return cmd
}

// rotateFailure classifies a failed regenerateAppKey the way createFailure does
// a mint: a lost answer means every key may be revoked and a new one may exist.
func rotateFailure(err error, ref string) error {
	mapped := api.MapError(err)
	code := exitcode.FromError(mapped)
	if code == exitcode.Unavailable || code == exitcode.Error {
		return exitcode.Newf(code, "regenerateAppKey was sent and its answer was lost, so EVERY key of %s may already be revoked and a new key may exist whose value nobody has. It was not retried and no file was kept. Run `hadron app key list %s` to see what is active; rotate again if you need a key you hold. Cause: %s",
			ref, ref, failureClass(code))
	}
	return withServerCode(exitcode.Newf(code, "regenerateAppKey refused: %s; no key was rotated", failureClass(code)), err)
}
