# Worker reporting relationships (CLI #835)

The reporting relation belongs to the Worker and survives session changes.
This client implements merged server #1737 / `cor:agt:020:12` and does not
recreate its permission or cycle rules.

## Writes

`team session start --reports-to <worker>` forwards the target name, URN or
id in SessionInput. An omitted flag leaves the field absent; an explicitly
empty value is invalid. The existing bind transaction decides the relation.

`team worker set-reports-to <worker> <target>` resolves the source through the
existing worker ref seam and passes the target to the server to resolve within
the source's App. `--clear` sends explicit null instead; target and clear are
mutually exclusive. There is no client permission precheck or read-modify-write.
The structural selected-field attribution policy includes setWorkerReportsTo,
so a bound caller acts as its worker session. Refusals are not retried.

The server's message remains unchanged; a bounded error extension preserves
its code, separate from the numeric CLI exit category. SELF/CYCLE/OTHER_APP
are input refusals (2); RETIRED/HELD are state conflicts (5); FORBIDDEN is 8;
missing/unreadable is 4. No arbitrary extension payload is forwarded.

## Reads

Worker get/list and session rows use a shared minimal worker identity
projection `{id, name, urn}` for reportsTo. reportsToRetired marks a stale
claim, never clears it. Roster rows are grouped stably by target id, rather
than name or role; unbound workers remain in those groups. The existing
prompt-free roster projection and paginated staff scan remain in use;
teamRoster's shipped reportsTo grouping contract is implemented through the
same persisted Worker relation, preserving existing list JSON/columns.

Known unset shows `-`; a retired target gets `(retired)`; a masked relation
shows `? (unavailable)`. The existing hasLiveSession visibility discriminator
prevents masked null from being presented as an unset relation. Display labels
fall back to the target id, retaining actionable identity in JSON.

Whoami never caches reportsTo in its worktree binding. The local identity
remains recoverable even when the server is unavailable: a best-effort read
of the CURRENT worker relation is bounded to five seconds and returns
reportingKnown=false on failure/masking. The server fallback uses the nested
worker projection for every candidate; with several candidates it does not
choose a single relation. --check's endedAt/open-session meaning is unchanged.

These relation projections require the merged #1737 server contract. No
client-side emulation or alias for servers without reportsTo is introduced.
The best-effort local whoami read degrades to unknown on such a server;
worker reads/set/bind report schema mismatch through the ordinary mapping.

## Evidence and limits

Red-first tests cover missing get/list/retired lines, setter and bind flag,
and current whoami relation. Acceptance also covers explicit-null clear,
omission on bind, masked/unset distinction, grouping by identity on unbound
rows, source/bound actor and fallback candidates. Existing identity recovery,
liveness, empty arrays, prompt-free roster and paging tests remain.

Read-only live smoke observed Jane reportsTo Marco with the full identity
on both worker get and whoami. No author live set/clear/bind was performed.
Independent QA must verify the server-backed permission/refusal/no-oracle
matrix, hand-over/clear, bind preservation and relationship persistence.
