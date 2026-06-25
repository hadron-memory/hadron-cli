# `spec edit` edits the abstract too (#99 item 3)

A spec's body and abstract are one logical unit, but until now `spec edit`
touched only the body and explicitly punted the abstract to `node update
--abstract-file`. So maintaining one spec meant two commands with two mental
models — and (per #99 item 2) authoring a freshly-scaffolded contract took an
extra lint round-trip because the abstract and body were fixed in separate
steps. This change folds the abstract into `spec edit`.

## Design

`spec edit <citation>` now updates body **and/or** abstract:

- **Non-interactive.** Adds `--abstract <val>` (`-` reads stdin) and
  `--abstract-file <path>`, mirroring `spec new`. They combine freely with
  `--content`/`--content-file`: pass either, or both, in one call. A field whose
  flag is omitted is preserved (the omit-to-preserve wire contract on
  `NodeInput`). Guards mirror the body's: `--abstract` xor `--abstract-file`, and
  `--content -` and `--abstract -` can't both read stdin (it's consumable once).

- **Interactive.** With no content/abstract flag, `spec edit` opens a single
  `$EDITOR` buffer holding *both* fields, divided by sentinel comment lines:

  ```
  <!-- === ABSTRACT === one paragraph; the RAG retrieval surface. Edit below. -->
  <current abstract>
  <!-- === BODY === the spec markdown. Edit below. -->
  <current body>
  ```

  On save the buffer is split back on the sentinels. The body divider is
  load-bearing: if it's removed we refuse to write rather than guess where the
  body starts (no silent truncation). Everything above the body divider (minus
  the abstract sentinel) is the abstract, trimmed; everything below is the body,
  preserved verbatim so an untouched buffer round-trips to a no-op.

Each field is written only when it actually changed (CRLF normalized to LF
first, as before), so editing only the body still sends a body-only update and
leaves the abstract untouched — and vice-versa. Nothing changed → nothing
written. An empty abstract is a deliberate clear (the server normalizes
empty/whitespace to null); lint then flags the missing abstract, as expected.

The `editResultDTO` gains `bodyChanged` and `abstractChanged` (additive);
`changed` now means "body **or** abstract changed" (it could only mean body
before, so existing `changed` consumers are unaffected). The human render shows
the body line-delta when the body changed and an "abstract updated" line when
the abstract changed; the old "remember to refresh the abstract" reminder now
fires only when the body changed but the abstract didn't.

## Why the combined buffer (not body-only + flags)

Flags alone would let an *agent* update the abstract in `spec edit`, but a human
editing interactively would still be split across two commands. The single
buffer is what makes body+abstract genuinely one edit. The seam
(`SetEditorFuncForTest`) is unchanged — it still maps a string buffer to an
edited string; only the buffer's content (now combined) differs, so the existing
interactive/no-op/CRLF tests keep working (their edits land in the body section,
which sorts last).

## Out of scope

The other four #99 items (feature-`:00` auto-contract opt-in, the
memory-not-found hint, single-product inference, the flag-name audit) ship
separately. Item 2 (one-pass lint) needs no code change — `lintNode` already
reports every rubric gap in a single pass; the round-trip it described was the
two-command split this change removes.
