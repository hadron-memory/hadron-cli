package memory

import (
	"errors"
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

type transferDependentDTO struct {
	Kind     string `json:"kind"`
	Count    int    `json:"count"`
	Handling string `json:"handling"`
}

type transferBlockerDTO struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// transferDTO is the stable --json shape for either a dry-run preview or an
// applied transfer. The server owns the address and dependency decisions.
type transferDTO struct {
	MemoryID     string                 `json:"memoryId"`
	OldURN       string                 `json:"oldUrn"`
	NewURN       string                 `json:"newUrn"`
	FromOwner    string                 `json:"fromOwner"`
	ToOwner      string                 `json:"toOwner"`
	CurrentClass string                 `json:"currentClass"`
	TargetClass  string                 `json:"targetClass"`
	Dependents   []transferDependentDTO `json:"dependents"`
	Blockers     []transferBlockerDTO   `json:"blockers"`
	CanApply     bool                   `json:"canApply"`
	Applied      bool                   `json:"applied"`
}

func transferResultDTO(p *gen.TransferMemoryOwnershipTransferMemoryOwnershipMemoryOwnerTransferPreview) transferDTO {
	d := transferDTO{
		MemoryID: p.MemoryId, OldURN: p.OldUrn, NewURN: p.NewUrn,
		FromOwner: p.FromOwner, ToOwner: p.ToOwner,
		CurrentClass: string(p.CurrentClass), TargetClass: string(p.TargetClass),
		Dependents: []transferDependentDTO{}, Blockers: []transferBlockerDTO{},
		CanApply: p.CanApply, Applied: p.Applied,
	}
	for _, dep := range p.Dependents {
		if dep != nil {
			d.Dependents = append(d.Dependents, transferDependentDTO{dep.Kind, dep.Count, string(dep.Handling)})
		}
	}
	for _, blocker := range p.Blockers {
		if blocker != nil {
			message := blocker.Message
			// The server's refusal names its GraphQL argument. Render the CLI
			// remedy on this command's surface.
			if blocker.Code == "MEMORY_TRANSFER_DEPENDENTS" {
				message = strings.ReplaceAll(message, "resetGroupMembers: true", "--reset-group-members")
			}
			d.Blockers = append(d.Blockers, transferBlockerDTO{blocker.Code, message})
		}
	}
	return d
}

func newCmdTransfer(f *cmdutil.Factory) *cobra.Command {
	var org, user, class string
	var apply, yes, resetGroupMembers bool
	cmd := &cobra.Command{
		Use:   "transfer <memoryRef> (--org <ref> | --user <ref>)",
		Short: "Preview or apply a memory ownership transfer",
		Long: `Preview a memory ownership transfer and its dependent handling.

Exactly one destination owner is required. The default is a read-only preview.
Pass --apply to request an interactive confirmation, or --yes to apply in a
script. Apply repeats the server check under its lock and pins the previewed
new URN with expectedNewUrn; a stale preview fails without transferring.

Personal and private memories may transfer only to an organization you
administer. Crossing a user/org boundary requires an explicit --class. Group
members block transfer unless --reset-group-members is given; that flag
revokes all old memberships, as shown in the preview's REMOVE rows.

On apply, the preview is shown on stderr before confirmation and only the
final result goes to stdout. --json emits one result document on stdout.
Refusal codes are retained
in blockers; a blocked preview exits with the corresponding typed exit code.`,
		Example: `  hadron memory transfer hrn:mem:alice:notes --org acme.com --class knowledge
  hadron memory transfer hrn:mem:alice:notes --org acme.com --class knowledge --apply
  hadron memory transfer hrn:mem:alice:notes --org acme.com --class knowledge --yes --json`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := validateMemoryRef(args[0]); err != nil {
				return err
			}
			org = strings.TrimSpace(org)
			user = strings.TrimSpace(user)
			class = strings.TrimSpace(class)
			for _, flag := range []struct{ name, value string }{{"org", org}, {"user", user}, {"class", class}} {
				if cmd.Flags().Changed(flag.name) && flag.value == "" {
					return exitcode.Newf(exitcode.Usage, "--%s was supplied empty (check for an unset variable)", flag.name)
				}
			}
			if (org == "") == (user == "") {
				return exitcode.Newf(exitcode.Usage, "exactly one of --org or --user is required")
			}
			if apply && yes {
				return exitcode.Newf(exitcode.Usage, "use either --apply or --yes, not both")
			}
			var orgRef, userRef *string
			if org != "" {
				orgRef = &org
			} else {
				userRef = &user
			}
			var targetClass *gen.MemoryClass
			if class != "" {
				switch class {
				case "knowledge", "group", "personal", "private":
					v := gen.MemoryClass(class)
					targetClass = &v
				default:
					return exitcode.Newf(exitcode.Usage, "--class must be knowledge, group, personal, or private")
				}
			}
			client, err := f.GraphQLClient()
			if err != nil {
				return err
			}
			ref := cmdutil.CanonicalMemoryRef(args[0])
			previewResp, err := gen.TransferMemoryOwnership(cmd.Context(), client, ref, orgRef, userRef, targetClass, nil, resetGroupMembers, true)
			if err != nil {
				return api.MapError(err)
			}
			if previewResp.TransferMemoryOwnership == nil {
				return errors.New("server returned an empty transferMemoryOwnership preview")
			}
			preview := transferResultDTO(previewResp.TransferMemoryOwnership)
			if preview.Applied || preview.NewURN == "" {
				return errors.New("server returned an invalid transferMemoryOwnership preview")
			}
			if !apply && !yes {
				if err := output.Write(f.IOStreams, f.JSON, preview, func(w io.Writer) error { return writeTransferTable(w, preview) }); err != nil {
					return err
				}
				return transferBlockerError(preview)
			}
			if !preview.CanApply {
				if err := output.Write(f.IOStreams, f.JSON, preview, func(w io.Writer) error { return writeTransferTable(w, preview) }); err != nil {
					return err
				}
				return transferBlockerError(preview)
			}
			// Keep stdout to one final result on success. The preview and prompt
			// share stderr, so an interactive caller sees the plan before consent.
			if f.JSON {
				if err := output.WriteJSON(f.IOStreams.ErrOut, preview); err != nil {
					return err
				}
			} else if err := writeTransferTable(f.IOStreams.ErrOut, preview); err != nil {
				return err
			}
			prompt := fmt.Sprintf("Transfer %s to %s?", preview.OldURN, preview.NewURN)
			if resetGroupMembers {
				prompt = fmt.Sprintf("Transfer %s to %s and revoke its old group memberships?", preview.OldURN, preview.NewURN)
			}
			if err := cmdutil.Confirm(f.IOStreams, yes, prompt); err != nil {
				return err
			}
			appliedResp, err := gen.TransferMemoryOwnership(cmd.Context(), client, ref, orgRef, userRef, targetClass, &preview.NewURN, resetGroupMembers, false)
			if err != nil {
				return api.MapError(err)
			}
			if appliedResp.TransferMemoryOwnership == nil {
				return errors.New("server returned an empty transferMemoryOwnership apply result")
			}
			applied := transferResultDTO(appliedResp.TransferMemoryOwnership)
			if !applied.Applied || applied.NewURN != preview.NewURN {
				return errors.New("server returned an invalid transferMemoryOwnership apply result")
			}
			return output.Write(f.IOStreams, f.JSON, applied, func(w io.Writer) error {
				_, err := fmt.Fprintf(w, "Transferred %s → %s\n", applied.OldURN, applied.NewURN)
				return err
			})
		},
	}
	cmd.Flags().StringVar(&org, "org", "", "destination organization ref")
	cmd.Flags().StringVar(&user, "user", "", "destination user ref")
	cmd.Flags().StringVar(&class, "class", "", "target memory class")
	cmd.Flags().BoolVar(&resetGroupMembers, "reset-group-members", false, "revoke old group memberships as part of transfer")
	cmd.Flags().BoolVar(&apply, "apply", false, "prompt to apply after preview")
	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "apply after preview without prompting")
	return cmd
}

func transferBlockerError(p transferDTO) error {
	if p.CanApply {
		return nil
	}
	if len(p.Blockers) == 0 {
		return exitcode.Newf(exitcode.Error, "server refused transfer without a blocker code")
	}
	// The preview already rendered all blockers, including their machine codes.
	return exitcode.Silent(api.ExitCodeForExtension(p.Blockers[0].Code))
}

func writeTransferTable(w io.Writer, p transferDTO) error {
	t := output.NewTable(w)
	t.Row("Memory", p.MemoryID)
	t.Row("URN", p.OldURN, "→", p.NewURN)
	t.Row("Owner", p.FromOwner, "→", p.ToOwner)
	t.Row("Class", p.CurrentClass, "→", p.TargetClass)
	if err := t.Flush(); err != nil {
		return err
	}
	if len(p.Dependents) > 0 {
		t = output.NewTable(w, "DEPENDENT", "COUNT", "HANDLING")
		for _, dep := range p.Dependents {
			t.Row(dep.Kind, fmt.Sprint(dep.Count), dep.Handling)
		}
		if err := t.Flush(); err != nil {
			return err
		}
	}
	if len(p.Blockers) > 0 {
		t = output.NewTable(w, "REFUSAL", "REASON")
		for _, blocker := range p.Blockers {
			t.Row(blocker.Code, blocker.Message)
		}
		return t.Flush()
	}
	if p.CanApply && !p.Applied {
		_, err := io.WriteString(w, "Preview only. Use --apply or --yes to transfer.\n")
		return err
	}
	return nil
}
