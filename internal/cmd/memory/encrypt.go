package memory

import (
	"fmt"
	"io"
	"os"
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
	ID          string `json:"id"`
	URN         string `json:"urn"`
	Name        string `json:"name"`
	IsEncrypted bool   `json:"isEncrypted"`
}

func newCmdEncrypt(f *cmdutil.Factory) *cobra.Command {
	var dataKey, passphraseFile string
	var yes bool
	cmd := &cobra.Command{
		Use:   "encrypt <memoryRef>",
		Short: "Encrypt a private memory with a key you keep (cannot be undone)",
		Long: `Encrypt a private memory with a key you keep, so that its content is stored
as ciphertext. Only a memory of class private can be encrypted, and only its
owner may do it.

This is ONE-WAY: there is no decrypt command, and the server never stores the
key, so keep it safe. If you lose it, the content is lost.

Give exactly one of:
  --data-key       a key: 32 random bytes written in base64 (44 characters,
                   ending in =). Make one with openssl rand -base64 32, save it
                   somewhere safe, and pass it on stdin with --data-key - so it
                   never lands in your shell history.
  --passphrase-file  a file holding a passphrase ("-" reads stdin). The server
                   derives the key from it and stores only the salt, never the
                   key. A trailing newline is dropped; the passphrase is never
                   printed.

What changes afterwards:
  - The hadron CLI cannot read the content any more: node get prints the
    stored ciphertext (enc:v1:...), and search does not find words in it.
  - Approvals of its nodes end (node approve, node verify).
  - memory clone refuses it, and memory validate skips its stale-abstract
    check.

It is refused while the memory holds a decision log, or while its vector
index is on. It asks "Encrypt memory <memoryRef>? This rewrites ALL node
content as ciphertext and cannot be undone from the CLI" on a terminal; --yes
skips the question and is required when input is not a terminal (otherwise
exit 2).`,
		Example: `  openssl rand -base64 32 > diary.key   # keep this file safe
  hadron memory encrypt hrn:mem:jane:diary --data-key - --yes < diary.key
  hadron memory encrypt hrn:mem:jane:diary --passphrase-file ~/.diary-pass --yes`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			// Exactly one secret; gated on Changed so --passphrase-file= is an
			// empty value refused, not an absent flag (cli#1340).
			usePassphrase := cmd.Flags().Changed("passphrase-file")
			if usePassphrase && cmd.Flags().Changed("data-key") {
				return exitcode.Newf(exitcode.Usage, "--data-key and --passphrase-file are mutually exclusive: give one. Nothing was sent")
			}
			if usePassphrase && strings.TrimSpace(passphraseFile) == "" {
				return exitcode.Newf(exitcode.Usage, "--passphrase-file is empty: name a file, or - for stdin. Nothing was sent")
			}
			if !usePassphrase && dataKey == "" {
				return exitcode.Newf(exitcode.Usage, "a data key is required, or a passphrase — pass --data-key - or --passphrase-file <file|->")
			}
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
			var keyArg, passArg *string
			if usePassphrase {
				pass, err := readPassphrase(passphraseFile, f.IOStreams.In)
				if err != nil {
					return err
				}
				passArg = &pass
			} else {
				key, err := readDataKey(dataKey, f.IOStreams.In)
				if err != nil {
					return err
				}
				keyArg = &key
			}
			resp, err := gen.EncryptMemory(cmd.Context(), client, memID, keyArg, passArg)
			if err != nil {
				return api.MapError(err)
			}
			if resp.EncryptMemory == nil {
				return exitcode.Newf(exitcode.Error, "server returned no memory")
			}
			m := resp.EncryptMemory
			dto := encryptDTO{ID: m.Id, URN: m.Urn, Name: m.Name, IsEncrypted: m.IsEncrypted}
			return output.Write(f.IOStreams, f.JSON, dto, func(w io.Writer) error {
				_, err := fmt.Fprintf(w, "✓ encrypted %s (%s)\n", dto.URN, dto.Name)
				return err
			})
		},
	}
	cmd.Flags().StringVar(&dataKey, "data-key", "", `the key: base64 of 32 random bytes ("-" reads it from stdin, recommended)`)
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
		data, err = os.ReadFile(path)
	}
	if err != nil {
		return "", exitcode.Newf(exitcode.Usage, "reading --passphrase-file: %v", err)
	}
	v := strings.TrimRight(string(data), "\r\n")
	if v == "" {
		return "", exitcode.Newf(exitcode.Usage, "the passphrase is empty. Nothing was sent")
	}
	return v, nil
}
