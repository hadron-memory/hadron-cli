# memory set and agent create send --slug with the create

Unreleased (cli#3223, the companion of hadron-server#5203 S1).

- `hadron memory set --org ... --name ... --slug <s>` sends the slug as
  `createMemory(slug:)` in the one call. The follow-up rename is gone, so a taken
  or reserved slug is the server's refusal and **nothing is created**; there is
  no longer a window where the memory exists under a derived slug.
- `hadron agent create --slug <s>` is new and does the same for `createAgent`.
- `--owner-me --slug` stays refused (a user-owned memory's slug is derived).
- A create **without** `--slug` is unchanged in the CLI and keeps the old,
  slug-free request, so it still works on a server older than #5203 S1.
  `--slug` needs a server from 2026-11-26 or later.

**Behaviour change once hadron-server#5203 S3 (iteration) ships.** A derived
slug no longer refuses when taken: a second `memory set --name "Project KB"`
creates `project-kb` with a suffix, and an `agent create` with a duplicate name
creates under a suffixed slug instead of `AGENT_URN_TAKEN`. The output echoes the
URN; scripts that relied on the refusal to detect a duplicate should pass
`--slug`, which is still refused when taken. This release ships before S3.
