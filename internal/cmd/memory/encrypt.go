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
	ID          string `json:"id"`
	URN         string `json:"urn"`
	Name        string `json:"name"`
	IsEncrypted bool   `json:"isEncrypted"`
}

func newCmdEncrypt(f *cmdutil.Factory) *cobra.Command {
	var dataKey string
	var yes bool
	cmd := &cobra.Command{
		Use:   "encrypt <memoryRef>",
		Short: "Encrypt a private memory with a key you keep (cannot be undone)",
		Long: `Encrypt a private memory with a key you keep, so that its content is stored
as ciphertext. Only a memory of class private can be encrypted, and only its
owner may do it.

This is ONE-WAY: there is no decrypt command, and the server never stores the
key, so keep it safe. If you lose it, the content is lost.

The key is 32 random bytes written in base64 (44 characters, ending in =).
Make one with openssl rand -base64 32, save it somewhere safe, and pass it on
stdin with --data-key - so it never lands in your shell history.

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
  hadron memory encrypt hrn:mem:jane:diary --data-key - --yes < diary.key`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if dataKey == "" {
				return exitcode.Newf(exitcode.Usage, "a data key is required — pass --data-key - to read it from stdin")
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
			key, err := readDataKey(dataKey, f.IOStreams.In)
			if err != nil {
				return err
			}
			resp, err := gen.EncryptMemory(cmd.Context(), client, memID, key)
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
