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

type appKeyCreatedDTO struct {
	ID         string  `json:"id"`
	KeyPreview string  `json:"keyPreview"`
	Label      *string `json:"label"`
	Out        string  `json:"out"`
}

const revokeRoute = "hadron app key revoke <key-id>"

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

--label must be non-empty when given: an empty or whitespace-only label (for
example, an unset shell variable) is refused with exit 2 before anything is
opened or minted. Omit it for the server's default label.

Pass an App ID, hrn:app:<root>:<slug>, or the <root>:<slug> short form.

--out is opened first, before anything is minted: a path that exists (a file or
a symlink) is refused with exit 2 and nothing is sent. --force writes a temp
file beside it and renames it over --out, so the key never sits in a file wider
than 0600 and a symlink is replaced, never followed. If the server refuses, the
file this command created is removed.

On Windows, mode 0600 only clears the read-only attribute. The key file is
private only because of the access list it inherits from its directory: private
under your user profile, readable by others in a shared directory. Keep key
files under your user profile.

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
			if cmd.Flags().Changed("label") && strings.TrimSpace(label) == "" {
				return exitcode.Newf(exitcode.Usage, "--label is empty: give the key a label, or omit --label for the server's default (nothing was minted)")
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
			// On windows a directory's Go mode reads 0777 whatever its ACL says, so the
			// check would always fire; the help states the windows boundary instead.
			if fi, err := os.Stat(filepath.Dir(abs)); err == nil && runtime.GOOS != "windows" && fi.Mode().Perm()&0o077 != 0 {
				fmt.Fprintf(f.IOStreams.ErrOut, "warning: %s is accessible to group or others (mode %o); the key file is 0600 but keep it in a private directory\n", filepath.Dir(abs), fi.Mode().Perm())
			}

			var labelArg *string
			if label != "" {
				labelArg = &label
			}
			resp, err := gen.CreateAppKey(cmd.Context(), client, ref, labelArg)
			if err != nil {
				cleanup := pending.Abort()
				return withCleanup(createFailure(err, ref, label, cleanup == nil), cleanup)
			}
			if resp == nil || resp.CreateAppKey == nil || resp.CreateAppKey.Key == nil || resp.CreateAppKey.RawKey == "" {
				return withCleanup(exitcode.Newf(exitcode.Error, "the server returned no key; run `hadron app key list %s` and revoke any unexpected key labelled %s", ref, labelOrDefault(label)), pending.Abort())
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
func createFailure(err error, ref, label string, fileRemoved bool) error {
	mapped := api.MapError(err)
	code := exitcode.FromError(mapped)
	if code == exitcode.Unavailable || code == exitcode.Error {
		kept := ""
		if fileRemoved {
			kept = " and no file was kept"
		}
		return exitcode.Newf(code, "createAppKey was sent and its answer was lost, so a key labelled %s may exist whose value nobody has. It was not retried%s. Run `hadron app key list %s` and revoke any unexpected key with that label (%s). Cause: %s",
			labelOrDefault(label), kept, ref, revokeRoute, failureClass(code))
	}
	return exitcode.Newf(code, "createAppKey refused: %s; no key was minted", failureClass(code))
}

// failureClass names a failure by its exit code. Server and transport message
// text is never echoed on this credential-minting path: the raw-key prefix is
// tunable (cor:dmo:050:05), so no pattern can recognise every key a message
// might quote.
func failureClass(code int) string {
	switch code {
	case exitcode.AuthRequired:
		return "not signed in or credentials rejected"
	case exitcode.Forbidden:
		return "not permitted to manage keys for this app"
	case exitcode.NotFound:
		return "app not found or not visible"
	case exitcode.Conflict:
		return "state conflict"
	case exitcode.Usage:
		return "invalid input"
	case exitcode.Unavailable:
		return "server or network unavailable"
	}
	return "server error"
}

// withCleanup keeps a failure's own message, exit code and recovery route and
// appends a cleanup failure (the reserved key file could not be removed), so
// neither hides the other. A nil cleanup returns the failure as it was.
func withCleanup(failure, cleanup error) error {
	if cleanup == nil {
		return failure
	}
	combined := exitcode.Newf(exitcode.FromError(failure), "%v. Also: %v", failure, cleanup)
	// Rebuilding the error would drop the server code the --json envelope reads.
	var coded interface{ ServerCode() string }
	if errors.As(failure, &coded) {
		return &codedError{error: combined, code: coded.ServerCode()}
	}
	return combined
}
