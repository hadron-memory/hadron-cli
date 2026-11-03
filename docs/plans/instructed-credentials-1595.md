# Instructed credential reads and revokes (#1595)

Five existing commands gain an optional `--instruction <kind>:<ref>`:
App key list (three selections), key revoke, scope revoke, memory-grant list
and memory-grant rm. Their DTOs and no-flag documents stay unchanged. Instructed
calls use separate generated operations against server c3ca05b545 (#3340).

The parser and S1 usage text move from package team to internal/instruction;
team's S1 callers retain their existing wrappers and behavior. The root installs
a credential-specific binding verification function on Factory, implemented in
team using its existing binding reader and verifier. It is called only when the
flag is present, before confirmation or network activity. A complete same-server
binding is required; a missing/corrupt/incomplete/foreign-server binding is exit
2, with no person fallback. Target-App/holder authority is checked by the server
in the single credential request, not by an ambient App or a client lookup.

Xanthos's acceptance-2 correction (#71555): without the flag the five existing
commands are headerless person calls even from a bound worktree. They were never
on the authored-write allowlist. This PR leaves that policy and their wire bytes
unchanged; adding them to the allowlist would be a regression. Flagged commands
explicitly carry X-Hadron-Session. Unknown kinds/blank refs are local usage errors;
the ref after the first colon is preserved byte for byte.

All three key-list selections use top-level appKeys, never App.keys. At provider
c3ca05b545, AppKey.scopes and hostUses use the holder's managesApp predicate and
are compatible with an admitted instructed list. Selections are kept; any nested
refusal fails the command and is never reported as an empty part. Mint/rotate/
grant stay person-only and have no new flag. No retry or downgrade is added.

An instructed list records one credential event per call/page, no dedupe. A
changing revoke records one and a no-op records none. The provider owns the
recording transaction; the CLI neither reconstructs events nor verifies that a
holder actually gave an instruction. Host-launched sessions are refused whatever
they claim; no grant arm applies here. Minimum provider c3ca05b545 is in help.

The schema refresh also adds NodeFilter.childrenOf. Its omitempty annotation is
added to each existing NodeFilter operation, preserving omission semantics in
unrelated commands (caught red by TestNodeFilterOmitsEveryUnsetField). Unwrapped
snapshot surfaces retain owning gaps: childrenOf #1253, run team-snapshot setter
#1578. No new command is implemented for either here.

Verification: red-first command tests, exact pre-change generated-document pins,
one-request/header/claim pins for seven variants, invalid input/binding and
server-code/no-downgrade tests; existing S1 suite, full suite/lint/generation,
help inventory and coverage checks. Independent LOCAL/BLIND, scoped attribution
security at every head, and actual-Sonnet private-stack QA are required at the
frozen green head. Author fake-server results are not real provider evidence.
