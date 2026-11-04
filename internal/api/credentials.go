package api

import (
	"context"
	"sync"
)

// CredentialKind names which credential a request carried, for diagnostics
// only (hadron-cli#1624). It never selects a credential: the token a client
// sends is fixed when the client is built, and recording what was sent cannot
// change it, its precedence or what the server sees.
type CredentialKind string

const (
	// CredentialStoredUser is the person's own login from the token store.
	CredentialStoredUser CredentialKind = "stored-user"
	// CredentialEnv is HADRON_TOKEN.
	CredentialEnv CredentialKind = "env"
	// CredentialImpersonation is a stored admin-impersonation token.
	CredentialImpersonation CredentialKind = "impersonation"
	// CredentialHostKey is a host key read from a host configuration.
	CredentialHostKey CredentialKind = "host-key"
	// CredentialExplicit is a token the command was handed directly (stdin).
	CredentialExplicit CredentialKind = "explicit"
)

// CredentialLedger records the kinds of credential this invocation actually put
// on the wire, filled in at send time by the shared transport. The entry point
// reads it to decide whether a refusal can honestly be blamed on the stored
// login. A nil ledger records nothing and reports nothing.
type CredentialLedger struct {
	mu    sync.Mutex
	kinds map[CredentialKind]struct{}
}

// Sent records that a request carrying a credential of this kind was sent.
func (l *CredentialLedger) Sent(kind CredentialKind) {
	if l == nil || kind == "" {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.kinds == nil {
		l.kinds = map[CredentialKind]struct{}{}
	}
	l.kinds[kind] = struct{}{}
}

// Only reports whether every credential-carrying request so far used this one
// kind, and at least one was sent. Nothing sent, or a mix of kinds, is unknown
// provenance and reports false: a caller must not then assert which credential
// the server refused.
func (l *CredentialLedger) Only(kind CredentialKind) bool {
	if l == nil {
		return false
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	_, ok := l.kinds[kind]
	return ok && len(l.kinds) == 1
}

// WithCredentialSent records kind in l each time this client sends a request
// that carries its token. A client without a token records nothing.
func WithCredentialSent(l *CredentialLedger, kind CredentialKind) ClientOption {
	return func(c *nullSafeGraphQLClient) {
		if c.doer != nil {
			c.doer.onSent = func() { l.Sent(kind) }
		}
	}
}

type rawCredentialCtx struct{}

type rawCredential struct {
	ledger *CredentialLedger
	kind   CredentialKind
}

// WithRawCredentialSent asks RawGraphQL (`hadron api`) to record kind in l when
// it sends its token, the same record the typed client keeps.
func WithRawCredentialSent(ctx context.Context, l *CredentialLedger, kind CredentialKind) context.Context {
	return context.WithValue(ctx, rawCredentialCtx{}, rawCredential{ledger: l, kind: kind})
}

func rawCredentialSent(ctx context.Context) {
	if rc, ok := ctx.Value(rawCredentialCtx{}).(rawCredential); ok {
		rc.ledger.Sent(rc.kind)
	}
}
