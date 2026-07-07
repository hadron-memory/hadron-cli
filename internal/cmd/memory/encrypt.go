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
		Use:   "encrypt <memory>",
		Short: "Convert a plaintext memory to encrypted-at-rest",
		Long: `Convert a plaintext memory to encrypted at rest.

You provide the data key; the server rewrites every node's content and data as
ciphertext in a single transaction. This is ONE-WAY — there is no decrypt
command, so keep the data key safe. Pass the key on stdin with ` + "`--data-key -`" + `
so it never lands in your shell history.

Because it rewrites all content irreversibly, this prompts on a terminal and
requires --yes when run non-interactively.`,
		Example: `  printf '%s' "$DATA_KEY" | hadron memory encrypt acme.com::kb --data-key - --yes`,
		Args:    cobra.ExactArgs(1),
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
	cmd.Flags().StringVar(&dataKey, "data-key", "", `the encryption data key ("-" reads stdin, recommended)`)
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
