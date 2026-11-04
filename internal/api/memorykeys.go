package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/vektah/gqlparser/v2/gqlerror"

	"github.com/hadron-memory/hadron-cli/internal/exitcode"
)

// MemoryKeysHeader carries caller-held passphrases for encrypted memories
// (hadron-server src/lib/memoryUnlock.ts, stageRequestMemoryKeys). The value is
// a JSON array of {memory, passphrase}: at most MaxMemoryKeys entries and
// MaxMemoryKeysBytes bytes. cor:sec:040:04: a key is never a field on a query
// or mutation; it travels beside the operation and lasts that one request.
const MemoryKeysHeader = "X-Hadron-Memory-Keys"

// The server's bounds (memoryUnlock.ts MAX_HEADER_ENTRIES / MAX_HEADER_BYTES),
// checked locally so an over-limit invocation is refused before any request.
const (
	MaxMemoryKeys      = 8
	MaxMemoryKeysBytes = 16 * 1024
)

// MemoryKey is one --unlock entry (hadron-cli#1234). v1 is passphrase-only:
// the header's raw dataKey form is deliberately not offered (Victor delta 3).
type MemoryKey struct {
	Memory     string `json:"memory"`
	Passphrase string `json:"passphrase"`
}

type memoryKeysCtx struct{}

// memoryKeysValue is the encoded header plus the secrets an error rendering
// must never echo.
type memoryKeysValue struct {
	header  string
	secrets []string
}

// WithMemoryKeys marks every GraphQL request made with ctx as carrying the
// invocation's --unlock passphrases. The root installs it once on the command
// context, so it rides the requests THIS invocation makes and nothing else; it
// is never persisted or cached (ruling 0129). Empty keys leave ctx unchanged.
func WithMemoryKeys(ctx context.Context, keys []MemoryKey) (context.Context, error) {
	if len(keys) == 0 {
		return ctx, nil
	}
	if len(keys) > MaxMemoryKeys {
		return ctx, exitcode.Newf(exitcode.Usage, "at most %d --unlock memories per invocation (got %d). Nothing was sent", MaxMemoryKeys, len(keys))
	}
	encoded, err := json.Marshal(keys)
	if err != nil {
		return ctx, exitcode.Newf(exitcode.Usage, "the --unlock entries could not be encoded. Nothing was sent")
	}
	if len(encoded) > MaxMemoryKeysBytes {
		return ctx, exitcode.Newf(exitcode.Usage, "the --unlock passphrases are too long together (the server takes at most %d bytes). Nothing was sent", MaxMemoryKeysBytes)
	}
	v := memoryKeysValue{header: string(encoded)}
	v.secrets = append(v.secrets, v.header)
	for _, k := range keys {
		v.secrets = append(v.secrets, k.Passphrase)
		// The header is JSON, so an echo of it carries the ESCAPED spelling.
		if b, err := json.Marshal(k.Passphrase); err == nil {
			if esc := strings.TrimSuffix(strings.TrimPrefix(string(b), `"`), `"`); esc != k.Passphrase {
				v.secrets = append(v.secrets, esc)
			}
		}
	}
	return context.WithValue(ctx, memoryKeysCtx{}, v), nil
}

func memoryKeysFrom(ctx context.Context) (memoryKeysValue, bool) {
	if ctx == nil {
		return memoryKeysValue{}, false
	}
	v, ok := ctx.Value(memoryKeysCtx{}).(memoryKeysValue)
	return v, ok && v.header != ""
}

// HasMemoryKeys reports whether ctx carries --unlock passphrases.
func HasMemoryKeys(ctx context.Context) bool {
	_, ok := memoryKeysFrom(ctx)
	return ok
}

// unlockCodeExit is the server's MemoryUnlockError.code set and the exit code
// each maps to in the existing contract (internal/exitcode).
var unlockCodeExit = map[string]int{
	"WRONG_KEY":        exitcode.Usage,
	"BAD_UNLOCK_INPUT": exitcode.Usage,
	"NOT_ENCRYPTED":    exitcode.Usage,
	"NOT_FOUND":        exitcode.NotFound,
	"UNAUTHENTICATED":  exitcode.AuthRequired,
}

var unlockCodeText = map[string]string{
	"WRONG_KEY":        "a passphrase does not match its memory",
	"BAD_UNLOCK_INPUT": "the server could not use an --unlock entry (for example, the memory was encrypted with a raw data key, which v1 does not take)",
	"NOT_ENCRYPTED":    "an --unlock memory is not encrypted; drop --unlock for it",
	"NOT_FOUND":        "an --unlock memory was not found (only the owner of an encrypted memory can unlock it)",
	"UNAUTHENTICATED":  "unlocking needs a signed-in user",
}

// unlockRefusal turns the server's context-time unlock failure into a typed
// CLI error. The server stages the header before any resolver runs, so the
// refusal arrives with no path and no data; an error that names a path came
// from a resolver and keeps its ordinary mapping. The server's message is NOT
// repeated: a fixed sentence per code cannot carry a secret back.
func unlockRefusal(ctx context.Context, list gqlerror.List) error {
	if !HasMemoryKeys(ctx) {
		return nil
	}
	for _, e := range list {
		if e == nil || len(e.Path) > 0 {
			continue
		}
		code := extensionCode(e)
		exit, ok := unlockCodeExit[code]
		if !ok {
			continue
		}
		return &serverCodeError{
			error: exitcode.Newf(exit, "--unlock refused (%s): %s. The request was refused before it ran; the passphrase is not shown", code, unlockCodeText[code]),
			code:  code,
		}
	}
	return nil
}

// memoryKeySecrets lists the --unlock values a rendering must never echo: the
// encoded header and each passphrase in its raw and JSON-escaped spellings.
func memoryKeySecrets(req *http.Request) []string {
	keys, ok := memoryKeysFrom(req.Context())
	if !ok {
		if h := req.Header.Get(MemoryKeysHeader); h != "" {
			return []string{h}
		}
		return nil
	}
	return keys.secrets
}

// redactUnlockSecrets scrubs the --unlock secrets from what can be RENDERED as
// an error: the whole of a non-200 answer (an error page, an echoing gateway,
// Apollo's context-failure 500), and only the errors list of a 200 envelope.
// Unlike the 32-byte operation key, a passphrase can be short or a common
// word, so a successful answer's data — the plaintext the user asked for — is
// never rewritten.
//
// A JSON body is scrubbed by STRUCTURE, never as bytes (PR #1616 BLIND P2-1):
// a passphrase such as `errors` or `KEY` would otherwise rewrite the envelope's
// keys or the refusal's extensions.code, and the classification that runs on
// the scrubbed body (hasGraphQLErrorsEnvelope, unlockRefusal, codeFromEnvelope)
// would lose the exit code and serverCode the contract promises. String values
// are scrubbed; the GraphQL envelope's own keys and an identifier-shaped
// extensions.code are protocol, not echo, and are kept. A body that is not
// JSON has no structure to keep and is scrubbed whole (fail closed).
func redactUnlockSecrets(status int, body []byte, secrets []string) []byte {
	if len(secrets) == 0 {
		return body
	}
	doc, err := decodeJSONUseNumber(body)
	if err != nil {
		return redactSecrets(body, secrets)
	}
	changed := false
	if obj, ok := doc.(map[string]any); ok && status == http.StatusOK {
		errs, ok := obj["errors"]
		if !ok {
			return body
		}
		obj["errors"] = scrubJSON(errs, "", secrets, &changed)
	} else {
		doc = scrubJSON(doc, "", secrets, &changed)
	}
	if !changed {
		return body
	}
	var out bytes.Buffer
	enc := json.NewEncoder(&out)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(doc); err != nil {
		return redactSecrets(body, secrets)
	}
	return bytes.TrimRight(out.Bytes(), "\n")
}

func decodeJSONUseNumber(body []byte) (any, error) {
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.UseNumber()
	var doc any
	if err := dec.Decode(&doc); err != nil {
		return nil, err
	}
	if dec.More() {
		return nil, errors.New("trailing data after the JSON value")
	}
	return doc, nil
}

// envelopeKeys are the GraphQL response's own property names (spec §7.1):
// structure the classifiers read, never an echo, so never rewritten.
var envelopeKeys = map[string]bool{
	"data": true, "errors": true, "message": true, "locations": true, "line": true,
	"column": true, "path": true, "extensions": true, "code": true,
}

// scrubJSON returns v with every secret spelling replaced in its string values
// and in any object key that is not an envelope key. parent is the key v sat
// under, so an error's extensions.code — a server enum the exit code and
// serverCode are derived from — is kept when it is identifier-shaped.
func scrubJSON(v any, parent string, secrets []string, changed *bool) any {
	switch t := v.(type) {
	case string:
		out := string(redactSecrets([]byte(t), secrets))
		if out != t {
			*changed = true
		}
		return out
	case []any:
		for i := range t {
			t[i] = scrubJSON(t[i], parent, secrets, changed)
		}
		return t
	case map[string]any:
		out := make(map[string]any, len(t))
		for k, val := range t {
			if code, ok := val.(string); ok && k == "code" && parent == "extensions" && isProtocolCode(code) {
				out[k] = code
				continue
			}
			nk := k
			if !envelopeKeys[k] {
				nk = string(redactSecrets([]byte(k), secrets))
				if nk != k {
					*changed = true
				}
			}
			out[nk] = scrubJSON(val, k, secrets, changed)
		}
		return out
	default:
		return v
	}
}

// isProtocolCode reports whether code has the shape of a server error code
// (WRONG_KEY, AiServiceConfigNotFoundError): letters, digits and underscores,
// bounded. The encoded --unlock header is base64 JSON and never has this shape.
func isProtocolCode(code string) bool {
	if code == "" || len(code) > 64 {
		return false
	}
	for i := 0; i < len(code); i++ {
		if !isNameByte(code[i]) {
			return false
		}
	}
	return true
}
