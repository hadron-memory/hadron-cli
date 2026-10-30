package app

// `app key create` (cli#1223, split from #63) — mint an App key straight into
// a 0600 file. The raw key is shown by the server exactly once, so the order
// is the point: the destination is opened BEFORE the mutation (a refused path
// never mints an unrecoverable key), the key goes to the file and nowhere
// else unless --print-key, and no error path prints it. createAppKey is never
// retried: after a lost answer a key may exist whose raw value nobody has.

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"

	"github.com/spf13/cobra"

	"github.com/hadron-memory/hadron-cli/internal/api"
	"github.com/hadron-memory/hadron-cli/internal/api/gen"
	"github.com/hadron-memory/hadron-cli/internal/cmdutil"
	"github.com/hadron-memory/hadron-cli/internal/exitcode"
	"github.com/hadron-memory/hadron-cli/internal/output"
)

// RenameFile is os.Rename; tests replace it to fail after a successful mint.
var RenameFile = os.Rename

type appKeyCreatedDTO struct {
	ID         string  `json:"id"`
	KeyPreview string  `json:"keyPreview"`
	Label      *string `json:"label"`
	Out        string  `json:"out"`
}

// secretLike matches key-shaped tokens (a prefix_body or a long unbroken
// run) so a server message that happens to carry one is never repeated. The
// key's prefix is a server-side tunable (cor:dmo:050:05), hence the shape
// rather than "hdr_app_".
var secretLike = regexp.MustCompile(`[A-Za-z][A-Za-z0-9]*_[A-Za-z0-9_\-]{12,}|[A-Za-z0-9_\-+/=]{32,}`)

func redactSecrets(s string) string { return secretLike.ReplaceAllString(s, "[redacted]") }

func newCmdKeyCreate(f *cmdutil.Factory) *cobra.Command {
	var out, label string
	var force, printKey bool
	cmd := &cobra.Command{
		Use:   "create <app-ref> --out <file> [--label <text>] [--force] [--print-key]",
		Short: "Mint an App key into a 0600 file",
		Long: `Mint a new App key for <app-ref> and write it to --out, mode 0600. The server
shows the raw key once; it goes to the file and is never printed (text or
--json) unless --print-key asks for it on stdout.

<app-ref> is required (an App ID, hrn:app:<root>:<slug> or <root>:<slug>); the
active App and --app are not used. Only whoever manages the App may create a
key, as themselves: the server refuses an App key, an impersonation and a
worker's session.

--out is opened before the key is requested and must not exist (a symlink
counts): a refused path never mints a key nobody can read. --force replaces
it instead, through a 0600 temp file in the same directory renamed into place.

The new key holds no scope. Grant what it needs next:
  hadron app key scope grant <key-id> <scope> [--host-id <host>]

If the answer to the request is lost (timeout, gateway error) a key may exist
that you do not hold: the command never retries; run 'hadron app key list
<app-ref>' and revoke any unexpected key with that label.`,
		Example: `  hadron app key create acme.com:support --out /etc/hadron/host.key --label "orca1 host poller"
  hadron app key scope grant <key-id> host:operate --host-id orca1`,
		Args: func(_ *cobra.Command, args []string) error {
			if len(args) != 1 {
				return exitcode.Newf(exitcode.Usage, "pass exactly one <app-ref> (an App ID, hrn:app:<root>:<slug> or <root>:<slug>); got %d", len(args))
			}
			return nil
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			ref, err := requiredReadAppRef(args[0])
			if err != nil {
				return err
			}
			if strings.TrimSpace(out) == "" {
				return exitcode.Newf(exitcode.Usage, "--out <file> is required: the raw key is shown once and is written there")
			}
			if printKey && f.JSON {
				return exitcode.Newf(exitcode.Usage, "--print-key cannot be combined with --json: the raw key is never part of the JSON output")
			}
			var labelPtr *string
			// --label= (an unset variable) is passed-and-empty, not absent: refuse
			// it here, before the file is opened or anything is minted.
			if cmd.Flags().Changed("label") {
				l := strings.TrimSpace(label)
				if l == "" {
					return exitcode.Newf(exitcode.Usage, "--label was given empty (an unset variable?); name the key, or leave --label off")
				}
				labelPtr = &l
			}
			abs, err := filepath.Abs(out)
			if err != nil {
				return exitcode.Newf(exitcode.Usage, "--out %q: %v", out, err)
			}
			dir := filepath.Dir(abs)
			if info, err := os.Stat(dir); err == nil && info.Mode().Perm()&0o077 != 0 {
				fmt.Fprintf(f.IOStreams.ErrOut, "warning: %s is accessible to group or others (mode %o); the key file is 0600, but consider a private directory\n", dir, info.Mode().Perm())
			}
			file, cleanup, err := openKeyDestination(abs, force)
			if err != nil {
				return err
			}
			defer func() { _ = file.Close() }()
			client, err := f.GraphQLClient()
			if err != nil {
				cleanup()
				return err
			}
			resp, err := gen.CreateAppKey(cmd.Context(), client, ref, labelPtr)
			if err != nil {
				cleanup()
				return createAppKeyError(err, ref, labelPtr)
			}
			raw := resp.CreateAppKey.RawKey
			key := resp.CreateAppKey.Key
			if err := writeKeyFile(file, raw, abs, force); err != nil {
				cleanup()
				fmt.Fprintf(f.IOStreams.ErrOut, "the key was created but could not be written (%s). Revoke it: key id %s, preview %s\n  hadron api 'mutation($id: ID!) { revokeAppKey(id: $id) }' -F id=%s\n", redactSecrets(err.Error()), key.Id, key.KeyPreview, key.Id)
				return exitcode.Newf(exitcode.Error, "App key %s was created but not saved; it must be revoked", key.Id)
			}
			dto := appKeyCreatedDTO{ID: key.Id, KeyPreview: key.KeyPreview, Label: key.Label, Out: abs}
			return output.Write(f.IOStreams, f.JSON, dto, func(w io.Writer) error {
				if _, err := fmt.Fprintf(w, "✓ created App key %s (%s)\n  label: %s\n  file:  %s\n  next:  hadron app key scope grant %s <scope> [--host-id <host>]\n", dto.ID, dto.KeyPreview, labelText(dto.Label), dto.Out, dto.ID); err != nil {
					return err
				}
				if printKey {
					_, err := fmt.Fprintln(w, raw)
					return err
				}
				return nil
			})
		},
	}
	cmd.Flags().StringVar(&out, "out", "", "file to write the key to (mode 0600; must not exist unless --force)")
	cmd.Flags().StringVar(&label, "label", "", "label for the key, shown in the App key list")
	cmd.Flags().BoolVar(&force, "force", false, "replace an existing --out (via a temp file and rename)")
	cmd.Flags().BoolVar(&printKey, "print-key", false, "also print the raw key to stdout (not with --json)")
	return cmd
}

func labelText(l *string) string {
	if l == nil || *l == "" {
		return "-"
	}
	return *l
}

// openKeyDestination opens the file the key will be written to, before any
// request. Without force that is --out itself, created exclusively and never
// through a symlink; with force a temp file beside it. cleanup removes the
// file this call created.
func openKeyDestination(abs string, force bool) (*os.File, func(), error) {
	if !force {
		file, err := os.OpenFile(abs, syscall.O_CREAT|syscall.O_EXCL|syscall.O_WRONLY|syscall.O_NOFOLLOW, 0o600)
		if err != nil {
			if errors.Is(err, os.ErrExist) {
				return nil, nil, exitcode.Newf(exitcode.Usage, "%s already exists (or is a symlink); nothing was requested. Choose another --out or pass --force to replace it", abs)
			}
			return nil, nil, exitcode.Newf(exitcode.Usage, "cannot create %s: %v; nothing was requested", abs, err)
		}
		return file, func() { _ = file.Close(); _ = os.Remove(abs) }, nil
	}
	if info, err := os.Lstat(abs); err == nil && info.IsDir() {
		return nil, nil, exitcode.Newf(exitcode.Usage, "%s is a directory; nothing was requested", abs)
	}
	file, err := os.CreateTemp(filepath.Dir(abs), ".hadron-key-*")
	if err != nil {
		return nil, nil, exitcode.Newf(exitcode.Usage, "cannot create a temp file beside %s: %v; nothing was requested", abs, err)
	}
	name := file.Name()
	return file, func() { _ = file.Close(); _ = os.Remove(name) }, nil
}

func writeKeyFile(file *os.File, raw, abs string, force bool) error {
	if _, err := file.WriteString(raw + "\n"); err != nil {
		return err
	}
	if err := file.Sync(); err != nil {
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	if force {
		return RenameFile(file.Name(), abs)
	}
	return nil
}

// createAppKeyError turns a failed createAppKey into the CLI's error. Nothing
// from the response body is trusted to be free of key material, so the text is
// redacted. A transport failure may have been applied: say how to find and
// revoke the key, and never retry.
func createAppKeyError(err error, ref string, label *string) error {
	mapped := api.MapError(err)
	code := exitcode.FromError(mapped)
	if code == exitcode.Unavailable {
		which := "with no label (any key created just now)"
		if label != nil && *label != "" {
			which = fmt.Sprintf("labelled %q", *label)
		}
		return exitcode.Newf(code, "the answer to createAppKey was lost; it was NOT retried. A key may exist that you do not hold: run `hadron app key list %s` and revoke any unexpected key %s (`hadron api 'mutation($id: ID!) { revokeAppKey(id: $id) }' -F id=<key-id>`)", ref, which)
	}
	return exitcode.New(code, errors.New(redactSecrets(mapped.Error())))
}
