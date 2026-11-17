// Package secret implements `hadron secret ...` — management for the
// owner-scoped, encrypted secret store. Secret values are write-only: commands
// can send them to mutations, but never print or select them.
package secret

import (
	"encoding/json"
	"fmt"
	"io"
	"regexp"

	"github.com/spf13/cobra"

	"github.com/hadron-memory/hadron-cli/internal/api/gen"
	"github.com/hadron-memory/hadron-cli/internal/cmdutil"
	"github.com/hadron-memory/hadron-cli/internal/exitcode"
	"github.com/hadron-memory/hadron-cli/internal/output"
)

const (
	scopeUser = "user"
	scopeOrg  = "org"
	scopeApp  = "app"

	kindGeneric      = "generic"
	kindWebFetchAuth = "webfetch-auth"
)

var secretNameRE = regexp.MustCompile(`^[a-z0-9-]{1,64}$`)

// NewCmdSecret builds the `hadron secret` command group.
func NewCmdSecret(f *cmdutil.Factory) *cobra.Command {
	cmd := &cobra.Command{
		Use:     "secret <command>",
		Aliases: []string{"secrets"},
		Short:   "Manage owner-scoped encrypted secrets",
		Long: `Store encrypted secrets (API tokens, passwords, header values) that agent
runs use, without the value appearing in your shell history, a command's
arguments or any hadron output. Whether the agent's model may see it depends
on the kind (below).

Each secret belongs to one owner:
  user  your own; only you can create, list or delete it (no admin override)
  org   an organization's; an org ADMIN or OWNER creates and deletes it, and
        any member of the org can list its metadata
  app   an App's (--scope app --owner <app ref>); the server decides who may
        store, list and delete it, and keeps every stored value as a version
        (secret set adds one; secret versions lists them; secret events is
        the App secret's audit log). An App secret is never resolved by a
        run: nothing a run does can read it

When a run needs a secret it looks it up by name, in the user's secrets first,
then the org's (never an App's). A generic secret's value can be read by the agent's model; a
webfetch-auth secret is only injected into matching web requests and the model
never sees it.

Values are write-only: secret create and secret update send the value, and
no command ever prints it (secret get shows only the metadata). Pass it on
stdin, from a file or at a no-echo prompt, never as an argument. Typical flow:
secret create, then secret list to see what exists, secret set to store a new
value (an org or user secret is overwritten in place; an App secret gets a new
version), secret update for metadata, and secret rm when it is no longer needed.`,
		Example: `  printf '%s' "$GITHUB_TOKEN" | hadron secret create --name github-token --scope user --kind generic --value-file -
  hadron secret list --scope user
  printf '%s' "$TOKEN" | hadron secret create --name deploy-token --scope app --owner hrn:app:acme.com:ops --kind generic --value-file -
  printf '%s' "$NEW" | hadron secret set <id> --value-file -
  hadron secret versions <id>`,
	}
	cmd.AddCommand(newCmdCreate(f))
	cmd.AddCommand(newCmdGet(f))
	cmd.AddCommand(newCmdLs(f))
	cmd.AddCommand(newCmdRm(f))
	cmd.AddCommand(newCmdUpdate(f))
	cmd.AddCommand(newCmdSet(f))
	cmd.AddCommand(newCmdVersions(f))
	cmd.AddCommand(newCmdEvents(f))
	return cmd
}

// secretDTO is the stable --json shape for Secret. It intentionally mirrors
// only the inspectable server fields; value is not exposed by design.
type secretDTO struct {
	ID        string           `json:"id"`
	OwnerType string           `json:"ownerType"`
	OwnerID   string           `json:"ownerId"`
	Name      string           `json:"name"`
	Kind      string           `json:"kind"`
	Metadata  *json.RawMessage `json:"metadata"`
	// CurrentVersion and Protected are the server's answer, shown as given
	// (cor:api:240:01): null / false for org and user secrets.
	CurrentVersion *int    `json:"currentVersion"`
	Protected      bool    `json:"protected"`
	CreatedAt      string  `json:"createdAt"`
	CreatedBy      *string `json:"createdBy"`
	UpdatedAt      string  `json:"updatedAt"`
	UpdatedBy      *string `json:"updatedBy"`
}

func dtoFromFields(s gen.SecretFields) secretDTO {
	return secretDTO{
		ID:             s.Id,
		OwnerType:      s.OwnerType,
		OwnerID:        s.OwnerId,
		Name:           s.Name,
		Kind:           s.Kind,
		Metadata:       s.Metadata,
		CurrentVersion: s.CurrentVersion,
		Protected:      s.Protected,
		CreatedAt:      s.CreatedAt,
		CreatedBy:      s.CreatedBy,
		UpdatedAt:      s.UpdatedAt,
		UpdatedBy:      s.UpdatedBy,
	}
}

func validateOwner(scope, owner string) (string, *string, error) {
	switch scope {
	case scopeUser:
		if owner == "" {
			return scope, nil, nil
		}
	case scopeOrg, scopeApp:
		if owner == "" {
			return "", nil, exitcode.Newf(exitcode.Usage, "--owner is required for --scope %s", scope)
		}
	default:
		return "", nil, exitcode.Newf(exitcode.Usage, "--scope must be one of user, org, app")
	}
	return scope, &owner, nil
}

func validateName(name string) error {
	if !secretNameRE.MatchString(name) {
		return exitcode.Newf(exitcode.Usage, "--name must be lowercase [a-z0-9-] and at most 64 characters")
	}
	return nil
}

func validateKind(kind string) error {
	switch kind {
	case kindGeneric, kindWebFetchAuth:
		return nil
	default:
		return exitcode.Newf(exitcode.Usage, "--kind must be one of generic, webfetch-auth")
	}
}

// readSecretMaterial reads the --value-file material (cmdutil.ReadSecretInput).
func readSecretMaterial(ioStreams *output.IOStreams, source, label string) (string, error) {
	return cmdutil.ReadSecretInput(ioStreams, source, "value-file", label, "secret value")
}

func jsonStringValue(s string) (json.RawMessage, error) {
	b, err := json.Marshal(s)
	if err != nil {
		return nil, err
	}
	return json.RawMessage(b), nil
}

func jsonObjectValue(obj map[string]string) (json.RawMessage, error) {
	b, err := json.Marshal(obj)
	if err != nil {
		return nil, err
	}
	return json.RawMessage(b), nil
}

func ensureJSONObject(raw *json.RawMessage, flag string) (*json.RawMessage, error) {
	if raw == nil {
		return nil, nil
	}
	var obj map[string]any
	if err := json.Unmarshal(*raw, &obj); err != nil {
		return nil, exitcode.Newf(exitcode.Usage, "--%s must be a JSON object: %v", flag, err)
	}
	if obj == nil {
		return nil, exitcode.Newf(exitcode.Usage, "--%s must be a JSON object", flag)
	}
	return raw, nil
}

func writeSecretLine(w io.Writer, verb string, dto secretDTO) error {
	_, err := fmt.Fprintf(w, "%s secret %s  %s/%s  owner %s %s\n", verb, output.SafeCell(dto.Name), output.SafeCell(dto.Kind), output.SafeCell(dto.ID), output.SafeCell(dto.OwnerType), output.SafeCell(dto.OwnerID))
	return err
}
