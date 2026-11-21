# Instructed read-client revoke (cli#2754)

`secret client revoke` accepts `--instruction <kind>:<ref>` with the existing instruction parser and strict worker-session resolver. Without it, the existing person operation, request variables and JSON DTO are unchanged. An explicitly empty flag is invalid.

The instructed path snapshots a complete binding for the selected server and App before the client lookup. It rechecks that snapshot before each `mySecretReadClients` page and again after confirmation, immediately before one instructed `revokeGrant`. Lookup pages carry the worker session so held clients can list; only the revoke carries the instruction. It never retries or falls back to the person. The optional claim is isolated in a second typed operation so the original person document is unchanged.

The CLI checks syntax and binding, not authority. The server checks the reference, holder and grant App under cor:acl:140:05 and cor:sec:060:01. Every grant refusal remains GRANT_NOT_MANAGER; client-list absence remains the existing separate exit 4. Unknown revoke outcomes retain exit 7, no replay and the client-list recovery. Check instructed recovery with `secret client list --all --as-worker`.

Server evidence: revokeGrant’s instructed arm is present in server merge 5eaeffadb (#3876, 2026-11-10); inspected server origin/main 9e7e70305bcba52336eb52dfb63ae0e27407e234. No schema refresh, server change, signature/issue path or general team grant command is included. #1846 remains its own unbuilt surface.

Gate: T2 Standard plus security at every head, distinct non-Codex LOCAL and BLIND engineers, QA through a compiled CLI with fake HTTP. No live revoke or secret read is needed. Tests start with the red-only commit f8680621. Mutants and checks are recorded in the PR receipt.
