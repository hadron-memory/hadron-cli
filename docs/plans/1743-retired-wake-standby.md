# Retired isolation and standby client surfaces

Built for cli#1743 against server Phase 1, hadron-server#3611 at
798473e7a22413a354c907505e4e554b2c7bdd01. This change grants no release authority.
The released CLI version and its exact documents remain evidence for the
separately gated Phase 2 of server#3606.

## Continuing commands

`team isolation set|clear` and delivery-settings `--standby` are removed.
Wake-settings get, mention settings, pause and clear-pause continue. Delivery
get/set retain their six supported settings, ordinary authenticated caller,
App scope, manager write gate and typed refusal handling.

The intentional JSON retirement removes isolated, isolatedAt,
isolatedByHandle, isolatedUntil and canManageIsolation from wake-settings,
and standbyEnabled/standbyEffective from delivery-settings. Remaining fields
stay in explicit command DTOs; unrelated worker/task/session output is unchanged.

## Documents and omission

Phase 1 keeps deprecated compatibility fields in its SDL. New CLI documents
select none of them. Isolation mutations are deleted. Delivery set uses six
optional scalar variables with omitempty and an inline input containing only
supported fields. This prevents the generated compatibility input type from
reintroducing standbyEnabled, including an explicit null. The local pointer
holder is not a request type: genqlient still generates and sends the request.
Omitted variables omit the corresponding input fields; explicit zero remains
present. An empty patch still reaches the manager-gated setter.

No schema snapshot refresh is required to remove selections. All four actual
generated documents validate against the exact Phase-1 exported SDL. A GraphQL
input-coercion harness verifies empty, single-field and explicit-zero inputs
reach a stub resolver without null or standby keys. This is schema/input
validation, not real resolver, authorization or provider execution.

## Verification

Red commit 8a9ec420 fails on the original selected fields, JSON keys and accepted
retired commands. Continuing-path tests assert actual request documents, exact
remaining JSON key counts, and locally refused retired commands. Existing tests
retain omission, numeric bounds, manager-only writes, ordinary credentials,
pause/clear and typed-refusal controls.

Full go test, lint and focused race checks pass. The compiled loopback
acceptance script passes 55 cases (continuing reads/writes, patch omission,
refusals, transport failure, help and retired flag rejection). It isolates HOME
and XDG before executing the CLI. No live server/ACL or delivery behavior is
claimed. Mutation checks restore retired output, drop a forwarded busy value,
and re-add an isolation selection; assertion failures are retained outside the
repository, and source/generated files are restored afterward.

Historical delivery and stop-signal plans retain their reasoning with bounded
retirement notices. The coverage matrix explicitly excludes the retired roots
and compatibility input; it does not claim those server fields were removed.
