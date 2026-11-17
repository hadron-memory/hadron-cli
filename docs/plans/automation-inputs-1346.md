# Automation and run coverage (#1346)

The CLI reaches thirteen existing server operation/argument gaps. No server,
MCP or portal contract changes; authorization, hash validation, scope resolution
and run snapshots remain server-owned.

- Raw `automation preview KIND REF` returns `{trigger,hash,graph}` with the server's
  JSON graph/digest. Human output labels graph.hash and flags an incomplete/sanitized graph display;
  review the full --json output before adoption. It does not resolve a human
  name or require an App context.
- `automation re-adopt KIND REF --expected-hash HASH --yes` sends the reviewed
  hash once after confirmation. It does not fetch or compute a replacement hash.
  Server refusals map through MapError, false refuses success. MANUAL names the
  origin run; future trigger consent does not rewrite the executed run snapshot.
- Approval `--limit` is the server page size while resolving a name. Resolution
  still drains every cursor page, retaining ambiguity protection. Without the
  flag the variable is omitted, preserving the existing default.
- Schedule creation chooses exactly one cron/run-at. Updates choose at most one;
  the server switches mode. Scope is optional at create, preserved when absent
  at update, and empty update scope resets to App memories. Agent update selects
  an installed Agent, checked by the server.
- Webhook creation forwards an optional scope without changing shown-once
  credential handling.
- Run list keeps single --status, adds mutually exclusive --statuses, parent,
  sort and direction. Every page gets the same variables. Existing DTOs remain.

Wire tests cover all thirteen rows; red commit 94c9aee4 fails on missing flags
or operations on main. Refusal-only tests are controls, not independently red
behavior evidence. Supplementary tests pin omitted fields and changed-hash
mapping. QA is through the compiled CLI per T1 gate 6540755575; no production
writes or release/install are part of this work.
