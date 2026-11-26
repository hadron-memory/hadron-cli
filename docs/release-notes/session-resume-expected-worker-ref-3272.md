# `team session start` (resume): the expected worker is sent as `expectedWorkerRef`

Unreleased (cli#3272, the CLI half of hadron-server#5163: a hard rename with no
alias, cor:api:140:01).

**Minimum server version: 0.48.0** (`HADRON_SERVER_VERSION`) for a resume in a
directory that has a binding file.

| Resume | Server below 0.48.0 | This server floor and later |
|---|---|---|
| binding file (lapsed or ended) | refused, exit 2: nothing consumed or written, and not retried without the check; use `start --as <worker>` | sends `expectedWorkerRef` |
| no binding file | works: no expected worker is sent | unchanged |

A CLI released before this one sends `expectedWorkerId`, which a 0.48.0 server
refuses in the same case: upgrade the CLI, or use `start --as <worker>`.

Only the request's field name changes. No flag or `--json` shape changes: the
`WORKER_MISMATCH` refusal's extension keys are still `expectedWorkerId` and
`expectedWorkerName`, as the server sends them.

The schema snapshot refresh also carries the server's person-owned credit ledger
(hadron-server#5207), which no command reaches yet (cli#3072): `billing
balance`, `ledger`, `grant` and `adjust` still name an organization, send no
`userRef`, and print the same shapes.
