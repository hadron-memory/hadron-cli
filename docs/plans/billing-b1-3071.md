# Billing B1: spend, adjust, required source, expiring credit (cli#3071)

Design as built. Built against hadron-server `17cdf79a1` (A1 #5007, A2 #5008,
A4 #5011, A5 #5012).

| Surface | As built |
|---|---|
| `billing spend <org>` | `aiSpend`. `--from`, `--to`, `--group-by day\|app\|model\|key-owner` all required; there is no grouping by person. A bare `YYYY-MM-DD` is sent as midnight UTC, anything else follows `cmdutil.ParseTimeFlag` (ruling 1 b). |
| `billing adjust <org>` | `adjustCredits`. Amount, source pair and note required; the amount is opaque; confirmed, sent once. |
| `billing grant` | The source pair is required before any request. The SDL keeps it nullable, the server requires it at run time. |
| `billing balance` | `--json` gains `expiring` (`[]` when none), in the server's order; text lists the parts under the balance. |
| Exit codes | `IDEMPOTENCY_CONFLICT` is 5 by a literal row, and the `--json` failure carries `serverCode` (ruling 6). |
| `user set-roles` | Accepts `billing`. Combining it with another role is the server's refusal (ruling 7). `org member set-role` still refuses it. |

Cost and charged are never one figure (cor:dmo:170:04). The server sums the cost
of priced units only, so a row whose every unit is unpriced arrives as
`costMicroCents: "0"`; the table shows `unpriced`. `--json` passes the server's
figures unchanged, with `unpricedEvents` beside them.

`spend --json` carries one key more than ruling 2's draft: `totalGroups`, which
A2 landed with ("so a short list is never read as the whole one").

Not built here: the server's reserved source types are not mirrored (ruling 5);
the `prices set` wording "effective now" changes when hadron-server#5021 lands.
