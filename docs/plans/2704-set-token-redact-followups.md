# cli#2704 — set-token redact follow-ups (symmetric server-code check, sentinel-equal token, U+2028 spelling)

As-built plan for the four non-blocking follow-ups from the #2683 delta seats
(Jack F1/F2/F4, Rook F1), all on the credential-redaction surface #2650 built.
One slice, no behavior change beyond closing the named gaps. Issue:
https://github.com/hadron-memory/hadron-cli/issues/2704

## What main has at this PR's base (`6e2d19de`; first read at `49bdc792`)

- `setTokenError` drops a server code only on a redaction **fixpoint**,
  `redactToken(code, token) != code`. This plan was written against
  `49bdc792`, where the guard was the substring check
  `strings.Contains(code, token)`; main's cli#2671 fold (`fd3d668a`) replaced
  that with the fixpoint before this PR's base, closing the escaped-spelling
  gap (Jack F2) the substring form had. What the fixpoint still misses: a
  token equal to the marker `"[REDACTED]"` redacts to itself, so both a
  carried code and the rendered message ride through unscrubbed
  (cli#2714 Dara P1).
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
   - the marker case: a token that is or contains `"[REDACTED]"` cannot be
     scrubbed by replacement (an equal token rewrites to itself, and token
     material survives beside the marker), so `setTokenError` answers with a
     fixed refusal carrying nothing from the server's message; the code guard
     still applies (cli#2714 Dara P1).
   - URN guard: splice when `u != "" && !containsAnySpelling(u, token)` — no
     fixpoint, so the sentinel-equal token can no longer wedge it open (Jack F1).
2. **Node line-separator spellings at both generator sites**: every
   generated Go spelling also contributes the variant with its textual
   `\u2028`/`\u2029` escape sequences unescaped to the raw characters
   (`nodeJSONForm` in each file — a scan of the Go-encoded body by whole
   JSON escape units, so an escaped backslash is consumed as a unit and a
   LITERAL backslash-u in the token stays literal, cli#2714 Dara P2; the two
   implementations are deliberately independent per #2650's design).
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
   - cli#2714 round-2 pins: the marker token never appears in the rendered
     message, and a code carrying it is dropped; a literal backslash-u token
     generates its true mixed Node spelling (never the bogus raw-separator
     one the blind replacer derived) at both generator sites; the api
     depth-2 cases are labeled by their derivation chain (Node-then-Go,
     Go-then-Go) with U+2029 covered; the fake server decodes its request
     and encodes its response (no interpolated JSON); the fallback pin
     asserts `dto.Memory` equals the expected canonical typed ref.

## Mutants (one per guard, run at the fix head)

| Mutant | Guard that must go red | Result |
| --- | --- | --- |
| `setTokenError` guard back to the base's fixpoint, `redactToken(code, token) != code` (the `strings.Contains` form this plan was first written against no longer exists on main) | marker pin (cli#2714 Dara P1 / Rodolfo P3-1) — the F2 pin alone does NOT kill this one, which is why the marker pin exists | killed (rendered message + carried code, re-run at the fix head) |
| URN guard back to `redactToken(u, token) == u` | F1 pin | killed (human + JSON) |
| drop the node-variant `add` in `sync.go` | sync U+2028 pin | killed |
| drop the node-variant `add` in `memorykeys.go` | api U+2028 pins | killed (both) |
| `nodeJSONForm` back to the blind `\u2028`/`\u2029` replacer | literal-backslash-u pins (cli#2714 Dara P2) | killed (both sites) |
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
