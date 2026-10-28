package cmdutil

import (
	"context"
	"strings"

	"github.com/Khan/genqlient/graphql"
	urn "github.com/hadron-memory/urn-lib-go"

	"github.com/hadron-memory/hadron-cli/internal/api"
	"github.com/hadron-memory/hadron-cli/internal/api/gen"
	"github.com/hadron-memory/hadron-cli/internal/exitcode"
)

// InstallRef accepts only the provider's id-or-fully-qualified-URN forms.
// In particular an ambient App context must never fill a relative install ref.
func InstallRef(ref, kind string) (string, error) {
	ref = strings.TrimSpace(ref)
	if IsEntityID(ref) {
		return ref, nil
	}
	normalized := urn.NormalizeScheme(ref)
	prefix := "hrn:" + kind + ":"
	if !strings.HasPrefix(normalized, prefix) {
		return "", exitcode.Newf(exitcode.Usage, "%s must be an id or fully-qualified hrn:%s: URN", kind, kind)
	}
	body := strings.ReplaceAll(strings.TrimPrefix(normalized, prefix), "::", ":")
	parts := strings.Split(body, ":")
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return "", exitcode.Newf(exitcode.Usage, "%s must be an id or fully-qualified hrn:%s: URN", kind, kind)
	}
	if kind == "app" {
		return CanonicalAppRef("<app>", ref)
	}
	if _, err := CanonicalizeURN("<agent>", ref); err != nil {
		return "", err
	}
	return ref, nil
}

// ParseInstallTarget validates the two explicit refs without making a request.
func ParseInstallTarget(raw string) (*gen.InstallTargetInput, error) {
	app, agent, ok := strings.Cut(raw, "/")
	if !ok || strings.Contains(agent, "/") {
		return nil, exitcode.Newf(exitcode.Usage, "--install needs <app>/<agent>, both ids or fully-qualified URNs")
	}
	appRef, err := InstallRef(app, "app")
	if err != nil {
		return nil, err
	}
	agentRef, err := InstallRef(agent, "agent")
	if err != nil {
		return nil, err
	}
	return &gen.InstallTargetInput{AppRef: appRef, AgentRef: agentRef}, nil
}

// InstallMemoryRef reads metadata only. Null must never become an empty memory
// filter: the provider conceals absent and unreadable install memories together.
func InstallMemoryRef(ctx context.Context, client graphql.Client, target *gen.InstallTargetInput) (string, error) {
	r, err := gen.InstallMemory(ctx, client, target.AppRef, target.AgentRef)
	if err != nil {
		return "", api.MapError(err)
	}
	if r.InstallMemory == nil {
		return "", exitcode.Newf(exitcode.NotFound, "install memory is not available: it may not exist yet or may not be readable by you; this read created nothing")
	}
	if r.InstallMemory.Urn == "" {
		return "", exitcode.Newf(exitcode.Error, "server returned no install-memory URN")
	}
	return r.InstallMemory.Urn, nil
}
