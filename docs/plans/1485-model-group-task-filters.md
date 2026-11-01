# Model-group task filters (cli#1485 S2)

The CLI exposes server#3029 requirements through task create/update. Provider
snapshot verified at server `966cd86ea8305e12fb526be7a195466bc855005b` (contains
#3029); make schema produced an unchanged SDL. Generated output changes only
the shared AgentTaskFields requirements selection; input omitempty remains.

`--model-group` maps to modelGroupIn; `--model-group-not` maps to
modelGroupNotIn. The shared parser splits commas and rejects entirely empty
supplied lists before HTTP. It preserves each supplied string, duplicates,
case and overlap. The server normalizes/validates and owns all matching and
authority. No preflight group read or retry.

Create uses the typed input, with both new fields omitted unless named.
Update uses the existing strict JSON patch: unrelated flags omit requirements,
each requirement flag replaces the entire object, and clear sends null.
Task requirements DTOs normalize absent group arrays to [] without changing
null requirements; human detail and list use existing safe-list rendering.
The shared fragment also exposes returned group requirements on other task
receipts, with no new flags elsewhere. Worker membership's S3 null semantics
are untouched.

Red-first tests cover wire bytes, raw overlapping lists, combined requirements,
empty and clear-conflicting flags before any HTTP, omitted keys, replacement,
null clear, get/list fields and typed refusal chains/no retry. Independent QA
must supplement compiled fake HTTP with one fresh private provider stack for
accepted persistence, replacement/clear and unknown-group refusal. Synthetic
checks do not prove provider behavior. No production changes, deploy or release;
release waits for a deployed provider containing #3029. Refs #1485.

Existing API error categories are reused (MODEL_GROUP_NOT_FOUND=4 via suffix,
BAD_USER_INPUT=2, TEAM_AGENT_AMBIGUOUS=2). Group-flag writes additionally use
WithServerCode around the unchanged mapped task error, so the root JSON sink
keeps extensions.code; ordinary task writes keep their previous output.

Expected cost: one mutation for create/update, no group/requirements pre-read;
create may also perform its pre-existing App-label decoration read. Get is one
targeted task query; plain list one page query, no extra requests per row.
Two added arrays are normally a few short names (<1KiB per task); at the
provider's 32-name/64-character bounds each pair adds about 4KiB, roughly 80KiB
for a 20-row page before existing metadata. This is an estimate, not a measured
live benchmark; QA records actual fixture/provider response bytes and timings.
