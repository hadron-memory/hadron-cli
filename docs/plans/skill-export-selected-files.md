# Selected individual skill-file export (#749)

## Contract

`cor:agt:030:03` revision 6 permits an individual-file export to name task
nodes. Plugin bundles still select by scope. The server owns selection,
collision judgment, local-edit judgment, and rendering; the CLI owns guarded
filesystem I/O and the report. The paired server implementation is
hadron-server#1409, whose first published contract head was `b3383a2a`.

`hadron skill export --node <id-or-fully-qualified-node-URN>` is repeatable.
Without it, export keeps the original unscoped `skillPlan` request and report.
With it, the CLI validates and de-duplicates refs before accessing a client or
HOME, then calls `selectedSkillFilePlan` once for each known host. The selected
request has no `memories` input. `--node --prune` is a usage error before I/O.

The CLI still submits *all safely readable* installed Hadron file facts for each host. This
lets the server pair files and detect occupied targets and local edits without
treating unselected installations as orphans. The server separately checks
readable enabled names outside the selection for collisions. A selected plan
must return `orphanAssessmentSkipped: true` and `orphans: []`; the CLI refuses
the whole host before writing if either assertion fails. It also refuses a
plan containing an entry whose node ID or emitted URN does not match a named
selector. The existing link, foreign-file, atomic-write, and local-edit guards
then execute each selected entry. Selected `FAIL` and `REFUSE` results exit 5
after the report, while other selected entries still run.

`selectionResults` describes refs without host entries. A missing or denied
task is a host-free `FAIL` with an opaque `node-unavailable` reason and no node
ID; the CLI reports that ref once across the two host requests. A readable
task with no declaration for one host is a `SKIP` with its node ID, reported
for that host. Selected JSON adds `selectedNodes`,
`orphanAssessmentSkipped: true`, and `selections` when there are such results.
The unscoped JSON keys remain unchanged.

Both GraphQL operations repeat the complete `SkillFileFactsInput` omission
directives. Genqlient chooses one operation's directives when generating that
shared Go input type; a partial set can silently turn omitted fields into
explicit `null` in existing `skill status` and unscoped `skill export` calls
(`findings:genqlient-shared-input-omitempty-must-match-across-ops`).

## Verification

The command acceptance tests use a disposable HOME and a fake GraphQL server.
They pin the dedicated operation and selected refs; all local facts submitted;
selected writes in both hosts; unselected bytes and mtimes unchanged; opaque
unavailable and per-host skip reports; invalid refs and `--prune` refused before
I/O; and unsafe scope or unrelated entries refused before writes. The identity
test covers flat v2, legacy v1, and compound-memory node URNs. Existing export
acceptance tests cover the shared writer, including dry-run and local file
guards. Full `go test ./...`, `go vet ./...`, and `make lint` are the PR gates.

No installed skills, plugin bundles, global export, release tag, or server
deployment are changed by this implementation.
