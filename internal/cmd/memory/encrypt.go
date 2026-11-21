package memory

import (
	"fmt"
	"io"
	"strings"

	"github.com/spf13/cobra"

	"github.com/hadron-memory/hadron-cli/internal/api"
	"github.com/hadron-memory/hadron-cli/internal/api/gen"
	"github.com/hadron-memory/hadron-cli/internal/cmdutil"
	"github.com/hadron-memory/hadron-cli/internal/exitcode"
	"github.com/hadron-memory/hadron-cli/internal/output"
)

// encryptDTO is the stable --json shape for a memory-encryption result.
type encryptDTO struct {
	ID              string `json:"id"`
	URN             string `json:"urn"`
	Name            string `json:"name"`
	IsEncrypted     bool   `json:"isEncrypted"`
	DetailsWithheld bool   `json:"detailsWithheld,omitempty"`
}

func newCmdEncrypt(f *cmdutil.Factory) *cobra.Command {
	var dataKey, dataKeyFile, passphraseFile string
	var yes bool
	cmd := &cobra.Command{
		Use:   "encrypt <memoryRef>",
		Short: "Encrypt a private memory with a key you keep (cannot be undone)",
		Long: `Encrypt a private memory with a key you keep, so that its content is stored
as ciphertext. Only a memory of class private can be encrypted, and only its
strict owner may start it. An owning App key may complete encryption of an
already-encrypted private memory, using its existing data key only.

This is ONE-WAY: there is no decrypt command, and the server never stores the
key, so keep it safe. If you lose it, the content is lost.

Give exactly one of:
  --data-key-file  a file holding a key ("-" reads stdin): 32 random bytes
                   written in base64 (44 characters, ending in =). Make one
                   with openssl rand -base64 32 > key, chmod 600 key, and keep
                   it safe. A file that group or others can access is refused.
  --data-key       the same key on stdin (--data-key -). A literal key in the
                   argument is DEPRECATED: it shows in process listings and
                   shell history, so it warns now and a later release refuses it.
  --passphrase-file  a file holding a passphrase ("-" reads stdin). The server
                   derives the key from it and stores only the salt, never the
                   key. A trailing newline is dropped; the passphrase is never
                   printed. Files accessible by group or others are refused (chmod 600).
                   On Windows, use stdin ("-"): file permission checks are unsupported.

What changes afterwards:
  - Content is read back only with the passphrase: add --unlock <memoryRef>
    (a global flag) to node get or any other read, and you are asked for it
    with no echo. Without --unlock, node get prints the stored ciphertext
    (enc:v1:...). Scripts use --unlock-passphrase-file <file> (only you may
    read it: chmod 600) or --unlock-passphrase-stdin. The passphrase is never
    an argument or an environment variable, and it is not remembered: every
    invocation asks again. v1 unlocks only a passphrase-encrypted memory, not
    one encrypted with --data-key. Search does not find words inside it.
  - Approvals of its nodes end (node approve, node verify).
  - memory clone refuses it, and memory validate skips its stale-abstract
    check.

It is refused while the memory holds a decision log, or while its vector
index is on. It asks "Encrypt memory <memoryRef>? This rewrites ALL node
content as ciphertext and cannot be undone from the CLI" on a terminal; --yes
skips the question and is required when input is not a terminal (otherwise
exit 2).`,
		Example: `  (umask 077; openssl rand -base64 32 > diary.key)   # keep this file safe, mode 600
  hadron memory encrypt hrn:mem:jane:diary --data-key-file diary.key --yes
  hadron memory encrypt hrn:mem:jane:diary --data-key-file - --yes < diary.key
  hadron memory encrypt hrn:mem:jane:diary --passphrase-file ~/.diary-pass --yes
  hadron node get entries:today -m hrn:mem:jane:diary --unlock hrn:mem:jane:diary
  hadron node get entries:today -m hrn:mem:jane:diary --unlock hrn:mem:jane:diary --unlock-passphrase-file ~/.diary-pass`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cmd.SetContext(api.WithoutKeyMaterial(cmd.Context()))
			// Exactly one secret; gated on Changed so --passphrase-file= is an
			// empty value refused, not an absent flag (cli#1340).
			usePassphrase := cmd.Flags().Changed("passphrase-file")
			if usePassphrase && cmd.Flags().Changed("data-key") {
				return exitcode.Newf(exitcode.Usage, "--data-key and --passphrase-file are mutually exclusive: give one. Nothing was sent")
			}
			if usePassphrase && cmd.Flags().Changed("data-key-file") {
				return exitcode.Newf(exitcode.Usage, "--data-key-file and --passphrase-file are mutually exclusive: give one. Nothing was sent")
			}
			// The key file is read, and its mode checked, before any request.
			key, err := newDataKeyInput(cmd, dataKey, dataKeyFile)
			if err != nil {
				return err
			}
			if usePassphrase && strings.TrimSpace(passphraseFile) == "" {
				return exitcode.Newf(exitcode.Usage, "--passphrase-file is empty: name a file, or - for stdin. Nothing was sent")
			}
			if !usePassphrase && !key.fileSet && dataKey == "" {
				return exitcode.Newf(exitcode.Usage, "a data key is required, or a passphrase — pass --data-key-file <file|-> or --passphrase-file <file|->")
			}
			// A file is checked before any lookup. Stdin stays after confirmation.
			var pass string
			if usePassphrase && passphraseFile != "-" {
				pass, err = readPassphrase(passphraseFile, f.IOStreams.In)
				if err != nil {
					return err
				}
			}
			key.WarnLiteral(f.IOStreams.ErrOut)
			client, err := f.GraphQLClient()
			if err != nil {
				return err
			}
			memID, err := resolveMemoryID(cmd, client, args[0])
			if err != nil {
				return err
			}
			// Confirm BEFORE reading the key from stdin: `--data-key -` consumes
			// stdin, so reading it first would starve the interactive prompt (EOF →
			// cancel) and would also swallow a piped key on a run that then refuses
			// for lack of --yes. Confirm first, then read the key.
			if err := cmdutil.Confirm(f.IOStreams, yes, fmt.Sprintf(
				"Encrypt memory %s? This rewrites ALL node content as ciphertext and cannot be undone from the CLI — keep your data key safe.",
				args[0])); err != nil {
				return err
			}
			material := api.MemoryKey{Memory: memID}
			if usePassphrase {
				if passphraseFile == "-" {
					pass, err = readPassphrase(passphraseFile, f.IOStreams.In)
					if err != nil {
						return err
					}
				}
				material.Passphrase = pass
			} else {
				k, err := key.Read(f.IOStreams.In)
				if err != nil {
					return err
				}
				material.DataKey = k
			}
			// Server-resolved principal, never token prefix or --app context:
			// a user with an App context is still an owner using memory-keys.
			principal, err := gen.MemoryEncryptionPrincipal(cmd.Context(), client)
			if err != nil {
				return api.MapError(err)
			}
			if principal == nil || principal.AuthContext == nil {
				return exitcode.Newf(exitcode.AuthRequired, "encryption principal could not be authenticated; nothing was encrypted")
			}
			app := principal.AuthContext.PrincipalType == gen.PrincipalTypeApp
			if !app && principal.AuthContext.PrincipalType != gen.PrincipalTypeUser {
				return exitcode.Newf(exitcode.Usage, "memory encryption requires the owner or owning App's completion key; nothing was encrypted")
			}
			encryptCtx, err := api.WithEncryptionKey(cmd.Context(), material, app)
			if err != nil {
				return err
			}
			resp, err := gen.EncryptMemory(encryptCtx, client, memID)
			if err != nil {
				mapped := api.MapError(err)
				if api.ServerCodeOf(err) == "BAD_USER_INPUT" && strings.Contains(err.Error(), "Provide exactly one of dataKey or passphrase") {
					mapped = fmt.Errorf("the encryption secret did not reach the server (the key header may have been stripped); nothing was encrypted. Check the proxy/server header support before retrying: %w", mapped)
				}
				return api.Detached(mapped, func(text string) string { return api.RedactEncryptionText(encryptCtx, text) })
			}
			if resp == nil || resp.EncryptMemory == nil || !resp.EncryptMemory.IsEncrypted {
				return exitcode.Newf(exitcode.Unavailable, "memory encryption was not confirmed; inspect the memory before retrying. Nothing was retried")
			}
			m := resp.EncryptMemory
			dto := encryptDTO{ID: m.Id, URN: m.Urn, Name: m.Name, IsEncrypted: m.IsEncrypted}
			// The write is confirmed. Withhold only sensitive metadata without
			// turning a committed irreversible operation into an apparent failure.
			for _, field := range []*string{&dto.ID, &dto.URN, &dto.Name} {
				if api.RedactEncryptionText(encryptCtx, *field) != *field {
					*field = ""
					dto.DetailsWithheld = true
				}
			}
			return output.Write(f.IOStreams, f.JSON, dto, func(w io.Writer) error {
				if dto.DetailsWithheld {
					_, err := fmt.Fprintln(w, "✓ encrypted memory (secret-bearing details withheld)")
					return err
				}
				_, err := fmt.Fprintf(w, "✓ encrypted %s (%s)\n", dto.URN, dto.Name)
				return err
			})
		},
	}
	cmd.Flags().StringVar(&dataKeyFile, "data-key-file", "", `file holding the key: base64 of 32 random bytes (mode 600; "-" reads stdin)`)
	cmd.Flags().StringVar(&dataKey, "data-key", "", `the key from stdin ("-"); a literal value is deprecated (visible in ps and shell history): use --data-key-file`)
	cmd.Flags().StringVar(&passphraseFile, "passphrase-file", "", `file holding a passphrase to derive the key from ("-" reads stdin)`)
	cmd.Flags().BoolVar(&yes, "yes", false, "skip the confirmation prompt (required non-interactively)")
	return cmd
}

// readDataKey reads the encryption key: "-" pulls it from stdin, so the key
// never appears in argv or shell history. The value is trimmed either way —
// keys are base64/hex, so surrounding whitespace (a stray newline from a pipe, a
// copy-paste space) is never meaningful and silently keeping it would cause
// hard-to-debug failures. An empty key is rejected.
func readDataKey(v string, stdin io.Reader) (string, error) {
	if v == "-" {
		data, err := io.ReadAll(stdin)
		if err != nil {
			return "", err
		}
		v = string(data)
	}
	v = strings.TrimSpace(v)
	if v == "" {
		return "", exitcode.Newf(exitcode.Usage, "empty data key — provide a non-empty key via --data-key - (stdin) or --data-key <value>")
	}
	return v, nil
}

// readPassphrase reads the passphrase from a file or stdin ("-"). Only the
// line ending is dropped: surrounding spaces may be part of a passphrase. An
// empty passphrase is refused. Never echoed.
func readPassphrase(path string, stdin io.Reader) (string, error) {
	var data []byte
	var err error
	if path == "-" {
		data, err = io.ReadAll(stdin)
	} else {
		data, err = cmdutil.ReadSecretFile(path, "--passphrase-file")
		if err != nil {
			return "", err
		}
	}
	if err != nil {
		return "", exitcode.Newf(exitcode.Usage, "reading --passphrase-file: input could not be read. Nothing was sent")
	}
	v := strings.TrimRight(string(data), "\r\n")
	if v == "" {
		return "", exitcode.Newf(exitcode.Usage, "the passphrase is empty. Nothing was sent")
	}
	return v, nil
}
