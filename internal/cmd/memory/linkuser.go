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

// linkUserDTO is the stable --json shape for `memory link-user`. It carries
// the promoted memory plus previousUrn, because the promotion RE-MINTS the
// URN: a caller holding the anonymous URN needs to learn the new one, and
// needs to know the old one stopped resolving.
type linkUserDTO struct {
	Memory      memoryDTO `json:"memory"`
	PreviousURN string    `json:"previousUrn"`
	Encrypted   bool      `json:"encrypted"`
}

func newCmdLinkUser(f *cmdutil.Factory) *cobra.Command {
	var externalUser, dataKey string
	var yes bool
	cmd := &cobra.Command{
		Use:   "link-user <memoryRef> --external-user <id> [--data-key -] [--yes]",
		Short: "Promote an anonymous memory to a registered user (App key only)",
		Long: `Link an anonymous/session memory to a real user — the anonymous →
registered promotion an App runs when an end user signs up.

Requires an APP KEY. The server gates this on the calling App, so a personal
user token is refused; authenticate with the App's key (HADRON_TOKEN, or
` + "`hadron auth login --with-token`" + `). The memory must be a user/anonymous memory
belonging to that same App's organization.

--external-user is the App's OWN identifier for the end user, not a Hadron user
id. The server resolves it to the app-scoped user that already carries it, or
provisions one — unless the App sets createUserPermission=DENY, which makes an
unknown id an error instead.

Two effects are worth planning for:

  * The memory's URN is RE-MINTED. The promotion mints a fresh URN and the
    anonymous one stops resolving, so stored references must be updated —
    the new and previous URNs are both reported.
  * The memory stops expiring. Its anonymous TTL is cleared, so it is no
    longer garbage-collected.

Passing --data-key additionally encrypts the memory at rest in the same
transaction: every node's content, abstract, and data are rewritten as
ciphertext and the memory lands private and owned by that user. Like
` + "`hadron memory encrypt`" + `, this is ONE-WAY — there is no decrypt command — so
keep the key. Pass it on stdin with ` + "`--data-key -`" + ` to keep it out of shell
history.

Because the promotion is irreversible, this prompts on a terminal and requires
--yes when run non-interactively.`,
		Example: `  hadron memory link-user hrn:mem:acme.com:anon-7f3 --external-user auth0|abc123 --yes
  printf '%s' "$DATA_KEY" | hadron memory link-user hrn:mem:acme.com:anon-7f3 \
    --external-user auth0|abc123 --data-key - --yes --json`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			externalUser = strings.TrimSpace(externalUser)
			if externalUser == "" {
				return exitcode.Newf(exitcode.Usage,
					"--external-user is required — the App's own identifier for the end user")
			}
			// linkMemoryToUser resolves an id OR a URN server-side, so hand it
			// the canonical ref rather than pre-resolving through memory(ref:):
			// that lookup is a second authorization surface an App key need not
			// satisfy, and failing it here would block a promotion the mutation
			// itself would have allowed.
			memRef := cmdutil.CanonicalMemoryRef(args[0])
			if memRef == "" {
				return exitcode.Newf(exitcode.Usage, "memory reference must not be empty")
			}

			// Presence, not emptiness: `--data-key "$KEY"` with an unset KEY
			// must NOT quietly skip encryption. Testing the value would make
			// that a silent no-op that leaves content plaintext even though
			// the caller asked for encryption; Changed() routes the empty
			// value into readDataKey, which rejects it (review on #301).
			wantsEncryption := cmd.Flags().Changed("data-key")

			what := fmt.Sprintf("memory %s to external user %q", args[0], externalUser)
			prompt := "Promote " + what +
				"? The memory's URN is re-minted (the anonymous one stops resolving) and its expiry is cleared."
			if wantsEncryption {
				prompt += " It is also encrypted at rest with the supplied data key — this is ONE-WAY."
			}
			// Confirm BEFORE reading the key: `--data-key -` consumes stdin,
			// which is also where the prompt is answered.
			if err := cmdutil.Confirm(f.IOStreams, yes, prompt); err != nil {
				return err
			}

			var key *string
			if wantsEncryption {
				k, err := readDataKey(dataKey, f.IOStreams.In)
				if err != nil {
					return err
				}
				key = &k
			}

			client, err := f.GraphQLClient()
			if err != nil {
				return err
			}
			// Read the current URN so the output can report what the promotion
			// displaced. Best-effort: an App key that can run the mutation may
			// not satisfy memory(ref:), so a miss must not block the promotion.
			var previousURN string
			if resp, gerr := gen.GetMemory(cmd.Context(), client, memRef); gerr == nil && resp != nil && resp.Memory != nil {
				previousURN = resp.Memory.Urn
			}

			resp, err := gen.LinkMemoryToUser(cmd.Context(), client, memRef, externalUser, key)
			if err != nil {
				return api.MapError(err)
			}
			// linkMemoryToUser is declared Memory! so a conformant server never
			// returns null without an error; guard the deref anyway.
			if resp == nil || resp.LinkMemoryToUser == nil {
				return exitcode.Newf(exitcode.Error, "link returned no memory")
			}
			dto := linkUserDTO{
				Memory:      dtoFromMemory(resp.LinkMemoryToUser),
				PreviousURN: previousURN,
				Encrypted:   resp.LinkMemoryToUser.IsEncrypted,
			}
			return output.Write(f.IOStreams, f.JSON, dto, func(w io.Writer) error {
				if _, err := fmt.Fprintf(w, "✓ linked memory %s to external user %s\n", dto.Memory.ID, externalUser); err != nil {
					return err
				}
				// The re-mint is the surprising part, so state it plainly and
				// show both URNs when the old one is known.
				if dto.PreviousURN != "" && dto.PreviousURN != dto.Memory.URN {
					if _, err := fmt.Fprintf(w, "  urn re-minted: %s → %s (the previous URN no longer resolves)\n",
						dto.PreviousURN, dto.Memory.URN); err != nil {
						return err
					}
				} else {
					if _, err := fmt.Fprintf(w, "  urn: %s (re-minted; any previous URN no longer resolves)\n", dto.Memory.URN); err != nil {
						return err
					}
				}
				if dto.Encrypted {
					if _, err := fmt.Fprintf(w, "  encrypted at rest (class %s) — keep the data key, there is no decrypt\n", dto.Memory.Class); err != nil {
						return err
					}
				}
				return nil
			})
		},
	}
	cmd.Flags().StringVar(&externalUser, "external-user", "", "the App's own identifier for the end user (not a Hadron user id)")
	_ = cmd.MarkFlagRequired("external-user")
	cmd.Flags().StringVar(&dataKey, "data-key", "", `also encrypt the memory at rest with this key ("-" reads stdin, recommended)`)
	cmd.Flags().BoolVar(&yes, "yes", false, "skip the confirmation prompt")
	return cmd
}
