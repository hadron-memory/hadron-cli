# Guarded console push (#977)

The command sends one input to a local agent terminal. It does not post chat,
mark a cursor, launch an agent, migrate callers, or install a binary. CHECKED:
6325527505; author attach6325488959; task01a1600d96ef7b7b81fa2f55ef314a81.

## Boundaries

The sender must have a nonempty server/App/worker/session binding and a live
server session. Recipient resolution is App-scoped. Each observation re-lists
Orca terminals, reads the worktree's resolved git-dir binding, verifies that
binding's server/App/worker/session against the server, and matches its open
session tool, Orca agentIdentity and recognized screen family. Exactly one
connected/writable candidate is required. Titles and arbitrary terminal handles
are not input flags. Malformed draft metadata or unknown screens hold. Orca omits the draft field on empty composers in the checked-in real captures: absent/null is accepted only with positive composer proof, never by itself.
Only Claude and Codex are supported. Current orcaterm screen/composer classifiers retain the teamrouter discipline and its corrected capture handling. Configuration/historical state is untouched.

The runtime terminal handle keys an own-attempt lock (the session stays part of each freshness comparison, not the lock key) via the existing delivery
lock implementation (under the user's Hadron cache). This serializes this
command, not a human, an old sender or another program. The recipient binding,
identity and screen are freshly checked before every clear/submission. Authority
lookup failures reveal fixed classes only; no server error payload is printed.

## State machine

`internal/guardedpush` owns decisions, timers, exact bytes and one-submit policy;
its clock and terminal callbacks are injected. Defaults are pre-send wait120s,
draft wait30s, unchanged interval30s, confirmation10s and poll250ms. Positive
intervals are bounded to24h; stability below30s is refused. Draft wait and
stability overlap. Changes reset stability; busy resets draft observation;
unknown state holds. An empty recognized idle composer proceeds promptly.

An eligible draft is kept in memory, then cleared by the existing proven
Backspace-per-rune adapter (`delivery.ClearKeys`, Claude2.1.303/Codex0.163.0
scratch evidence in delivery.Proven). Never Ctrl-C, Ctrl-U, Esc or clipboard.
The next screen must positively prove the composer empty. A final fresh check
precedes the single text-plus-Enter call. If anything changes after clear, stop
unconfirmed; never restore over newly typed input. Unsupported control bytes
hold rather than being injected as terminal keys. Newline/tab/Unicode draft
bytes are retained exactly. Combined payload over Orca's single-argv bound
holds before any clear; it is never truncated.

One payload labels the bound sender's message, a random attempt marker and
optional provenance seq. A captured draft follows a fixed unfinished,
not-submitted-by-its-author separator. It is uninterpreted quoted material.
The adapter does NOT use Client.Send's idempotent-retry feature: even an
ambiguous transport answer produces no second call, Enter or retry request.

Confirmation requires a subsequent, same-identity screen with the attempt
marker and positive running-turn evidence. Accepted/turn_started receipts or
old busy/history without that marker cannot confirm. A fast turn or scrolled
marker may be conservatively unconfirmed. Each confirmation is bounded; a
cancelled/unknown post-mutation state never invites automatic retry.

## Outcomes

Fixed DTO: outcome, phase, reason, draftIncluded, recipient, seq (nullable). `draftIncluded` is confirmation metadata: true only on confirmed combined delivery; false on an unconfirmed outcome is not proof that no draft was submitted. Recipient is the actionable canonical worker ID (not a display name), as explicitly selected for this command; it is the subject of the receipt. Human output names the bound App/source with the id as its fallback.
If receipt output fails after input, the command returns a fixed unconfirmed exit12 rather than exposing the writer error or inviting resend. Message, draft, screen, keys and raw errors never enter output/logs or durable
artifacts. Standard usage/auth/not-found codes are retained. Command-only codes:

| Exit | Outcome | Retry meaning |
|---|---|---|
|0|sent|Completed; do not resend|
|9|sent-with-draft|SUCCESS; do not resend|
|10|held-busy|No composer input|
|11|held-unknown|No composer input|
|12|unconfirmed|May have changed/submitted input; do not automatically retry|

Prefer a regular message file to --text, which has ordinary argv visibility.
Generated payload still uses Orca's existing text argv surface; no input argv
is logged by this command. Lock files contain owner/hashed identity only.

## Evidence and limits

Red-first f1e266b pins fake-clock/fake-terminal behavior. Tests model both
interfaces, stable/changing/late-changing drafts, locks, identity drift, clearing,
cancellation, no-retry uncertainty, bounded payload, screen correlation and
actual command JSON/privacy. All transports are fake; no real terminal or
human draft experiment is authorized. Independent compiled QA and scoped
security remain required, with Claude BLIND for this Codex author.

There is no atomic conditional Orca send. The final read-to-clear/send ms race
remains. Capture is memory-only, so process death after clearing can lose the
draft; successful fake tests do not prove durable recovery or atomic host safety.
Per-interface evidence is limited to the documented modeled/proven sequence;
new interfaces require evidence and a gate. No background resend or hold report.
The enforcement shim and hadron-chat-send/lifecycle/ticker migrations remain
separate gated tasks; existing unguarded callers are explicitly not fixed here.


## Author validation

Nine bounded production mutants each fail named observable tests: bypass busy
checks, omit draft stability reset, omit clear, omit the draft label, omit draft
bytes, double-submit, confirm without fresh marker/busy evidence, skip the real
own-attempt lock, and expose a raw send error. The harness asserts the exact
replacement and reads back the file, records its diff, requires a named test
failure (not a build error), and restores each mutation. An equivalent +0 rewrite
survives as the positive control. No hanging mutant or live terminal probe.

Original incident draft bytes were not supplied; tests use clearly named
synthetic sentinels plus the existing 2026-10-18 captured interface frames. They
do not claim to replay Holger's private half-written text. Core transport rules
and real-command output/privacy checks are distinct from independent QA seats.
