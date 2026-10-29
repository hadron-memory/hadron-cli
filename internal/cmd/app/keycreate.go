package app

// `app key create` — mint an App key straight into a 0600 file (cli#1223,
// cor:dmo:050:05). The raw key is shown once by the server and never recovered,
// so the order is the whole design: open --out FIRST (a refused path mints
// nothing), call createAppKey exactly once (never retried), then write; every
// path that ends without a written key removes the file it created.

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"

	"github.com/spf13/cobra"

	"github.com/hadron-memory/hadron-cli/internal/api"
	"github.com/hadron-memory/hadron-cli/internal/api/gen"
	"github.com/hadron-memory/hadron-cli/internal/cmdutil"
	"github.com/hadron-memory/hadron-cli/internal/exitcode"
	"github.com/hadron-memory/hadron-cli/internal/output"
	"github.com/hadron-memory/hadron-cli/internal/secretfile"
)

type appKeyCreatedDTO struct {
	ID         string  `json:"id"`
	KeyPreview string  `json:"keyPreview"`
	Label      *string `json:"label"`
	Out        string  `json:"out"`
}

// keyLikeToken matches a credential-shaped token (hdr_app_…, hdr_user_…) so a
// server or transport message that quotes one is never echoed. The prefix is a
// tunable (cor:dmo:050:05); this is a belt over the rule that no raw key is ever
// formatted into a message.
var keyLikeToken = regexp.MustCompile(`hdr_[A-Za-z0-9]+_[A-Za-z0-9_\-]{6,}`)

func redactKeys(s string) string { return keyLikeToken.ReplaceAllString(s, "[redacted]") }

const revokeRoute = "hadron api 'mutation($id: ID!) { revokeAppKey(id: $id) }' -F id=<key-id>"

func newCmdKeyCreate(f *cmdutil.Factory) *cobra.Command {
	var out, label string
	var force, printKey bool
	cmd := &cobra.Command{
		Use:   "create <app-ref> --out <file> [--label <text>] [--force] [--print-key]",
		Short: "Mint an App key into a 0600 file",
		Long: `Mint an App key and write it to --out, a new file at mode 0600. The raw key is
shown by the server once and cannot be recovered, so it goes into the file and
never to your terminal: the output is the key id, preview, label and the file's
absolute path. --print-key also prints the raw key to stdout (refused with
--json).

Pass an App ID, hrn:app:<root>:<slug>, or the <root>:<slug> short form.

--out is opened first, before anything is minted: a path that exists (a file or
a symlink) is refused with exit 2 and nothing is sent. --force writes a temp
file beside it and renames it over --out, so the key never sits in a file wider
than 0600 and a symlink is replaced, never followed. If the server refuses, the
file this command created is removed.

createAppKey is never retried. If its answer is lost (timeout, gateway error),
a key may exist whose value nobody has: the message names the label; run
hadron app key list <app-ref> and revoke any unexpected key with it.

A new key holds no scope. Grant what it may do next:
  hadron app key scope grant <key-id> <scope> [--host-id <host>]

Only a person who manages the App can mint, as themselves: the server refuses an
App key, an impersonated session and a bound worker's session (exit 8).`,
		Example: `  hadron app key create acme.com:ops --out /etc/hadron/host.key --label "orca1 host poller"
  hadron app key scope grant <key-id> host:operate --host-id orca1`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ref, err := requiredReadAppRef(args[0])
			if err != nil {
				return err
			}
			if out == "" {
				return exitcode.Newf(exitcode.Usage, "--out is required: name the file the key is written to")
			}
			if printKey && f.JSON {
				return exitcode.Newf(exitcode.Usage, "--print-key cannot be combined with --json")
			}
			abs, err := filepath.Abs(out)
			if err != nil {
				return exitcode.Newf(exitcode.Usage, "cannot resolve --out: %v", err)
			}
			client, err := f.GraphQLClient()
			if err != nil {
				return err
			}

			pending, err := secretfile.Open(abs, force)
			if err != nil {
				switch {
				case errors.Is(err, secretfile.ErrExists):
					return exitcode.Newf(exitcode.Usage, "%s already exists; nothing was minted (--force replaces it)", abs)
				case errors.Is(err, secretfile.ErrIsDir):
					return exitcode.Newf(exitcode.Usage, "%s is a directory; name a file", abs)
				case errors.Is(err, os.ErrNotExist):
					return exitcode.Newf(exitcode.Usage, "the directory of %s does not exist; nothing was minted", abs)
				}
				return exitcode.Newf(exitcode.Error, "cannot create %s: %v; nothing was minted", abs, err)
			}
			if fi, err := os.Stat(filepath.Dir(abs)); err == nil && fi.Mode().Perm()&0o077 != 0 {
				fmt.Fprintf(f.IOStreams.ErrOut, "warning: %s is accessible to group or others (mode %o); the key file is 0600 but keep it in a private directory\n", filepath.Dir(abs), fi.Mode().Perm())
			}

			var labelArg *string
			if label != "" {
				labelArg = &label
			}
			resp, err := gen.CreateAppKey(cmd.Context(), client, ref, labelArg)
			if err != nil {
				if aerr := pending.Abort(); aerr != nil {
					return leftBehind(aerr, "the key request failed")
				}
				return createFailure(err, ref, label)
			}
			if resp == nil || resp.CreateAppKey == nil || resp.CreateAppKey.Key == nil || resp.CreateAppKey.RawKey == "" {
				if aerr := pending.Abort(); aerr != nil {
					return leftBehind(aerr, "the server returned no key")
				}
				return exitcode.Newf(exitcode.Error, "the server returned no key; run `hadron app key list %s` and revoke any unexpected key labelled %s", ref, labelOrDefault(label))
			}
			created := resp.CreateAppKey
			if err := pending.Commit([]byte(created.RawKey + "\n")); err != nil {
				var lb *secretfile.LeftBehindError
				if errors.As(err, &lb) {
					return exitcode.Newf(exitcode.Error, "could not write the key file, and %v; key %s (%s) exists and its value may be partly on disk: revoke it (%s)",
						err, created.Key.Id, created.Key.KeyPreview, revokeRoute)
				}
				return exitcode.Newf(exitcode.Error, "%v after the key was minted: key %s (%s) exists and its value is lost; revoke it (%s)",
					err, created.Key.Id, created.Key.KeyPreview, revokeRoute)
			}

			dto := appKeyCreatedDTO{ID: created.Key.Id, KeyPreview: created.Key.KeyPreview, Label: created.Key.Label, Out: abs}
			return output.Write(f.IOStreams, f.JSON, dto, func(w io.Writer) error {
				if _, err := fmt.Fprintf(w, "✓ created key %s  %s  label %s\n  written to %s (mode 0600)\n",
					output.SafeCell(dto.ID), output.SafeCell(dto.KeyPreview), output.SafeCell(optionalReadText(dto.Label)), output.SafeCell(abs)); err != nil {
					return err
				}
				if printKey {
					_, err := fmt.Fprintln(w, created.RawKey)
					return err
				}
				return nil
			})
		},
	}
	cmd.Flags().StringVar(&out, "out", "", "file to write the key to, a new file at mode 0600 (required)")
	cmd.Flags().StringVar(&label, "label", "", "label for the key (default: the server's)")
	cmd.Flags().BoolVar(&force, "force", false, "replace an existing --out (written via a temp file and renamed)")
	cmd.Flags().BoolVar(&printKey, "print-key", false, "also print the raw key to stdout (refused with --json)")
	return cmd
}

func labelOrDefault(label string) string {
	if label == "" {
		return "(the default label)"
	}
	return fmt.Sprintf("%q", label)
}

// createFailure classifies a failed createAppKey. A transport-class failure or
// an internal fault means the request may have reached the server and minted a
// key nobody has; anything else is a refusal, and no key exists.
func createFailure(err error, ref, label string) error {
	mapped := api.MapError(err)
	code := exitcode.FromError(mapped)
	cause := redactKeys(mapped.Error())
	if code == exitcode.Unavailable || code == exitcode.Error {
		return exitcode.Newf(code, "createAppKey was sent and its answer was lost, so a key labelled %s may exist whose value nobody has. It was not retried and no file was kept. Run `hadron app key list %s` and revoke any unexpected key with that label (%s). Cause: %s",
			labelOrDefault(label), ref, revokeRoute, cause)
	}
	return exitcode.Newf(code, "%s", cause)
}

// leftBehind reports a cleanup that could not remove the reserved key file.
func leftBehind(err error, why string) error {
	return exitcode.Newf(exitcode.Error, "%s, and %v", why, err)
}
