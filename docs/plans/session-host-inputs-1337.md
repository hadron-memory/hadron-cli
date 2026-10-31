# Session and host input coverage (#1337)

This T1 slice exposes the 20 matrix gaps in #1337. The committed schema
already contains every field; no schema refresh or server change is needed.

Session start forwards optional provenance only when its flag was supplied.
Explicit PR number zero remains present. Resume is the existing no-worker
`session start` path: description/family are forwarded there, while all new
start-only flags refuse before redemption. Description cannot be silently
dropped on a named bind. Server reference, family, and permission rules remain
authoritative. End counters use a separate opt-in operation; ordinary end and
resume-claim documents and rollback calls remain unchanged.

Session list customer/type use the paged twin, preserving the existing total,
continuation, no-progress guard, and active-filter scan. An explicit filter
never retries with a wider document on older servers. The direct work-items
command sends workerRef to the server without live-roster resolution, so a
retired name remains usable; it returns one page with the server total and an
explicit DTO, including optional detail/model. No session fan-out or client
reconstruction of totals occurs.

The host configuration carries an explicit full capabilities declaration at
every registration refresh (default none). This does not implement or enable a
new host operation handler. In particular, this poller cannot execute
worker.reset-context, so its config should leave that capability undeclared.
This limited declaration surface is explicitly listed in the CHECKED gate and
confirmed by Ada (#57630); adding reset behavior is outside this slice.

Host availability is a separate explicit command, without running the poller.
It reuses key_file-only host authentication and the config's App, with no
session header or ambient token. Server ownership gates decide which mark may
be written or cleared. Local checks validate only argument shape; they do not
pre-read or duplicate ownership policy. Output is an explicit DTO and the host
key is never rendered. There is no automatic token/host failure classifier.

Tests use fake GraphQL only; they establish wire behavior and refusal mapping,
not live server authorization or host operation execution. No production host
mark, registration, session bind/end, install or release is part of validation.
