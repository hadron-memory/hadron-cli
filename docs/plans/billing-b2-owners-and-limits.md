# Design as built: billing B2, owners and limits (cli#3072)

Slice B2 of the metering epic (hadron-server#5006); the plan is the server's `subscription-billing-plan` section 8.

- **One owner selector** (`internal/cmd/billing/owner.go`) on `balance`, `ledger`, `spend`, `grant`, `adjust` and `limits`: the organization argument, `--user <person>` or `--owner-me`, exactly one (else exit 2, nothing sent). `--user` resolves through `cmdutil.ResolveUserExactly` (a near-miss would show the wrong person's money); `--owner-me` reads `me` once and refuses a credential that is not a person (exit 8) before any billing request. The request carries the one ref and omits the other (`omitempty`, never null).
- **DTOs** carry both `organizationId` and `ownerUserId`, always present, one null. Additive: nothing removed or renamed.
- **`billing limits`** (read), `limits set`, `limits clear` over `ownerLimits`, `setOwnerLimits`, `clearOwnerLimits`. The CLI keeps no list of keys (cor:acl:150:02): it prints what the server returns and passes unknown keys through. `unlimited` is sent as `null`. `--only-source-type` rides on both writes (gate ruling 4); the server refuses it without `--replace`.
- **Refusals.** `OWNER_LIMIT_REACHED` is a literal Conflict row (exit 5). `api.OwnerLimitDetails` forwards only the keys the server sent for it and for `NODE_CONTENT_TOO_LARGE` (`maxBytes`, gate ruling 5), never null, never computed. In `renderError` it runs after the clone reader so `agent clone` keeps the fields (ruling 6).
- **Divergence from the issue text.** The issue says `--note` is "kept with the row" on `limits clear`; the server's SDL says a clear's note is not stored (the row is deleted), so that help says so.
- Snapshot re-pinned to hadron-server `399b2d6b3` (A6, A7a-b, A10, A11, A13, A14 merged); `schema/SOURCE` names it.
