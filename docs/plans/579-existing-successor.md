# Superseding to an existing citation (#579, split A)

Supersede records a successor; rescind without a successor is a separate flow.

## Modes and output

The existing create-successor path still needs --title and can allocate a
legacy number or use --to. --by instead names an already-written citation in
the selected memory: cross-module/product is allowed, cross-memory is not.
No node is created and the old body is preserved by this mode. It conflicts
with every create-shaping flag, including explicitly supplied empty/false
forms. --reason is also refused with --by because its create-mode body note
is outside this mode. Blank --by is usage, not omission. --summary replaces historical text;
absence preserves the stored old abstract verbatim, including whitespace.

Both modes set the retirement abstract to:

    SUPERSEDED (<UTC date>) by <citation> (<successor name>); do not apply. Historical: <summary>

The source of the existing target name is the re-read target; the create path
uses the server's creation response, and its recovery path uses the fresh
successor edge projection. No label is parsed from a ref. UTC is evaluated at
the retirement write. The existing create path keeps its historical body-note
behavior; --by writes only tag/abstract plus the edge, never the body. The raw
read still supplies coherent history/tags and verifies source identity/role.
Supplying the abstract in the same update re-fingerprints it server-side.
There is no truncation: a lead/history over 2000 UTF-16 units is refused with
--summary guidance. Existing lint length/body rules remain independent.

The package-local result keeps every old key and edge-status/retired meaning.
Existing-target mode adds existingSuccessor:true; a no-op adds unchanged:true.
A same-successor anchored lead leaves the abstract untouched even if the old
display name/date has changed; the full operation is a no-op only when its
matching readable edge and retirement tag also exist. Missing facts are completed; a different/malformed lead conflicts rather than being nested or
silently reported unchanged. Conflicting existing successor edges also refuse.

## Validation and partial writes

The target operation selects ID, memory, loc, name, role/tags, isPlaceholder,
deletedAt and superseded-by links. Placeholder status is never inferred from
an empty body. Missing/unreadable, wrong-memory, non-spec, retired, placeholder,
deleted, self and cycle cases fail before a write. Graph traversal follows
all reachable superseded-by links with visited/active sets. A hidden link
cannot prove acyclicity and is refused. No whole-corpus scan is added.

After confirmation, source and target eligibility/identity and the graph are
re-read before the first write. Target citation movement is refused; a changed
name is read as display data. The stored source body/abstract/tags are read
coherently, rather than pairing a fresh body with stale history.

The new --by path reads the source through the existing spec-edit snapshot
and updates its immutable ID with expectedRevision. A concurrent source edit
refuses the retirement update rather than overwriting its tags/history; there
is no unguarded fallback. The existing create path retains its unguarded raw
read/update. Neither mode makes target validation and the edge atomic: a target
edit after the final read can race the write. The edge is a separate
mutation and its result is checked against a fresh old-node read before
retirement. Uncertain/missing/competing links stop retirement and report the
partial edge status; a competing successor exits conflict. A lost retirement
answer succeeds only when read-back shows both the tag and the exact requested
abstract. A visible tag alone is reported retired but returns an error because
the abstract is unconfirmed; unreadable state remains unknown. --by retries never
allocate a replacement. Create-mode partial recovery keeps its existing
edge-as-durable-marker rule; an unlinked same-title node is never auto-reused.

## Cost and compatibility

One targeted resolve/read locates the existing successor, and one slim read
per reachable successor-graph node is needed per validation traversal. There
are two traversals around confirmation, not a corpus-wide list or body fetch
per graph node. Source detail/raw reads, the edge read-back and existing
register advisory are bounded separate operations; successful --by writes
one edge (unless already present) and one retirement update, zero creates.
Graph cost is proportional to reachable links, not the whole memory; no small
constant latency claim is made for a large successor graph.

The committed schema already contains all selected fields. No schema refresh
or deployment-floor claim is made. The dedicated existing-target operation
fails explicitly on a provider lacking its fields; no unsafe fallback infers
placeholder state. The existing shared GetNode shape gains only the outgoing
target's existing name field (also in its raw/next-node twins to keep the
explicit generated typename coherent). The general reads otherwise retain
arguments and behavior.

## Evidence boundaries

Stateful fake GraphQL tests exercise actual commands, mutations and subsequent
raw/lint reads: existing and cross-module targets, no creates, all refusals,
reruns, current target names, unchanged raw bodies, summary/default history,
create-path lead, strict lint with a refreshed fingerprint, cap/UTF-16 handling,
help and agent contract. Source mutants target placeholder, cycle, abstract
write, second-read admission and the UTF-16 budget; an equivalent control stays
green. Independent cross-family LOCAL/BLIND and compiled CLI QA remain the
stamped seats. No real corpus, production or permanent spec write was used by
the author. Remaining races and other pre-existing lint findings are not
claimed fixed by a clean abstract-stale pin.

## Existing corpus migration

Strict lint promotes the new retirement-abstract warning to exit 5. Canonical
RESCINDED/WITHDRAWN leads remain valid, never converted into supersessions.
Legacy supersessions can be inspected and repaired with the existing-target
mode and its source CAS; no corpus backfill runs. Explicit empty --summary
keeps empty history. The create-mode retirement update remains unguarded and
is disclosed as a follow-up; it is not a new concurrency guarantee.
