# Paged web-poll and signing-request list reads (cli#2032)

Design as built for hadron-cli#2032, adopting server#4123 / #4193
(`webPollsPage`, `Node.signingRequestsPage`).

## What changed

| Command | Before | After |
|---|---|---|
| `web-poll list --org` | `webPolls` (a legacy array that the server now truncates to one page) | `webPollsPage(orgRef, first, after)`; one page per call |
| `node signing-request list` | `Node.signingRequests(state, first)` | `Node.signingRequestsPage(state, first, after)`; one page per call |

Both commands take `--limit <n>` (web-poll gains it) and `--after <cursor>`.
Neither walks pages: continuing is an explicit second invocation.

## Decisions (from the gate, hadron-cli#2032)

- **Cursor is opaque.** `--after` is passed to the server exactly as a previous
  page printed it. The CLI never decodes, builds or edits one. The server's
  keyset encoding can change without a CLI change.
- **`--limit`**: any positive value through GraphQL Int max is forwarded; the
  server clamps above 100 and never refuses. The old CLI-side refusal of node
  `--limit` above 100 is removed. Zero, negatives, non-integers and overflow are
  usage errors (exit 2) before any request. An empty `--after` is a usage error.
- **JSON.** `web-poll list --json` becomes `{items, hasMore, nextCursor}`, an
  intentional **breaking** change from the bare array (the legacy field already
  returned one bounded page, so the array silently truncated). `node
  signing-request list --json` keeps `nodeId/urn/loc/requests` and adds
  `hasMore` and `nextCursor`. `total` is not selected and not shown.
- **Human output.** Tables are unchanged; each list appends `has more: true|false`
  and, only when there is a cursor, `next cursor: <cursor>`. An empty page says
  it is empty on that page, never that nothing exists.
- **Untrustworthy metadata is an error, not an empty success** (exit 1, no
  output): a missing page, `hasMore` with a missing or empty cursor, a cursor
  equal to the one the request sent, or a final page (`hasMore` false) that still
  carries a cursor (the terminal page has `nextCursor` null). `cmdutil.CheckPage` holds the rule; it
  reads the cursor as an opaque string only.
- **No fallback.** A server without the page fields answers with its own
  refusal; the CLI does not fall back to the deprecated arrays. Refusal mapping
  (`MapErrorKeepingCode`, `mapSigningError`) is unchanged.
- **No completeness claim.** Each page reflects the list at the time of that
  call; there is no snapshot guarantee across calls.

## Out of scope

Server changes, auto-pagination, other list migrations, any new access rule,
and a CLI release.
