# Billing B1: spend, adjust, required source, expiring credit (cli#3071)

Rulings cited below are the numbered rulings of cli#3071: 1 a bare date on
`--from` / `--to`; 2 the `spend --json` shape; 3 how the table shows unpriced
spend (ruling 3 is the `unpriced` COST cell); 5 whether to mirror the server's
reserved source types; 6 `serverCode` in the `--json` failure envelope; 7
`--role billing` with other roles.

Design as built. Built against hadron-server `17cdf79a1` (A1 #5007, A2 #5008,
A4 #5011, A5 #5012).

| Surface | As built |
|---|---|
| `billing spend <org>` | `aiSpend`. `--from`, `--to`, `--group-by day\|app\|model\|key-owner` all required; there is no grouping by person. A bare `YYYY-MM-DD` is sent as midnight UTC, anything else follows `cmdutil.ParseTimeFlag` (ruling 1: accept exactly `YYYY-MM-DD` as midnight UTC, everything else through the shared rule). |
| `billing adjust <org>` | `adjustCredits`. Amount, source pair and note required; the amount is opaque; confirmed, sent once. |
| `billing grant` | The source pair is required before any request. The SDL keeps it nullable, the server requires it at run time. |
| `billing balance` | `--json` gains `expiring` (`[]` when none), in the server's order; text lists the parts under the balance. |
| Exit codes | `IDEMPOTENCY_CONFLICT` is 5 by a literal row, and the `--json` failure carries `serverCode` (ruling 6: `serverCode` in the `--json` failure envelope). |
| `user set-roles` | Accepts `billing`. Combining it with another role is the server's refusal (ruling 7: pass the role through, the server refuses a combination). `org member set-role` still refuses it. |

Cost and charged are never one figure (cor:dmo:170:04). The server sums the cost
of priced units only, so a row whose every unit is unpriced arrives as
`costMicroCents: "0"`; the table shows `unpriced`. `--json` passes the server's
figures unchanged, with `unpricedEvents` beside them.

`spend --json` carries one key more than ruling 2's draft (the `--json` shape): `totalGroups`, which
A2 landed with ("so a short list is never read as the whole one").

Not built here: the server's reserved source types are not mirrored (ruling 5: no client list, the server's `BAD_USER_INPUT` passes through as exit 2);
the `prices set` wording "effective now" changes when hadron-server#5021 lands.

Follow-up (cli#3207): a null element in `spend` rows or `balance` expiring parts
fails the command with exit 1 and prints nothing (the schema types them
non-null; skipping one would show a short list as the whole one). `ledger` and
`prices` still skip a null item. The `adjust` prompt shows the note.
