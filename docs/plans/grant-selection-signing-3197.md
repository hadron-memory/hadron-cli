# Signing a selection of grants (cli#3197)

Design as built. Server: hadron-server#5001 (PR #5176, `42086bf2`); contract
`cor:acl:140:07` ("Signing a selection") and `cor:acl:140:09`.

## Surface

| Command | Operation |
|---|---|
| `team grant request-selection-signature <grant-id>...` | `requestGrantSelectionSignature` |
| `team grant signing-request get-selection <selection-id>` | `grantSigningSelection` |
| `team grant signing-request withdraw-selection <selection-id>` | `withdrawGrantSigningSelection` |

Code: `internal/cmd/team/grant_selection.go`, `internal/cmdutil/grantsigning.go`.
The one-grant verbs and their operations are unchanged.

## Decisions

- **No schema refresh.** The committed snapshot (`schema/SOURCE`,
  `17cdf79a1292c95af8d7ebaac4f5c86110b5fb7a`) already contains `42086bf2`.
- **Separate verbs, not a flag.** A selection is its own entity with its own id
  and shape, and the server answers its items as unknown on the one-grant read
  and withdraw.
- **The server judges the list.** Ids are sent as given; the CLI has no cap or
  duplicate check. Only no id at all, or a blank one, is refused locally.
- **Items reuse the one-grant DTO**, so a script reads one shape. Text prints the
  ceremony URL once: every item's `ceremonyUrl` is the selection's page.
- **Refusals.** The server's message and code are kept; `grantRef`, `reason`,
  `cap` and `state` are added in text and as `--json` `extensions`. No other
  extension is copied, and a `grantRef` appears only when the caller passed it.
- **Older server.** A server may offer one-grant signing and no selection, so
  the "does not offer" sentence is the selection's own.
- **Help** states the Decided parts only (one touch, each grant on its own, the
  cap). It does not repeat the one-grant help's sentence that only a worker
  session is answered: the server also answers a person for grants they may read.

## Not covered

The portal multi-select (portal#2638), MCP tools (the server decided GraphQL
only), and signing itself, which happens on the ceremony page.
