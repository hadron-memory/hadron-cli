# Asset public-link companion (#897), as built

Server #2287 supplies the ratified switch and readable Asset flags. The CLI
refreshes its snapshot from merged server main, then generates typed queries.
`asset public-link <id-or-URN> (--on | --off)` calls the shared switch directly,
with no memory lookup, inferred management rights or retries. ON uses the
existing `cmdutil.Confirm` pattern; non-interactive callers need `--yes`. OFF
needs no confirmation. The prompt states that anyone with the link can download
only when serving and eligibility permit, so an outer-gate-disabled deployment
can record opt-in without claiming that publication occurred.

`asset list` and `asset url` add explicit DTO booleans `publicLinkEnabled`,
`publicLinkAvailable`, and `canManagePublicLink`; the toggle result reports those
fields and nullable `publicUrl`. The states remain independent: a null URL never
means OFF. Text distinguishes OFF, ON/serving disabled, and ON/no URL. Capability
is displayed from the server and never computed locally. Existing list paging,
URL exit 5 and stdout/stderr split remain. `asset get`'s byte-download and JSON
contract is unchanged, per gate amendment 6380553039; metadata read-back there
requires a future server surface and is outside this build.

Tests use the command factory and a fake GraphQL server. A separate first commit
pins the missing command/read-back red on main. Coverage includes ON/OFF wire
booleans, missing/both flags, unconfirmed non-interactive ON making no request,
additive JSON fields, the independent states in text, typed refusal exits and
older-server schema-skew guidance. Existing asset download tests remain intact.

No server policy, audit, deployment setting or portal behavior is changed.
