# Report-only provider-filter inspection (cli#953, slice A)

`team worker find-filtered [worker]` is an operator diagnostic. It makes no
recovery decision. There is no apply flag, process-control interface, prompt
writer, session mutation, git invocation or automatic trigger.

The old gate was rechecked at main 65c07c21. Wolfgang's scope interpretation
[6975717606](https://github.com/hadron-memory/hadron-cli/issues/953#issuecomment-6975717606)
resolves the changed onboard helper: check locally for the Orca executable,
then use terminal-list availability; do not call `orca status`. The command
retains onboard's fixed exit-2 refusal. Other scan failures exit 1; a completed
scan exits 0, including BLOCKED and UNREADABLE rows.

The command uses purpose-built reads containing only worker identity fields.
A named worker takes one query; a team scan pages 200 id/name pairs at a time
until the reported total, including retired workers with surviving local bindings.
Every named worker lookup must prove membership in the selected App, regardless
of worker-reference spelling; selected and returned App URNs are canonicalized,
while opaque App IDs compare directly. It joins these
worker IDs to bindings in terminal-listed worktrees on the same server. Missing
server metadata and malformed or incomplete terminal lists are not accepted as
proof of an empty result. It reads `.git`
pointers itself, or `.hadron-team` for plain directories; it never runs git.
Unattributable worktrees are omitted. A named team worker without a terminal is
UNREADABLE/no_terminal. Duplicate terminals produce a single AMBIGUOUS row,
not a randomly selected screen. Team membership comes from the server roster;
terminal titles and agentIdentity do not establish worker identity.

`internal/orcaterm.FilterProof` reuses the existing noticeHeader and noticeBody
patterns and IdleProof. A contiguous body after the exact notice header plus
idle prompt evidence is BLOCKED. Header-only, body-only, unproven idle and
running-turn evidence are AMBIGUOUS. Footer thread titles are not headers.
Failed screen reads are UNREADABLE. No matched line or draft is returned.
The shared screen matching and delivery classifications are otherwise unchanged.

`internal/filterreport` has no mutation capabilities. It reads bounded local
binding files and `/proc` metadata. Foreign-uid argv is not read. The `ok`
shape is deliberately stricter than the newer host census: one exact
worker-supervise wrapper at the worktree and one direct node /.../codex child,
with cwd at or below the worktree. It does not merge nested logical agents or
accept a wrapper several ancestors away. Multiple wrappers/agents, missing
members, foreign uid and outside cwd remain diagnostic codes. As a read-only
snapshot of changing processes, this is context for an operator, never proof
authorizing action. Unreadable/disappearing process entries are not guessed.
Unix file opens are no-follow and nonblocking, with no create flags, and read
lengths are capped. No terminal/draft/argv content appears in any error.

JSON is an array (including `[]`) of six stable string fields: worker, worktree,
terminal, classification, agent_tree and note. Metadata fields have display
bounds; classification and note are fixed codes. Text renders those same
fields and, for BLOCKED, only the provider-route footer specified by the gate.

## Validation and independent seats

Author fixtures cover notice evidence, running and ambiguous screens, process
shapes over fake `/proc`, table/JSON redaction, unreadable errors, local-host
refusal before roster/process/screen reads, read-only Orca allowlisting, and
before/after file snapshots including a wrapper prompt file. AST checks reject
write/signal calls and process-control imports in the scanner. No signal seam
is exposed: the scanner cannot signal even in production. No live worker
terminal or process census is used in tests.

T2 seats remain cross-family BLIND and LOCAL (different engineers), security at
every head, and QA through a compiled CLI against fake Orca, HTTP and `/proc`.
For that QA build only, the unexported `team.findFilteredProcRoot` string can be
set by Go `-ldflags -X`; production defaults to `/proc`, with no runtime flag or
environment override. No real team host is a QA target. UX is waived. Any
recovery proposal needs its own issue and gate; this PR uses Refs #953.

The generic ambient-scope checklist recommends an extra scope/source line. This
command follows the gate’s six-field output contract instead; scope is selected
by explicit/configured App or the nearest binding. Authentication is required.
The authored-write client hook is intentionally absent from these read queries,
so reading a roster cannot invoke git for attribution.
