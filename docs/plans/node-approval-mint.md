# Node approvals, verification, and per-node minting in the CLI

> **Status: in progress.** hadron-cli#802 consumes hadron-server#1591. The
> server's PR 1 contract is recorded on that issue; PR 2 will define the mint
> fields and report. Update this document to the design as built before the CLI
> PR is opened.

## Scope and ordering

The server ships two sequential changes. PR 1 introduces revision approval,
integrity verification, and status reads. PR 2 introduces per-node minting and
backfills prior corpus state. The CLI can implement PR 1 against its published
GraphQL contract while it is under review, but readiness waits for the merged
server contract and the PR 2 mint shape.

The CLI commands are `node approve <ref>`, `memory approve-all -m <memory>`,
`node verify <ref>`, and `node mint` / `spec mint` for all unminted nodes of the
memory. Reads show server status in `node get`, `spec get`, `spec list`, and
`spec describe`; `spec list` filters by minted and approval state. The server
remains the authority for permissions, the current revision, mint blockers,
and verification verdicts. Client output uses explicit DTOs and typed codes.

## Approval hash v1

The hash covers `Node.name` (called `title` in the serialization), abstract,
and content, in that order. For each field, null has a `name null\n` header;
a string has a `name <UTF-8 byte count>\n` header, its bytes, and a final
newline. A `hadron-approval-v1\n` version line comes first. CRLF and lone CR
become LF before byte counting. There is no trimming or Unicode
normalization. The result is a full lowercase SHA-256 hex string. The CLI's
`internal/approvalhash` package uses the server's fixture from commit
`c80638ec` to check every canonical byte and hash, including null versus
empty, multibyte UTF-8, line endings, and field-boundary text.

The hash is an integrity check on content, title, and abstract. Other node
fields and edges are deliberately excluded. A legitimate edit creates a new
revision, so a prior approval becomes `SUPERSEDED`; an out-of-band change to
the approved current revision is `TAMPERED`. CLI output must keep those two
states distinct.

## Compatibility and read consistency

New status fields are read through separate typed operations. An older server
that rejects those fields should still return the pre-approval node/spec read,
with approval and mint status reported as unavailable rather than guessed.
`node get` already brackets content with revision probes and keeps the
after-probe's stamps; approval status belongs in that same after-probe because
an approval can arrive without changing the revision.

Whole-memory reads page to exhaustion. A list filter must be applied by the
server before pagination; client-side filtering of a capped page would silently
omit matching nodes. The mint report comes from the server's complete gate,
including approval and minted-to-unminted citation blockers.

## Verification gates

- Server fixture parity at the byte and hash levels.
- Command tests against the typed operations, including older-server fallback,
  idempotent approval, conflict/refusal mapping, status states, and JSON shapes.
- Full Go suite, lint, generated-client freshness, and the CLI Hadron review
  checklist before a PR.
- Dan's GLM source review and Cody's QA at the exact head.
