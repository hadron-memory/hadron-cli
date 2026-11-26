# `team session start` (resume): the CLI follows the server's `expectedWorkerRef`

Unreleased (cli#3272, the companion of hadron-server#5163, a hard rename with no
alias: cor:api:140:01).

**Minimum server version: 0.48.0** (`HADRON_SERVER_VERSION`), for a resume in a
directory that has a binding file. A CLI released before this one is refused that
same resume once a 0.48.0 server deploys.

## What changed on the wire

One request field. **Every flag and every `--json` shape is unchanged.**
`ResumeSessionInput.expectedWorkerId` is now `expectedWorkerRef`; the CLI sends
the bound worker's id in it, as before.

| Resume | Server below 0.48.0 | Server 0.48.0 and later |
|---|---|---|
| binding file (lapsed or ended) | refused, exit 2: nothing consumed or written, and not retried without the check; use `start --as <worker>` | sends `expectedWorkerRef` |
| no binding file | works: no expected worker is sent | unchanged |

## Unchanged on purpose

- The `WORKER_MISMATCH` refusal's extension keys are still `expectedWorkerId` and
  `expectedWorkerName`, as the server sends them.
- `billing balance`, `ledger`, `grant` and `adjust` still name an organization
  and print the same shapes. The schema refresh carries the server's person-owned
  credit ledger (hadron-server#5207), which no command reaches yet (cli#3072);
  `grant` and `adjust` send no `userRef`.

## Ships with

hadron-server#5163 (PR #5314), in the same release window.
