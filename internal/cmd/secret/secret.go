// Package secret implements `hadron secret ...` — management for the
// owner-scoped, encrypted secret store. Secret values are write-only: commands
// can send them to mutations, but never print or select them.
package secret

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"regexp"
	"strings"

	"golang.org/x/term"

	"github.com/spf13/cobra"

	"github.com/hadron-memory/hadron-cli/internal/api/gen"
	"github.com/hadron-memory/hadron-cli/internal/cmdutil"
	"github.com/hadron-memory/hadron-cli/internal/exitcode"
	"github.com/hadron-memory/hadron-cli/internal/output"
)

const (
	scopeUser   = "user"
	scopeOrg    = "org"
	scopeApp    = "app"
	scopeMemory = "memory"

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
		Long: `Manage Hadron's owner-scoped encrypted secret store.

Values are write-only: create sends secret material to the server, but no
command ever prints or selects it. Supply values via stdin, a file, or an
interactive no-echo prompt — never argv.`,
	}
	cmd.AddCommand(newCmdCreate(f))
	cmd.AddCommand(newCmdLs(f))
	cmd.AddCommand(newCmdRm(f))
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
	CreatedAt string           `json:"createdAt"`
	CreatedBy *string          `json:"createdBy"`
	UpdatedAt string           `json:"updatedAt"`
	UpdatedBy *string          `json:"updatedBy"`
}

func dtoFromFields(s gen.SecretFields) secretDTO {
	return secretDTO{
		ID:        s.Id,
		OwnerType: s.OwnerType,
		OwnerID:   s.OwnerId,
		Name:      s.Name,
		Kind:      s.Kind,
		Metadata:  s.Metadata,
		CreatedAt: s.CreatedAt,
		CreatedBy: s.CreatedBy,
		UpdatedAt: s.UpdatedAt,
		UpdatedBy: s.UpdatedBy,
	}
}

func validateOwner(scope, owner string) (string, *string, error) {
	switch scope {
	case scopeUser:
		if owner == "" {
			return scope, nil, nil
		}
	case scopeOrg, scopeApp, scopeMemory:
		if owner == "" {
			return "", nil, exitcode.Newf(exitcode.Usage, "--owner is required for --scope %s", scope)
		}
	default:
		return "", nil, exitcode.Newf(exitcode.Usage, "--scope must be one of user, org, app, memory")
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

func readSecretMaterial(ioStreams *output.IOStreams, source, label string) (string, error) {
	var data []byte
	var err error
	switch {
	case source == "-":
		data, err = io.ReadAll(ioStreams.In)
	case strings.HasPrefix(source, "@"):
		data, err = os.ReadFile(strings.TrimPrefix(source, "@"))
	case source != "":
		data, err = os.ReadFile(source)
	case !ioStreams.IsInputTerminal():
		data, err = io.ReadAll(ioStreams.In)
	default:
		fmt.Fprintf(ioStreams.ErrOut, "%s: ", label)
		input, ok := ioStreams.In.(*os.File)
		if !ok {
			fmt.Fprintln(ioStreams.ErrOut)
			return "", exitcode.Newf(exitcode.Usage, "interactive secret prompt requires file-backed stdin")
		}
		data, err = term.ReadPassword(int(input.Fd()))
		fmt.Fprintln(ioStreams.ErrOut)
	}
	if err != nil {
		if source != "" && source != "-" {
			return "", exitcode.Newf(exitcode.Usage, "reading --value-file: %v", err)
		}
		return "", err
	}
	value := strings.TrimRight(string(data), "\r\n")
	if value == "" {
		return "", exitcode.Newf(exitcode.Usage, "no secret value provided; pipe it on stdin, pass --value-file, or run interactively")
	}
	return value, nil
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
	_, err := fmt.Fprintf(w, "%s secret %s  %s/%s  owner %s %s\n", verb, dto.Name, dto.Kind, dto.ID, dto.OwnerType, dto.OwnerID)
	return err
}
