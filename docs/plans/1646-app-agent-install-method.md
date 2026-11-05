# App agent installation provenance (#1646)

The listing mirrors server #3446 (merged d0b374cd8accbc9c47cb0dbd3f6162fabc7a71c2),
not a client inference about imports. The schema snapshot was exported from an
isolated checkout of that exact merged revision.

`app agent list` uses `AppAgentInstallationRoster`: the existing roster fragment
plus `App.appAgents.installMethod` and importer id/URN/name. `team role list`
continues using the old `AppAgentRoster` document, with no new fields or extra
request. One listing sends one GraphQL request, scoped to one App; payload grows
linearly with its readable agents and installs, with only three scalar importer
fields per derived entry (no prompt, import graph, or additional fan-out).

The stable member DTO gains explicit nullable `installMethod` and `importer`
(`{id,urn,name}`), joined by agent id alongside existing boot-prompt pointers.
The roster order and readable-agent filtering remain the server's. Missing joins
and withheld fields stay null; a null method never becomes ROW. Human INSTALL
shows installed/derived only for served values. `?` means unavailable, distinct
from the ROLE and importer `—` for no value. The importer cell includes its name
and actionable URN, sanitized with SafeCell; JSON retains source strings.

Older providers without #3446 reject the richer listing operation. There is no
fallback that fabricates provenance. Casting, install writes, authorization,
and public server fields are unchanged. Docs #645 follows after the companions.

The coverage tool currently enumerates roots and inputs, not output fields.
Two explicit output-field command lines in its generated header record this
companion; operation-selection tests pin them to the listing and keep the old
role-warning document free of the new fields. This does not claim generic
output-field enumeration.

Red-first commit 0fd102c2 pins served ROW/DERIVED and wholly withheld fixtures,
exact member JSON including importer shape/null keys, and human marks. Existing
App-context, boot-pointer and safe-cell fixtures exercise the richer operation;
team-role fixtures keep the old one. Independent CLI QA uses compiled binary
stub fixtures (human + JSON), per gate 6630115678; no live-server leg is claimed.
