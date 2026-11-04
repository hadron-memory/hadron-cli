// Package cmdutil provides the Factory injected into every command:
// lazily-resolved config, token store, and API client, plus the
// values of the persistent --json/--server/--app flags.
package cmdutil

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"sync"
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

	// GraphQLRequestContext decorates individual authenticated generated calls.
	// The root installs the worktree attribution policy; queries stay unchanged.
	GraphQLRequestContext func(context.Context, *graphql.Request) context.Context

	// InstructedContext is installed by the root. Explicit instruction commands
	// ask it to verify the worktree binding; ordinary calls never use it.
	InstructedContext func(context.Context) (context.Context, error)

	// BeginInstructedWrite is installed by the root (cli#1594). A command that
	// today writes as the person calls it before its first lookup, then calls
	// the returned function at send time for an instructed write's context:
	// the worker session when the binding is verified for the target App,
	// else the person's, with a note on stderr.
	BeginInstructedWrite func(context.Context) func(ctx context.Context, targetAppID, targetAppRef string) (context.Context, bool, error)

	// BeginServerTargetInstructedWrite snapshots a person-write binding before
	// confirmation. The server resolves the target App and owns its admission.
	BeginServerTargetInstructedWrite func(context.Context) func(context.Context) (context.Context, bool, error)

	// Persistent flag values, bound by the root command.
	JSON       bool
	ServerFlag string
	AppFlag    string

	// Overridable for tests.
	ConfigFn     func() (*config.Config, error)
	TokenStoreFn func() store.Store

	// DeliveryPending collects the optional messages-waiting metadata of every
	// successful typed response (cli#1084); the entry point renders it once.
	DeliveryPending *api.DeliveryPendingRecorder

	// credentials records which kinds of credential this invocation actually
	// sent (cli#1624), so a refusal's remedy follows the request, not a second
	// token lookup at render time. Read it through Credentials().
	credentials     *api.CredentialLedger
	credentialsOnce sync.Once

	cfg        *config.Config
	tokenStore store.Store
}

func NewFactory() *Factory {
	return &Factory{
		IOStreams:    output.System(),
		HTTPClient:   &http.Client{Timeout: 30 * time.Second},
		ConfigFn:     config.Load,
		TokenStoreFn: store.Resolve,

		DeliveryPending: &api.DeliveryPendingRecorder{},
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

// App resolves the App context — the --app flag, then the config default —
// shape-checked and canonicalized (CanonicalAppRef, #540): what comes back is
// an App id or hrn:app:<root>:<slug>, never a value the server would refuse
// as "not fully qualified" in its own words.
// Empty means no App context, which the server treats as fine.
func (f *Factory) App() (string, error) {
	if f.AppFlag != "" {
		return CanonicalAppRef("--app", f.AppFlag)
	}
	cfg, err := f.Config()
	if err != nil {
		return "", err
	}
	// `hadron app set-active` refuses a malformed value at write time, so
	// this catches only a hand-edited config — but it catches it with the
	// remedy, where the server's refusal would name a GraphQL field the user
	// never saw (#540).
	app, err := CanonicalAppRef("the configured App context", cfg.App())
	if err != nil {
		return "", exitcode.Newf(exitcode.Usage, "%v (`hadron app set-active <ref>` replaces it)", err)
	}
	return app, nil
}

// NoteAppIsContextOnly warns on stderr that an explicitly-passed --app did NOT
// scope this listing (#383). `--app` is the persistent App-CONTEXT flag, not a
// filter, so `agent list --app <A>` returns the same rows for every <A> —
// which is how an agent installed only in another org's App came to be
// printed as though it were on this team.
//
// Only the explicit flag triggers the note: a configured default App is
// ambient context nobody passed expecting a filter, so noting it on every
// invocation would be pure noise. Stderr keeps the --json stdout contract
// untouched.
func (f *Factory) NoteAppIsContextOnly(scope string) {
	if f.AppFlag == "" {
		return
	}
	fmt.Fprintf(f.IOStreams.ErrOut,
		"note: --app sets the App context and does NOT scope this listing — these are %s, not the App's roster. Use `hadron app agent list %s` for the installed agents, or `hadron team worker list --app %s` for the staff.\n",
		scope, f.AppFlag, f.AppFlag)
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
	// Admin impersonation: a LIVE stored impersonation token supersedes the
	// admin's own credential (so every command runs read-only as the target).
	// HADRON_TOKEN still wins above — an explicit env credential is never
	// silently overridden. An expired stored token is deleted inside
	// ResolveImpersonationToken and we fall through to the real credential.
	if impToken := auth.ResolveImpersonationToken(f.TokenStore(), server); impToken != "" {
		return impToken, auth.SourceImpersonation, nil
	}
	// A corrupt/unreadable store surfaces as an error here rather than a false
	// SourceNone, so a broken auth.json fails loud instead of "not signed in" (#125).
	return auth.ResolveToken(f.TokenStore(), server)
}

// Credentials returns this invocation's sent-credential record (cli#1624),
// creating it on first use. Every client that sends a credential records its
// kind here at send time; it never chooses which credential is sent.
func (f *Factory) Credentials() *api.CredentialLedger {
	f.credentialsOnce.Do(func() {
		if f.credentials == nil {
			f.credentials = &api.CredentialLedger{}
		}
	})
	return f.credentials
}

// CredentialKindFor maps where Token() found the token to the kind recorded
// when it is sent. SourceNone sends no credential and maps to "".
func CredentialKindFor(source auth.TokenSource) api.CredentialKind {
	switch source {
	case auth.SourceStore:
		return api.CredentialStoredUser
	case auth.SourceEnv:
		return api.CredentialEnv
	case auth.SourceImpersonation:
		return api.CredentialImpersonation
	}
	return ""
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
	client, err := api.NewClient(server, token, f.HTTPClient, api.WithDeliveryPending(f.DeliveryPending),
		api.WithCredentialSent(f.Credentials(), CredentialKindFor(source)))
	if err != nil {
		return nil, err
	}
	if f.GraphQLRequestContext != nil {
		return &requestContextClient{inner: client, context: f.GraphQLRequestContext}, nil
	}
	return client, nil
}

// PublicGraphQLClient returns a client for the server's PUBLIC surface. It
// attaches credentials when they exist but does NOT require them, so a query
// the server serves anonymously still works when signed out.
//
// authenticated reports whether credentials were attached — a caller can then
// say which surface the answer came from instead of implying it was privileged.
// A corrupt token store still errors rather than silently degrading to
// anonymous (#125): failing loud beats a confusing half-answer.
func (f *Factory) PublicGraphQLClient() (client graphql.Client, authenticated bool, err error) {
	server, err := f.Server()
	if err != nil {
		return nil, false, err
	}
	token, source, err := f.Token()
	if err != nil {
		return nil, false, err
	}
	if source == auth.SourceNone {
		// NewClient sends no bearer for an empty token, and RequireSecureURL
		// permits plain http when there are no credentials to leak.
		token = ""
	}
	c, err := api.NewClient(server, token, f.HTTPClient, api.WithDeliveryPending(f.DeliveryPending),
		api.WithCredentialSent(f.Credentials(), CredentialKindFor(source)))
	if err != nil {
		return nil, false, err
	}
	return c, token != "", nil
}

// requestContextClient keeps the hook above the existing transport, so bearer,
// redirect and session-security rules remain identical.
type requestContextClient struct {
	inner   graphql.Client
	context func(context.Context, *graphql.Request) context.Context
}

func (c *requestContextClient) MakeRequest(ctx context.Context, req *graphql.Request, resp *graphql.Response) error {
	return c.inner.MakeRequest(c.context(ctx, req), req, resp)
}
