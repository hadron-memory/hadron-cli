# Memory ownership transfer CLI (#782)

The server contract is `transferMemoryOwnership` from hadron-server #1481,
merged at `397b7f7b78f6a04248f2098d7c42e57ce26b0e60`. The CLI schema
snapshot was exported from that exact commit. The canonical ownership rule is
`cor:acl:010:02`: personal/private content can leave its owner's namespace only
for an organization that owner administers; it cannot move to another user.

## Command and sequence

`hadron memory transfer <memoryRef> (--org <ref> | --user <ref>) [--class <c>]
[--reset-group-members] [--apply | --yes]` calls the mutation with
`dryRun: true` first. Without either apply flag, it renders the preview and
exits. `--apply` asks on a terminal; `--yes` applies without prompting. Both
apply modes call the same mutation again with `dryRun: false` and the preview's
`newUrn` as `expectedNewUrn`. The server recomputes under a lock and refuses
a stale target with `STALE_TRANSFER_PREVIEW` (exit 5). The CLI never constructs
or guesses the target URN.

The preview lists each dependent's `kind`, `count`, and `handling` (CARRY,
REFUSE, REMOVE, or CREATE), plus typed blockers. `--reset-group-members` is
sent on both calls; it asks the server to revoke old group memberships in the
transfer transaction. A group destination gets the caller as its first owner
member. The command requires the server to return `applied: false` for a
preview and `applied: true` with the previewed URN after apply.

## Output and failures

`--json` returns one stable DTO with the preview or final result. On a
successful apply, the preview is written to stderr and only the final result
to stdout, for both human and JSON modes. A blocked preview is written to stdout with all blocker codes and a
nonzero exit. Invalid target/class/refusal exits 2, missing or concealed
source exits 4, stale/dependent/state conflict exits 5, and permission refusal
exits 8. The first blocker determines the exit class; every blocker remains in
the output for diagnostics. The same code mapping handles an apply error.

No live transfer is needed for CLI validation. Command tests use a fake
GraphQL server to assert preview-only calls, confirmation gating, exact
`expectedNewUrn` forwarding, reset forwarding, refusal output, and stale
apply handling.
