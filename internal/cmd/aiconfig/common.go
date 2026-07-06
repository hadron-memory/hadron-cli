package aiconfig

import (
	"fmt"
	"io"
	"strings"

	"github.com/hadron-memory/hadron-cli/internal/api/gen"
	"github.com/hadron-memory/hadron-cli/internal/exitcode"
)

// resolveOwner maps the mutually-exclusive --app/--agent/--org flags to an
// (ownerType, ownerId). Exactly one must be set. ownerId may be an ID or a URN
// (the server dispatches either).
func resolveOwner(app, agent, org string) (gen.AiConfigOwnerType, string, error) {
	set := 0
	var t gen.AiConfigOwnerType
	var id string
	if app != "" {
		set, t, id = set+1, gen.AiConfigOwnerTypeApp, app
	}
	if agent != "" {
		set, t, id = set+1, gen.AiConfigOwnerTypeAgent, agent
	}
	if org != "" {
		set, t, id = set+1, gen.AiConfigOwnerTypeOrganization, org
	}
	switch {
	case set == 0:
		return "", "", exitcode.Newf(exitcode.Usage, "an owner is required — pass exactly one of --app, --agent, or --org")
	case set > 1:
		return "", "", exitcode.Newf(exitcode.Usage, "--app, --agent, and --org are mutually exclusive")
	}
	return t, id, nil
}

// resolveSecret reads a secret value: "-" pulls it from stdin (trimmed), so a
// key never has to appear in argv or shell history. An empty stdin is rejected:
// otherwise an unset `$KEY` piped in would silently send "" — which on `update`
// *clears* the stored key. To clear deliberately, use --api-key "".
func resolveSecret(v string, stdin io.Reader) (string, error) {
	if v == "-" {
		data, err := io.ReadAll(stdin)
		if err != nil {
			return "", err
		}
		key := strings.TrimSpace(string(data))
		if key == "" {
			return "", exitcode.Newf(exitcode.Usage, `no API key on stdin (--api-key -); to clear the key use --api-key ""`)
		}
		return key, nil
	}
	return v, nil
}

// dtoFromFields maps a masked AiServiceConfig (the mutation return) into the
// stable --json DTO shared with `ai-config ls`. Never key material.
func dtoFromFields(f gen.AiServiceConfigFields) aiConfigDTO {
	return aiConfigDTO{
		ID:            f.Id,
		Name:          f.Name,
		OwnerType:     string(f.OwnerType),
		OwnerID:       f.OwnerId,
		Provider:      f.Provider,
		Model:         f.Model,
		HasAPIKey:     f.HasApiKey,
		APIKeyPreview: f.ApiKeyPreview,
		Params:        f.Params,
		Enabled:       f.Enabled,
		CreatedAt:     f.CreatedAt,
		UpdatedAt:     f.UpdatedAt,
	}
}

// writeConfigLine renders the human summary for create/update — masked, so it
// shows only the key preview, never the key.
func writeConfigLine(w io.Writer, verb string, dto aiConfigDTO) error {
	state := "enabled"
	if !dto.Enabled {
		state = "disabled"
	}
	key := "no key"
	if dto.HasAPIKey {
		key = "key set"
		if dto.APIKeyPreview != nil && *dto.APIKeyPreview != "" {
			key = "key " + *dto.APIKeyPreview
		}
	}
	if _, err := fmt.Fprintf(w, "%s %s  %s/%s  %s  %s\n", verb, dto.Name, dto.Provider, dto.Model, state, key); err != nil {
		return err
	}
	_, err := fmt.Fprintf(w, "  id %s  owner %s %s\n", dto.ID, dto.OwnerType, dto.OwnerID)
	return err
}
