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
   - the marker case grew into the round-4 rule (Eden's ruling after two
     independent readers found replacement recreating token text): partial
     redaction of the message is only attempted when it provably writes the
     token nowhere. `replacementCanRecreateToken(token)` — the token contains
     the marker, or the marker contains the token — or the message carrying
     any covered spelling (`containsAnySpelling(msg, token)`) means a fixed
     refusal replaces the whole message: the scrubber's only replacement is
     the marker, and a token contained in it is re-created inside every
     replacement ("REDACTED" → "[REDACTED]"), a token spanning a seam
     re-forms around it ("x[" in "xx[" → "x[REDACTED]"), and a message
     carrying an escaped spelling is never scrubbed piecemeal (cli#2714
     round 2 Dara P1, round 3 Bruno P2 / Dara P2).
   - and the refusal grew into the round-5 rule (Eden's pairing ruling after
     Bruno's round-4 delta P2 and Dara's BLIND delta P2 found the same class
     independently: the refusal "the server refused the source token" itself
     contains a token "server refused" / "source token"): the drop path's
     message is a CHECKED cascade (`tokenSafeRefusal`), not a constant — the
     full refusal, then a second fixed text sharing no word with it
     (`"input declined"`), then no echo at all (the empty message, the only
     text that provably carries no spelling of a non-empty token; the exit
     code and a clean server code still identify the refusal). Every tier is
     gated by the same `containsAnySpelling` predicate before it is printed.
     The partial-redaction candidate is likewise judged in its FINAL rendered
     form (Dara's BLIND delta verification note, made concrete):
     `output.SafeLine` neutralises control characters to a space and can
     append an ellipsis at the rune bound, and either transform can join
     characters into a spelling the raw message never carried (message
     "a\x00b", token "a b") — so the third arm of the drop guard tests the
     SafeLine output, not only the raw message.
   - `replacementCanRecreateToken` renamed `tokenOverlapsMarker` (Dara's
     round-4 delta P3): the old name claimed every replacement hazard, but
     the helper tests only mutual containment of token and marker — the
     seam-spanning token is caught by the message arm, which is why both
     arms stand.
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
   - cli#2714 round-4 pins: token equal to / substring of / containing the
     marker, a seam-spanning token ("x[" in "xx["), and a message carrying
     only an escaped spelling all render the fixed refusal and keep an
     ordinary code; a token disjoint from the marker over a token-free
     message keeps the server's own text (never over-redact). Round 3 also
     corrected the api depth-2 fixture bytes (raw separator source bytes had
     made the Go expectations duplicate the Node ones; the true Go spellings
     are now asserted, final entry relabeled Node-then-Node — the generator
     was right, only the fixture bytes were wrong), and moved the 2650 cap
     pin to a token-free long message, where the cap now applies.
   - cli#2714 round-5 pins (Eden's pairing ruling): a token that is a word of
     the refusal ("refused", "server"), a substring of it, or equal to the
     whole of it renders the SECOND fixed text, not the first; a token inside
     BOTH fixed texts ("ed" — in "refused" and in "declined") renders no echo
     at all; a token inside the second text only ("input") keeps the full
     refusal (the cascade walks only when a tier is unsafe for this token).
     Exact-equality assertions on the rendered message, plus the unchanged
     carried-code rule on every tier. Jerry's three design-review asks are
     folded in: an exhaustive loop drives EVERY substring of both fixed
     texts and of their concatenation through the drop path (totality for
     the named cases); command-level pins assert on the RENDERED output —
     the no-echo tier renders the bare `hadron: ` prefix on stderr and a
     `--json` envelope that keeps the exit code and the clean server code
     with an empty message, and a code carrying the token is dropped with
     the message still empty; the second tier renders `hadron: input
     declined` on stderr. The renderer's own fixed words (the prefix) are a
     stated limit, not the drop path's output. A second pin holds the output
     side (Dara's BLIND delta note): a control character between the token's
     halves ("a\x00b" over token "a b") must render the full refusal — the
     sanitizer must not manufacture a spelling — while an unrelated token
     over the same message keeps the sanitized server text (no
     over-redaction). The round-4 loop's refusal assertion also became exact
     equality (Dara's round-4 delta P3): an error holding both the refusal
     and a leftover echo of the server message no longer passes.

## Mutants (one per guard, run at the fix head)

| Mutant | Guard that must go red | Result |
| --- | --- | --- |
| `setTokenError` guard back to the base's fixpoint, `redactToken(code, token) != code` (the `strings.Contains` form this plan was first written against no longer exists on main) | carried-code assertions of the marker/round-4 pins detect the guard mutation; the rendered-message assertions detect removal of the fixed-refusal branch — credited separately (cli#2714 round-3 Dara P3); the F2 pin alone does NOT kill this one | killed (re-run at the round-4 head: 17 assertion failures) |
| drop-branch back to the one-directional `strings.Contains(token, marker)` of round 3 | round-4 pin: substring / seam / escaped-only cases (cli#2714 Bruno P2, Dara P2) | killed (10 assertion failures) |
| drop only the message-spelling arm (`replacementCanRecreateToken` alone) | round-4 pin: the escaped-spelling-only case (a disjoint token over a message carrying its escaped spelling) | killed (3 assertion failures) |
| `tokenSafeRefusal` back to the unconditional round-4 constant (`return fullTokenRefusal`) | round-5 pin: word / substring / whole-refusal cases (cli#2714 Bruno round-4 P2, Dara round-4 P2) | killed (8 assertion failures; re-verified at the final head) |
| second tier returned unchecked (walk to `secondTokenRefusal` without its `containsAnySpelling` gate) | round-5 pin: the token-inside-both case ("ed") | killed (2 assertion failures) |
| terminal tier returns `fullTokenRefusal` instead of no echo | round-5 pin: the token-inside-both case ("ed") | killed (2 assertion failures) |
| final-candidate arm dropped (guard tests the raw message only, as before the round-5 fix) | sanitization pin: the control-character case ("a\x00b" over "a b") | killed (2 assertion failures) |
| cascade tiers reordered (`secondTokenRefusal` first) | the round-4 exact-equality assertions (Dara's round-4 P3 fix): every round-4 case would render the second text | killed (9 assertion failures) — the exhaustive-substring loop stays safe under a reorder (each tier is still gated), so the equality pins carry this one |
| URN guard back to `redactToken(u, token) == u` | F1 pin | killed (human + JSON) |
| drop the node-variant `add` in `sync.go` | sync U+2028 pin | killed |
| drop the node-variant `add` in `memorykeys.go` | api U+2028 pins | killed (both) |
| `nodeJSONForm` back to the blind `\u2028`/`\u2029` replacer | literal-backslash-u pins (cli#2714 Dara P2) | killed (both sites) |
| URN guard never splices (`false`) | F4 positive pin | **survived the first draft** |

The never-splice mutant's survival (the last row) exposed the F4 pin as written vacuous: it typed
`acme.com:kb`, which `CanonicalMemoryRef` locally composes to the same URN
the splice would carry, so fallback and splice were indistinguishable. The
pin now types the memory by id (a ref that cannot canonicalize to a URN
locally), so the receipt can carry the server's URN only through the splice;
the re-run mutant is killed on both the human line and the `--json` DTO.

## Spec FIT

None — client-internal redaction hardening; the concept crosses no repo
boundary (the server's JSON encoding behavior is a fact the client defends
against, not a contract another repo must match).
