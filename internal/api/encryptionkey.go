package api

import (
	"context"

	"github.com/vektah/gqlparser/v2/gqlerror"

	"github.com/hadron-memory/hadron-cli/internal/exitcode"
)

type encryptionCtx struct{}

// WithoutKeyMaterial keeps authentication and attribution, but overrides any
// inherited --unlock/operation key. Encryption lookups must never carry them:
// a first-encryption key on a plaintext memory is valid only on the mutation.
func WithoutKeyMaterial(ctx context.Context) context.Context {
	ctx = context.WithValue(ctx, memoryKeysCtx{}, memoryKeysValue{})
	ctx = context.WithValue(ctx, operationKeyCtx{}, "")
	return context.WithValue(ctx, encryptionCtx{}, false)
}

// WithEncryptionKey carries exactly the target's entry, on this call only.
// --unlock itself remains passphrase-only. App completions use operation-key;
// the caller has obtained the principal from the server, not guessed a token.
func WithEncryptionKey(ctx context.Context, key MemoryKey, app bool) (context.Context, error) {
	ctx = WithoutKeyMaterial(ctx)
	if (key.DataKey == "") == (key.Passphrase == "") {
		return ctx, exitcode.Newf(exitcode.Usage, "encryption needs exactly one secret")
	}
	if app {
		if key.Passphrase != "" {
			return ctx, exitcode.Newf(exitcode.Usage, "an App key can complete encryption with --data-key only; a passphrase is not supported. Nothing was encrypted")
		}
		if err := ValidateOperationKey(key.DataKey); err != nil {
			return ctx, err
		}
		ctx = WithOperationKey(ctx, key.DataKey)
	} else {
		var err error
		ctx, err = WithMemoryKeys(ctx, []MemoryKey{key})
		if err != nil {
			return ctx, exitcode.Newf(exitcode.Usage, "the encryption key does not fit the request header (limit %d bytes); nothing was encrypted", MaxMemoryKeysBytes)
		}
	}
	return context.WithValue(ctx, encryptionCtx{}, true), nil
}

// Context-time key refusals use encrypt's vocabulary, never --unlock's. Only
// pathless errors come from header staging; resolver errors map normally.
func encryptionRefusal(list gqlerror.List) error {
	for _, e := range list {
		if e == nil || len(e.Path) > 0 {
			continue
		}
		code := extensionCode(e)
		exit, ok := unlockCodeExit[code]
		if !ok {
			continue
		}
		text := "the server refused the encryption key; check the key and target memory"
		switch code {
		case "NOT_ENCRYPTED":
			text = "this server is too old for header-based memory encryption; upgrade the server before retrying"
		case "NOT_FOUND":
			text = "the memory was not found or encryption is not permitted for this caller"
		case "UNAUTHENTICATED":
			text = "memory encryption needs an authenticated owner or the owning App's completion key"
		}
		return &serverCodeError{error: exitcode.Newf(exit, "memory encrypt refused (%s): %s. No argument fallback was attempted", code, text), code: code}
	}
	return nil
}

// RedactEncryptionText uses the transport's complete secret spellings, including
// JSON-escaped memory-key entries, for command-built diagnostics and answers.
// A successful encryption answer is not a plaintext read and must not echo keys.
func RedactEncryptionText(ctx context.Context, text string) string {
	var secrets []string
	if key := operationKeyFrom(ctx); key != "" {
		secrets = append(secrets, key)
	}
	if keys, ok := memoryKeysFrom(ctx); ok {
		secrets = append(secrets, keys.secrets...)
	}
	return string(redactSecrets([]byte(text), secrets))
}
