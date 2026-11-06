# Snapshot grant consent and guard pins (cli#1612 remainder)

Gate: Xan6671974496; scoped FIT Vera6671930854; ruling0204.
This slice changes only legacy non-TTY revoke consent ordering and strengthens
existing receipt/history tests. Earlier receipt guards shipped in #1611 and
instructed list/revoke in #1615; they are not new implementation here.

## As built

After local instruction/id/App validation, legacy non-TTY without --yes calls
the existing Confirm refusal before constructing the GraphQL client or reading
history. This exits2 for live, ended and missing rows alike. The prior ended-row
shortcut remains available only after consent (--yes or the existing TTY flow).
TTY legacy still reads returned App/trigger metadata then confirms once; accept
permits one revoke, decline exits6 without mutation. Instructed revoke retains
pre-confirmation, session snapshot/recheck and one-event-per-admitted-page warning.
No authority, token/header, provider, by-id or schema change.

History stays limit20, advancing by actual returned row count until hasMore is
false. A selected early row cannot bypass validation of a later malformed page.
No partial list is printed and no revoke is sent on that failure. Public explicit
list paging is unchanged. Internal page200 was dropped by ruling0204; #1619's
by-id consumer is separate. Existing eight-field JSON, nullable revoked metadata,
terminal-safe text, structured refusals and unknown-write/no-retry stay unchanged.

## Evidence and review checklist

Red8c64f6d5 observes provider reads on the old branch before the fix897353f3.
Tests cover live/ended/missing with explicit and ambient App, isolated config;
receipt tests assert distinctive error prose, not merely a generic nonzero exit.
The new stateful27-row fixture returns11 rows per page despite requested20 and
requires offsets0/11/22, includeRevoked:true, exhaustion before one mutation.
Malformed second-page null-page/null-row/incomplete-row covers list and revoke.

Five separate production mutants fail assertions: late consent, omitted required
fields, omitted set-live/trigger match, stop after first page, ignored later-page
error. Source is restored byte-for-byte between each; restored tests pass.
Logs retained in author scratch; independent seats repeat their own checks.

Author checklist: consent truthfulness, explicit/ambient App source, fail-closed
paging, typed error/DTO compatibility, no retry, test-double access patterns and
nonempty mutant patches checked. GraphQL/optional input/coverage/new server-code
checklists do not apply: operations/schema/DTO/error mapping are unchanged.
Help explains non-TTY refusal and TTY metadata flow; recovery still names existing
list --include-revoked. Example command spelling unchanged. Full review inventory
and skip reasons accompany the PR receipt. No real provider or production proof;
compiled stateful-fake QA is assigned at the frozen green head per the gate.
