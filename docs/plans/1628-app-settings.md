# App install/update remaining settings

The settings flags expose the committed createApp/updateApp SDL. Existing
operations remain for legacy calls; explicit new settings select separately
generated typed operations. No schema fallback drops an unsupported setting.

Presence governs serialization: omitted fields preserve/default on the server;
false and zero are sent. Repeated tools/surfaces replace the list, clear sends
[], and expiry clear sends null. StringArray preserves commas and order. Policy
files receive syntax-only JSON validation before any HTTP; policy updates are
absent because updateApp has no policy argument in the snapshot. Deprecated AI
shims remain excluded; ai-config is the supported route.

Enums and rename slugs are locally validated. Numeric ranges, timestamp meaning,
policy shape and authorization remain server-owned. Surfaces are descriptive,
not an authorization boundary; changing type never derives a surface list.

Update confirmation names changed fields without values, except a rename reads
the current App URN only for an interactive rename and uses urn-lib parser/composer to show old and new full
URNs. New install settings require confirmation, while legacy-only installs keep
the original request document and no-prompt path. The --type enum is locally
validated on every install path, including refusal of an explicit empty value.
Receipts retain explicit existing DTOs and JSON keys.

Tests start red on main and record exact variable keysets, false/zero values,
null/empty clears, replacement ordering, policy validation, confirmation and
legacy document selection. QA uses a compiled binary against fake transport;
no production installs or settings writes are authorized by this work.

Governing contracts: cor:api:240:02, cor:acl:040:02, cor:agt:020:13,
cor:aut:020:02, with CHECKED/FIT on cli#1628. Merge main after cli#1634/#1641.
