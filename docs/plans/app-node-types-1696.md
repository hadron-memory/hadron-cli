# App node type union parity

`app nodes --types` uses the same StringSlice, Changed check and pointer-to-slice shape as `node list --types`. The existing generated NodeFilter binding preserves omission versus explicit empty; no SDL, query, generator or output contract change is needed. CSV syntax is parsed by pflag, then values pass unchanged: no trimming, case folding, deduplication or local validation policy.

The server owns BAD_USER_INPUT for empty lists, singular/plural conflict, comment and more than 20 entries before deduplication. cor:api:090:02 covers appNodesPage through its shared appNodesRead/nodeTypesOrRefuse path (Vera 6660135616). Pagination and access rules are unchanged; cor:api:120:04 is untouched.

The parity test runs both command doors against the same wire expectations and refusal envelopes, including omission, explicit [], CSV, spaces/case/duplicates, conflict, comment, over-cap, NOT_FOUND and FORBIDDEN. The tracker pin rejects #63 on every appNodesPage row. The remaining approvalState/contentScope/minted gaps belong to cli#1697; this change implements none of them.

Red commit 4a5b1f84 fails on the missing flag and stale matrix citations. QA is compiled CLI against fake HTTP; no provider or deployment claim is made. Compatible server fields already exist in the committed schema and generated client; unknown older schema inputs fail explicitly rather than retrying without a filter.
