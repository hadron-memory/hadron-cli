# Team text receipt displays

`team worker cast` retains its existing success line, ID, placement and prompt.
When the returned worker has a nonempty URN, text adds a separate `  urn: <urn>`
line. It uses the server's address rather than composing one from name or App.
Missing/null/empty URNs add no line. The existing JSON `urn` and all other keys
are unchanged. The new server-supplied cell is rendered through SafeCell.

`team chat read` keeps `[seq]` first and adds a UTC minute timestamp when its
existing `at` string parses as RFC3339 (fractional seconds accepted). It converts
offsets to UTC and drops seconds without rounding: `[8] 2026-11-06T01:20Z Iris`.
Missing, empty or malformed times add no timestamp and do not fail the read.
JSON preserves the original `at` string, including its offset/fractional precision.

Both changes are rendering only: same operations, variables, cursor/read-state
and request count. No new flags, server fields, retry or access decision.
Baseline JSON and the chat text operation list are captured from unmodified main
as deterministic command-test fixtures; a separate test-only red commit precedes
the production edits. Fake execution demonstrates wire/output behavior, not a
real cast or deployed-server contract. No platform spec change is needed for
these client-local display additions.
