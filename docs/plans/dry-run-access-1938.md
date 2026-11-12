# Write-access checks for the remaining client-computed dry runs

Issue #1938 extends the #1912/#1937 condition to `spec supersede`,
`spec extract`, and `node update`. The server decides `effectiveAccess`;
the CLI does not decide roles or change any server permission.

The existing helper moves from `internal/cmd/spec` to `internal/cmdutil`.
The existing new/edit/rescind target-read and no-user ordering stays
unchanged. The shared helper resolves a raw memory ID to its server-returned
URN for signed-in users before auditing it, as described below. The new
calls run after target reads and before successful plan
output. Supersede covers allocated and explicit successors, existing-successor
mode, resume, and unchanged results; existing successor reads/eligibility checks
still precede access. Extract creates in the source memory and checks that
same memory. Node update uses the memory id returned by its existing raw read.
The minimal `DryRunMemoryIdentity` operation reads only id/URN through the
ordinary memory read gate; it is called only after user auth and only for raw
IDs. Missing, mismatched or empty identity fails closed, with no access audit
or plan. Typed URN callers and successful no-user credentials do not make
that read. No content lookup, URN reconstruction or write is introduced.

This distinction is required by server source `0c816205c84aea62053df77908b26cf581474d1e`:
`src/lib/auth/effectiveAccess.ts:resolveResource` dispatches by typed URN; a
bare ID there denotes an AI config. `memory(ref:)` accepts memory IDs.
`CanonicalMemoryRef` preserves IDs; normalization alone cannot supply a
memory's type. The BLIND P2 caught the initial untyped node audit; red-first
25d08064 models the real dispatch refusal. The fixed lookup uses only an
already-existing schema field, generated normally without an SDL change.

Request delta after a successful target read: one AuthContext read; one
EffectiveAccess for a user; plus one memory identity read for a user's raw
memory ID. No-user credentials add only AuthContext. No fanout is added;
bytes and latency are not benchmarked by the author.

A signed-in user denied write access receives
`Forbidden: no write access to this memory`, exit 8, with no plan. Auth/access
lookup failures keep ordinary error mapping. A successfully resolved credential
without a user keeps the previous preview behavior. This is a preflight
snapshot, not a guarantee that a later real write will succeed. Unreadable or
missing targets keep their existing not-found response before either access
query. Non-dry paths continue to rely on the actual write's server gate.

Renumber is excluded: cor:spc:020:05's reader dry-run rule and server#3990's
ruling are separate. Replace is already server-gated and is unchanged. No spec
mint or Built-line update belongs to this client extension: cor:api:290:01 is
precedent, not governing law here; cor:api:140:03's concealment rule still holds.

## Validation

Red-first commit 747fe0e3 reproduced successful reader plans for each command.
The expanded fake-server matrix covers supersede's alternate paths, text/JSON
refusals, writer/no-user/App-key positives, unreadable targets, unreadable
existing successor, access-query failures, exact access resource and user,
request ordering/counts, identity-read failure/empty-memory controls, and no
writes. Existing affected command regressions
remain in place; fixtures explicitly permit the added read-only AuthContext
operation instead of treating it as a mutation.

Three separate compiling command mutants replace the new gate calls with
`error(nil)`: supersede (including existing-successor mode), extract, and node
update. Each fails its own reader-refusal pins. An equivalent string conversion
on the node memory argument must remain green. Mutation source anchors and
failure output are checked; sources are restored afterwards. The P2 fix also
passes an equivalent server-URN string conversion and kills a compiling
regression mutant that retains the bare ID after the identity read.

Author evidence is loopback/in-process only, not live provider or production
permission evidence. The T1 stamp requires independent cross-family LOCAL and
BLIND readers, plus one independent built-CLI QA seat. UX is waived and §10 is
not applicable per the gate. Hosted CI supplies full-suite evidence at the
frozen head; the author stops at ready and does not merge or release.
