# App-manager instructions, as built (#2448)

Six existing writes accept an optional instruction: App properties set,
App model-group set/clear, team delivery-settings set, team ticker settings
set, and team grant rm. This implements cor:dmo:050:12, gated by #2448,
without changing server authorization or adding MCP arguments.

Each has a separate generated instructed operation. No-flag operations,
variables, DTOs and output remain unchanged. Optional settings retain their
existing omit semantics; instruction and expected revision are independent.
The schema already contains all six arguments. The highest provider minimum
is 4bac6127 (#3398); properties/model-group arrived in ba077810 (#3376),
revoke in c3ca05b5 (#3340).

The App commands reuse memberInstruction/memberWriteContext; team commands
reuse staffingInstruction and BeginInstructedWrite. Both reach the existing
snapshotForInstruction/instructedSession App-target helper. They do not use
BeginServerTargetInstructedWrite. Parsing and snapshot precede the first
lookup or confirmation; rechecking follows client setup and precedes the
mutation. Lookups are ordinary person reads. A complete unchanged binding
must match both server and target App to attach a worker session.

Missing/unusable/changed binding sends the claim as the person, following
instructedAsPerson: unbound is quiet, unusable emits a note. No server refusal
causes retry or downgrade. Confirmation for instructed clear/revoke happens
before network activity; the original plain revoke flow is preserved.

Tests pin all six plain document hashes and exact variables, instructed wire
and output, malformed flags with zero calls, fallback states, binding changes
during lookup and confirmation, no retry, and the exact six-command flag scope.
Independent QA is compiled-CLI fixture execution; this change does not claim
to validate provider authorization or production event emission.
