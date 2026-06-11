// Package cmdutil provides the Factory injected into every command:
// lazily-resolved config, token store, and API client, plus the
// values of the persistent --json/--server/--app flags.
package cmdutil

import (
	"net/http"
	"os"
	"time"

	"github.com/Khan/genqlient/graphql"

	"github.com/hadron-memory/hadron-cli/internal/api"
	"github.com/hadron-memory/hadron-cli/internal/auth"
	"github.com/hadron-memory/hadron-cli/internal/auth/store"
	"github.com/hadron-memory/hadron-cli/internal/config"
	"github.com/hadron-memory/hadron-cli/internal/exitcode"
	"github.com/hadron-memory/hadron-cli/internal/output"
)

type Factory struct {
	IOStreams  *output.IOStreams
	HTTPClient *http.Client

	// Persistent flag values, bound by the root command.
	JSON       bool
	ServerFlag string
	AppFlag    string

	// Overridable for tests.
	ConfigFn     func() (*config.Config, error)
	TokenStoreFn func() store.Store

	cfg        *config.Config
	tokenStore store.Store
}

func NewFactory() *Factory {
	return &Factory{
		IOStreams:    output.System(),
		HTTPClient:   &http.Client{Timeout: 30 * time.Second},
		ConfigFn:     config.Load,
		TokenStoreFn: store.Resolve,
	}
}

func (f *Factory) Config() (*config.Config, error) {
	if f.cfg == nil {
		cfg, err := f.ConfigFn()
		if err != nil {
			return nil, err
		}
		f.cfg = cfg
	}
	return f.cfg, nil
}

// Server resolves the server base URL: --server flag, then
// HADRON_SERVER env, then config, then the hosted default.
func (f *Factory) Server() (string, error) {
	if f.ServerFlag != "" {
		return f.ServerFlag, nil
	}
	cfg, err := f.Config()
	if err != nil {
		return "", err
	}
	return cfg.Server(), nil
}

// App resolves the App URN context: --app flag, then config default.
// Empty means no App context, which the server treats as fine.
func (f *Factory) App() (string, error) {
	if f.AppFlag != "" {
		return f.AppFlag, nil
	}
	cfg, err := f.Config()
	if err != nil {
		return "", err
	}
	return cfg.App(), nil
}

func (f *Factory) TokenStore() store.Store {
	if f.tokenStore == nil {
		f.tokenStore = f.TokenStoreFn()
	}
	return f.tokenStore
}

// Token returns the active token and its source for the resolved
// server ("" source when unauthenticated). HADRON_TOKEN is checked
// before the token store so CI never triggers a keyring probe.
func (f *Factory) Token() (string, auth.TokenSource, error) {
	if env := os.Getenv(store.EnvToken); env != "" {
		return env, auth.SourceEnv, nil
	}
	server, err := f.Server()
	if err != nil {
		return "", auth.SourceNone, err
	}
	token, source := auth.ResolveToken(f.TokenStore(), server)
	return token, source, nil
}

// GraphQLClient returns an authenticated genqlient client, failing
// with the AuthRequired exit code when no credentials are present.
func (f *Factory) GraphQLClient() (graphql.Client, error) {
	server, err := f.Server()
	if err != nil {
		return nil, err
	}
	token, source, err := f.Token()
	if err != nil {
		return nil, err
	}
	if source == auth.SourceNone {
		return nil, exitcode.Newf(exitcode.AuthRequired, "not signed in to %s — run `hadron auth login` or set %s", server, store.EnvToken)
	}
	return api.NewClient(server, token, f.HTTPClient)
}
