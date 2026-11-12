# Write-access checks for the remaining client-computed dry runs

Issue #1938 extends the #1912/#1937 condition to `spec supersede`,
`spec extract`, and `node update`. The server decides `effectiveAccess`;
the CLI does not decide roles or change any server permission.

The existing helper moves from `internal/cmd/spec` to `internal/cmdutil`.
Its implementation and the existing new/edit/rescind call ordering stay
unchanged. The new calls run after target reads and before successful plan
output. Supersede covers allocated and explicit successors, existing-successor
mode, resume, and unchanged results; existing successor reads/eligibility checks
still precede access. Extract creates in the source memory and checks that
same memory. Node update uses the memory id returned by its existing raw read.
No new content lookup or write is introduced.

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
request ordering/counts, and no writes. Existing affected command regressions
remain in place; fixtures explicitly permit the added read-only AuthContext
operation instead of treating it as a mutation.

Three separate compiling command mutants replace the new gate calls with
`error(nil)`: supersede (including existing-successor mode), extract, and node
update. Each fails its own reader-refusal pins. An equivalent string conversion
on the node memory argument must remain green. Mutation source anchors and
failure output are checked; sources are restored afterwards.

Author evidence is loopback/in-process only, not live provider or production
permission evidence. The T1 stamp requires independent cross-family LOCAL and
BLIND readers, plus one independent built-CLI QA seat. UX is waived and §10 is
not applicable per the gate. Hosted CI supplies full-suite evidence at the
frozen head; the author stops at ready and does not merge or release.
