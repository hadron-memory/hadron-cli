# cli#2704 — set-token redact follow-ups (symmetric server-code check, sentinel-equal token, U+2028 spelling)

As-built plan for the four non-blocking follow-ups from the #2683 delta seats
(Jack F1/F2/F4, Rook F1), all on the credential-redaction surface #2650 built.
One slice, no behavior change beyond closing the named gaps. Issue:
https://github.com/hadron-memory/hadron-cli/issues/2704

## What main has today (verified at `49bdc792`)

- `setTokenError` drops a server code only when `strings.Contains(code, token)`
  — the raw token. An **escaped** spelling of the token inside a carried code
  passes the guard and reaches the user (Jack F2).
- The URN guard in `sync.go` splices the server's URN when
  `redactToken(u, token) == u` — a **fixpoint test**, vacuously true for every
  `u` when the token equals the sentinel `"[REDACTED]"` (replace-all of the
  sentinel by itself is identity and that token has no escaped spellings), so a
  token-carrying URN splices into the receipt (Jack F1).
- `escapedSpellings` (both sites: `internal/cmd/memory/sync.go` for the token,
  `internal/api/memorykeys.go` for `--unlock` secrets, depth 2) generates Go's
  HTML-escaped and `SetEscapeHTML(false)` forms. Go escapes **U+2028/U+2029 in
  both forms**; Node's `JSON.stringify` writes them **raw** — so the "Node
  form" the generator models is wrong for line separators, and the true Node
  spelling (raw U+2028 alongside `\"` for a quote) is never matched (Rook F1).
- No positive pin that a token-free URN is spliced into the receipt (Jack F4);
  only the negative paths are pinned.

## The fix

1. **`containsAnySpelling(s, token) bool`** in `sync.go`: containment of the
   literal or any escaped spelling — the honest predicate. Used by both guards:
   - `setTokenError`: drop the code when `containsAnySpelling(code, token)`
     (catches escaped spellings — Jack F2 — and keeps the empty-token behavior).
   - URN guard: splice when `u != "" && !containsAnySpelling(u, token)` — no
     fixpoint, so the sentinel-equal token can no longer wedge it open (Jack F1).
   generated Go spelling also contributes the variant with its textual
   `\u2028`/`\u2029` escape sequences unescaped to the raw characters
   (`nodeLineSeparatorReplacer` in each file —
   the two implementations are deliberately independent per #2650's design).
   In `memorykeys.go` the variants join `next`, so depth 2 derives from them
   too (Node escaping twice keeps the raw character). Superset-safe for
   redaction; guards only become stricter (fail closed). For tokens without
   line separators the variant equals the original and nothing is added — zero
   change for existing inputs.
3. **Pins** (`internal/cmd/memory/sync_set_token_2704_test.go`,
   `internal/api/memorykeys_test.go`), red-first where main is wrong:
   - F2: a server code carrying only the escaped spelling is dropped (red).
   - F1: sentinel-equal token, URN containing it → typed ref, not the URN (red,
     command-level through the fake GraphQL server).
   - Rook F1: `redactToken` redacts the Node-true spelling (`\"` + raw U+2028;
     U+2029 sibling) (red); `escapedSpellings` covers the Node spelling at
     depth 1 and 2, and `redactUnlockSecrets` scrubs a Node-form echo (red).
   - F4: token-free URN splices into the receipt, human line and `--json` DTO
     unmarshalled (positive pin — guards against over-redaction regressions).

## Mutants (one per guard, run at the fix head)

| Mutant | Guard that must go red | Result |
| --- | --- | --- |
| `setTokenError` guard back to `strings.Contains(code, token)` | F2 pin | killed |
| URN guard back to `redactToken(u, token) == u` | F1 pin | killed (human + JSON) |
| drop the node-variant `add` in `sync.go` | sync U+2028 pin | killed |
| drop the node-variant `add` in `memorykeys.go` | api U+2028 pins | killed (both) |
| URN guard never splices (`false`) | F4 positive pin | **survived the first draft** |

The mutant-5 survival exposed the F4 pin as written vacuous: it typed
`acme.com:kb`, which `CanonicalMemoryRef` locally composes to the same URN
the splice would carry, so fallback and splice were indistinguishable. The
pin now types the memory by id (a ref that cannot canonicalize to a URN
locally), so the receipt can carry the server's URN only through the splice;
the re-run mutant is killed on both the human line and the `--json` DTO.

## Spec FIT

None — client-internal redaction hardening; the concept crosses no repo
boundary (the server's JSON encoding behavior is a fact the client defends
against, not a contract another repo must match).
